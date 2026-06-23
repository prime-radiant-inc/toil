package inspect

import (
	"encoding/json"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestFlowProcessor_BasicFlow(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	dur := int64(5000) // 5 seconds

	// node_started
	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "write_code",
		Timestamp: t0,
	})

	// communicate decision via node_output
	argsPayload := map[string]any{
		"output": map[string]any{
			"decision": "pass",
			"message":  "tests pass",
		},
	}
	argsJSON, _ := json.Marshal(argsPayload)
	proc.ProcessEvent(makeNodeOutputEvent("write_code", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(argsJSON),
		},
	}))

	// node_completed
	proc.ProcessEvent(state.Event{
		Type:       "node_completed",
		NodeID:     "write_code",
		Timestamp:  t0.Add(5 * time.Second),
		DurationMs: &dur,
	})

	result := proc.Result().(FlowResult)

	if len(result.Events) != 3 {
		t.Fatalf("expected 3 flow events, got %d", len(result.Events))
	}

	// Verify order and types
	if result.Events[0].Type != "started" {
		t.Errorf("event[0].Type: got %q, want %q", result.Events[0].Type, "started")
	}
	if result.Events[0].Node != "write_code" {
		t.Errorf("event[0].Node: got %q, want %q", result.Events[0].Node, "write_code")
	}
	if result.Events[0].Ts != "2026-04-19T10:00:00Z" {
		t.Errorf("event[0].Ts: got %q, want %q", result.Events[0].Ts, "2026-04-19T10:00:00Z")
	}

	if result.Events[1].Type != "decision" {
		t.Errorf("event[1].Type: got %q, want %q", result.Events[1].Type, "decision")
	}
	if result.Events[1].Decision != "pass" {
		t.Errorf("event[1].Decision: got %q, want %q", result.Events[1].Decision, "pass")
	}
	if result.Events[1].Message != "tests pass" {
		t.Errorf("event[1].Message: got %q, want %q", result.Events[1].Message, "tests pass")
	}

	if result.Events[2].Type != "completed" {
		t.Errorf("event[2].Type: got %q, want %q", result.Events[2].Type, "completed")
	}
	if result.Events[2].DurationS != 5.0 {
		t.Errorf("event[2].DurationS: got %f, want 5.0", result.Events[2].DurationS)
	}
}

func TestFlowProcessor_EdgePrompts(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	// First node starts and makes a decision
	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "verify_code_meets_acceptance_criteria",
		Timestamp: t0,
	})

	argsJSON, _ := json.Marshal(map[string]any{
		"output": map[string]any{
			"decision": "revise",
			"message":  "needs changes",
		},
	})
	proc.ProcessEvent(makeNodeOutputEvent("verify_code_meets_acceptance_criteria", map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call-1",
			"arguments_json": string(argsJSON),
		},
	}))

	// Edge prompt targeting write_code
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "write_code",
		Text:      "Spec review found violations. Fix them.",
		Timestamp: t0.Add(1 * time.Second),
	})

	result := proc.Result().(FlowResult)

	// Find the edge event
	var edgeEvent *FlowEvent
	for i := range result.Events {
		if result.Events[i].Type == "edge" {
			edgeEvent = &result.Events[i]
			break
		}
	}

	if edgeEvent == nil {
		t.Fatal("expected an edge flow event")
	}
	if edgeEvent.From != "verify_code_meets_acceptance_criteria" {
		t.Errorf("edge From: got %q, want %q", edgeEvent.From, "verify_code_meets_acceptance_criteria")
	}
	if edgeEvent.To != "write_code" {
		t.Errorf("edge To: got %q, want %q", edgeEvent.To, "write_code")
	}
	if edgeEvent.Prompt != "Spec review found violations. Fix them." {
		t.Errorf("edge Prompt: got %q, want %q", edgeEvent.Prompt, "Spec review found violations. Fix them.")
	}
}

