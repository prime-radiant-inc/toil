package engine

import (
	"context"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestRunLoopReturnsCancelledWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{{ID: "a", Kind: "system"}},
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Inputs:  map[string]any{},
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	eng := &Engine{}
	ready := []readyNode{{ID: "a"}}
	_, err := eng.runLoop(ctx, testRunID1, t.TempDir(), workflow, runState, runContext, logger, ready)
	if err != ErrRunCancelled {
		t.Fatalf("expected ErrRunCancelled, got %v", err)
	}
}

func TestRunLoopSetsRunningNodesToCancelledOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{{ID: "a", Kind: "system"}},
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("a", func(n *state.NodeState) {
		n.Status = "running"
	})
	runContext := &RunContext{
		Inputs:  map[string]any{},
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	eng := &Engine{}
	ready := []readyNode{{ID: "a"}}
	_, _ = eng.runLoop(ctx, testRunID1, t.TempDir(), workflow, runState, runContext, logger, ready)

	status, _ := runState.NodeStatus("a")
	if status != statusCancelled {
		t.Fatalf("expected node status 'cancelled', got %q", status)
	}
}
