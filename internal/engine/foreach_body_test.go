package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestMatchEdgesExpr_ExpandedIDResolvesToTemplate(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.x", Item: "i", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	ctx := &EvalContext{Status: statusFailed}
	matched := matchEdgesExpr(workflow, "template::0", ctx)
	if len(matched) != 1 {
		t.Fatalf("expected 1 edge match for template::0, got %d", len(matched))
	}
	if matched[0].To != "orch" {
		t.Fatalf("expected edge to orch, got %q", matched[0].To)
	}
}

func TestMatchEdgesExpr_TemplatePrefixExtraction(t *testing.T) {
	// IDs without "::" behave as before.
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
		Edges: []definitions.Edge{{From: "a", To: "b", When: "default"}},
	}
	ctx := &EvalContext{Decision: "default"}
	matched := matchEdgesExpr(workflow, "a", ctx)
	if len(matched) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(matched))
	}
}

func TestMatchEdgesExpr_TemplateHandlesMultipleExpandedIndices(t *testing.T) {
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "next", When: "default"},
		},
	}
	ctx := &EvalContext{Decision: "default"}
	for _, id := range []string{"template::0", "template::1", "template::2"} {
		matched := matchEdgesExpr(workflow, id, ctx)
		if len(matched) != 1 {
			t.Fatalf("expected 1 edge for %s, got %d", id, len(matched))
		}
	}
	// Non-matching prefix
	matched := matchEdgesExpr(workflow, "other::0", ctx)
	if len(matched) != 0 {
		t.Fatalf("expected 0 edges for other::0, got %d", len(matched))
	}
	// Sanity — keep strings.Contains usage to prevent unused-import removal later
	_ = strings.Contains
}

func TestForEachBody_ExpandsItemsUsingTemplate(t *testing.T) {
	_ = io.Discard // prevent unused-import removal in future edits
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{
				ID: "orch",
				ForEach: &definitions.ForEach{
					List: "input.items", Item: "item", Body: "template",
				},
			},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b", "c"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}

	// Expanded nodes use the TEMPLATE ID, not the orchestrator ID.
	for _, suffix := range []string{"::0", "::1", "::2"} {
		status, exists := runState.NodeStatus("template" + suffix)
		if !exists {
			t.Fatalf("expected expanded node template%s, not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected template%s completed, got %q", suffix, status)
		}
	}

	// Orchestrator itself completes
	status, _ := runState.NodeStatus("orch")
	if status != statusCompleted {
		t.Fatalf("expected orch completed, got %q", status)
	}
}

func TestForEachBody_ItemsArrayHasStatusAndExpandedID(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items []map[string]any, got %T", output.Data["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for idx, item := range items {
		if item["status"] != "succeeded" {
			t.Errorf("item %d: expected status=succeeded, got %v", idx, item["status"])
		}
		wantExpanded := fmt.Sprintf("template::%d", idx)
		if item["expanded_id"] != wantExpanded {
			t.Errorf("item %d: expected expanded_id=%q, got %v", idx, wantExpanded, item["expanded_id"])
		}
		if item["id"] != fmt.Sprintf("%d", idx) {
			t.Errorf("item %d: expected id=%q, got %v", idx, fmt.Sprintf("%d", idx), item["id"])
		}
	}
}

func TestForEachBody_ConcurrentFailureEdgeAbsorbsFailure(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{
				ID:   "template",
				Kind: "system",
				// Tests inject a failure via the "fail_on_item" magic marker.
			},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"ok-a", "__FAIL__", "ok-c"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("expected ForEach to settle (no error), got %v", err)
	}
	items := output.Data["items"].([]map[string]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[1]["status"] != "failed-handled" {
		t.Errorf("expected item 1 status=failed-handled, got %v", items[1]["status"])
	}
	if _, ok := items[1]["failure_context"]; !ok {
		t.Errorf("expected failure_context on failed item, got keys %v", keysOf(items[1]))
	}
	if items[0]["status"] != "succeeded" || items[2]["status"] != "succeeded" {
		t.Errorf("expected siblings to succeed")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestForEachBody_DecisionAllSucceeded(t *testing.T) {
	output := runForEachWithStatuses(t, []string{"succeeded", "succeeded"})
	if output.Decision != "all_succeeded" {
		t.Fatalf("expected all_succeeded, got %q", output.Decision)
	}
}

func TestForEachBody_DecisionSomeFailed(t *testing.T) {
	output := runForEachWithStatuses(t, []string{"succeeded", "failed-handled"})
	if output.Decision != "some_failed" {
		t.Fatalf("expected some_failed, got %q", output.Decision)
	}
}

func TestForEachBody_DecisionAllFailed(t *testing.T) {
	output := runForEachWithStatuses(t, []string{"failed-handled", "failed-handled"})
	if output.Decision != "all_failed" {
		t.Fatalf("expected all_failed, got %q", output.Decision)
	}
}

func TestForEachBody_DecisionEmptyList(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}
	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}
	if output.Decision != "all_succeeded" {
		t.Fatalf("expected all_succeeded for empty list, got %q", output.Decision)
	}
}

// runForEachWithStatuses runs a 2-item ForEach where each item's outcome is
// controlled by the given want slice: "succeeded" runs normally; anything
// else triggers the failOnItem hook.
func runForEachWithStatuses(t *testing.T, want []string) NodeOutput {
	t.Helper()
	dir := t.TempDir()
	items := make([]any, len(want))
	for i, w := range want {
		if w == "succeeded" {
			items[i] = fmt.Sprintf("ok-%d", i)
		} else {
			items[i] = "__FAIL__"
		}
	}
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": items})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}
	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	return output
}

