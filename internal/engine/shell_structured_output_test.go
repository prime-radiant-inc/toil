package engine

import (
	"context"
	"io"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

func TestShellRoleNode_DefaultsToTextWhenNoStructuredOutputExpected(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-shell-default-text",
		Name:    "Shell Default Text",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:     "shell",
				Kind:   "role",
				Runner: "shell-runner",
				Prompt: "echo hello",
			},
		},
	}
	setupRunForResume(t, runsDir, "run-shell-default-text", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: testInputHello},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell-runner": {ID: "shell-runner", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), "run-shell-default-text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDefault {
		t.Fatalf("expected decision 'default', got %q", output.Decision)
	}
	if output.Message != testInputHello {
		t.Fatalf("expected message 'hello', got %q", output.Message)
	}
	if output.Data != nil {
		t.Fatalf("expected nil data for plain shell nodes, got %#v", output.Data)
	}
}

func TestShellRoleNode_ParsesStructuredOutputWhenDecisionsPresent(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-shell-structured",
		Name:    "Shell Structured",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:        "shell",
				Kind:      "role",
				Runner:    "shell-runner",
				Prompt:    "emit json",
				Decisions: definitions.StringDecisions("prepared"),
			},
		},
	}
	setupRunForResume(t, runsDir, "run-shell-structured", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"prepared","message":"ok","data":{"stories":[{"id":"story-1"}]},"artifacts":[]}`},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell-runner": {ID: "shell-runner", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), "run-shell-structured")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != "prepared" {
		t.Fatalf("expected decision 'prepared', got %q", output.Decision)
	}
	if output.Message != "ok" {
		t.Fatalf("expected message 'ok', got %q", output.Message)
	}
	if output.Data == nil {
		t.Fatal("expected non-nil output data")
	}
	if _, ok := output.Data["stories"]; !ok {
		t.Fatalf("expected output.data.stories to be present, got %#v", output.Data)
	}
}
