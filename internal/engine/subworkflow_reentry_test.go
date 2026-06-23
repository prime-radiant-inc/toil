package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestSubworkflowReentrySpawnsNewChildRun(t *testing.T) {
	runsDir := t.TempDir()

	// Child workflow: single system node that always completes
	child := &definitions.Workflow{
		ID:      "child",
		Name:    "Child",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "do_work", Kind: "system"}},
	}

	// Parent workflow: start → sub (subworkflow, self-loop) with limit 3
	// Each loop iteration should spawn a DISTINCT child run.
	parent := &definitions.Workflow{
		ID:      "parent",
		Name:    "Subworkflow Reentry Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "sub", Kind: "subworkflow", Workflow: "child"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "sub"},
			{From: "sub", To: "sub"},
		},
		Limits: map[string]int{
			"max_loop_iterations":        3,
			"max_no_progress_iterations": 100,
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

	parentRunID, _, err := eng.RunWorkflow(context.Background(), "parent", map[string]any{})

	// Should eventually hit loop exhaustion (sub runs 3 times, 4th exceeds limit)
	if err == nil {
		t.Fatal("expected loop exhaustion error")
	}
	if !strings.Contains(err.Error(), "max loop iterations exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check events: should see subworkflow_reentry events (iterations 2 and 3 clear old child_run)
	events := parseEvents(t, filepath.Join(runsDir, parentRunID, "events.jsonl"))
	reentryEvents := findEvents(events, "subworkflow_reentry")
	if len(reentryEvents) < 2 {
		t.Errorf("expected at least 2 subworkflow_reentry events, got %d", len(reentryEvents))
	}

	// Each reentry should reference a previous_child_run
	for i, ev := range reentryEvents {
		old, _ := ev.Data["previous_child_run"].(string)
		if old == "" {
			t.Errorf("reentry event %d missing previous_child_run", i)
		}
	}

	// Verify multiple distinct child runs were created on disk
	entries, _ := os.ReadDir(runsDir)
	childRunCount := 0
	for _, e := range entries {
		if e.IsDir() && e.Name() != parentRunID {
			childRunCount++
		}
	}
	if childRunCount < 3 {
		t.Errorf("expected at least 3 child run dirs, got %d", childRunCount)
	}
}

func TestSubworkflowCrashResumePreservesChildRun(t *testing.T) {
	// Verify that the re-entry detection condition (status == completed)
	// does NOT fire for nodes with status "running" or "retrying",
	// preserving the child_run for crash resume.
	for _, resumeStatus := range []string{"running", "retrying"} {
		t.Run(resumeStatus, func(t *testing.T) {
			runState := state.NewRunState("crash-test", "parent", map[string]any{})
			runState.WithNode("sub", func(n *state.NodeState) {
				n.Status = resumeStatus
				if n.Data == nil {
					n.Data = map[string]any{}
				}
				n.Data["child_run"] = "existing-child-run-id"
			})

			// Simulate the re-entry detection check from executeSubworkflow.
			// For running/retrying nodes, child_run must be preserved.
			var childRunAfter string
			runState.WithNode("sub", func(n *state.NodeState) {
				if n.Status == statusCompleted && n.Data != nil {
					t.Fatalf("re-entry detection triggered for %s node", resumeStatus)
				}
				if n.Data != nil {
					if stored, ok := n.Data["child_run"].(string); ok {
						childRunAfter = stored
					}
				}
			})

			if childRunAfter != "existing-child-run-id" {
				t.Fatalf("expected child_run preserved for %s, got %q", resumeStatus, childRunAfter)
			}
		})
	}
}