func TestForEachBody_DAGSkipsDependentsOfFailedItem(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "template",
			}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	// item-0 fails; item-1 depends on item-0 and must skip; item-2 is independent.
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "__FAIL__"},
			map[string]any{"id": "b", "item": "ok-b", "depends_on": []any{"a"}},
			map[string]any{"id": "c", "item": "ok-c"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}
	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("expected no error (failure edge present), got %v", err)
	}
	items := output.Data["items"].([]map[string]any)
	if items[0]["status"] != "failed-handled" {
		t.Errorf("expected item 0 failed-handled, got %v", items[0]["status"])
	}
	if items[1]["status"] != "skipped" {
		t.Errorf("expected item 1 skipped, got %v", items[1]["status"])
	}
	if _, hasReason := items[1]["reason"]; !hasReason {
		t.Errorf("expected skipped item to have 'reason'")
	}
	if items[2]["status"] != "succeeded" {
		t.Errorf("expected item 2 succeeded, got %v", items[2]["status"])
	}
}

func TestForEachBody_ConcurrentNoFailureEdgeStillFailsFast(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
		// No failure edge
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"ok", "__FAIL__"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err == nil {
		t.Fatalf("expected error when no failure edge exists")
	}
}

func TestForEachBody_PersistsItemsToNodeState(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}

	// Verify items[] is persisted to the orchestrator's NodeState.Data.
	// This is what lets RunContextFromState rebuild the output on resume.
	var data map[string]any
	var message, decision string
	runState.WithNode("orch", func(n *state.NodeState) {
		data = n.Data
		message = n.Message
		decision = n.Decision
	})
	if data == nil {
		t.Fatalf("expected orch NodeState.Data to be populated, got nil")
	}
	items, ok := data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected orch NodeState.Data[items] to be []map[string]any, got %T", data["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["expanded_id"] != "template::0" {
		t.Errorf("expected item[0].expanded_id=template::0, got %v", items[0]["expanded_id"])
	}
	if decision != "all_succeeded" {
		t.Errorf("expected decision=all_succeeded, got %q", decision)
	}
	if message == "" {
		t.Errorf("expected non-empty message, got empty")
	}
}

func TestForEachBody_PersistsEmptyItemsToNodeState(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	var data map[string]any
	runState.WithNode("orch", func(n *state.NodeState) {
		data = n.Data
	})
	if data == nil {
		t.Fatalf("expected NodeState.Data populated even for empty ForEach")
	}
	items, ok := data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected empty items slice, got %T", data["items"])
	}
	if len(items) != 0 {
		t.Fatalf("expected empty items, got %d", len(items))
	}
}

func TestForEachBody_TemplateNotStartNode(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "template", Kind: "system"},
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
		},
	}
	starts := startNodes(workflow)
	for _, s := range starts {
		if s.ID == "template" {
			t.Fatalf("template should not be a start node; got start list %+v", starts)
		}
	}
	found := false
	for _, s := range starts {
		if s.ID == "orch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("orch should be a start node; got %+v", starts)
	}
}

func TestForEachBody_ReexecutionResetsExpandedItems(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"first-a", "first-b"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	// First pass: 2 items
	firstOutput, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	firstItems := firstOutput.Data["items"].([]map[string]any)
	if len(firstItems) != 2 {
		t.Fatalf("first pass: expected 2 items, got %d", len(firstItems))
	}

	// Verify orchestrator is marked completed (setup for re-execution detection)
	var orchStatus string
	runState.WithNode("orch", func(n *state.NodeState) {
		orchStatus = n.Status
	})
	if orchStatus != statusCompleted {
		t.Fatalf("expected orch statusCompleted after first pass, got %q", orchStatus)
	}

	// Verify expanded items are completed
	for _, id := range []string{"template::0", "template::1"} {
		status, _ := runState.NodeStatus(id)
		if status != statusCompleted {
			t.Fatalf("expected %s statusCompleted after first pass, got %q", id, status)
		}
	}

	// Second pass: different item count (1 item instead of 2)
	runState.Inputs["items"] = []any{"second-a"}
	runContext.Inputs = runState.Inputs
	secondOutput, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	secondItems := secondOutput.Data["items"].([]map[string]any)
	if len(secondItems) != 1 {
		t.Fatalf("second pass: expected 1 item (new list has 1), got %d", len(secondItems))
	}

	// Without the reset fix, template::0 would skip execution (statusCompleted from
	// pass 1) and the aggregated items[] would still reflect pass 1's data. With the
	// fix, the orchestrator's persisted NodeState.Data[items] must match the second
	// pass's item count.
	var orchData map[string]any
	runState.WithNode("orch", func(n *state.NodeState) {
		orchData = n.Data
	})
	orchItems := orchData["items"].([]map[string]any)
	if len(orchItems) != 1 {
		t.Fatalf("expected orch NodeState.Data[items] to be updated to 1 item after pass 2, got %d", len(orchItems))
	}
	if orchItems[0]["expanded_id"] != "template::0" {
		t.Errorf("expected item 0 expanded_id=template::0, got %v", orchItems[0]["expanded_id"])
	}
}

