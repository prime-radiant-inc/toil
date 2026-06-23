package inspect

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestOutputsProcessor_ExtractsNodeOutputs(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	now := time.Now()
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.Message = "all good"
		n.Data = map[string]any{"score": 9}
		n.Artifacts = []string{"artifact-1.txt"}
		n.StartedAt = &now
	})
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.Message = "done"
		n.StartedAt = &now
	})

	proc := NewOutputsProcessor(rs)
	result := proc.Result().(OutputsResult)

	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}

	// Nodes sorted by ID: node-a first, node-b second
	if result.Nodes[0].ID != "node-a" {
		t.Errorf("Nodes[0].ID: got %q, want %q", result.Nodes[0].ID, "node-a")
	}
	if result.Nodes[1].ID != "node-b" {
		t.Errorf("Nodes[1].ID: got %q, want %q", result.Nodes[1].ID, "node-b")
	}

	nodeB := result.Nodes[1]
	if nodeB.Message != "all good" {
		t.Errorf("node-b Message: got %q, want %q", nodeB.Message, "all good")
	}
	if nodeB.Data["score"] != 9 {
		t.Errorf("node-b Data score: got %v, want 9", nodeB.Data["score"])
	}
	if len(nodeB.Artifacts) != 1 || nodeB.Artifacts[0] != "artifact-1.txt" {
		t.Errorf("node-b Artifacts: got %v, want [artifact-1.txt]", nodeB.Artifacts)
	}
}

func TestOutputsProcessor_EmptyNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewOutputsProcessor(rs)

	result := proc.Result().(OutputsResult)

	if result.Nodes == nil {
		t.Error("Nodes should not be nil for empty run")
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(result.Nodes))
	}
}

func TestOutputsProcessor_SortedByID(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	now := time.Now()
	for _, id := range []string{"zebra", "apple", "mango"} {
		id := id
		rs.WithNode(id, func(n *state.NodeState) {
			n.Status = "completed"
			n.StartedAt = &now
		})
	}

	proc := NewOutputsProcessor(rs)
	result := proc.Result().(OutputsResult)

	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(result.Nodes))
	}
	if result.Nodes[0].ID != "apple" {
		t.Errorf("Nodes[0].ID: got %q, want apple", result.Nodes[0].ID)
	}
	if result.Nodes[1].ID != "mango" {
		t.Errorf("Nodes[1].ID: got %q, want mango", result.Nodes[1].ID)
	}
	if result.Nodes[2].ID != "zebra" {
		t.Errorf("Nodes[2].ID: got %q, want zebra", result.Nodes[2].ID)
	}
}

func TestOutputsProcessor_OmitsEmptyFields(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	now := time.Now()
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &now
		// no message, no data, no artifacts
	})

	proc := NewOutputsProcessor(rs)
	result := proc.Result().(OutputsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.Message != "" {
		t.Errorf("expected empty message, got %q", n.Message)
	}
	if n.Data != nil {
		t.Errorf("expected nil data, got %v", n.Data)
	}
	if n.Artifacts != nil {
		t.Errorf("expected nil artifacts, got %v", n.Artifacts)
	}
}

func TestOutputsProcessor_ProcessEventIsNoOp(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewOutputsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})

	if proc.Changed() {
		t.Error("Changed() should always return false for outputs processor")
	}
}
