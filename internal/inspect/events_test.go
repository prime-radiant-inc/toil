package inspect

import (
	"encoding/json"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// makeNodeOutputEvent creates a node_output state.Event with the given JSON
// payload double-encoded into the Text field (matching real runner output).
func makeNodeOutputEvent(nodeID string, payload any) state.Event {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return state.Event{
		Type:   "node_output",
		NodeID: nodeID,
		Text:   string(b),
	}
}

func TestParseRunnerEvent_SerfRoundTimings(t *testing.T) {
	payload := map[string]any{
		"kind": "ROUND_TIMINGS",
		"data": map[string]any{
			"round":             2,
			"total_round_ns":    int64(5_000_000_000),
			"input_tokens":      1234,
			"output_tokens":     567,
			"cache_read_tokens": 890,
			"reasoning_tokens":  42,
		},
	}
	ev := makeNodeOutputEvent("node-a", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.NodeID != "node-a" {
		t.Errorf("NodeID: got %q, want %q", inner.NodeID, "node-a")
	}
	if inner.Kind != "ROUND_TIMINGS" {
		t.Errorf("Kind: got %q, want %q", inner.Kind, "ROUND_TIMINGS")
	}
	if inner.RoundTimings == nil {
		t.Fatal("RoundTimings is nil")
	}
	rt := inner.RoundTimings
	if rt.Round != 2 {
		t.Errorf("Round: got %d, want 2", rt.Round)
	}
	if rt.TotalRoundNs != 5_000_000_000 {
		t.Errorf("TotalRoundNs: got %d, want 5000000000", rt.TotalRoundNs)
	}
	if rt.InputTokens != 1234 {
		t.Errorf("InputTokens: got %d, want 1234", rt.InputTokens)
	}
	if rt.OutputTokens != 567 {
		t.Errorf("OutputTokens: got %d, want 567", rt.OutputTokens)
	}
	if rt.CacheReadTokens != 890 {
		t.Errorf("CacheReadTokens: got %d, want 890", rt.CacheReadTokens)
	}
	if rt.ReasoningTokens != 42 {
		t.Errorf("ReasoningTokens: got %d, want 42", rt.ReasoningTokens)
	}
}

func TestParseRunnerEvent_NonNodeOutput(t *testing.T) {
	ev := state.Event{
		Type:   "node_started",
		NodeID: "node-b",
		Text:   `{"kind":"ROUND_TIMINGS","data":{}}`,
	}
	_, ok := ParseRunnerEvent(ev)
	if ok {
		t.Fatal("expected ParseRunnerEvent to return false for non-node_output event")
	}
}

func TestParseRunnerEvent_SerfSessionStart(t *testing.T) {
	payload := map[string]any{
		"kind": "SESSION_START",
		"data": map[string]any{
			"profile": "default",
			"model":   "claude-opus-4-5",
		},
	}
	ev := makeNodeOutputEvent("node-c", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.Kind != "SESSION_START" {
		t.Errorf("Kind: got %q, want %q", inner.Kind, "SESSION_START")
	}
	if inner.SessionStart == nil {
		t.Fatal("SessionStart is nil")
	}
	if inner.SessionStart.Profile != "default" {
		t.Errorf("Profile: got %q, want %q", inner.SessionStart.Profile, "default")
	}
	if inner.SessionStart.Model != "claude-opus-4-5" {
		t.Errorf("Model: got %q, want %q", inner.SessionStart.Model, "claude-opus-4-5")
	}
}

func TestParseRunnerEvent_SerfCommunicate(t *testing.T) {
	argsPayload := map[string]any{
		"output": map[string]any{
			"decision": "approve",
			"message":  "looks good",
			"data":     map[string]any{"score": 9},
		},
	}
	argsJSON, _ := json.Marshal(argsPayload)

	payload := map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-123",
			"arguments_json": string(argsJSON),
		},
	}
	ev := makeNodeOutputEvent("node-d", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.Kind != "TOOL_CALL_START" {
		t.Errorf("Kind: got %q, want %q", inner.Kind, "TOOL_CALL_START")
	}
	if inner.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	if inner.ToolCall.Name != "communicate" {
		t.Errorf("ToolCall.Name: got %q, want %q", inner.ToolCall.Name, "communicate")
	}
	if inner.Communicate == nil {
		t.Fatal("Communicate is nil")
	}
	if inner.Communicate.Decision != "approve" {
		t.Errorf("Decision: got %q, want %q", inner.Communicate.Decision, "approve")
	}
	if inner.Communicate.Message != "looks good" {
		t.Errorf("Message: got %q, want %q", inner.Communicate.Message, "looks good")
	}
}

func TestParseRunnerEvent_SerfSteeringInjected(t *testing.T) {
	payload := map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "You must reconsider your approach.",
		},
	}
	ev := makeNodeOutputEvent("node-e", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.Kind != "STEERING_INJECTED" {
		t.Errorf("Kind: got %q, want %q", inner.Kind, "STEERING_INJECTED")
	}
	if inner.SteeringText != "You must reconsider your approach." {
		t.Errorf("SteeringText: got %q, want %q", inner.SteeringText, "You must reconsider your approach.")
	}
}

func TestParseRunnerEvent_SchemaValidationError(t *testing.T) {
	payload := map[string]any{
		"kind": "TOOL_CALL_OUTPUT_DELTA",
		"data": map[string]any{
			"delta": "tool args schema validation failed: output.decision is required",
		},
	}
	ev := makeNodeOutputEvent("node-f", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.Kind != "TOOL_CALL_OUTPUT_DELTA" {
		t.Errorf("Kind: got %q, want %q", inner.Kind, "TOOL_CALL_OUTPUT_DELTA")
	}
	want := "tool args schema validation failed: output.decision is required"
	if inner.SchemaError != want {
		t.Errorf("SchemaError: got %q, want %q", inner.SchemaError, want)
	}
}

func TestParseRunnerEvent_AssistantTextEnd(t *testing.T) {
	payload := map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"text":  "",
			"model": "gpt-5.4-2026-03-05",
			"usage": map[string]any{
				"input_tokens":      12120,
				"output_tokens":     240,
				"cache_read_tokens": 11136,
				"reasoning_tokens":  45,
			},
		},
	}
	ev := makeNodeOutputEvent("node-h", payload)

	inner, ok := ParseRunnerEvent(ev)
	if !ok {
		t.Fatal("expected ParseRunnerEvent to return true")
	}
	if inner.Kind != "ASSISTANT_TEXT_END" {
		t.Errorf("Kind: got %q, want ASSISTANT_TEXT_END", inner.Kind)
	}
	if inner.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if inner.Usage.InputTokens != 12120 {
		t.Errorf("InputTokens: got %d, want 12120", inner.Usage.InputTokens)
	}
	if inner.Usage.OutputTokens != 240 {
		t.Errorf("OutputTokens: got %d, want 240", inner.Usage.OutputTokens)
	}
	if inner.Usage.CacheReadTokens != 11136 {
		t.Errorf("CacheReadTokens: got %d, want 11136", inner.Usage.CacheReadTokens)
	}
	if inner.Usage.ReasoningTokens != 45 {
		t.Errorf("ReasoningTokens: got %d, want 45", inner.Usage.ReasoningTokens)
	}
	if inner.Usage.Model != "gpt-5.4-2026-03-05" {
		t.Errorf("Model: got %q, want gpt-5.4-2026-03-05", inner.Usage.Model)
	}
}

