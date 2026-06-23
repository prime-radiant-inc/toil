package inspect

import (
	"testing"

	"primeradiant.com/toil/internal/state"
)

func TestInputsProcessor_ReturnsRunInputs(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", map[string]any{
		"foo": "bar",
		"num": 42,
	})
	proc := NewInputsProcessor(rs)

	result := proc.Result().(InputsResult)

	if result.Inputs["foo"] != "bar" {
		t.Errorf("foo: got %v, want %q", result.Inputs["foo"], "bar")
	}
	if result.Inputs["num"] != 42 {
		t.Errorf("num: got %v, want 42", result.Inputs["num"])
	}
}

func TestInputsProcessor_EmptyInputs(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", map[string]any{})
	proc := NewInputsProcessor(rs)

	result := proc.Result().(InputsResult)

	if result.Inputs == nil {
		t.Error("Inputs should not be nil for empty run inputs")
	}
	if len(result.Inputs) != 0 {
		t.Errorf("expected empty inputs, got %d entries", len(result.Inputs))
	}
}

func TestInputsProcessor_NilInputs(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewInputsProcessor(rs)

	result := proc.Result().(InputsResult)

	if result.Inputs == nil {
		t.Error("Inputs should not be nil even when run has nil inputs")
	}
}

func TestInputsProcessor_ProcessEventIsNoOp(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", map[string]any{"x": 1})
	proc := NewInputsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "ROUND_TIMINGS",
		"data": map[string]any{"round": 1, "input_tokens": 100},
	}))

	if proc.Changed() {
		t.Error("Changed() should always return false for inputs processor")
	}
}