func TestForEachBody_IntegrationFailureToOrchestrator(t *testing.T) {
	// Three tasks: two succeed, one fails. Orchestrator decision is some_failed,
	// items[] has the failed item with failure_context. This is the build_component
	// use case in miniature.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.tasks", Item: "item", Body: "implement",
			}},
			{ID: "implement", Kind: "system"},
			{ID: "recover", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "implement", To: "orch", When: "status == 'failed'"},
			{From: "orch", To: "recover", When: "some_failed"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"tasks": []any{"task-a", "__FAIL__", "task-c"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	if output.Decision != "some_failed" {
		t.Fatalf("expected some_failed decision, got %q", output.Decision)
	}
	items := output.Data["items"].([]map[string]any)
	if items[1]["status"] != "failed-handled" {
		t.Fatalf("expected item 1 failed-handled, got %v", items[1]["status"])
	}
	// Verify matchEdgesExpr would route correctly for this output
	ctx := &EvalContext{Decision: "some_failed"}
	edges := matchEdgesExpr(workflow, "orch", ctx)
	if len(edges) != 1 || edges[0].To != "recover" {
		t.Fatalf("expected orch -> recover edge, got %v", edges)
	}
}

func TestForEachBody_UnhandledFailurePersistsItems(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
		// No failure edge on the template → failures are unhandled
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"ok", "__FAIL__"},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err == nil {
		t.Fatalf("expected unhandled failure to propagate")
	}

	// Orchestrator should be marked failed
	var orchStatus string
	var orchData map[string]any
	runState.WithNode("orch", func(n *state.NodeState) {
		orchStatus = n.Status
		orchData = n.Data
	})
	if orchStatus != statusFailed {
		t.Fatalf("expected orch status=failed, got %q", orchStatus)
	}

	// But items[] should be persisted so a recovery edge can see per-item context
	if orchData == nil {
		t.Fatalf("expected orch NodeState.Data populated on failure, got nil")
	}
	items, ok := orchData["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in NodeState.Data, got %T", orchData["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (1 ok, 1 failed), got %d", len(items))
	}
	// The failing item (index 1) should have status=failed and failure_context
	if items[1]["status"] != outcomeFailed {
		t.Errorf("expected item 1 status=failed, got %v", items[1]["status"])
	}
	if _, has := items[1]["failure_context"]; !has {
		t.Errorf("expected item 1 to have failure_context even though unhandled")
	}
}

func TestHasTemplateFailureEdge_MalformedExpressionNotAbsorbed(t *testing.T) {
	// Regression guard: an expression that fails to parse/tokenize must NOT
	// silently become a failure edge. Otherwise a typo absorbs genuine
	// failures as failed-handled and the orchestrator reports aggregate
	// success instead of surfacing the error.
	cases := []string{
		"status == 'failed",       // unterminated string
		"status == 'failed' && @", // invalid token
	}
	for _, when := range cases {
		t.Run(when, func(t *testing.T) {
			workflow := &definitions.Workflow{
				Nodes: []definitions.Node{
					{ID: "orch", ForEach: &definitions.ForEach{Body: "tmpl"}},
					{ID: "tmpl", Kind: "system"},
				},
				Edges: []definitions.Edge{{From: "tmpl", To: "orch", When: when}},
			}
			if hasTemplateFailureEdge(workflow, "tmpl", nil) {
				t.Fatalf("malformed expression %q must not be treated as failure edge", when)
			}
		})
	}
}

func TestHasTemplateFailureEdge_RejectsDefaultAndEmptyEdges(t *testing.T) {
	// A default-when or empty-when edge from the template must NOT count as a
	// failure edge. Only expression-based edges that match failed status do.
	cases := []struct {
		name   string
		when   string
		isFail bool
	}{
		{"empty_when", "", false},
		{"default_when", "default", false},
		{"decision_when", "approved", false},
		{"failure_expression", "status == 'failed'", true},
		{"complex_failure_expression", "status == 'failed' && decision == ''", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			workflow := &definitions.Workflow{
				Nodes: []definitions.Node{
					{ID: "orch", ForEach: &definitions.ForEach{Body: "tmpl"}},
					{ID: "tmpl", Kind: "system"},
				},
				Edges: []definitions.Edge{{From: "tmpl", To: "orch", When: c.when}},
			}
			got := hasTemplateFailureEdge(workflow, "tmpl", nil)
			if got != c.isFail {
				t.Fatalf("when=%q: hasTemplateFailureEdge returned %v, want %v", c.when, got, c.isFail)
			}
		})
	}
}

