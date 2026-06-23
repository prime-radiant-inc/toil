package inspect

import (
	"encoding/json"
	"testing"

	"primeradiant.com/toil/internal/state"
)

func TestDecisionsProcessor_ExtractsCommunicateDecisions(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	// First, a node_started event to establish attempt 1
	proc.ProcessEvent(state.Event{
		Type:   "node_started",
		NodeID: "node-a",
	})

	// Then a communicate tool call
	argsPayload := map[string]any{
		"output": map[string]any{
			"decision": "approve",
			"message":  "looks good",
			"data":     map[string]any{"score": 9.0},
		},
	}
	argsJSON, _ := json.Marshal(argsPayload)
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(argsJSON),
		},
	}))

	result := proc.Result().(DecisionsResult)

	if len(result.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(result.Decisions))
	}

	d := result.Decisions[0]
	if d.Node != "node-a" {
		t.Errorf("Node: got %q, want %q", d.Node, "node-a")
	}
	if d.Attempt != 1 {
		t.Errorf("Attempt: got %d, want 1", d.Attempt)
	}
	if d.Decision != "approve" {
		t.Errorf("Decision: got %q, want %q", d.Decision, "approve")
	}
	if d.Message != "looks good" {
		t.Errorf("Message: got %q, want %q", d.Message, "looks good")
	}
	if d.Data == nil {
		t.Fatal("Data should not be nil")
	}
	if d.Data["score"] != 9.0 {
		t.Errorf("Data[score]: got %v, want 9.0", d.Data["score"])
	}
}

func TestDecisionsProcessor_MultipleAttemptsForSameNode(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	argsReject, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "reject",
			"message":  "needs work",
		},
	})
	argsApprove, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "approve",
			"message":  "fixed",
		},
	})

	// Attempt 1
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(argsReject),
		},
	}))

	// Attempt 2
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-2",
			"arguments_json": string(argsApprove),
		},
	}))

	result := proc.Result().(DecisionsResult)

	if len(result.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(result.Decisions))
	}

	if result.Decisions[0].Attempt != 1 {
		t.Errorf("first decision attempt: got %d, want 1", result.Decisions[0].Attempt)
	}
	if result.Decisions[0].Decision != "reject" {
		t.Errorf("first decision: got %q, want %q", result.Decisions[0].Decision, "reject")
	}

	if result.Decisions[1].Attempt != 2 {
		t.Errorf("second decision attempt: got %d, want 2", result.Decisions[1].Attempt)
	}
	if result.Decisions[1].Decision != "approve" {
		t.Errorf("second decision: got %q, want %q", result.Decisions[1].Decision, "approve")
	}
}

func TestDecisionsProcessor_IgnoresNonCommunicateToolCalls(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "bash",
			"call_id":        "call-1",
			"arguments_json": `{"command":"ls"}`,
		},
	}))

	result := proc.Result().(DecisionsResult)

	if len(result.Decisions) != 0 {
		t.Errorf("expected 0 decisions for non-communicate tool call, got %d", len(result.Decisions))
	}
}

func TestDecisionsProcessor_MultipleNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	argsA, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "pass",
			"message":  "tests pass",
		},
	})
	argsB, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "fail",
			"message":  "tests fail",
		},
	})

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-b"})

	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(argsA),
		},
	}))
	proc.ProcessEvent(makeNodeOutputEvent("node-b", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-2",
			"arguments_json": string(argsB),
		},
	}))

	result := proc.Result().(DecisionsResult)

	if len(result.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(result.Decisions))
	}
}

func TestDecisionsProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false before any events")
	}

	args, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "done",
			"message":  "complete",
		},
	})
	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(args),
		},
	}))

	if !proc.Changed() {
		t.Error("Changed() should be true after processing a communicate event")
	}

	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after calling Result()")
	}
}

func TestDecisionsProcessor_DataFieldPreserved(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewDecisionsProcessor(rs)

	args, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "approve",
			"message":  "with data",
			"data": map[string]any{
				"score":    8.5,
				"category": "A",
				"tags":     []any{"urgent", "reviewed"},
			},
		},
	})

	proc.ProcessEvent(state.Event{Type: "node_started", NodeID: "node-a"})
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(args),
		},
	}))

	result := proc.Result().(DecisionsResult)

	d := result.Decisions[0]
	if d.Data["score"] != 8.5 {
		t.Errorf("Data[score]: got %v, want 8.5", d.Data["score"])
	}
	if d.Data["category"] != "A" {
		t.Errorf("Data[category]: got %v, want A", d.Data["category"])
	}
	tags, ok := d.Data["tags"].([]any)
	if !ok {
		t.Fatal("Data[tags] should be a slice")
	}
	if len(tags) != 2 {
		t.Errorf("Data[tags] length: got %d, want 2", len(tags))
	}
}
