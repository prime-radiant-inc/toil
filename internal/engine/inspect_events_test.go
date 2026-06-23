package engine

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

func TestNodeEdgePromptEvent(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "edge-test",
		Name:    "Edge Test",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "step_a", Kind: "role", Runner: "test-runner",
				Decisions: definitions.DecisionList{
					{ID: "done"},
				},
			},
			{ID: "step_b", Kind: "role", Runner: "test-runner"},
		},
		Edges: []definitions.Edge{
			{
				From: "step_a", To: "step_b", When: "done",
				Prompt: "Step A completed. Now do step B.",
			},
		},
	}

	setupRunForResume(t, runsDir, "run-edge", workflow, nil)

	runner := &captureRunner{
		result: runners.Result{
			Output: `{"decision":"done","message":"ok","data":{}}`,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "serf"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, _ = eng.ResumeRun(context.Background(), "run-edge")

	events := parseEvents(t, filepath.Join(runsDir, "run-edge", "events.jsonl"))

	// Find node_edge_prompt for step_b
	edgeEvents := findEvents(events, "node_edge_prompt")
	var found bool
	for _, e := range edgeEvents {
		if e.NodeID == "step_b" {
			if e.Text != "Step A completed. Now do step B." {
				t.Errorf("edge prompt text = %q, want %q", e.Text, "Step A completed. Now do step B.")
			}
			found = true
		}
	}
	if !found {
		t.Error("expected node_edge_prompt event for step_b")
	}

	// step_a should NOT have an edge prompt (it's the first node)
	for _, e := range edgeEvents {
		if e.NodeID == "step_a" {
			t.Error("step_a should not have a node_edge_prompt event")
		}
	}
}

func TestNodeInputsResolvedEvent(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "inputs-test",
		Name:    "Inputs Test",
		Version: 1,
		Inputs:  map[string]string{"spec": "string"},
		Nodes: []definitions.Node{
			{
				ID: "worker", Kind: "role", Runner: "test-runner",
				Inputs: map[string]any{
					"spec": "${workflow_input.spec}",
				},
			},
		},
	}

	inputs := map[string]any{"spec": "build a calculator"}
	setupRunForResume(t, runsDir, "run-inputs", workflow, inputs)

	runner := &captureRunner{
		result: runners.Result{
			Output: `{"decision":"done","message":"ok","data":{}}`,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "serf"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, _ = eng.ResumeRun(context.Background(), "run-inputs")

	events := parseEvents(t, filepath.Join(runsDir, "run-inputs", "events.jsonl"))
	inputEvents := findEvents(events, "node_inputs_resolved")

	var found bool
	for _, e := range inputEvents {
		if e.NodeID == "worker" && e.Data != nil {
			if e.Data["spec"] == "build a calculator" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected node_inputs_resolved event for worker with spec input")
	}
}
