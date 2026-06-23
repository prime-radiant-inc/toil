package inspect

import (
	"os"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

type mockRunLoader struct {
	states map[string]*state.RunState
	events map[string][]state.Event
}

func (m *mockRunLoader) LoadState(runID string) (*state.RunState, error) {
	if rs, ok := m.states[runID]; ok {
		return rs, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockRunLoader) LoadEvents(runID string) ([]state.Event, error) {
	return m.events[runID], nil
}

func TestTreeProcessor_RootWithOneChild(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(30 * time.Second)

	root := state.NewRunState("root-run", "main-wf", nil)
	root.StartedAt = start
	root.FinishedAt = &fin
	root.Status = "completed"

	root.WithNode("step-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "child-run-1"}
	})

	childStart := start.Add(2 * time.Second)
	childFin := start.Add(20 * time.Second)
	child := state.NewRunState("child-run-1", "sub-wf", nil)
	child.StartedAt = childStart
	child.FinishedAt = &childFin
	child.Status = "completed"

	loader := &mockRunLoader{
		states: map[string]*state.RunState{
			"child-run-1": child,
		},
	}

	proc := NewTreeProcessor(root)
	proc.SetLoader(loader)

	result := proc.Result().(TreeResult)

	if result.RunID != "root-run" {
		t.Errorf("RunID: got %q, want %q", result.RunID, "root-run")
	}
	if result.WorkflowID != "main-wf" {
		t.Errorf("WorkflowID: got %q, want %q", result.WorkflowID, "main-wf")
	}
	if result.Status != "completed" {
		t.Errorf("Status: got %q, want %q", result.Status, "completed")
	}
	if result.DurationS != 30.0 {
		t.Errorf("DurationS: got %f, want 30.0", result.DurationS)
	}
	if len(result.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(result.Children))
	}
	if result.Children[0].NodeID != "step-a" {
		t.Errorf("child NodeID: got %q, want %q", result.Children[0].NodeID, "step-a")
	}
	if result.Children[0].RunID != "child-run-1" {
		t.Errorf("child RunID: got %q, want %q", result.Children[0].RunID, "child-run-1")
	}
	if result.Children[0].WorkflowID != "sub-wf" {
		t.Errorf("child WorkflowID: got %q, want %q", result.Children[0].WorkflowID, "sub-wf")
	}
	if result.Children[0].Status != "completed" {
		t.Errorf("child Status: got %q, want %q", result.Children[0].Status, "completed")
	}
	if result.Children[0].DurationS != 18.0 {
		t.Errorf("child DurationS: got %f, want 18.0", result.Children[0].DurationS)
	}
}

func TestTreeProcessor_DeepHierarchy(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(60 * time.Second)

	root := state.NewRunState("root", "wf-root", nil)
	root.StartedAt = start
	root.FinishedAt = &fin
	root.Status = "completed"
	root.WithNode("spawn-child", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "child"}
	})

	childStart := start.Add(5 * time.Second)
	childFin := start.Add(40 * time.Second)
	child := state.NewRunState("child", "wf-child", nil)
	child.StartedAt = childStart
	child.FinishedAt = &childFin
	child.Status = "completed"
	child.WithNode("spawn-grandchild", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "grandchild"}
	})

	gcStart := start.Add(10 * time.Second)
	gcFin := start.Add(30 * time.Second)
	grandchild := state.NewRunState("grandchild", "wf-gc", nil)
	grandchild.StartedAt = gcStart
	grandchild.FinishedAt = &gcFin
	grandchild.Status = "completed"

	loader := &mockRunLoader{
		states: map[string]*state.RunState{
			"child":      child,
			"grandchild": grandchild,
		},
	}

	proc := NewTreeProcessor(root)
	proc.SetLoader(loader)

	result := proc.Result().(TreeResult)

	if len(result.Children) != 1 {
		t.Fatalf("root: expected 1 child, got %d", len(result.Children))
	}
	childNode := result.Children[0]
	if childNode.RunID != "child" {
		t.Errorf("child RunID: got %q, want %q", childNode.RunID, "child")
	}
	if len(childNode.Children) != 1 {
		t.Fatalf("child: expected 1 grandchild, got %d", len(childNode.Children))
	}
	gcNode := childNode.Children[0]
	if gcNode.RunID != "grandchild" {
		t.Errorf("grandchild RunID: got %q, want %q", gcNode.RunID, "grandchild")
	}
	if gcNode.WorkflowID != "wf-gc" {
		t.Errorf("grandchild WorkflowID: got %q, want %q", gcNode.WorkflowID, "wf-gc")
	}
	if len(gcNode.Children) != 0 {
		t.Errorf("grandchild: expected 0 children, got %d", len(gcNode.Children))
	}
}