func TestForEachBody_TransientErrorsPropagateThroughFailureEdge(t *testing.T) {
	// When a template has a failure edge AND executeSingle returns a transient
	// error (ErrSubworkflowInProgress or ErrApprovalPending), the executor
	// must NOT absorb it as failed-handled — it must propagate so the
	// outer executeForEach's transient-wait handling kicks in.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "subworkflow", Workflow: "child"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	childWorkflow := &definitions.Workflow{
		ID:    "child",
		Nodes: []definitions.Node{{ID: "noop", Kind: "system"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"wf": workflow, "child": childWorkflow}}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{Definitions: bundle, RunsDir: dir}

	// Pre-seed: the expanded item has a prior child_run still marked running,
	// so executeSubworkflow returns ErrSubworkflowInProgress.
	runState.WithNode("template::0", func(n *state.NodeState) {
		n.Status = statusRunning
		n.Data = map[string]any{"child_run": "child-run-in-progress"}
	})
	// Write the child state.json so LoadState succeeds
	childRunDir := filepath.Join(dir, "child-run-in-progress")
	if err := os.MkdirAll(childRunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childState := state.NewRunState("child-run-in-progress", "child", nil)
	childState.Status = "running"
	if err := state.SaveState(filepath.Join(childRunDir, "state.json"), childState); err != nil {
		t.Fatal(err)
	}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if !errors.Is(err, ErrSubworkflowInProgress) {
		t.Fatalf("expected ErrSubworkflowInProgress to propagate through failure edge, got %v", err)
	}
}

func TestHasTemplateFailureEdge_ResolvesInputAndNodeExpressions(t *testing.T) {
	// Regression guard: hasTemplateFailureEdge must receive a RunContext so
	// expressions that reference inputs AND node outputs can resolve.
	// Without it, status == 'failed' && node.X.data.Y evaluates as nil and
	// the function returns false — template failure edges silently stop
	// absorbing.

	t.Run("input_expression", func(t *testing.T) {
		workflow := &definitions.Workflow{
			Nodes: []definitions.Node{
				{ID: "orch", ForEach: &definitions.ForEach{Body: "tmpl"}},
				{ID: "tmpl", Kind: "system"},
			},
			Edges: []definitions.Edge{{
				From: "tmpl", To: "orch",
				When: "status == 'failed' && input.mode == 'absorb'",
			}},
		}
		absorb := &RunContext{Inputs: map[string]any{"mode": "absorb"}, Outputs: map[string]NodeOutput{}}
		if !hasTemplateFailureEdge(workflow, "tmpl", absorb) {
			t.Fatalf("expected detection with input.mode=absorb")
		}
		propagate := &RunContext{Inputs: map[string]any{"mode": "propagate"}, Outputs: map[string]NodeOutput{}}
		if hasTemplateFailureEdge(workflow, "tmpl", propagate) {
			t.Fatalf("expected no detection with input.mode=propagate")
		}
	})

	t.Run("node_expression", func(t *testing.T) {
		// Exactly the scenario the Fix 17 commit message flags: an expression
		// referencing node.X.data.Y. Validates the RunContext.Outputs is
		// threaded through.
		workflow := &definitions.Workflow{
			Nodes: []definitions.Node{
				{ID: "preflight", Kind: "system"},
				{ID: "orch", ForEach: &definitions.ForEach{Body: "tmpl"}},
				{ID: "tmpl", Kind: "system"},
			},
			Edges: []definitions.Edge{{
				From: "tmpl", To: "orch",
				When: "status == 'failed' && node.preflight.data.retry == 'yes'",
			}},
		}
		retry := &RunContext{
			Inputs: map[string]any{},
			Outputs: map[string]NodeOutput{
				"preflight": {Data: map[string]any{"retry": "yes"}},
			},
		}
		if !hasTemplateFailureEdge(workflow, "tmpl", retry) {
			t.Fatalf("expected detection with node.preflight.data.retry=yes")
		}
		noRetry := &RunContext{
			Inputs: map[string]any{},
			Outputs: map[string]NodeOutput{
				"preflight": {Data: map[string]any{"retry": "no"}},
			},
		}
		if hasTemplateFailureEdge(workflow, "tmpl", noRetry) {
			t.Fatalf("expected no detection with node.preflight.data.retry=no")
		}
	})
}

func TestDAG_TransientErrorAllowsSiblingsToComplete(t *testing.T) {
	// Runtime regression guard: a per-item ErrApprovalPending must NOT call
	// dagCancel — sibling goroutines should complete their own work
	// independently. Before Fix 4 (round 4), one item's pending approval
	// would cancel its siblings mid-flight.
	//
	// Iterate to catch goroutine scheduling variants: if dagCancel were
	// spuriously called on the transient path, the sibling's ctx.Err()
	// check at the top of executeSingle would occasionally return
	// context.Canceled depending on timing. Looping exposes this.
	for iter := 0; iter < 20; iter++ {
		dir := t.TempDir()
		workflow := &definitions.Workflow{
			ID: "wf",
			Nodes: []definitions.Node{
				{ID: "orch", ForEach: &definitions.ForEach{
					List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
				}},
				{ID: "tmpl", Kind: "system"},
			},
		}
		runState := state.NewRunState(testRunID1, "wf", map[string]any{
			"items": []any{
				map[string]any{"id": "a", "item": "__PEND__"},
				map[string]any{"id": "b", "item": "ok-b"},
			},
		})
		runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
		logger, _ := newTestLogger(t)
		eng := &Engine{transientPendingOnItem: "__PEND__"}

		_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
		if !errors.Is(err, ErrApprovalPending) {
			_ = logger.Close()
			t.Fatalf("iter %d: expected ErrApprovalPending to propagate, got %v", iter, err)
		}
		statusB, _ := runState.NodeStatus("tmpl::1")
		if statusB != statusCompleted {
			_ = logger.Close()
			t.Fatalf("iter %d: sibling item b should have completed, got status %q", iter, statusB)
		}
		var msgB string
		runState.WithNode("tmpl::1", func(n *state.NodeState) { msgB = n.Message })
		if strings.Contains(strings.ToLower(msgB), "cancel") {
			_ = logger.Close()
			t.Fatalf("iter %d: item b has cancellation-tainted message: %q", iter, msgB)
		}
		_ = logger.Close()
	}
}

func TestMarkSkipped_EmitsEventsAndFlushesState(t *testing.T) {
	// Regression guard: markSkipped must flush state to disk AND emit
	// node_skipped events. Both invariants verified end-to-end.
	dir := t.TempDir()
	states := make([]forEachItemState, 3)
	for i := range states {
		states[i] = forEachItemState{ID: fmt.Sprintf("%d", i), ExpandedID: fmt.Sprintf("tmpl::%d", i)}
	}
	deps := map[int][]int{0: {1}, 1: {2}}
	runState := state.NewRunState("run-x", "wf", nil)
	for _, id := range []string{"tmpl::0", "tmpl::1", "tmpl::2"} {
		runState.WithNode(id, func(n *state.NodeState) { n.Status = statusPending })
	}
	logger, logPath := newTestLogger(t)

	if err := state.SaveState(filepath.Join(dir, "state.json"), runState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	markSkipped(states, deps, 0,
		[]string{"a", "b", "c"}, runState,
		[]string{"tmpl::0", "tmpl::1", "tmpl::2"},
		dir, "run-x", logger, "", false, nil)
	_ = logger.Close()

	// On-disk state: tmpl::1 and tmpl::2 must persist as statusSkipped.
	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, id := range []string{"tmpl::1", "tmpl::2"} {
		status, _ := loaded.NodeStatus(id)
		if status != statusSkipped {
			t.Errorf("%s: expected persisted status=skipped, got %q", id, status)
		}
	}

	// Event log: two node_skipped events must have been emitted.
	eventsBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile events: %v", err)
	}
	skippedCount := 0
	for _, line := range strings.Split(string(eventsBytes), "\n") {
		if strings.Contains(line, `"type":"node_skipped"`) {
			skippedCount++
		}
	}
	if skippedCount != 2 {
		t.Errorf("expected 2 node_skipped events, got %d; events: %s", skippedCount, string(eventsBytes))
	}
}

func TestDAG_CancellationPropagatesAsError(t *testing.T) {
	// Regression guard: a fully-cancelled DAG ForEach must NOT report
	// all_succeeded. Before Fix R1#2, the cancel branch in executeForEachDAG
	// didn't set firstUnhandledErr, so the orchestrator completed with
	// decision=all_succeeded (skipped items don't count as failed).
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "ok-a"},
			map[string]any{"id": "b", "item": "ok-b"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	// Cancel ctx before dispatch — executeSingle's top-of-function ctx.Err()
	// guard returns context.Canceled to every item, exercising the cancel
	// branch in executeForEachDAG.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from fully-cancelled DAG ForEach, got %v", err)
	}
	orchStatus, _ := runState.NodeStatus("orch")
	if orchStatus != statusFailed {
		t.Fatalf("orchestrator status after cancellation: got %q, want %q", orchStatus, statusFailed)
	}
}

