package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

func TestSubworkflowStartedEventIncludesChildRunID(t *testing.T) {
	runsDir := t.TempDir()

	child := &definitions.Workflow{
		ID:      "child",
		Name:    "Child",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
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

	parentRunID, _, err := eng.RunWorkflow(context.Background(), "parent", map[string]any{})
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, parentRunID, "events.jsonl"))
	started := findEvent(events, "subworkflow_started")
	if started == nil {
		t.Fatal("expected subworkflow_started event")
	}
	if started.Data == nil {
		t.Fatal("expected subworkflow_started to have data")
	}
	childRun, _ := started.Data["child_run"].(string)
	if childRun == "" {
		t.Fatalf("expected subworkflow_started child_run to be set, got %q", childRun)
	}
	if started.Data["child_workflow"] != "child" {
		t.Fatalf("expected child_workflow child, got %v", started.Data["child_workflow"])
	}

	if _, err := os.Stat(filepath.Join(runsDir, childRun, "state.json")); err != nil {
		t.Fatalf("expected child run state.json to exist: %v", err)
	}
}
