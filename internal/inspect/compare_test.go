package inspect

import (
	"encoding/json"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestCompareProcessor_DifferentDurations(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	finA := start.Add(20 * time.Second)
	runA := state.NewRunState("run-a", "wf-1", nil)
	runA.StartedAt = start
	runA.FinishedAt = &finA
	runA.Status = "completed"
	runA.WithNode("step-1", func(n *state.NodeState) {
		n.Status = "completed"
		n.Attempts = 1
	})

	finB := start.Add(30 * time.Second)
	runB := state.NewRunState("run-b", "wf-1", nil)
	runB.StartedAt = start
	runB.FinishedAt = &finB
	runB.Status = "completed"
	runB.WithNode("step-1", func(n *state.NodeState) {
		n.Status = "completed"
		n.Attempts = 2
	})

	loader := &mockRunLoader{
		states: map[string]*state.RunState{
			"run-b": runB,
		},
		events: map[string][]state.Event{
			"run-b": {},
		},
	}

	proc := NewCompareProcessor(runA)
	proc.SetLoader(loader)
	proc.SetOtherRunID("run-b")

	result := proc.Result().(CompareResult)

	if result.Runs[0] != "run-a" {
		t.Errorf("Runs[0]: got %q, want %q", result.Runs[0], "run-a")
	}
	if result.Runs[1] != "run-b" {
		t.Errorf("Runs[1]: got %q, want %q", result.Runs[1], "run-b")
	}

	// Duration: A=20s, B=30s, delta=10, pct=50%
	if result.Comparison.DurationS.A != 20.0 {
		t.Errorf("DurationS.A: got %f, want 20.0", result.Comparison.DurationS.A)
	}
	if result.Comparison.DurationS.B != 30.0 {
		t.Errorf("DurationS.B: got %f, want 30.0", result.Comparison.DurationS.B)
	}
	if result.Comparison.DurationS.Delta != 10.0 {
		t.Errorf("DurationS.Delta: got %f, want 10.0", result.Comparison.DurationS.Delta)
	}
	if result.Comparison.DurationS.Pct != 50.0 {
		t.Errorf("DurationS.Pct: got %f, want 50.0", result.Comparison.DurationS.Pct)
	}

	// Attempts: A=1, B=2, delta=1, pct=100%
	if result.Comparison.TotalAttempts.A != 1.0 {
		t.Errorf("TotalAttempts.A: got %f, want 1.0", result.Comparison.TotalAttempts.A)
	}
	if result.Comparison.TotalAttempts.B != 2.0 {
		t.Errorf("TotalAttempts.B: got %f, want 2.0", result.Comparison.TotalAttempts.B)
	}
	if result.Comparison.TotalAttempts.Delta != 1.0 {
		t.Errorf("TotalAttempts.Delta: got %f, want 1.0", result.Comparison.TotalAttempts.Delta)
	}
	if result.Comparison.TotalAttempts.Pct != 100.0 {
		t.Errorf("TotalAttempts.Pct: got %f, want 100.0", result.Comparison.TotalAttempts.Pct)
	}
}

func TestCompareProcessor_TokenComparison(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	runA := state.NewRunState("run-a", "wf-1", nil)
	runA.StartedAt = start
	runA.FinishedAt = &fin
	runA.Status = "completed"

	runB := state.NewRunState("run-b", "wf-1", nil)
	runB.StartedAt = start
	runB.FinishedAt = &fin
	runB.Status = "completed"

	// Build events with token data for run-a (from ASSISTANT_TEXT_END)
	eventsA := []state.Event{
		makeNodeOutputEvent("node-1", map[string]any{
			"kind": "ASSISTANT_TEXT_END",
			"data": map[string]any{
				"model": "gpt-5.4",
				"usage": map[string]any{
					"input_tokens":  1000,
					"output_tokens": 500,
				},
			},
		}),
	}

	// Build events with token data for run-b
	eventsB := []state.Event{
		makeNodeOutputEvent("node-1", map[string]any{
			"kind": "ASSISTANT_TEXT_END",
			"data": map[string]any{
				"model": "gpt-5.4",
				"usage": map[string]any{
					"input_tokens":  2000,
					"output_tokens": 1000,
				},
			},
		}),
	}

	loader := &mockRunLoader{
		states: map[string]*state.RunState{
			"run-b": runB,
		},
		events: map[string][]state.Event{
			"run-b": eventsB,
		},
	}

	proc := NewCompareProcessor(runA)
	proc.SetLoader(loader)
	proc.SetOtherRunID("run-b")

	// Feed events for run-a
	for _, e := range eventsA {
		proc.ProcessEvent(e)
	}

	result := proc.Result().(CompareResult)

	if result.Comparison.Tokens == nil {
		t.Fatal("Tokens should not be nil when token events exist")
	}

	// Total tokens: A=1500, B=3000
	if result.Comparison.Tokens.Total.A != 1500 {
		t.Errorf("Tokens.Total.A: got %f, want 1500", result.Comparison.Tokens.Total.A)
	}
	if result.Comparison.Tokens.Total.B != 3000 {
		t.Errorf("Tokens.Total.B: got %f, want 3000", result.Comparison.Tokens.Total.B)
	}
	if result.Comparison.Tokens.Total.Delta != 1500 {
		t.Errorf("Tokens.Total.Delta: got %f, want 1500", result.Comparison.Tokens.Total.Delta)
	}

	// Cost: verify it's computed and the delta is correct direction
	if result.Comparison.Tokens.CostUSD.A <= 0 {
		t.Errorf("Tokens.CostUSD.A should be positive, got %f", result.Comparison.Tokens.CostUSD.A)
	}
	if result.Comparison.Tokens.CostUSD.B <= result.Comparison.Tokens.CostUSD.A {
		t.Errorf("Tokens.CostUSD.B (%f) should be greater than A (%f)",
			result.Comparison.Tokens.CostUSD.B, result.Comparison.Tokens.CostUSD.A)
	}
}

func TestCompareProcessor_OtherRunNotFound(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	runA := state.NewRunState("run-a", "wf-1", nil)
	runA.StartedAt = start
	runA.FinishedAt = &fin
	runA.Status = "completed"

	loader := &mockRunLoader{
		states: map[string]*state.RunState{}, // empty — run-b not found
	}

	proc := NewCompareProcessor(runA)
	proc.SetLoader(loader)
	proc.SetOtherRunID("run-b")

	raw := proc.Result()
	// Should return an error result, not panic
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var errResult map[string]any
	if err := json.Unmarshal(data, &errResult); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if _, ok := errResult["error"]; !ok {
		t.Error("expected error field in result when other run not found")
	}
}

func TestCompareProcessor_ProcessEventIsNoOp(t *testing.T) {
	runA := state.NewRunState("run-a", "wf-1", nil)
	proc := NewCompareProcessor(runA)

	// ProcessEvent should not panic without loader/otherID set
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "a"})

	if proc.Changed() {
		t.Error("Changed() should be false — compare does not use events for change tracking")
	}
}

func TestCompareProcessor_RegisteredAsAspect(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc, err := NewProcessor("compare", rs)
	if err != nil {
		t.Fatalf("NewProcessor('compare'): %v", err)
	}
	if proc == nil {
		t.Fatal("NewProcessor('compare') returned nil")
	}
}

func TestCompareProcessor_NoOtherRunID(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	fin := start.Add(10 * time.Second)

	runA := state.NewRunState("run-a", "wf-1", nil)
	runA.StartedAt = start
	runA.FinishedAt = &fin
	runA.Status = "completed"

	loader := &mockRunLoader{
		states: map[string]*state.RunState{},
	}

	proc := NewCompareProcessor(runA)
	proc.SetLoader(loader)
	// No SetOtherRunID call

	raw := proc.Result()
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var errResult map[string]any
	if err := json.Unmarshal(data, &errResult); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if _, ok := errResult["error"]; !ok {
		t.Error("expected error field in result when no other run ID set")
	}
}