func TestParseRunnerEvent_PlainText(t *testing.T) {
	ev := state.Event{
		Type:   "node_output",
		NodeID: "node-g",
		Text:   "This is just plain text output, not JSON.",
	}
	_, ok := ParseRunnerEvent(ev)
	if ok {
		t.Fatal("expected ParseRunnerEvent to return false for plain text")
	}
}

func TestChildRun(t *testing.T) {
	t.Run("extracts child_run from Data", func(t *testing.T) {
		node := &state.NodeState{
			Data: map[string]any{
				"child_run": "run-abc-123",
			},
		}
		got := ChildRun(node)
		if got != "run-abc-123" {
			t.Errorf("got %q, want %q", got, "run-abc-123")
		}
	})

	t.Run("nil node returns empty string", func(t *testing.T) {
		got := ChildRun(nil)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("nil Data returns empty string", func(t *testing.T) {
		node := &state.NodeState{}
		got := ChildRun(node)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("missing child_run key returns empty string", func(t *testing.T) {
		node := &state.NodeState{
			Data: map[string]any{
				"other_key": "value",
			},
		}
		got := ChildRun(node)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}

func TestChildRun_NonStringValue(t *testing.T) {
	node := &state.NodeState{
		Data: map[string]any{
			"child_run": 12345,
		},
	}
	got := ChildRun(node)
	if got != "" {
		t.Errorf("got %q, want empty string for non-string child_run", got)
	}
}

func TestDetectAttemptBoundaries_EmptyEvents(t *testing.T) {
	boundaries := DetectAttemptBoundaries(nil, "node-x")
	if len(boundaries) != 0 {
		t.Errorf("expected 0 boundaries for nil events, got %d", len(boundaries))
	}
	boundaries = DetectAttemptBoundaries([]state.Event{}, "node-x")
	if len(boundaries) != 0 {
		t.Errorf("expected 0 boundaries for empty events, got %d", len(boundaries))
	}
}

func TestDetectAttemptBoundaries(t *testing.T) {
	events := []state.Event{
		{Type: "run_started", NodeID: ""},
		{Type: "node_started", NodeID: "node-x"},
		{Type: "node_output", NodeID: "node-x"},
		{Type: "node_started", NodeID: "node-y"},
		{Type: "node_started", NodeID: "node-x"},
		{Type: "node_output", NodeID: "node-x"},
		{Type: "node_completed", NodeID: "node-x"},
	}

	boundaries := DetectAttemptBoundaries(events, "node-x")

	if len(boundaries) != 2 {
		t.Fatalf("expected 2 boundaries, got %d", len(boundaries))
	}
	if boundaries[0] != 1 {
		t.Errorf("boundaries[0]: got %d, want 1", boundaries[0])
	}
	if boundaries[1] != 4 {
		t.Errorf("boundaries[1]: got %d, want 4", boundaries[1])
	}

	// Node with no starts
	none := DetectAttemptBoundaries(events, "node-z")
	if len(none) != 0 {
		t.Errorf("expected 0 boundaries for node-z, got %d", len(none))
	}
}
