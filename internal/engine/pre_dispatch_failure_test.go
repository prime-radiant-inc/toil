package engine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// Regression test for PRI-1570: a node whose input expression fails to
// resolve must produce a node_failed event with the actual error text,
// and NodeState.Error must be populated so buildFailureContext surfaces
// the root cause downstream (rather than the empty-failure_context
// "attempts:0, last_message:"" shape that swallows the diagnosis).
func TestExecuteSingle_PreDispatchResolveInputsFailure_EmitsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{{
			ID:   "consumer",
			Kind: "system",
			Inputs: map[string]any{
				// Field that the resolver doesn't know about — same
				// shape as the resolver gap surfaced during the
				// May 12, 2026 live test (see docs/superpowers/specs/
				// 2026-05-12-surgeon-as-judge-design.md).
				"surgeon_session": "node.plan_tasks.bogus_field",
			},
		}},
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	// Mark the upstream node completed so the resolver gets past the
	// "no such node" branch and lands on the unknown-field branch we want.
	runState.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
	})
	runContext := &RunContext{
		Inputs: runState.Inputs,
		Outputs: map[string]NodeOutput{
			"plan_tasks": {Decision: "approved", Message: "ok"},
		},
	}
	logger, logPath := newTestLogger(t)
	eng := &Engine{}

	_, err := eng.executeSingle(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", nil, runContext, logger, runState, nil, "")
	if err == nil {
		t.Fatal("expected resolve-inputs error, got nil")
	}

	// NodeState.Error must contain the resolver failure text so failure_context
	// can surface it.
	node := runState.Node("consumer")
	if node.Status != statusFailed {
		t.Fatalf("expected status %q, got %q", statusFailed, node.Status)
	}
	if !strings.Contains(node.Error, "bogus_field") {
		t.Fatalf("expected NodeState.Error to mention bogus_field, got %q", node.Error)
	}

	// failure_context must surface the error text.
	fc := buildFailureContext(runState, "consumer", t.TempDir())
	got, _ := fc["error"].(string)
	if !strings.Contains(got, "bogus_field") {
		t.Fatalf("expected failure_context.error to mention bogus_field, got %q", got)
	}

	// events.jsonl must contain a node_failed event with the error text so a
	// debugger can find the root cause without traversing engine source.
	_ = logger.Close()
	eventsBytes, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read events: %v", readErr)
	}
	var foundFailedWithError bool
	for _, line := range strings.Split(string(eventsBytes), "\n") {
		if line == "" {
			continue
		}
		var ev state.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type != "node_failed" || ev.NodeID != "consumer" {
			continue
		}
		if errText, ok := ev.Data["error"].(string); ok && strings.Contains(errText, "bogus_field") {
			foundFailedWithError = true
			break
		}
	}
	if !foundFailedWithError {
		t.Fatalf("expected node_failed event with error text mentioning bogus_field, got:\n%s", eventsBytes)
	}
}
