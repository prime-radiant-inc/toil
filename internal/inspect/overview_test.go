package inspect

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestOverviewProcessor_CompletedRun(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(30 * time.Second)

	rs := state.NewRunState("run-abc", "my-workflow", map[string]any{"x": 1})
	rs.StartedAt = start
	rs.FinishedAt = &fin
	rs.Status = "completed"

	aStart := start
	aEnd := start.Add(10 * time.Second)
	bStart := start.Add(5 * time.Second)
	bEnd := start.Add(25 * time.Second)

	rs.WithNode("node-a", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "approve"
		n.Attempts = 1
		n.StartedAt = &aStart
		n.EndedAt = &aEnd
	})
	rs.WithNode("node-b", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "pass"
		n.Attempts = 2
		n.StartedAt = &bStart
		n.EndedAt = &bEnd
		n.Data = map[string]any{"child_run": "run-child-1"}
	})

	proc := NewOverviewProcessor(rs)
	result := proc.Result().(OverviewResult)

	if result.RunID != "run-abc" {
		t.Errorf("RunID: got %q, want %q", result.RunID, "run-abc")
	}
	if result.WorkflowID != "my-workflow" {
		t.Errorf("WorkflowID: got %q, want %q", result.WorkflowID, "my-workflow")
	}
	if result.Status != "completed" {
		t.Errorf("Status: got %q, want %q", result.Status, "completed")
	}
	if result.DurationS != 30.0 {
		t.Errorf("DurationS: got %f, want 30.0", result.DurationS)
	}
	if result.StartedAt != "2026-04-19T10:00:00Z" {
		t.Errorf("StartedAt: got %q, want %q", result.StartedAt, "2026-04-19T10:00:00Z")
	}
	if result.FinishedAt != "2026-04-19T10:00:30Z" {
		t.Errorf("FinishedAt: got %q, want %q", result.FinishedAt, "2026-04-19T10:00:30Z")
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}

	// Nodes sorted by start time: node-a first, then node-b
	if result.Nodes[0].ID != "node-a" {
		t.Errorf("first node: got %q, want %q", result.Nodes[0].ID, "node-a")
	}
	if result.Nodes[0].Status != "completed" {
		t.Errorf("node-a status: got %q, want %q", result.Nodes[0].Status, "completed")
	}
	if result.Nodes[0].Decision != "approve" {
		t.Errorf("node-a decision: got %q, want %q", result.Nodes[0].Decision, "approve")
	}
	if result.Nodes[0].Attempts != 1 {
		t.Errorf("node-a attempts: got %d, want 1", result.Nodes[0].Attempts)
	}
	if result.Nodes[0].DurationS != 10.0 {
		t.Errorf("node-a DurationS: got %f, want 10.0", result.Nodes[0].DurationS)
	}

	if result.Nodes[1].ID != "node-b" {
		t.Errorf("second node: got %q, want %q", result.Nodes[1].ID, "node-b")
	}
	if result.Nodes[1].ChildRun != "run-child-1" {
		t.Errorf("node-b ChildRun: got %q, want %q", result.Nodes[1].ChildRun, "run-child-1")
	}
	if result.Nodes[1].Attempts != 2 {
		t.Errorf("node-b attempts: got %d, want 2", result.Nodes[1].Attempts)
	}
}

func TestOverviewProcessor_DelegatesToTokens(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin
	rs.Status = "completed"

	proc := NewOverviewProcessor(rs)

	// Feed token events (from ASSISTANT_TEXT_END, where serf reports usage)
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"model": "gpt-5.4",
			"usage": map[string]any{
				"input_tokens":      500,
				"output_tokens":     200,
				"cache_read_tokens": 300,
				"reasoning_tokens":  10,
			},
		},
	}))

	result := proc.Result().(OverviewResult)

	if result.Tokens == nil {
		t.Fatal("Tokens should not be nil when token events were processed")
	}
	if result.Tokens.Input != 500 {
		t.Errorf("Tokens.Input: got %d, want 500", result.Tokens.Input)
	}
	if result.Tokens.Output != 200 {
		t.Errorf("Tokens.Output: got %d, want 200", result.Tokens.Output)
	}
}

func TestOverviewProcessor_DelegatesToModels(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.Status = "running"

	proc := NewOverviewProcessor(rs)

	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "SESSION_START",
		"data": map[string]any{
			"profile": "default",
			"model":   "claude-opus-4-5",
		},
	}))

	result := proc.Result().(OverviewResult)

	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0] != "claude-opus-4-5" {
		t.Errorf("model: got %q, want %q", result.Models[0], "claude-opus-4-5")
	}
}

func TestOverviewProcessor_UnfinishedRun(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.Status = "running"
	// No FinishedAt

	proc := NewOverviewProcessor(rs)
	result := proc.Result().(OverviewResult)

	if result.DurationS != 0 {
		t.Errorf("DurationS: got %f, want 0 for unfinished run", result.DurationS)
	}
	if result.FinishedAt != "" {
		t.Errorf("FinishedAt: got %q, want empty for unfinished run", result.FinishedAt)
	}
}

func TestOverviewProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewOverviewProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false before any events")
	}

	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"model": "gpt-5.4",
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		},
	}))

	if !proc.Changed() {
		t.Error("Changed() should be true after delegate processes event")
	}
}

func TestOverviewProcessor_TokensNilWhenNoEvents(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(5 * time.Second)

	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.StartedAt = start
	rs.FinishedAt = &fin
	rs.Status = "completed"

	proc := NewOverviewProcessor(rs)
	result := proc.Result().(OverviewResult)

	// With no token events, Tokens should be nil
	if result.Tokens != nil {
		t.Error("Tokens should be nil when no token events were processed")
	}
}