func TestFlowProcessor_LoopDetection(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	// Helper to emit a decision from a node
	emitDecision := func(nodeID, decision string, offset time.Duration) {
		proc.ProcessEvent(state.Event{
			Type:      "node_started",
			NodeID:    nodeID,
			Timestamp: t0.Add(offset),
		})
		argsJSON, _ := json.Marshal(map[string]any{
			"output": map[string]any{
				"decision": decision,
				"message":  "msg",
			},
		})
		proc.ProcessEvent(makeNodeOutputEvent(nodeID, map[string]any{
			"kind": "TOOL_CALL_START",
			"data": map[string]any{
				"tool_name":      "communicate",
				"call_id":        "call-" + nodeID,
				"arguments_json": string(argsJSON),
			},
		}))
	}

	// Simulate: write_code→verify_code_meets_acceptance_criteria loop 3 times, then breaks
	// Round 1: write_code decides "review" → edge to verify_code_meets_acceptance_criteria
	emitDecision("write_code", "review", 0)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "verify_code_meets_acceptance_criteria",
		Text:      "Review the code",
		Timestamp: t0.Add(1 * time.Second),
	})

	// verify_code_meets_acceptance_criteria decides "revise" → edge to write_code
	emitDecision("verify_code_meets_acceptance_criteria", "revise", 2*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "write_code",
		Text:      "Fix violations",
		Timestamp: t0.Add(3 * time.Second),
	})

	// Round 2
	emitDecision("write_code", "review", 4*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "verify_code_meets_acceptance_criteria",
		Text:      "Review the code",
		Timestamp: t0.Add(5 * time.Second),
	})

	emitDecision("verify_code_meets_acceptance_criteria", "revise", 6*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "write_code",
		Text:      "Fix violations",
		Timestamp: t0.Add(7 * time.Second),
	})

	// Round 3
	emitDecision("write_code", "review", 8*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "verify_code_meets_acceptance_criteria",
		Text:      "Review the code",
		Timestamp: t0.Add(9 * time.Second),
	})

	emitDecision("verify_code_meets_acceptance_criteria", "revise", 10*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "write_code",
		Text:      "Fix violations",
		Timestamp: t0.Add(11 * time.Second),
	})

	// Break the loop: verify_code_meets_acceptance_criteria decides "approve" → edge to deliver
	emitDecision("write_code", "review", 12*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "verify_code_meets_acceptance_criteria",
		Text:      "Review the code",
		Timestamp: t0.Add(13 * time.Second),
	})

	emitDecision("verify_code_meets_acceptance_criteria", "approve", 14*time.Second)
	proc.ProcessEvent(state.Event{
		Type:      "node_edge_prompt",
		NodeID:    "deliver",
		Text:      "Deliver the code",
		Timestamp: t0.Add(15 * time.Second),
	})

	result := proc.Result().(FlowResult)

	// Should have at least one loop_detected annotation
	var loopAnnotation *FlowAnnotation
	for i := range result.Annotations {
		if result.Annotations[i].Type == "loop_detected" {
			loopAnnotation = &result.Annotations[i]
			break
		}
	}

	if loopAnnotation == nil {
		t.Fatal("expected a loop_detected annotation")
	}

	if loopAnnotation.Count < 3 {
		t.Errorf("loop Count: got %d, want >= 3", loopAnnotation.Count)
	}

	// The nodes involved should include both write_code and verify_code_meets_acceptance_criteria
	nodeSet := map[string]bool{}
	for _, n := range loopAnnotation.Nodes {
		nodeSet[n] = true
	}
	if !nodeSet["write_code"] || !nodeSet["verify_code_meets_acceptance_criteria"] {
		t.Errorf("loop Nodes: got %v, want to include write_code and verify_code_meets_acceptance_criteria", loopAnnotation.Nodes)
	}

	if loopAnnotation.ResolvedBy != "approve" {
		t.Errorf("loop ResolvedBy: got %q, want %q", loopAnnotation.ResolvedBy, "approve")
	}
}

