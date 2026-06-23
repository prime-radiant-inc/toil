package inspect

import (
	"encoding/json"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// makeToolCallEvent builds a TOOL_CALL_START node_output event.
func makeToolCallEvent(nodeID, toolName, argsJSON string) state.Event {
	return makeNodeOutputEvent(nodeID, map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      toolName,
			"call_id":        "call-1",
			"arguments_json": argsJSON,
		},
	})
}

// makeRoundTimingsEvent builds a ROUND_TIMINGS node_output event.
func makeRoundTimingsEvent(nodeID string, round int, totalRoundNs int64) state.Event {
	return makeNodeOutputEvent(nodeID, map[string]any{
		"kind": "ROUND_TIMINGS",
		"data": map[string]any{
			"round":             round,
			"total_round_ns":    totalRoundNs,
			"input_tokens":      100,
			"output_tokens":     50,
			"cache_read_tokens": 0,
			"reasoning_tokens":  0,
		},
	})
}

// makeSessionStartEvent builds a SESSION_START node_output event with model only
// (simulating the `data` field format used in tests).
func makeSessionStartEvent(nodeID, model string) state.Event {
	return makeNodeOutputEvent(nodeID, map[string]any{
		"kind": "SESSION_START",
		"data": map[string]any{
			"model": model,
		},
	})
}

// makeCommunicateEvent builds a TOOL_CALL_START event for the communicate tool.
func makeCommunicateEvent(nodeID, decision, message string) state.Event {
	argsPayload := map[string]any{
		"output": map[string]any{
			"decision": decision,
			"message":  message,
		},
	}
	argsJSON, _ := json.Marshal(argsPayload)
	return makeToolCallEvent(nodeID, "communicate", string(argsJSON))
}

