package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestDownstreamNodes_LinearChain(t *testing.T) {
	// A → B → C, downstream of A = {A, B, C}
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "A"},
			{ID: "B"},
			{ID: "C"},
		},
		Edges: []definitions.Edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
		},
	}
	got := downstreamNodes(wf, "A")
	want := map[string]bool{"A": true, "B": true, "C": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d nodes, got %d: %v", len(want), len(got), got)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected node %q in downstream set", id)
		}
	}
}

func TestDownstreamNodes_Diamond(t *testing.T) {
	// A → B, A → C, B → D, C → D, downstream of A = {A, B, C, D}
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "A"},
			{ID: "B"},
			{ID: "C"},
			{ID: "D"},
		},
		Edges: []definitions.Edge{
			{From: "A", To: "B"},
			{From: "A", To: "C"},
			{From: "B", To: "D"},
			{From: "C", To: "D"},
		},
	}
	got := downstreamNodes(wf, "A")
	want := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d nodes, got %d: %v", len(want), len(got), got)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected node %q in downstream set", id)
		}
	}
}

func TestDownstreamNodes_Cycle(t *testing.T) {
	// A → B → A, downstream of A = {A, B} (no infinite loop)
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "A"},
			{ID: "B"},
		},
		Edges: []definitions.Edge{
			{From: "A", To: "B"},
			{From: "B", To: "A"},
		},
	}
	got := downstreamNodes(wf, "A")
	want := map[string]bool{"A": true, "B": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d nodes, got %d: %v", len(want), len(got), got)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected node %q in downstream set", id)
		}
	}
}

func TestDownstreamNodes_MidChain(t *testing.T) {
	// A → B → C, downstream of B = {B, C} (not A)
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "A"},
			{ID: "B"},
			{ID: "C"},
		},
		Edges: []definitions.Edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
		},
	}
	got := downstreamNodes(wf, "B")
	want := map[string]bool{"B": true, "C": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d nodes, got %d: %v", len(want), len(got), got)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected node %q in downstream set", id)
		}
	}
	if got["A"] {
		t.Error("node A should not be in downstream set of B")
	}
}

// testWorkflow returns a minimal valid workflow for retrigger tests.
func testWorkflow(nodes []definitions.Node, edges []definitions.Edge) *definitions.Workflow {
	return &definitions.Workflow{
		ID:      "wf1",
		Name:    "Test Workflow",
		Version: 1,
		Nodes:   nodes,
		Edges:   edges,
	}
}

// snapshotWorkflowForTest writes a workflow.yaml into the run directory so that
// loadWorkflowSnapshot can find it.
func snapshotWorkflowForTest(t *testing.T, runDir string, workflow *definitions.Workflow) {
	t.Helper()
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
	}
	if err := eng.snapshotWorkflow(runDir, workflow); err != nil {
		t.Fatalf("snapshot workflow: %v", err)
	}
}

// setupRetriggerTest creates a run directory with a state file and workflow
// snapshot, returning the engine and run directory path.
func setupRetriggerTest(t *testing.T, workflow *definitions.Workflow, runState *state.RunState) (*Engine, string) {
	t.Helper()
	runsDir := t.TempDir()
	runDir := filepath.Join(runsDir, runState.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("create run dir: %v", err)
	}
	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatalf("save state: %v", err)
	}
	snapshotWorkflowForTest(t, runDir, workflow)

	eng := &Engine{
		RunsDir: runsDir,
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		EventStdout: io.Discard,
	}
	return eng, runDir
}

func TestRetriggerNode_RejectsRunningRun(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusRunning

	eng, _ := setupRetriggerTest(t, wf, rs)
	err := eng.RetriggerNode(testRunID1, "A")
	if err == nil {
		t.Fatal("expected error for running run")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected error containing 'terminal', got: %v", err)
	}
}

func TestRetriggerNode_RejectsCancelledRun(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusCancelled

	eng, _ := setupRetriggerTest(t, wf, rs)
	err := eng.RetriggerNode(testRunID1, "A")
	if err == nil {
		t.Fatal("expected error for cancelled run")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected error containing 'terminal', got: %v", err)
	}
}

func TestRetriggerNode_RejectsPausedRun(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusPaused

	eng, _ := setupRetriggerTest(t, wf, rs)
	err := eng.RetriggerNode(testRunID1, "A")
	if err == nil {
		t.Fatal("expected error for paused run")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected error containing 'terminal', got: %v", err)
	}
}

func TestRetriggerNode_RejectsUnknownNode(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed

	eng, _ := setupRetriggerTest(t, wf, rs)
	err := eng.RetriggerNode(testRunID1, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error containing 'not found', got: %v", err)
	}
}