func TestForEachBody_EmptyListReExecutionCleansOrphans(t *testing.T) {
	// Regression guard: when a ForEach re-executes with an empty list AFTER
	// a prior pass produced expanded items, the orphan cleanup must still
	// fire. Before the fix, the empty-list early-return ran BEFORE the
	// cleanup block, so orphans leaked into state.json.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "tmpl"}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a", "b", "c"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	if _, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	// Re-execute with empty list.
	runState.Inputs["items"] = []any{}
	if _, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	for _, orphan := range []string{"tmpl::0", "tmpl::1", "tmpl::2"} {
		if _, ok := runState.NodeStatus(orphan); ok {
			t.Errorf("expected orphan %s cleaned up after empty re-exec (in-memory)", orphan)
		}
	}
	// Verify persistence: a crash here mustn't resurrect orphans from disk.
	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, orphan := range []string{"tmpl::0", "tmpl::1", "tmpl::2"} {
		if _, ok := loaded.NodeStatus(orphan); ok {
			t.Errorf("expected orphan %s absent from state.json (on-disk)", orphan)
		}
	}
}

func TestForEachBody_DownstreamResetCleansOrphansOnReexec(t *testing.T) {
	// When retrigger of an upstream node calls resetNodeState on a
	// downstream orchestrator, the orchestrator's status becomes
	// statusPending. On its next execution with fewer items, the orphan
	// cleanup must still fire even though reexecuting is false.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "tmpl"}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a", "b", "c"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	// First pass.
	if _, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	// Simulate retrigger's resetNodeState on the downstream orchestrator
	// AND its expanded children, matching RetriggerNode's actual semantics
	// (it walks downstream + templateToOrch to include ForEach children).
	runState.WithNode("orch", func(n *state.NodeState) {
		resetNodeState(n)
	})
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, n := range nodes {
			if forEachParentID(id) == "tmpl" {
				resetNodeState(n)
			}
		}
	})

	// Re-execute with only 1 item.
	runState.Inputs["items"] = []any{"only-one"}
	if _, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState); err != nil {
		t.Fatalf("downstream-reset pass: %v", err)
	}

	if _, ok := runState.NodeStatus("tmpl::0"); !ok {
		t.Error("expected tmpl::0 to exist after re-exec")
	}
	for _, orphan := range []string{"tmpl::1", "tmpl::2"} {
		if _, ok := runState.NodeStatus(orphan); ok {
			t.Errorf("expected orphan %s cleaned up after downstream-reset re-exec (in-memory)", orphan)
		}
	}
	// On-disk verification: a crash here mustn't resurrect orphans.
	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, orphan := range []string{"tmpl::1", "tmpl::2"} {
		if _, ok := loaded.NodeStatus(orphan); ok {
			t.Errorf("expected orphan %s absent from state.json (on-disk)", orphan)
		}
	}
}

// Note: a dedicated runtime test for context.DeadlineExceeded is not
// included here — system-kind nodes complete before the deadline fires,
// and mocking a blocking runner to trigger real deadline propagation is
// more machinery than the invariant warrants. The DeadlineExceeded
// classification is structurally covered by the shared
// `errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded)`
// clauses in both executors; TestDAG_CancellationPropagatesAsError
// exercises the sibling context.Canceled path, which is the same code.

func TestForEachBody_SkippedRoundTripsOnResume(t *testing.T) {
	// A skipped item (DAG dependent whose ancestor failed) must survive
	// a save/load round-trip without being re-launched on resume. This
	// is the build_component crash-mid-ForEach scenario.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "template",
			}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "__FAIL__"},
			map[string]any{"id": "b", "item": "ok", "depends_on": []any{"a"}},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	out1, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	items1 := out1.Data["items"].([]map[string]any)
	if items1[1]["status"] != statusSkipped {
		t.Fatalf("first pass: expected item 1 skipped, got %v", items1[1]["status"])
	}

	// Real save/load round-trip.
	statePath := filepath.Join(dir, "state.json")
	if err := state.SaveState(statePath, runState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	loaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	// Flip orchestrator back to running to simulate a crash-resume.
	loaded.WithNode("orch", func(n *state.NodeState) { n.Status = statusRunning })
	// Set up a fresh runContext sourced from the reloaded state.
	newRunContext := &RunContext{Inputs: loaded.Inputs, Outputs: map[string]NodeOutput{}}
	// Clear the fail hook so if item 0 re-runs it would succeed; with the fix
	// it's short-circuited as failed-handled.
	eng.failOnItem = ""

	out2, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, newRunContext, logger, loaded)
	if err != nil {
		t.Fatalf("resume pass: %v", err)
	}
	items2 := out2.Data["items"].([]map[string]any)
	if items2[0]["status"] != outcomeFailedHandled {
		t.Fatalf("resume: expected item 0 failed-handled, got %v", items2[0]["status"])
	}
	if items2[1]["status"] != statusSkipped {
		t.Fatalf("resume: expected item 1 still skipped, got %v", items2[1]["status"])
	}
	if out2.Decision != "all_failed" {
		t.Fatalf("resume: expected aggregate decision all_failed, got %q", out2.Decision)
	}
}

