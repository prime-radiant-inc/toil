package inspect

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestTimingProcessor_BasicDurations(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	nodeAEnd := start.Add(3 * time.Second)
	nodeBEnd := start.Add(5 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	fin := nodeBEnd
	rs.FinishedAt = &fin

	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &start
		n.EndedAt = &nodeAEnd
	})
	nodeBS := start.Add(1 * time.Second)
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &nodeBS
		n.EndedAt = &nodeBEnd
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	if result.TotalS != 5.0 {
		t.Errorf("TotalS: got %f, want 5.0", result.TotalS)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 node timings, got %d", len(result.Nodes))
	}

	// Nodes should be sorted by start time
	if result.Nodes[0].ID != "node-a" {
		t.Errorf("first node: got %q, want %q", result.Nodes[0].ID, "node-a")
	}
	if result.Nodes[1].ID != "node-b" {
		t.Errorf("second node: got %q, want %q", result.Nodes[1].ID, "node-b")
	}

	// node-a: 3s of 5s total = 60%
	if result.Nodes[0].DurationS != 3.0 {
		t.Errorf("node-a DurationS: got %f, want 3.0", result.Nodes[0].DurationS)
	}
	if result.Nodes[0].Pct != 60.0 {
		t.Errorf("node-a Pct: got %f, want 60.0", result.Nodes[0].Pct)
	}

	// node-b: 4s of 5s total = 80%
	if result.Nodes[1].DurationS != 4.0 {
		t.Errorf("node-b DurationS: got %f, want 4.0", result.Nodes[1].DurationS)
	}
	if result.Nodes[1].Pct != 80.0 {
		t.Errorf("node-b Pct: got %f, want 80.0", result.Nodes[1].Pct)
	}
}

func TestTimingProcessor_ConcurrentNodes(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin

	// Overlapping: node-a [0,6], node-b [2,8]
	aStart := start
	aEnd := start.Add(6 * time.Second)
	bStart := start.Add(2 * time.Second)
	bEnd := start.Add(8 * time.Second)

	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &bStart
		n.EndedAt = &bEnd
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	// node-a should be concurrent with node-b and vice versa
	found := false
	for _, nt := range result.Nodes {
		if nt.ID == "node-a" {
			for _, c := range nt.ConcurrentWith {
				if c == "node-b" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected node-a to be concurrent with node-b")
	}

	found = false
	for _, nt := range result.Nodes {
		if nt.ID == "node-b" {
			for _, c := range nt.ConcurrentWith {
				if c == "node-a" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected node-b to be concurrent with node-a")
	}
}

func TestTimingProcessor_Bottleneck(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin

	aStart := start
	aEnd := start.Add(2 * time.Second)
	bStart := start
	bEnd := start.Add(8 * time.Second)

	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &bStart
		n.EndedAt = &bEnd
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	if len(result.Bottlenecks) == 0 {
		t.Fatal("expected at least one bottleneck")
	}
	if result.Bottlenecks[0].Node != "node-b" {
		t.Errorf("bottleneck node: got %q, want %q", result.Bottlenecks[0].Node, "node-b")
	}
	if result.Bottlenecks[0].DurationS != 8.0 {
		t.Errorf("bottleneck DurationS: got %f, want 8.0", result.Bottlenecks[0].DurationS)
	}
}

func TestTimingProcessor_NoFinishedRun(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	// FinishedAt is nil — still running

	aStart := start
	aEnd := start.Add(3 * time.Second)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	// TotalS should be 0 since run isn't finished
	if result.TotalS != 0 {
		t.Errorf("TotalS: got %f, want 0 for unfinished run", result.TotalS)
	}
}

func TestTimingProcessor_SkipsNodesWithoutTimestamps(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(5 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin

	aStart := start
	aEnd := start.Add(3 * time.Second)
	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "pending"
		// No timestamps
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node timing (skipping pending), got %d", len(result.Nodes))
	}
	if result.Nodes[0].ID != "node-a" {
		t.Errorf("node ID: got %q, want %q", result.Nodes[0].ID, "node-a")
	}
}

func TestTimingProcessor_ChangedAlwaysFalse(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTimingProcessor(rs)
	if proc.Changed() {
		t.Error("Changed() should return false (timing is computed from state, not events)")
	}
}

func TestTimingProcessor_NonOverlappingNodes(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin

	// Sequential: node-a [0,3], node-b [5,8]
	aStart := start
	aEnd := start.Add(3 * time.Second)
	bStart := start.Add(5 * time.Second)
	bEnd := start.Add(8 * time.Second)

	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.StartedAt = &bStart
		n.EndedAt = &bEnd
	})

	proc := NewTimingProcessor(rs)
	result := proc.Result().(TimingResult)

	for _, nt := range result.Nodes {
		if len(nt.ConcurrentWith) > 0 {
			t.Errorf("node %q should not be concurrent with anything, got %v", nt.ID, nt.ConcurrentWith)
		}
	}
}
