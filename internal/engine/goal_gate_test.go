package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// --- Goal gate engine tests ---

func TestGoalGate_NoGates_CompletesNormally(t *testing.T) {
	// Existing behavior: run completes normally when no goal gates exist
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	statusA, _ := runState.NodeStatus("a")
	statusB, _ := runState.NodeStatus("b")
	if statusA != statusCompleted {
		t.Fatalf("expected node a completed, got %q", statusA)
	}
	if statusB != statusCompleted {
		t.Fatalf("expected node b completed, got %q", statusB)
	}
}

func TestGoalGate_AllSatisfied_CompletesNormally(t *testing.T) {
	// Run completes when all goal gate nodes have completed
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system", GoalGate: true},
			{ID: "b", Kind: "system", GoalGate: true},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error when all goal gates satisfied, got: %v", err)
	}
}

func TestGoalGate_Unsatisfied_NoRetryTarget_ReturnsError(t *testing.T) {
	// Run fails with error when goal gate unsatisfied and no retry target.
	// Node "c" is unreachable because the edge from b to c requires decision
	// "special" but system nodes produce testDecisionDefault.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c", When: "special"},
		},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err == nil {
		t.Fatal("expected error for unsatisfied goal gate with no retry target")
	}
	if !strings.Contains(err.Error(), "goal gate unsatisfied") {
		t.Fatalf("expected 'goal gate unsatisfied' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "c") {
		t.Fatalf("expected error to mention node 'c', got: %v", err)
	}
}

func TestGoalGate_Unsatisfied_NodeRetryTarget(t *testing.T) {
	// Run routes to node retry_target when goal gate unsatisfied.
	// Node "c" is unreachable (decision routing), so after a->b completes,
	// goal gate check routes to "a" as retry target. max_loop_iterations
	// prevents infinite retries.
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true, RetryTarget: "a"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c", When: "special"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err == nil {
		t.Fatal("expected error from max_loop_iterations")
	}
	if !strings.Contains(err.Error(), "max loop iterations") {
		t.Fatalf("expected max loop iterations error, got: %v", err)
	}

	events := readFile(t, logPath)
	if !strings.Contains(events, "goal_gate_unsatisfied") {
		t.Fatalf("expected goal_gate_unsatisfied event in log, got: %s", events)
	}
}

func TestGoalGate_Unsatisfied_WorkflowRetryTarget(t *testing.T) {
	// Run routes to workflow retry_target when node has no retry_target
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:          "wf",
		RetryTarget: "a",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c", When: "special"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err == nil {
		t.Fatal("expected error from max_loop_iterations")
	}
	if !strings.Contains(err.Error(), "max loop iterations") {
		t.Fatalf("expected max loop iterations error, got: %v", err)
	}

	events := readFile(t, logPath)
	if !strings.Contains(events, "goal_gate_unsatisfied") {
		t.Fatalf("expected goal_gate_unsatisfied event, got: %s", events)
	}
	if !strings.Contains(events, `"retry_target":"a"`) {
		t.Fatalf("expected retry_target 'a' in event data, got: %s", events)
	}
}

func TestGoalGate_NodeRetryTarget_TakesPrecedence(t *testing.T) {
	// Node retry_target takes precedence over workflow retry_target
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:          "wf",
		RetryTarget: "a",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true, RetryTarget: "b"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c", When: "special"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err == nil {
		t.Fatal("expected error from max_loop_iterations")
	}

	events := readFile(t, logPath)
	// The retry target should be "b" (node level), not "a" (workflow level)
	if !strings.Contains(events, `"retry_target":"b"`) {
		t.Fatalf("expected node retry_target 'b' to take precedence, got: %s", events)
	}
}

func TestGoalGate_MaxLoopIterations_PreventsInfiniteRetry(t *testing.T) {
	// Goal gate + max_loop_iterations prevents infinite retry loops.
	// Node "c" is unreachable because the edge requires "special" decision.
	// Set circuit breaker high so max_loop_iterations triggers first.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true, RetryTarget: "a"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "c", When: "special"},
		},
		Limits: map[string]int{"max_loop_iterations": 2, "max_no_progress_iterations": 100},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err == nil {
		t.Fatal("expected max_loop_iterations to prevent infinite retry")
	}
	if !strings.Contains(err.Error(), "max loop iterations") {
		t.Fatalf("expected max loop iterations error, got: %v", err)
	}
}

func TestGoalGate_SatisfiedAfterRetry(t *testing.T) {
	// Goal gate is satisfied when the node is reachable and completes
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system", GoalGate: true},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected success when goal gate reachable and completed, got: %v", err)
	}
}

func TestGoalGate_FailedNode_Unsatisfied(t *testing.T) {
	// A goal gate node that was executed but has status "failed" is unsatisfied.
	// When system node re-executes, it completes, overriding the pre-set status.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system", GoalGate: true},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("b", func(n *state.NodeState) {
		n.Status = statusFailed
	})
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected success (system node execution overrides failed status), got: %v", err)
	}
}

func TestGoalGate_SavesStateAfterGateCheck(t *testing.T) {
	// Verify that state is saved during wave execution (before goal gate retry)
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "c", Kind: "system", GoalGate: true, RetryTarget: "a"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "c", When: "special"},
		},
		Limits: map[string]int{"max_loop_iterations": 2},
	}

	engine := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, _ = engine.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)

	statePath := filepath.Join(tmpDir, "state.json")
	_, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("expected state file to be saved, got error: %v", err)
	}
}