func TestForEachBody_OrchestratorStartedAtPreservedAcrossResume(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	// First pass completes normally.
	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	var firstStart *time.Time
	runState.WithNode("orch", func(n *state.NodeState) { firstStart = n.StartedAt })
	if firstStart == nil {
		t.Fatal("first pass: StartedAt should be set")
	}

	// Simulate a resume (still-running status, StartedAt preserved).
	runState.WithNode("orch", func(n *state.NodeState) { n.Status = statusRunning })

	// Force a delay so Now() would differ if StartedAt were overwritten.
	time.Sleep(5 * time.Millisecond)

	_, err = eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("resume pass: %v", err)
	}
	var secondStart *time.Time
	runState.WithNode("orch", func(n *state.NodeState) { secondStart = n.StartedAt })
	if secondStart == nil || !secondStart.Equal(*firstStart) {
		t.Fatalf("resume: StartedAt changed; first=%v, second=%v", firstStart, secondStart)
	}
}

func TestForEachBody_OrphanedExpandedNodesCleanedOnReexecution(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a", "b", "c"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	// First pass: 3 items.
	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	for _, id := range []string{"template::0", "template::1", "template::2"} {
		if _, ok := runState.NodeStatus(id); !ok {
			t.Fatalf("first pass: expected %s to exist", id)
		}
	}

	// Re-execute with only 1 item.
	runState.Inputs["items"] = []any{"just-one"}
	_, err = eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("re-exec: %v", err)
	}
	// template::0 should survive; template::1 and template::2 should be gone.
	if _, ok := runState.NodeStatus("template::0"); !ok {
		t.Error("expected template::0 after re-exec")
	}
	for _, orphan := range []string{"template::1", "template::2"} {
		if _, ok := runState.NodeStatus(orphan); ok {
			t.Errorf("expected orphan %s cleaned up after re-exec", orphan)
		}
	}
}

func TestForEachBody_OrchestratorLifecycleTimestamps(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"a", "b"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	var started, ended *time.Time
	runState.WithNode("orch", func(n *state.NodeState) {
		started = n.StartedAt
		ended = n.EndedAt
	})
	if started == nil {
		t.Fatalf("orchestrator should have StartedAt set")
	}
	if ended == nil {
		t.Fatalf("orchestrator should have EndedAt set")
	}
	if ended.Before(*started) {
		t.Fatalf("EndedAt (%v) before StartedAt (%v)", *ended, *started)
	}
}

func TestForEachBody_SkippedItemsStateIsSkipped(t *testing.T) {
	// Skipped expanded items should have NodeState.Status=skipped, not pending.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "template",
			}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "__FAIL__"},
			map[string]any{"id": "b", "item": "ok", "depends_on": []any{"a"}},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}
	// item b was skipped because its dep a failed. Its NodeState must reflect this.
	status, _ := runState.NodeStatus("template::1")
	if status != statusSkipped {
		t.Fatalf("expected template::1 status=skipped, got %q", status)
	}
	var msg string
	runState.WithNode("template::1", func(n *state.NodeState) {
		msg = n.Message
	})
	if msg == "" {
		t.Errorf("expected skipped node to have a message identifying the failed dependency")
	}
}

func TestForEachBody_FailedHandledRoundTripsOnResume(t *testing.T) {
	// An item absorbed as failed-handled persists its status to NodeState so
	// resume doesn't re-run it. On the second pass, the short-circuit returns
	// the stored outcome instead of re-dispatching.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{"__FAIL__", "ok"}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{failOnItem: "__FAIL__"}

	// First pass: item 0 fails-handled, item 1 succeeds, orchestrator ends some_failed.
	out, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if out.Decision != "some_failed" {
		t.Fatalf("first pass: expected some_failed, got %q", out.Decision)
	}

	// Verify NodeState for item 0 is statusFailedHandled, not statusFailed.
	var item0Status string
	runState.WithNode("template::0", func(n *state.NodeState) {
		item0Status = n.Status
	})
	if item0Status != statusFailedHandled {
		t.Fatalf("expected template::0 status=failed-handled after first pass, got %q", item0Status)
	}

	// Simulate a resume: flip orchestrator back to running (as after a crash
	// mid-flight), clear the runContext, then re-execute the orchestrator.
	runState.WithNode("orch", func(n *state.NodeState) {
		n.Status = statusRunning
	})
	runContext.Outputs = map[string]NodeOutput{}

	// Remove the failOnItem hook so that if item 0 re-runs, it would succeed.
	// If the short-circuit works, the item is NOT re-run and stays failed-handled.
	eng.failOnItem = ""

	out2, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("resume pass: %v", err)
	}
	// With the fix, item 0 was short-circuited as failed-handled. Without the
	// fix, it would re-run and succeed, flipping the decision to all_succeeded.
	if out2.Decision != "some_failed" {
		t.Fatalf("resume: expected some_failed (item 0 short-circuited as failed-handled), got %q", out2.Decision)
	}
	items := out2.Data["items"].([]map[string]any)
	if items[0]["status"] != outcomeFailedHandled {
		t.Fatalf("resume: expected item 0 failed-handled, got %v", items[0]["status"])
	}
	if _, has := items[0]["failure_context"]; !has {
		t.Errorf("resume: expected item 0 to retain failure_context, got keys %v", keysOf(items[0]))
	}
}