func TestRetriggerNode_ResetsTargetAndDownstream(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}, {ID: "B"}, {ID: "C"}},
		[]definitions.Edge{{From: "A", To: "B"}, {From: "B", To: "C"}},
	)

	now := time.Now().UTC()
	finishedAt := now.Add(time.Minute)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.Error = "node A failed"
	rs.FinishedAt = &finishedAt
	rs.Summary = "Run failed because node A timed out"
	rs.Description = "Detailed failure narrative from the narrative writer"

	// Set up nodes with completed state and various fields.
	for _, id := range []string{"A", "B", "C"} {
		rs.WithNode(id, func(n *state.NodeState) {
			n.Status = statusCompleted
			n.Decision = "done"
			n.Message = "finished"
			n.Error = "some old error"
			n.SessionID = "sess-old"
			started := now
			ended := now.Add(time.Second)
			n.StartedAt = &started
			n.EndedAt = &ended
			n.Data = map[string]any{"child_run": "sub-1"}
		})
	}

	eng, runDir := setupRetriggerTest(t, wf, rs)
	if err := eng.RetriggerNode(testRunID1, "A"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}

	// Reload state from disk.
	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	// Target node should be statusRetrying.
	if got := reloaded.Nodes["A"].Status; got != statusRetrying {
		t.Errorf("node A status = %q, want %q", got, statusRetrying)
	}
	// Downstream nodes should be "pending".
	for _, id := range []string{"B", "C"} {
		if got := reloaded.Nodes[id].Status; got != statusPending {
			t.Errorf("node %s status = %q, want %q", id, got, statusPending)
		}
	}

	// Fields should be cleared.
	for _, id := range []string{"A", "B", "C"} {
		n := reloaded.Nodes[id]
		if n.Decision != "" {
			t.Errorf("node %s Decision = %q, want empty", id, n.Decision)
		}
		if n.Message != "" {
			t.Errorf("node %s Message = %q, want empty", id, n.Message)
		}
		if n.StartedAt != nil {
			t.Errorf("node %s StartedAt should be nil", id)
		}
		if n.EndedAt != nil {
			t.Errorf("node %s EndedAt should be nil", id)
		}
		if n.SessionID != "" {
			t.Errorf("node %s SessionID = %q, want empty", id, n.SessionID)
		}
		if n.Error != "" {
			t.Errorf("node %s Error = %q, want empty", id, n.Error)
		}
	}

	// child_run should be cleared by retrigger.
	for _, id := range []string{"A", "B", "C"} {
		n := reloaded.Nodes[id]
		if n.Data != nil {
			if _, ok := n.Data["child_run"]; ok {
				t.Errorf("node %s Data[child_run] should be cleared after retrigger", id)
			}
		}
	}

	// Run status should be "running", error cleared, FinishedAt nil.
	if reloaded.Status != statusRunning {
		t.Errorf("run status = %q, want %q", reloaded.Status, statusRunning)
	}
	if reloaded.Error != "" {
		t.Errorf("run error = %q, want empty", reloaded.Error)
	}
	if reloaded.FinishedAt != nil {
		t.Error("run FinishedAt should be nil")
	}
	if reloaded.Summary != "" {
		t.Errorf("run Summary = %q, want empty", reloaded.Summary)
	}
	if reloaded.Description != "" {
		t.Errorf("run Description = %q, want empty", reloaded.Description)
	}

	// Verify node_retriggered event was logged.
	eventsPath := filepath.Join(runDir, "events.jsonl")
	events := parseEvents(t, eventsPath)
	retriggered := findEvents(events, "node_retriggered")
	if len(retriggered) != 1 {
		t.Fatalf("expected 1 node_retriggered event, got %d", len(retriggered))
	}
	if retriggered[0].NodeID != "A" {
		t.Errorf("node_retriggered event node_id = %q, want %q", retriggered[0].NodeID, "A")
	}

	// Verify run_resumed event was logged (needed for SSE status updates).
	resumed := findEvents(events, "run_resumed")
	if len(resumed) != 1 {
		t.Fatalf("expected 1 run_resumed event, got %d", len(resumed))
	}
}

func TestRetriggerNode_PreservesNodeData(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)

	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.WithNode("A", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Data = map[string]any{
			"child_run": "sub-42",
			"extra":     "metadata",
			"count":     float64(7),
		}
	})

	eng, runDir := setupRetriggerTest(t, wf, rs)
	if err := eng.RetriggerNode(testRunID1, "A"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}

	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	n := reloaded.Nodes["A"]
	if n.Data == nil {
		t.Fatal("node A Data is nil, expected preserved")
	}
	// child_run should be cleared so executeSubworkflow creates a fresh child.
	if _, ok := n.Data["child_run"]; ok {
		t.Errorf("Data[child_run] should be cleared after retrigger, got %v", n.Data["child_run"])
	}
	// Other Data keys should be preserved.
	if n.Data["extra"] != "metadata" {
		t.Errorf("Data[extra] = %v, want metadata", n.Data["extra"])
	}
	if n.Data["count"] != float64(7) {
		t.Errorf("Data[count] = %v, want 7", n.Data["count"])
	}
}

