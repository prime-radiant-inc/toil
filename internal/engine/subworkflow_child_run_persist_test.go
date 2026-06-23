package engine

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// TestSubworkflowChildRunPersistedBeforeBlockingResume asserts that by the
// time executeSubworkflow hands control to the synchronous engine.ResumeRun
// call for a freshly dispatched child, the parent's state.json on disk
// already records Data["child_run"] pointing at that child.
//
// Rationale: executeSubworkflow's call into the child's ResumeRun can block
// for the entire depth of a recursive subworkflow chain. If the server is
// stopped or killed anywhere in that window, a parent whose child_run
// pointer lives only in memory will, on restart, re-dispatch a brand-new
// child — colliding with any deterministic external state the orphan first
// child already created (worktrees keyed on the parent's TOIL_RUN_ID being
// the canonical hazard). Found in run fjord-ember-forge 2026-04-21.
func TestSubworkflowChildRunPersistedBeforeBlockingResume(t *testing.T) {
	runsDir := t.TempDir()

	child := &definitions.Workflow{
		ID:      "child",
		Name:    "Child",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "do_work", Kind: "system"}},
	}
	parent := &definitions.Workflow{
		ID:      "parent",
		Name:    "Parent",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "sub", Kind: "subworkflow", Workflow: "child"},
		},
	}

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"child":  child,
				"parent": parent,
			},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	// At the crash-exposure moment — after Data["child_run"] is set in
	// memory and before the blocking child ResumeRun — capture what's on
	// disk for the parent.
	var observedChildRun string
	var observedErr error
	eng.beforeChildResume = func(parentRunID, childRunID string) {
		rs, err := state.LoadState(filepath.Join(runsDir, parentRunID, "state.json"))
		if err != nil {
			observedErr = err
			return
		}
		rs.WithNodes(func(nodes map[string]*state.NodeState) {
			n, ok := nodes["sub"]
			if !ok || n.Data == nil {
				return
			}
			if s, ok := n.Data["child_run"].(string); ok {
				observedChildRun = s
			}
		})
	}

	parentRunID, _, err := eng.RunWorkflow(context.Background(), "parent", map[string]any{})
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	if observedErr != nil {
		t.Fatalf("hook: LoadState: %v", observedErr)
	}
	if observedChildRun == "" {
		t.Fatalf("expected parent state.json to record Data[\"child_run\"] before the blocking ResumeRun, but it was empty; a crash here would orphan the first child and the next resume would dispatch a duplicate (run %s)", parentRunID)
	}
}
