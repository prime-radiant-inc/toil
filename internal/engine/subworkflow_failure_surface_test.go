package engine

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// PRI-1582: when a subworkflow fails — whether at the node level or at
// the orchestrator level (max iterations, no_progress, etc.) — the
// parent NodeState must carry enough diagnostic so a debugger doesn't
// have to traverse into the child run.
//
// Concretely: stateNode.Error must be set (so buildFailureContext.error
// surfaces), and stateNode.Data must carry failed_child_node /
// failed_child_message even when no individual child node has
// Status=failed.

// alwaysFailRunner returns a fixed error so the child workflow's only
// node fails. Used to verify the "child node failed" path.
type alwaysFailRunner struct {
	err error
}

func (r *alwaysFailRunner) Run(_ context.Context, _ runners.Request, _ runners.LineHandler) (runners.Result, error) {
	return runners.Result{}, r.err
}

func TestExecuteSubworkflow_FailureSetsErrorOnParent(t *testing.T) {
	runsDir := t.TempDir()

	child := &definitions.Workflow{
		ID:      "child-fail",
		Name:    "Child Fail",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "leaf", Kind: "role", Runner: "always-fail", Decisions: definitions.StringDecisions("done")},
		},
	}
	parent := &definitions.Workflow{
		ID:      "parent-fail",
		Name:    "Parent Fail",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "sub", Kind: "subworkflow", Workflow: "child-fail"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("always-fail", &alwaysFailRunner{err: errors.New("child runner exploded")})
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"child-fail":  child,
				"parent-fail": parent,
			},
			Runners: map[string]*definitions.Runner{"always-fail": {ID: "always-fail", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	parentRunID, _, err := eng.RunWorkflow(context.Background(), "parent-fail", map[string]any{})
	if err == nil {
		t.Fatal("expected error from parent workflow")
	}

	parentState, loadErr := state.LoadState(filepath.Join(runsDir, parentRunID, "state.json"))
	if loadErr != nil {
		t.Fatalf("LoadState parent: %v", loadErr)
	}
	var sub *state.NodeState
	parentState.WithNodes(func(nodes map[string]*state.NodeState) { sub = nodes["sub"] })
	if sub == nil {
		t.Fatal("sub node missing from parent state")
	}
	if sub.Status != statusFailed {
		t.Fatalf("expected sub status %q, got %q", statusFailed, sub.Status)
	}
	// PRI-1582 #1: Error must be set, not just Message.
	if sub.Error == "" {
		t.Errorf("PRI-1582: sub.Error empty; want subworkflow failure to populate Error so buildFailureContext.error surfaces")
	}
	if sub.Data == nil {
		t.Fatal("sub.Data nil")
	}
	// PRI-1582 #2: failed_child_node populated from child run state.
	fcn, _ := sub.Data["failed_child_node"].(string)
	if fcn == "" {
		t.Errorf("expected failed_child_node populated, got Data=%+v", sub.Data)
	}

	// buildFailureContext must now include the error text.
	fc := buildFailureContext(parentState, "sub", runsDir)
	if got, _ := fc["error"].(string); !strings.Contains(got, "child runner exploded") {
		t.Errorf("buildFailureContext.error = %q, want it to mention child runner failure", got)
	}
}

// orchestratorLevelFailureRunner returns an error that looks like
// max-iteration exhaustion: the runner succeeds but with a decision
// the parent rejects via max_loop_iterations. To simulate this we
// can't easily contrive max_loop_iterations without a loop edge, so
// we use a simpler proxy: the child has a node that succeeds but the
// child workflow has a missing edge that causes a load-time error.
//
// Actually the cleanest model: a child workflow whose only role node
// returns successfully, but the child workflow has no edges from that
// node, so the engine's run loop returns "no edges" or similar. Or
// use a loop with max_loop_iterations: 1 that fires loop_exhausted_to
// nowhere (definitions validator may reject this — keep it as a unit
// test of the fix path instead).
//
// For now we test the orchestrator-level path by directly invoking
// executeSubworkflow with a child whose terminal state has no failed
// node but where ResumeRun returns an error.

// TestExecuteSubworkflow_OrchestratorLevelFailure exercises the
// fallback path: child workflow has a max_loop_iterations cap and
// the loop exhausts. No individual child node has Status=failed at
// the terminal state; the parent must still surface a useful
// failed_child_* signal via the fallback. (PRI-1582)
func TestExecuteSubworkflow_OrchestratorLevelFailure(t *testing.T) {
	runsDir := t.TempDir()

	// Child workflow: a loop on `loop` that exhausts after max=1.
	// The loop_exhausted_to target is missing on purpose so the loop
	// exhaustion bubbles up as an error. (Validator may reject this
	// shape; if so, this test would need adjustment to a more elaborate
	// fixture. As of writing, an unguarded loop just returns
	// loop-exhausted error at runtime.)
	child := &definitions.Workflow{
		ID:      "child-loop",
		Name:    "Child Loop",
		Version: 1,
		Limits:  map[string]int{"max_loop_iterations": 1},
		Nodes: []definitions.Node{
			{ID: "loop", Kind: "role", Runner: "always-loops", Decisions: definitions.StringDecisions("again", "done")},
		},
		Edges: []definitions.Edge{
			{From: "loop", To: "loop", When: "decision == 'again'"},
		},
	}
	parent := &definitions.Workflow{
		ID:      "parent-loop",
		Name:    "Parent Loop",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "sub", Kind: "subworkflow", Workflow: "child-loop"},
		},
	}
	registry := runners.NewRegistry()
	// Runner that always picks "again", forcing the loop to repeat
	// until max_loop_iterations exhausts it.
	_ = registry.Register("always-loops", &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"again","message":"loop turn 1"}`},
			{Output: `{"decision":"again","message":"loop turn 2"}`},
			{Output: `{"decision":"again","message":"loop turn 3"}`},
		},
	})
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"child-loop":  child,
				"parent-loop": parent,
			},
			Runners: map[string]*definitions.Runner{"always-loops": {ID: "always-loops", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	parentRunID, _, runErr := eng.RunWorkflow(context.Background(), "parent-loop", map[string]any{})
	if runErr == nil {
		t.Fatal("expected error from parent workflow (child loop exhausts)")
	}

	parentState, loadErr := state.LoadState(filepath.Join(runsDir, parentRunID, "state.json"))
	if loadErr != nil {
		t.Fatalf("LoadState parent: %v", loadErr)
	}
	var sub *state.NodeState
	parentState.WithNodes(func(nodes map[string]*state.NodeState) { sub = nodes["sub"] })
	if sub == nil {
		t.Fatal("sub node missing")
	}
	if sub.Error == "" {
		t.Errorf("PRI-1582: sub.Error empty on orchestrator-level child failure")
	}
	// Either the child node is in Status=failed (preferred path) OR the
	// fallback fires with failed_child_error + failed_child_node from
	// the most-recently-active child node.
	if sub.Data == nil {
		t.Fatal("sub.Data nil")
	}
	hasFailedChildNode := false
	if v, _ := sub.Data["failed_child_node"].(string); v != "" {
		hasFailedChildNode = true
	}
	hasFailedChildError := false
	if v, _ := sub.Data["failed_child_error"].(string); v != "" {
		hasFailedChildError = true
	}
	if !hasFailedChildNode && !hasFailedChildError {
		t.Errorf("PRI-1582: parent's sub.Data missing both failed_child_node and failed_child_error; got %+v", sub.Data)
	}
}