func TestForEachBody_EmptyListCancellationEmitsTerminator(t *testing.T) {
	// Regression guard: when an empty-list ForEach is invoked with an
	// already-cancelled ctx, the orchestrator must (a) not record itself
	// as completed/all_succeeded, and (b) emit a node_skipped terminator
	// event so consumers don't see a dangling node_started.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "tmpl"}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{"items": []any{}})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, logPath := newTestLogger(t)
	eng := &Engine{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	_ = logger.Close()

	orchStatus, _ := runState.NodeStatus("orch")
	if orchStatus != statusSkipped {
		t.Fatalf("expected orch status %q, got %q", statusSkipped, orchStatus)
	}
	eventsBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile events: %v", err)
	}
	startedCount := 0
	skippedCount := 0
	for _, line := range strings.Split(string(eventsBytes), "\n") {
		if strings.Contains(line, `"type":"node_started"`) && strings.Contains(line, `"node_id":"orch"`) {
			startedCount++
		}
		if strings.Contains(line, `"type":"node_skipped"`) && strings.Contains(line, `"node_id":"orch"`) {
			skippedCount++
		}
	}
	if startedCount != 1 {
		t.Errorf("expected 1 node_started for orch, got %d", startedCount)
	}
	if skippedCount != 1 {
		t.Errorf("expected 1 node_skipped (terminator) for orch, got %d", skippedCount)
	}
}

func TestExecuteSingle_CancelledCtxReturnsImmediately(t *testing.T) {
	// Regression guard: executeSingle must check ctx.Err() at top so a
	// cancelled context propagates as context.Canceled before any node
	// state is mutated. Without this, the DAG cancel branch in
	// executeForEachDAG would never fire on cancellation that races with
	// item dispatch.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{{ID: "n", Kind: "system"}},
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{Inputs: map[string]any{}, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.executeSingle(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", nil, runContext, logger, runState, nil, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	status, exists := runState.NodeStatus("n")
	if exists && status == statusRunning {
		t.Fatalf("node should not have been marked running after pre-cancelled ctx, got status %q", status)
	}
}

func TestDAG_ResumeOfFailedHandledItemMarksDependentsSkipped(t *testing.T) {
	// Regression guard: on resume, a DAG item whose persisted status is
	// failed-handled must NOT unblock its dependents. The failure was
	// absorbed, not succeeded — dependents gated on success should be
	// marked skipped.
	//
	// Pre-fix: the resume pre-resolution loop called d.resolve() for
	// every settled item, so a crashed-before-dependent-persisted run
	// would re-launch dependents on resume.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "tmpl", To: "orch", When: "status == 'failed'"},
		},
	}
	// item a (failed-handled on resume); item b depends on a.
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "ok"},
			map[string]any{"id": "b", "item": "ok", "depends_on": []any{"a"}},
		},
	})

	// Simulate a crash-then-resume where tmpl::0 is persisted as
	// failed-handled but tmpl::1 has no persisted state.
	runState.WithNode("tmpl::0", func(n *state.NodeState) {
		n.Status = statusFailedHandled
		n.Message = "something went wrong"
	})
	// Flip orch back to running to mimic resume.
	runState.WithNode("orch", func(n *state.NodeState) {
		n.Status = statusRunning
	})

	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}

	// Item b must end up statusSkipped — its dependency a was
	// failed-handled, so b should not have run.
	statusB, ok := runState.NodeStatus("tmpl::1")
	if !ok {
		t.Fatalf("expected tmpl::1 to exist after resume")
	}
	if statusB != statusSkipped {
		t.Fatalf("expected tmpl::1 statusSkipped after resume (dep was failed-handled), got %q", statusB)
	}
}

func TestDAG_CancelledItemMarksDependentsSkipped(t *testing.T) {
	// Regression guard: when an ancestor item is cancelled, its
	// transitive dependents must be marked statusSkipped — not left in
	// statusPending. Without this, items[] accounting undercounts and on
	// resume the orchestrator may consider these pending items still
	// actionable.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "ok-a"},
			map[string]any{"id": "b", "item": "ok-b"},
			map[string]any{"id": "c", "item": "ok-c", "depends_on": []any{"a"}},
			map[string]any{"id": "d", "item": "ok-d", "depends_on": []any{"a"}},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for i, expectedID := range []string{"tmpl::0", "tmpl::1", "tmpl::2", "tmpl::3"} {
		status, ok := loaded.NodeStatus(expectedID)
		if !ok {
			t.Errorf("item %d (%s): expected node to exist on disk, missing", i, expectedID)
			continue
		}
		if status != statusSkipped {
			t.Errorf("item %d (%s): expected statusSkipped, got %q", i, expectedID, status)
		}
	}
}