func TestFlowProcessor_ConcurrentNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	dur1 := int64(5000)
	dur2 := int64(3000)

	// node-a: starts at t0, completes at t0+5s
	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "node-a",
		Timestamp: t0,
	})

	// node-b: starts at t0+2s, completes at t0+5s (overlaps with node-a)
	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "node-b",
		Timestamp: t0.Add(2 * time.Second),
	})

	proc.ProcessEvent(state.Event{
		Type:       "node_completed",
		NodeID:     "node-b",
		Timestamp:  t0.Add(5 * time.Second),
		DurationMs: &dur2,
	})

	proc.ProcessEvent(state.Event{
		Type:       "node_completed",
		NodeID:     "node-a",
		Timestamp:  t0.Add(5 * time.Second),
		DurationMs: &dur1,
	})

	result := proc.Result().(FlowResult)

	var concurrentAnnotation *FlowAnnotation
	for i := range result.Annotations {
		if result.Annotations[i].Type == "concurrent" {
			concurrentAnnotation = &result.Annotations[i]
			break
		}
	}

	if concurrentAnnotation == nil {
		t.Fatal("expected a concurrent annotation")
	}

	nodeSet := map[string]bool{}
	for _, n := range concurrentAnnotation.Nodes {
		nodeSet[n] = true
	}
	if !nodeSet["node-a"] || !nodeSet["node-b"] {
		t.Errorf("concurrent Nodes: got %v, want to include node-a and node-b", concurrentAnnotation.Nodes)
	}

	if concurrentAnnotation.WallS <= 0 {
		t.Errorf("concurrent WallS: got %f, want > 0", concurrentAnnotation.WallS)
	}
}

func TestFlowProcessor_SteeringAnnotation(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "write_code",
		Timestamp: t0,
	})

	// Two steering events for write_code
	proc.ProcessEvent(makeNodeOutputEvent("write_code", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "You must reconsider your approach.",
		},
	}))
	proc.ProcessEvent(makeNodeOutputEvent("write_code", map[string]any{
		"kind": "STEERING_INJECTED",
		"data": map[string]any{
			"text": "Focus on the failing tests.",
		},
	}))

	result := proc.Result().(FlowResult)

	// Should have steering flow events
	steeringEvents := 0
	for _, e := range result.Events {
		if e.Type == "steering" {
			steeringEvents++
		}
	}
	if steeringEvents != 2 {
		t.Errorf("expected 2 steering flow events, got %d", steeringEvents)
	}

	// Should have a steering annotation
	var steeringAnnotation *FlowAnnotation
	for i := range result.Annotations {
		if result.Annotations[i].Type == "steering" {
			steeringAnnotation = &result.Annotations[i]
			break
		}
	}

	if steeringAnnotation == nil {
		t.Fatal("expected a steering annotation")
	}
	if steeringAnnotation.Node != "write_code" {
		t.Errorf("steering Node: got %q, want %q", steeringAnnotation.Node, "write_code")
	}
	if steeringAnnotation.Count != 2 {
		t.Errorf("steering Count: got %d, want 2", steeringAnnotation.Count)
	}
}

func TestFlowProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false before any events")
	}

	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "node-a",
		Timestamp: time.Now().UTC(),
	})

	if !proc.Changed() {
		t.Error("Changed() should be true after processing events")
	}

	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after calling Result()")
	}
}

func TestFlowProcessor_NodeFailed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	t0 := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	proc.ProcessEvent(state.Event{
		Type:      "node_started",
		NodeID:    "write_code",
		Timestamp: t0,
	})
	proc.ProcessEvent(state.Event{
		Type:      "node_failed",
		NodeID:    "write_code",
		Timestamp: t0.Add(3 * time.Second),
	})

	result := proc.Result().(FlowResult)

	if len(result.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result.Events))
	}
	if result.Events[1].Type != "failed" {
		t.Errorf("event[1].Type: got %q, want %q", result.Events[1].Type, "failed")
	}
	if result.Events[1].Node != "write_code" {
		t.Errorf("event[1].Node: got %q, want %q", result.Events[1].Node, "write_code")
	}
}

func TestFlowProcessor_EmptyResult(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewFlowProcessor(rs)

	result := proc.Result().(FlowResult)

	if result.Events == nil {
		t.Error("Events should be non-nil empty slice")
	}
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(result.Events))
	}
	if result.Annotations == nil {
		t.Error("Annotations should be non-nil empty slice")
	}
	if len(result.Annotations) != 0 {
		t.Errorf("expected 0 annotations, got %d", len(result.Annotations))
	}
}