func TestRetriggerNode_ResetsForEachChildren(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
		nil,
	)

	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed

	// Simulate expanded ForEach children in state (prefixed by template ID).
	rs.WithNode("process", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Message = "child failed"
	})
	for _, childID := range []string{"process_item::0", "process_item::1"} {
		rs.WithNode(childID, func(n *state.NodeState) {
			n.Status = statusCompleted
			n.Decision = "done"
			n.Message = "ok"
		})
	}

	eng, runDir := setupRetriggerTest(t, wf, rs)
	if err := eng.RetriggerNode(testRunID1, "process"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}

	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	// Parent should be retrying.
	if got := reloaded.Nodes["process"].Status; got != statusRetrying {
		t.Errorf("process status = %q, want retrying", got)
	}
	// Children should be reset to pending.
	for _, childID := range []string{"process_item::0", "process_item::1"} {
		n := reloaded.Nodes[childID]
		if n == nil {
			t.Fatalf("expected child node %q in state", childID)
		}
		if n.Status != statusPending {
			t.Errorf("%s status = %q, want %q", childID, n.Status, statusPending)
		}
		if n.Decision != "" {
			t.Errorf("%s Decision = %q, want empty", childID, n.Decision)
		}
		if n.Message != "" {
			t.Errorf("%s Message = %q, want empty", childID, n.Message)
		}
	}
}

func TestRetriggerNode_ReturnsErrorOnSaveFailure(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)

	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.WithNode("A", func(n *state.NodeState) {
		n.Status = statusFailed
	})

	eng, runDir := setupRetriggerTest(t, wf, rs)

	// SaveState writes atomically via a temp file + rename. Make the run
	// directory read-only so the temp-file write fails.
	if err := os.Chmod(runDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(runDir, 0o755) })

	err := eng.RetriggerNode(testRunID1, "A")
	if err == nil {
		t.Fatal("expected error when run directory is read-only")
	}
}

func TestRetriggerNode_ResetsJoinState(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{
			{ID: "A"},
			{ID: "B"},
			{ID: "C", Join: "all"},
		},
		[]definitions.Edge{
			{From: "A", To: "C"},
			{From: "B", To: "C"},
		},
	)

	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.WithNode("A", func(n *state.NodeState) { n.Status = statusCompleted })
	rs.WithNode("B", func(n *state.NodeState) { n.Status = statusCompleted })
	rs.WithNode("C", func(n *state.NodeState) { n.Status = statusFailed })
	// Simulate join state: C has received arrivals from A and B.
	rs.SetJoinState("C", []string{"A", "B"})

	eng, runDir := setupRetriggerTest(t, wf, rs)
	if err := eng.RetriggerNode(testRunID1, "A"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}

	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	// C is downstream of A, so its join state should be cleared.
	if arrivals := reloaded.GetJoinState("C"); len(arrivals) != 0 {
		t.Errorf("join state for C should be cleared, got %v", arrivals)
	}
}

func TestRetriggerNode_ResetsStartedAt(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}},
		nil,
	)

	oldStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.StartedAt = oldStart
	rs.WithNode("A", func(n *state.NodeState) {
		n.Status = statusFailed
	})

	eng, runDir := setupRetriggerTest(t, wf, rs)
	before := time.Now().UTC()
	if err := eng.RetriggerNode(testRunID1, "A"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}
	after := time.Now().UTC()

	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if reloaded.StartedAt.Before(before) || reloaded.StartedAt.After(after) {
		t.Errorf("run StartedAt = %v, want between %v and %v", reloaded.StartedAt, before, after)
	}
}

func TestRetriggerNode_ClearsNodeError(t *testing.T) {
	wf := testWorkflow(
		[]definitions.Node{{ID: "A"}, {ID: "B"}},
		[]definitions.Edge{{From: "A", To: "B"}},
	)

	rs := state.NewRunState(testRunID1, wf.ID, nil)
	rs.Status = statusFailed
	rs.WithNode("A", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Error = "connection timeout"
	})
	rs.WithNode("B", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Error = "upstream dependency failed"
	})

	eng, runDir := setupRetriggerTest(t, wf, rs)
	if err := eng.RetriggerNode(testRunID1, "A"); err != nil {
		t.Fatalf("RetriggerNode: %v", err)
	}

	reloaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	for _, id := range []string{"A", "B"} {
		n := reloaded.Nodes[id]
		if n.Error != "" {
			t.Errorf("node %s Error = %q, want empty after retrigger", id, n.Error)
		}
	}
}