func TestTreeProcessor_NoChildren(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	root := state.NewRunState("solo-run", "wf-solo", nil)
	root.StartedAt = start
	root.FinishedAt = &fin
	root.Status = "completed"
	root.WithNode("step-a", func(n *state.NodeState) {
		n.Status = "completed"
		// No child_run
	})

	loader := &mockRunLoader{
		states: map[string]*state.RunState{},
	}

	proc := NewTreeProcessor(root)
	proc.SetLoader(loader)

	result := proc.Result().(TreeResult)

	if result.RunID != "solo-run" {
		t.Errorf("RunID: got %q, want %q", result.RunID, "solo-run")
	}
	if len(result.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(result.Children))
	}
}

func TestTreeProcessor_NoLoader(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	root := state.NewRunState("root", "wf-1", nil)
	root.StartedAt = start
	root.FinishedAt = &fin
	root.Status = "completed"
	root.WithNode("step-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "child-1"}
	})

	proc := NewTreeProcessor(root)
	// No SetLoader call

	result := proc.Result().(TreeResult)

	// Without a loader, should show root info but no children
	if result.RunID != "root" {
		t.Errorf("RunID: got %q, want %q", result.RunID, "root")
	}
	if len(result.Children) != 0 {
		t.Errorf("expected 0 children without loader, got %d", len(result.Children))
	}
}

func TestTreeProcessor_ChildRunNotFound(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	root := state.NewRunState("root", "wf-1", nil)
	root.StartedAt = start
	root.FinishedAt = &fin
	root.Status = "completed"
	root.WithNode("step-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "missing-child"}
	})

	loader := &mockRunLoader{
		states: map[string]*state.RunState{}, // empty — child not found
	}

	proc := NewTreeProcessor(root)
	proc.SetLoader(loader)

	result := proc.Result().(TreeResult)

	// Graceful degradation: child_run referenced but not loadable
	// Should still produce a node entry with what we know
	if len(result.Children) != 1 {
		t.Fatalf("expected 1 child (degraded), got %d", len(result.Children))
	}
	if result.Children[0].RunID != "missing-child" {
		t.Errorf("child RunID: got %q, want %q", result.Children[0].RunID, "missing-child")
	}
	if result.Children[0].Status != "unknown" {
		t.Errorf("child Status: got %q, want %q", result.Children[0].Status, "unknown")
	}
}

func TestTreeProcessor_ProcessEventIsNoOp(t *testing.T) {
	root := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTreeProcessor(root)
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "a"})
	if proc.Changed() {
		t.Error("Changed() should be false — tree does not use events")
	}
}

func TestTreeProcessor_RegisteredAsAspect(t *testing.T) {
	root := state.NewRunState("run-1", "wf-1", nil)
	proc, err := NewProcessor("tree", root)
	if err != nil {
		t.Fatalf("NewProcessor('tree'): %v", err)
	}
	if proc == nil {
		t.Fatal("NewProcessor('tree') returned nil")
	}
}

func TestTreeProcessor_UnfinishedRun(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	root := state.NewRunState("running-run", "wf-1", nil)
	root.StartedAt = start
	root.Status = "running"
	// FinishedAt is nil

	proc := NewTreeProcessor(root)
	result := proc.Result().(TreeResult)

	if result.DurationS != 0 {
		t.Errorf("DurationS: got %f, want 0 for unfinished run", result.DurationS)
	}
	if result.Status != "running" {
		t.Errorf("Status: got %q, want %q", result.Status, "running")
	}
}
