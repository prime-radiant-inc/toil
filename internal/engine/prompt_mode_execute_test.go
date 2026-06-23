package engine

import (
	"context"
	"io"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

func TestExecuteRolePromptInputsModeDeclared(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf",
		Name:             "Workflow",
		Version:          1,
		PromptInputsMode: "declared",
		Inputs:           map[string]string{"spec": "string"},
		Nodes: []definitions.Node{
			{
				ID:     "agent",
				Kind:   "role",
				Runner: "capture",
				Prompt: "Use spec: ${input.spec}",
				Inputs: map[string]any{
					"spec": "${workflow_input.spec}",
				},
			},
		},
		Edges: []definitions.Edge{},
	}

	runInputs := map[string]any{
		"spec":    "spec-content",
		"stories": "THIS_SHOULD_NOT_APPEAR_IN_DECLARED_MODE",
	}
	setupRunForResume(t, runsDir, "run-declared", workflow, runInputs)

	capture := &captureRunner{
		result: runners.Result{
			Output:   `{"decision":"default","message":"ok","data":{},"artifacts":[]}`,
			ExitCode: 0,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("capture", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"capture": {ID: "capture", Type: "codex"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := eng.ResumeRun(context.Background(), "run-declared"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	prompt := capture.lastRequest.Prompt
	if !contains(prompt, "## Inputs") {
		t.Fatalf("expected '## Inputs' heading in declared mode prompt, got:\n%s", prompt)
	}
	// Declared input 'spec' should appear as a per-key heading with raw content
	if !contains(prompt, "### spec") {
		t.Fatalf("expected '### spec' per-key heading in prompt, got:\n%s", prompt)
	}
	if !contains(prompt, "spec-content") {
		t.Fatalf("expected raw spec content in fenced block, got:\n%s", prompt)
	}
	if contains(prompt, "THIS_SHOULD_NOT_APPEAR_IN_DECLARED_MODE") {
		t.Fatalf("did not expect undeclared run input in prompt, got:\n%s", prompt)
	}
}

func TestExecuteRolePromptInputsModeNone(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf",
		Name:             "Workflow",
		Version:          1,
		PromptInputsMode: "none",
		Inputs:           map[string]string{"spec": "string"},
		Nodes: []definitions.Node{
			{
				ID:     "agent",
				Kind:   "role",
				Runner: "capture",
				Prompt: "Use spec: ${input.spec}",
				Inputs: map[string]any{
					"spec": "${workflow_input.spec}",
				},
			},
		},
		Edges: []definitions.Edge{},
	}

	runInputs := map[string]any{
		"spec":    "spec-content",
		"stories": "THIS_SHOULD_NOT_APPEAR_IN_NONE_MODE",
	}
	setupRunForResume(t, runsDir, "run-none", workflow, runInputs)

	capture := &captureRunner{
		result: runners.Result{
			Output:   `{"decision":"default","message":"ok","data":{},"artifacts":[]}`,
			ExitCode: 0,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("capture", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"capture": {ID: "capture", Type: "codex"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := eng.ResumeRun(context.Background(), "run-none"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	prompt := capture.lastRequest.Prompt
	if contains(prompt, "## Inputs") {
		t.Fatalf("did not expect Inputs block in none mode prompt, got:\n%s", prompt)
	}
	if !contains(prompt, "Use spec: spec-content") {
		t.Fatalf("expected input interpolation to still work in none mode, got:\n%s", prompt)
	}
}