func TestDAG_TransientThenCancelPropagatesAsCancelled(t *testing.T) {
	// Regression guard for round-10 finding: when item A returns transient
	// (ErrApprovalPending) first, then item B is cancelled via ctx, the
	// orchestrator must propagate context.Canceled (operator intent) up,
	// NOT the prior transient error. Pre-fix: cancel branch's
	// `if firstUnhandledErr == nil` fell through, so the orchestrator
	// returned ErrApprovalPending — a user who just cancelled the run
	// would see it labeled as still-pending-approval.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "__PEND__"},
			map[string]any{"id": "b", "item": "ok-b"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{transientPendingOnItem: "__PEND__"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if errors.Is(err, ErrApprovalPending) || errors.Is(err, ErrSubworkflowInProgress) {
		t.Fatalf("transient error masked cancellation; got %v, want context.Canceled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDAG_TransientThenGenuineFailurePropagatesAndCancels(t *testing.T) {
	// Regression guard for round-8 finding: when item A returns a
	// transient wait (ErrApprovalPending), then item B genuinely fails,
	// dagCancel MUST fire (so in-flight siblings stop) AND the
	// orchestrator MUST propagate B's genuine error (not A's transient
	// wait) up to executeForEach.
	//
	// Pre-fix behavior: cancelSiblings was gated on firstUnhandledErr,
	// which the transient branch had already set — so dagCancel never
	// fired and the transient error masked the genuine failure.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
		// No failure edge → genuine failures are unhandled.
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "__PEND__"},
			map[string]any{"id": "b", "item": "__FAIL__"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{transientPendingOnItem: "__PEND__", failOnItem: "__FAIL__"}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if err == nil {
		t.Fatalf("expected genuine failure to propagate, got nil")
	}
	if errors.Is(err, ErrApprovalPending) || errors.Is(err, ErrSubworkflowInProgress) {
		t.Fatalf("expected genuine failure to mask transient wait, got transient err: %v", err)
	}
	orchStatus, _ := runState.NodeStatus("orch")
	if orchStatus != statusFailed {
		t.Fatalf("expected orch status %q, got %q", statusFailed, orchStatus)
	}
}

func TestDAG_TrueMidExecutionCancellation(t *testing.T) {
	// True mid-execution cancellation: dispatch one item that BLOCKS on
	// ctx.Done, then cancel ctx from outside. The blocking goroutine must
	// observe the cancel via ctx.Done, return context.Canceled, and the
	// orchestrator must produce a consistent statusFailed/skipped result.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "blocker", "item": "__BLOCK__"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{blockUntilCtxDoneOnItem: "__BLOCK__"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
		done <- err
	}()
	// Wait for the blocker to actually be in <-ctx.Done() before cancelling.
	if !waitForBlockerEntered(eng, 1*time.Second) {
		t.Fatal("blocker hook did not enter <-ctx.Done() within timeout")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after mid-execution cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orchestrator did not return after cancel — dagCancel may not be propagating to in-flight goroutines")
	}

	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	status, ok := loaded.NodeStatus("tmpl::0")
	if !ok {
		t.Fatalf("expected tmpl::0 to be persisted on disk after cancel")
	}
	if status != statusSkipped {
		t.Fatalf("expected tmpl::0 statusSkipped after cancel, got %q", status)
	}
}

func TestDAG_MidExecutionCancellationStopsRemainingItems(t *testing.T) {
	// Cancel ctx after some items have been dispatched. The cancel branch
	// in executeForEachDAG must fire for the in-flight items, mark them
	// skipped with reason=cancelled, and propagate context.Canceled.
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{
				List: "input.items", Item: "item", DependsOn: "depends_on", Body: "tmpl",
			}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "a", "item": "ok-a"},
			map[string]any{"id": "b", "item": "ok-b"},
		},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-dispatch cancel still exercises the in-flight cancel branch
	_, err := eng.executeNode(ctx, testRunID1, dir, workflow, &workflow.Nodes[0], "", "", nil, runContext, logger, runState)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	loaded, err := state.LoadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	skippedCount := 0
	for _, id := range []string{"tmpl::0", "tmpl::1"} {
		if status, ok := loaded.NodeStatus(id); ok && status == statusSkipped {
			skippedCount++
		}
	}
	if skippedCount == 0 {
		t.Fatalf("expected at least one item marked skipped on disk after cancellation")
	}
}

// waitForBlockerEntered polls the engine's blocker counter until it
// observes at least one increment (meaning the blockUntilCtxDoneOnItem
// hook has actually entered <-ctx.Done()). Returns true on success,
// false on timeout. Used by mid-execution cancellation tests to ensure
// the test actually exercises the in-flight path rather than racing
// with the early ctx.Err() short-circuit.
func waitForBlockerEntered(eng *Engine, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if eng.blockerEntered.Load() > 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// TestForEachBody_LoopVarAutoInjectedToSubworkflow pins the behavior
// that a ForEach orchestrator's loop-bound variable (e.g. `item: component`)
// is auto-propagated as an input to a subworkflow template's child run
// even when the template does NOT declare it in its explicit `inputs`
// map. This is how implement_spec.yaml's build_one_component /
// integrate_one_component templates pass `component` to their child
// workflows without listing it explicitly.
//
// Mechanism: the ForEach executor injects the item under node.ForEach.Item
// into the `extra` map passed to executeSingle. mergeDispatchInputs merges
// that map (Phase 3) so the dispatch context sees input.component regardless
// of whether the template declared a mapping for it.
//
// Keep this test pinned — reviewers occasionally flag this as a
// false-positive bug because the template looks like it's missing
// the input mapping. This test demonstrates the contract.
func TestForEachBody_LoopVarAutoInjectedToSubworkflow(t *testing.T) {
	dir := t.TempDir()
	parent := &definitions.Workflow{
		ID: "parent",
		Nodes: []definitions.Node{
			{
				ID: "orch",
				ForEach: &definitions.ForEach{
					List: "input.items", Item: "component", Body: "template",
				},
			},
			{
				ID:       "template",
				Kind:     "subworkflow",
				Workflow: "child",
				// NOTE: deliberately NO explicit `component` input.
			},
		},
	}
	child := &definitions.Workflow{
		ID:     "child",
		Inputs: map[string]string{"component": "object"},
		Nodes:  []definitions.Node{{ID: "noop", Kind: "system"}},
	}
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{"parent": parent, "child": child},
	}
	runState := state.NewRunState("run-1", "parent", map[string]any{
		"items": []any{map[string]any{"id": "a", "name": "alpha"}},
	})
	runContext := &RunContext{Inputs: runState.Inputs, Outputs: map[string]NodeOutput{}}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{Definitions: bundle, RunsDir: dir}

	_, _ = eng.executeNode(context.Background(), "run-1", dir, parent, &parent.Nodes[0], "", "", nil, runContext, logger, runState)

	var childRunID string
	runState.WithNode("template::0", func(n *state.NodeState) {
		if n.Data != nil {
			childRunID, _ = n.Data["child_run"].(string)
		}
	})
	if childRunID == "" {
		t.Fatalf("expected child run spawned, got none")
	}
	childState, err := state.LoadState(filepath.Join(dir, childRunID, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	comp, ok := childState.Inputs["component"]
	if !ok {
		t.Fatalf("child missing input.component; got inputs: %v", childState.Inputs)
	}
	compMap, ok := comp.(map[string]any)
	if !ok {
		t.Fatalf("input.component: expected map, got %T", comp)
	}
	if compMap["id"] != "a" {
		t.Errorf("input.component.id = %v, want a", compMap["id"])
	}
}
