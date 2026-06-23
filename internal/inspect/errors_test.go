package inspect

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestErrorsProcessor_SchemaValidation(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "tool args schema validation failed: output.decision is required",
		},
	}))

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Type != "schema_validation" {
		t.Errorf("Type: got %q, want %q", e.Type, "schema_validation")
	}
	if e.Node != "node-a" {
		t.Errorf("Node: got %q, want %q", e.Node, "node-a")
	}
	if e.Attempt != 1 {
		t.Errorf("Attempt: got %d, want 1", e.Attempt)
	}
	if e.Message == "" {
		t.Error("Message should not be empty")
	}
}

func TestErrorsProcessor_Steering(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-b"})
	proc.ProcessEvent(makeNodeOutputEvent("node-b", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "You must reconsider your approach.",
		},
	}))

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Type != "steering" {
		t.Errorf("Type: got %q, want %q", e.Type, "steering")
	}
	if e.Node != "node-b" {
		t.Errorf("Node: got %q, want %q", e.Node, "node-b")
	}
	if e.Message != "You must reconsider your approach." {
		t.Errorf("Message: got %q, want %q", e.Message, "You must reconsider your approach.")
	}
}

func TestErrorsProcessor_SilentExit(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	ts := time.Now().UTC()
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-c"})
	proc.ProcessEvent(state.Event{
		Type:      "node_failed",
		NodeID:    "node-c",
		Timestamp: ts,
		Data: map[string]any{
			"exit_code": float64(1),
			"error":     "exit status 1",
		},
	})

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Type != "silent_exit" {
		t.Errorf("Type: got %q, want %q", e.Type, "silent_exit")
	}
	if e.Node != "node-c" {
		t.Errorf("Node: got %q, want %q", e.Node, "node-c")
	}
	if e.Ts == "" {
		t.Error("Ts should not be empty")
	}
}

func TestErrorsProcessor_SilentExit_NotTriggeredWithText(t *testing.T) {
	// A node_failed event with meaningful text should NOT be a silent_exit.
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-d"})
	proc.ProcessEvent(state.Event{
		Type:   "node_failed",
		NodeID: "node-d",
		Text:   "some meaningful error text",
		Data: map[string]any{
			"exit_code": float64(1),
		},
	})

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors for node_failed with text, got %d", len(result.Errors))
	}
}

func TestErrorsProcessor_MultipleErrorTypes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "tool args schema validation failed: missing field",
		},
	}))

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-b"})
	proc.ProcessEvent(makeNodeOutputEvent("node-b", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "Try again.",
		},
	}))

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-c"})
	proc.ProcessEvent(state.Event{
		Type:   "node_failed",
		NodeID: "node-c",
		Data: map[string]any{
			"exit_code": float64(2),
		},
	})

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 3 {
		t.Fatalf("expected 3 errors, got %d", len(result.Errors))
	}

	types := make(map[string]bool)
	for _, e := range result.Errors {
		types[e.Type] = true
	}
	if !types["schema_validation"] {
		t.Error("expected schema_validation error")
	}
	if !types["steering"] {
		t.Error("expected steering error")
	}
	if !types["silent_exit"] {
		t.Error("expected silent_exit error")
	}
}

func TestErrorsProcessor_AttemptTracking(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	// Attempt 1 — schema error
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "tool args schema validation failed: first attempt",
		},
	}))

	// Attempt 2 — steering error
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "second attempt steering",
		},
	}))

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(result.Errors))
	}
	if result.Errors[0].Attempt != 1 {
		t.Errorf("first error attempt: got %d, want 1", result.Errors[0].Attempt)
	}
	if result.Errors[1].Attempt != 2 {
		t.Errorf("second error attempt: got %d, want 2", result.Errors[1].Attempt)
	}
}

func TestErrorsProcessor_ToolOutputDeltaIgnored(t *testing.T) {
	// Generic TOOL_CALL_OUTPUT_DELTA events (non-schema) should NOT
	// be flagged as errors — they produce false positives when tool
	// output contains the word "error" (e.g., file content).
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "error: command not found: foobar",
		},
	}))

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors (generic tool output ignored), got %d", len(result.Errors))
	}
}

func TestErrorsProcessor_SchemaValidationStillDetected(t *testing.T) {
	// Schema validation errors ARE still detected via SchemaError.
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "tool args schema validation failed: output.decision is required",
		},
	}))

	result := proc.Result().(ErrorsResult)

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 schema_validation error, got %d", len(result.Errors))
	}
	if result.Errors[0].Type != "schema_validation" {
		t.Errorf("Type: got %q, want schema_validation", result.Errors[0].Type)
	}
}

func TestErrorsProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false initially")
	}

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{"text": "steer"},
	}))

	if !proc.Changed() {
		t.Error("Changed() should be true after an error event")
	}

	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after Result()")
	}
}

func TestErrorsProcessor_EmptyResult(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewErrorsProcessor(rs)

	result := proc.Result().(ErrorsResult)

	if result.Errors == nil {
		t.Error("Errors should not be nil (should be empty slice)")
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(result.Errors))
	}
}
