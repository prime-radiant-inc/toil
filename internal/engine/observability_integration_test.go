package engine

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// PRI-1570 + PRI-1576 + PRI-1574 end-to-end integration:
//
//   - Build a ForEach workflow where one item's input expression is a
//     required (!) reference to a missing node.data key. It is load-valid
//     (`data` is a supported field) but unresolvable at runtime because
//     upstream emits no data, so evaluatePhase1 fails pre-dispatch.
//   - Run through engine.ResumeRun (full engine path, not just
//     executeSingle).
//   - PRI-1570: events.jsonl must contain a node_failed event whose
//     data.error names the resolver gap.
//   - PRI-1574: the workflow uses `node.X.status` and `node.X.attempts`
//     in a downstream node — those must resolve.
//   - PRI-1576: the ForEach orchestrator's items[] must include
//     failure_context.error on the failed item, and originating_failure
//     on each transitively-skipped item.
func TestObservabilityIntegration_ForEachCascadeWithResolverGap(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "obs-integration-wf",
		Name:    "Observability Integration",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:        "upstream",
				Kind:      "role",
				Runner:    "test-runner",
				Decisions: definitions.StringDecisions("ok"),
			},
			{
				ID: "fanout",
				ForEach: &definitions.ForEach{
					List:      "input.items",
					Item:      "item",
					DependsOn: "depends_on",
					Body:      "worker",
				},
				Decisions: definitions.StringDecisions(decisionAllSucceeded, "some_failed", "all_failed"),
			},
			{
				ID:     "worker",
				Kind:   "role",
				Runner: "test-runner",
				Inputs: map[string]any{
					// PRI-1570 surface: upstream emits no `data`, so this
					// required (!) data-key lookup fails hard at evaluatePhase1
					// (runtime pre-dispatch). It passes load-time validation
					// because `data` is a supported node field — only the absent
					// key is wrong, and only at run time. The `!` is load-bearing:
					// without it a missing data key resolves to nil (optional) and
					// the worker would dispatch instead of failing pre-dispatch.
					// All items share this template, so each fails the same way;
					// depends_on (item 0 has no deps, items 1-2 chain off it)
					// yields the failed+skipped cascade.
					"bogus_ref": "${node.upstream.data.bogus_field!}",
				},
				Decisions: definitions.StringDecisions("done"),
			},
		},
		Edges: []definitions.Edge{
			{From: "upstream", To: "fanout"},
			// Failure edge on the worker template — required so the
			// ForEach orchestrator can declare some_failed/all_failed
			// decisions. The target doesn't matter for this test (no
			// runner.Run call should succeed past evaluatePhase1 anyway).
			{From: "worker", To: "fanout", When: "status == 'failed'"},
		},
	}

	// Items: task-0 (no deps), task-1 (depends on task-0), task-2 (depends on task-1).
	// All three fail at evaluatePhase1. With sequential depends_on, item 0 fails
	// genuinely; items 1 and 2 get marked skipped with originating_failure.
	inputs := map[string]any{
		"items": []any{
			map[string]any{"id": "task-0", "depends_on": []any{}},
			map[string]any{"id": "task-1", "depends_on": []any{"task-0"}},
			map[string]any{"id": "task-2", "depends_on": []any{"task-1"}},
		},
	}

	runID := "obs-integration-run-1"
	setupRunForResume(t, runsDir, runID, workflow, inputs)

	// Upstream runner returns success with empty SessionID; no bogus_field on output.
	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"ok","message":"upstream done"}`, SessionID: "sess-upstream"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, runErr := engine.ResumeRun(context.Background(), runID)
	// We expect the run to end with the orchestrator reporting all_failed
	// (or to fail outright). Both shapes leave the diagnostic data in place.
	t.Logf("ResumeRun err (expected non-nil for failed orchestrator): %v", runErr)

	events := parseEvents(t, filepath.Join(runsDir, runID, "events.jsonl"))

	// PRI-1570: at least one node_failed event must carry the resolver error
	// in data.error for the failed worker item.
	var sawDiagnostic bool
	for _, ev := range events {
		if ev.Type != "node_failed" {
			continue
		}
		if !strings.Contains(ev.NodeID, "worker") {
			continue
		}
		if errText, ok := ev.Data["error"].(string); ok && strings.Contains(errText, "bogus_field") {
			sawDiagnostic = true
			break
		}
	}
	if !sawDiagnostic {
		t.Errorf("PRI-1570: expected node_failed event for worker item with data.error mentioning 'bogus_field'; got events:\n%s", dumpEventTypes(events))
	}

	// PRI-1576: load state.json and check the fanout orchestrator's items[].
	// Item 0 must have failure_context.error set; items 1 and 2 must have
	// originating_failure pointing back at task-0.
	finalState, err := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	var orchestrator *state.NodeState
	finalState.WithNodes(func(nodes map[string]*state.NodeState) {
		orchestrator = nodes["fanout"]
	})
	if orchestrator == nil {
		t.Fatalf("fanout node missing from state")
	}
	itemsAny, ok := orchestrator.Data["items"]
	if !ok {
		t.Fatalf("fanout.data has no items[]; data keys: %v", keysOf(orchestrator.Data))
	}
	items, ok := itemsAny.([]any)
	if !ok {
		t.Fatalf("items not []any: %T", itemsAny)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Item 0: failed, failure_context.error populated.
	item0, _ := items[0].(map[string]any)
	if item0 == nil {
		t.Fatalf("item 0 not a map: %T", items[0])
	}
	fc0, _ := item0["failure_context"].(map[string]any)
	if fc0 == nil {
		t.Fatalf("PRI-1570: item 0 missing failure_context; item: %+v", item0)
	}
	if errText, _ := fc0["error"].(string); !strings.Contains(errText, "bogus_field") {
		t.Errorf("PRI-1570: item 0 failure_context.error = %q, want it to mention bogus_field", errText)
	}

	// Items 1 and 2: skipped, originating_failure points back at task-0.
	for idx := 1; idx < 3; idx++ {
		item, _ := items[idx].(map[string]any)
		if item == nil {
			t.Errorf("item %d not a map", idx)
			continue
		}
		of, _ := item["originating_failure"].(map[string]any)
		if of == nil {
			t.Errorf("PRI-1576: item %d missing originating_failure; item keys: %v", idx, keysOf(item))
			continue
		}
		if of["id"] != "task-0" {
			t.Errorf("PRI-1576: item %d originating_failure.id = %v, want task-0", idx, of["id"])
		}
		if errText, _ := of["error"].(string); !strings.Contains(errText, "bogus_field") {
			t.Errorf("PRI-1576: item %d originating_failure.error = %q, want bogus_field", idx, errText)
		}
	}
}

// PRI-1574 integration: a downstream node references the new resolver
// surface (node.X.status, node.X.attempts, node.X.tags) via input
// expressions; the resolver must produce engine-managed values.
func TestObservabilityIntegration_ResolverSurfaceFromWorkflow(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "resolver-surface-wf",
		Name:    "Resolver Surface",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:     "first",
				Kind:   "role",
				Runner: "test-runner",
				Decisions: definitions.DecisionList{
					{ID: "tagged_choice", Tags: []string{"override"}},
				},
			},
			{
				ID:     "second",
				Kind:   "role",
				Runner: "test-runner",
				Inputs: map[string]any{
					"first_status":   "${node.first.status}",
					"first_tags":     "${node.first.tags}",
					"first_attempts": "${node.first.attempts}",
				},
				Decisions: definitions.StringDecisions("done"),
			},
		},
		Edges: []definitions.Edge{{From: "first", To: "second"}},
	}
	runID := "resolver-surface-run-1"
	setupRunForResume(t, runsDir, runID, workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"tagged_choice","message":"with tag"}`, SessionID: "sess-1"},
			{Output: `{"decision":"done","message":"saw upstream fields"}`, SessionID: "sess-2"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := engine.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	if len(runner.requests) != 2 {
		t.Fatalf("expected 2 runner calls, got %d", len(runner.requests))
	}
	// The second runner call's prompt should mention the resolved input values
	// (status=completed, tags=[override], attempts=1). Check via the inputs
	// recorded in events.
	events := parseEvents(t, filepath.Join(runsDir, runID, "events.jsonl"))
	var inputsEvent *state.Event
	for i := range events {
		if events[i].Type == "node_inputs_resolved" && events[i].NodeID == "second" {
			inputsEvent = &events[i]
			break
		}
	}
	if inputsEvent == nil {
		t.Fatalf("PRI-1574: expected node_inputs_resolved event for second, found none")
	}
	raw, _ := json.Marshal(inputsEvent.Data)
	body := string(raw)
	if !strings.Contains(body, `"first_status":"completed"`) {
		t.Errorf("PRI-1574: expected first_status=completed in resolved inputs, got: %s", body)
	}
	if !strings.Contains(body, `"override"`) {
		t.Errorf("PRI-1574: expected override tag in resolved inputs, got: %s", body)
	}
	if !strings.Contains(body, `"first_attempts":1`) {
		t.Errorf("PRI-1574: expected first_attempts=1 in resolved inputs, got: %s", body)
	}
}

func dumpEventTypes(events []state.Event) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString("  ")
		b.WriteString(ev.Type)
		b.WriteString(" node_id=")
		b.WriteString(ev.NodeID)
		if errText, ok := ev.Data["error"].(string); ok {
			b.WriteString(" error=")
			b.WriteString(errText)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Avoid unused import warnings when nothing reads os in this file directly.
var _ = os.Stat