func TestTranscriptProcessor_SingleNodeToolCalls(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "read_file", `{"path":"/tmp/foo.txt"}`))
	proc.ProcessEvent(makeToolCallEvent("node-a", "write_file", `{"path":"/tmp/out.txt","content":"hello"}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 2_000_000_000))

	result := proc.Result().(TranscriptResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	node := result.Nodes[0]
	if node.ID != "node-a" {
		t.Errorf("ID: got %q, want node-a", node.ID)
	}
	if len(node.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(node.Attempts))
	}
	attempt := node.Attempts[0]
	if attempt.Attempt != 1 {
		t.Errorf("Attempt: got %d, want 1", attempt.Attempt)
	}
	if len(attempt.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(attempt.Rounds))
	}
	round := attempt.Rounds[0]
	if round.Round != 1 {
		t.Errorf("Round: got %d, want 1", round.Round)
	}
	if len(round.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(round.ToolCalls))
	}
	if round.ToolCalls[0].Tool != "read_file" {
		t.Errorf("ToolCalls[0].Tool: got %q, want read_file", round.ToolCalls[0].Tool)
	}
	if round.ToolCalls[1].Tool != "write_file" {
		t.Errorf("ToolCalls[1].Tool: got %q, want write_file", round.ToolCalls[1].Tool)
	}
}

func TestTranscriptProcessor_ArgsPreviewTruncation(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	longArgs := `{"content":"` + strings.Repeat("x", 300) + `"}`

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "write_file", longArgs))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))

	result := proc.Result().(TranscriptResult)

	tc := result.Nodes[0].Attempts[0].Rounds[0].ToolCalls[0]
	if len(tc.ArgsPreview) > 200 {
		t.Errorf("ArgsPreview should be truncated to 200 chars, got %d", len(tc.ArgsPreview))
	}
	if tc.ArgsSize != len(longArgs) {
		t.Errorf("ArgsSize: got %d, want %d", tc.ArgsSize, len(longArgs))
	}
}

func TestTranscriptProcessor_ArgsNoTruncationWhenShort(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	shortArgs := `{"path":"/tmp/foo.txt"}`

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "read_file", shortArgs))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))

	result := proc.Result().(TranscriptResult)

	tc := result.Nodes[0].Attempts[0].Rounds[0].ToolCalls[0]
	if tc.ArgsPreview != shortArgs {
		t.Errorf("ArgsPreview: got %q, want %q", tc.ArgsPreview, shortArgs)
	}
	if tc.ArgsSize != len(shortArgs) {
		t.Errorf("ArgsSize: got %d, want %d", tc.ArgsSize, len(shortArgs))
	}
}

func TestTranscriptProcessor_MultipleAttempts(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	// Attempt 1
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "read_file", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))

	// Attempt 2
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "write_file", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 2_000_000_000))

	result := proc.Result().(TranscriptResult)

	node := result.Nodes[0]
	if len(node.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(node.Attempts))
	}
	if node.Attempts[0].Attempt != 1 {
		t.Errorf("Attempts[0].Attempt: got %d, want 1", node.Attempts[0].Attempt)
	}
	if node.Attempts[1].Attempt != 2 {
		t.Errorf("Attempts[1].Attempt: got %d, want 2", node.Attempts[1].Attempt)
	}
	if node.Attempts[0].Rounds[0].ToolCalls[0].Tool != "read_file" {
		t.Errorf("attempt 1 tool: got %q, want read_file", node.Attempts[0].Rounds[0].ToolCalls[0].Tool)
	}
	if node.Attempts[1].Rounds[0].ToolCalls[0].Tool != "write_file" {
		t.Errorf("attempt 2 tool: got %q, want write_file", node.Attempts[1].Rounds[0].ToolCalls[0].Tool)
	}
}

func TestTranscriptProcessor_RoundTiming(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	// 5 seconds in nanoseconds
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 5_000_000_000))

	result := proc.Result().(TranscriptResult)

	round := result.Nodes[0].Attempts[0].Rounds[0]
	if round.DurationS != 5.0 {
		t.Errorf("DurationS: got %f, want 5.0", round.DurationS)
	}
}

func TestTranscriptProcessor_Decision(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))
	proc.ProcessEvent(makeCommunicateEvent("node-a", "approve", "looks good"))

	result := proc.Result().(TranscriptResult)

	attempt := result.Nodes[0].Attempts[0]
	if attempt.Decision != "approve" {
		t.Errorf("Decision: got %q, want approve", attempt.Decision)
	}
	if attempt.Message != "looks good" {
		t.Errorf("Message: got %q, want 'looks good'", attempt.Message)
	}
}

func TestTranscriptProcessor_Model(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeSessionStartEvent("node-a", "claude-opus-4-5"))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))

	result := proc.Result().(TranscriptResult)

	attempt := result.Nodes[0].Attempts[0]
	if attempt.Model != "claude-opus-4-5" {
		t.Errorf("Model: got %q, want claude-opus-4-5", attempt.Model)
	}
}

func TestTranscriptProcessor_MultipleNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-b"})
	proc.ProcessEvent(makeToolCallEvent("node-b", "list_files", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-b", 1, 1_000_000_000))

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "read_file", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 2_000_000_000))

	result := proc.Result().(TranscriptResult)

	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	// Nodes sorted by ID
	if result.Nodes[0].ID != "node-a" {
		t.Errorf("Nodes[0].ID: got %q, want node-a", result.Nodes[0].ID)
	}
	if result.Nodes[1].ID != "node-b" {
		t.Errorf("Nodes[1].ID: got %q, want node-b", result.Nodes[1].ID)
	}
}

func TestTranscriptProcessor_MultipleRounds(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeToolCallEvent("node-a", "read_file", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 1, 1_000_000_000))
	proc.ProcessEvent(makeToolCallEvent("node-a", "write_file", `{}`))
	proc.ProcessEvent(makeRoundTimingsEvent("node-a", 2, 3_000_000_000))

	result := proc.Result().(TranscriptResult)

	attempt := result.Nodes[0].Attempts[0]
	if len(attempt.Rounds) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(attempt.Rounds))
	}
	if attempt.Rounds[0].Round != 1 {
		t.Errorf("Rounds[0].Round: got %d, want 1", attempt.Rounds[0].Round)
	}
	if attempt.Rounds[1].Round != 2 {
		t.Errorf("Rounds[1].Round: got %d, want 2", attempt.Rounds[1].Round)
	}
	if len(attempt.Rounds[0].ToolCalls) != 1 {
		t.Errorf("round 1 tool calls: got %d, want 1", len(attempt.Rounds[0].ToolCalls))
	}
	if len(attempt.Rounds[1].ToolCalls) != 1 {
		t.Errorf("round 2 tool calls: got %d, want 1", len(attempt.Rounds[1].ToolCalls))
	}
}

func TestTranscriptProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false before any events")
	}

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	if !proc.Changed() {
		t.Error("Changed() should be true after node_started")
	}

	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after calling Result()")
	}
}

func TestTranscriptProcessor_EmptyRun(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTranscriptProcessor(rs)

	result := proc.Result().(TranscriptResult)

	if result.Nodes == nil {
		t.Error("Nodes should not be nil for empty run")
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(result.Nodes))
	}
}
