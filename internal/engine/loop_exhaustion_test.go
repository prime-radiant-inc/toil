package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestLoopExhaustedWithoutTargetFails(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Loop Exhaustion Fail Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "looper", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "looper"},
			{From: "looper", To: "looper"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{})
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	eng := &Engine{RunsDir: dir}
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, dir, workflow, runState, runContext, logger,
		[]readyNode{{ID: "start"}})
	if err == nil {
		t.Fatal("expected error for loop exhaustion without target")
	}
	if !contains(err.Error(), "max loop iterations exceeded") {
		t.Fatalf("expected loop exceeded error, got: %v", err)
	}
}

func TestLoopExhaustedWithoutTargetLogsEvent(t *testing.T) {
	dir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Loop Exhaustion Event Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "looper", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "looper"},
			{From: "looper", To: "looper"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	runState := state.NewRunState(testRunID1, "wf", map[string]any{})

	eng := &Engine{RunsDir: dir}
	runContext := &RunContext{
		Inputs:  map[string]any{},
		Outputs: map[string]NodeOutput{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, dir, workflow, runState, runContext, logger,
		[]readyNode{{ID: "start"}})

	if err == nil {
		t.Fatal("expected error")
	}

	// Error should include decision and message from the last system node execution
	if !strings.Contains(err.Error(), "decision=default") {
		t.Fatalf("expected decision in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "system node completed") {
		t.Fatalf("expected message in error, got: %v", err)
	}
	// Short message (< 200 chars) should NOT be truncated
	if strings.Contains(err.Error(), "...") {
		t.Fatalf("short message should not be truncated, got: %v", err)
	}

	// Event log should contain loop_exhausted_failed
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read events: %v", readErr)
	}
	events := string(data)
	if !strings.Contains(events, `"loop_exhausted_failed"`) {
		t.Fatalf("expected loop_exhausted_failed event in log:\n%s", events)
	}
	if !strings.Contains(events, `"last_decision":"default"`) {
		t.Fatalf("expected last_decision in event data:\n%s", events)
	}
}

// TestExhaustionRoutesViaLoopExhaustedMetaDecision verifies that when a node
// has an outgoing edge with when: _loop_exhausted, the meta-decision routing
// path fires instead of the fatal error path.
//
// Graph: start → looper (self-loop, limit=2) with an outgoing _loop_exhausted
// edge to "done". After 3 dispatches (2 normal + 1 exhausted), the
// meta-decision fires and routes to "done".
func TestExhaustionRoutesViaLoopExhaustedMetaDecision(t *testing.T) {
	dir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:      "wf-meta",
		Name:    "Meta Decision Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "looper", Kind: "system"},
			{ID: "done", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "looper"},
			{From: "looper", To: "looper"},
			{From: "looper", To: "done", When: "_loop_exhausted"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	runState := state.NewRunState(testRunID1, "wf-meta", map[string]any{})
	eng := &Engine{RunsDir: dir, Definitions: &definitions.Bundle{}}
	runContext := &RunContext{
		Inputs:  map[string]any{},
		Outputs: map[string]NodeOutput{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, dir, workflow, runState, runContext, logger,
		[]readyNode{{ID: "start"}})
	if err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	// done node should have been reached.
	doneStatus, exists := runState.NodeStatus("done")
	if !exists {
		t.Fatal("done node was not executed")
	}
	if doneStatus != statusCompleted {
		t.Fatalf("expected done completed, got %q", doneStatus)
	}

	// looper's LastRoutingDecision should be _loop_exhausted (set by
	// synthesizeMetaCompletion before lazy-reset clears it on next dispatch).
	// Since looper is not re-dispatched after routing to done, it stays set.
	runState.WithNode("looper", func(n *state.NodeState) {
		if n.LastRoutingDecision != MetaDecisionLoopExhausted {
			t.Errorf("LastRoutingDecision=%q want %q", n.LastRoutingDecision, MetaDecisionLoopExhausted)
		}
	})
}

// TestExhaustionMetaDecisionLazyResetsCounter verifies that after a
// _loop_exhausted meta-decision, if the node is re-dispatched (e.g., the
// meta-decision target eventually routes back), the loop counter resets to 1
// rather than continuing from the exhausted value.
func TestExhaustionMetaDecisionLazyResetsCounter(t *testing.T) {
	rs := state.NewRunState("r-lazy", "wf", map[string]any{})

	// Simulate exhaustion: set LoopIterations=3 and LastRoutingDecision=_loop_exhausted
	rs.WithNode("looper", func(n *state.NodeState) {
		n.LoopIterations = 3
		n.LastRoutingDecision = MetaDecisionLoopExhausted
	})

	// Next dispatch should reset counter to 0 then increment to 1.
	count, exhausted := getAndIncrementLoopIterations(rs, "looper", 5)
	if count != 1 {
		t.Errorf("after lazy reset: count=%d want 1", count)
	}
	if exhausted {
		t.Error("after lazy reset: exhausted=true want false")
	}

	// LastRoutingDecision should be cleared.
	rs.WithNode("looper", func(n *state.NodeState) {
		if n.LastRoutingDecision != "" {
			t.Errorf("LastRoutingDecision=%q want cleared", n.LastRoutingDecision)
		}
		if n.LastRoutingAt != nil {
			t.Error("LastRoutingAt should be nil after lazy reset")
		}
	})
}

// TestLoopExhaustedErrorTruncatesLongMessage verifies the loopExhaustedError
// helper truncates messages longer than 200 characters with "..." and
// preserves short messages as-is.
func TestLoopExhaustedErrorTruncatesLongMessage(t *testing.T) {
	longMsg := strings.Repeat("x", 300)

	// Long message should be truncated
	err := loopExhaustedError("looper", "fail", longMsg)
	if !strings.Contains(err.Error(), "...") {
		t.Fatalf("expected truncation ellipsis in error, got: %v", err)
	}
	if strings.Contains(err.Error(), longMsg) {
		t.Fatalf("expected message to be truncated, but got full message in error")
	}
	if !strings.Contains(err.Error(), "decision=fail") {
		t.Fatalf("expected decision in error, got: %v", err)
	}

	// Short message should NOT be truncated
	shortErr := loopExhaustedError("looper", "pass", "all tests passed")
	if strings.Contains(shortErr.Error(), "...") {
		t.Fatalf("short message should not be truncated, got: %v", shortErr)
	}
	if !strings.Contains(shortErr.Error(), "all tests passed") {
		t.Fatalf("expected full short message in error, got: %v", shortErr)
	}

	// Empty message should work
	emptyErr := loopExhaustedError("node", "", "")
	if !strings.Contains(emptyErr.Error(), "max loop iterations exceeded for node") {
		t.Fatalf("expected base error message, got: %v", emptyErr)
	}
}
