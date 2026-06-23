package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

var errSimulated = fmt.Errorf("simulated failure")

func TestOnRunComplete_CalledOnCompletion(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-complete",
		Name:    "Complete Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-1"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-complete", workflow, nil)

	var callbackState *state.RunState
	var callbackDir string

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnRunComplete: func(rs *state.RunState, runDir string) {
			callbackState = rs
			callbackDir = runDir
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-complete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callbackState == nil {
		t.Fatal("expected OnRunComplete to be called")
	}
	if callbackState.Status != statusCompleted {
		t.Fatalf("expected status 'completed', got %q", callbackState.Status)
	}
	if callbackDir != filepath.Join(runsDir, "run-complete") {
		t.Fatalf("expected runDir %q, got %q", filepath.Join(runsDir, "run-complete"), callbackDir)
	}
}

func TestOnRunComplete_CalledOnFailure(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-fail",
		Name:    "Fail Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		errs: []error{errSimulated},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-fail", workflow, nil)

	var callbackState *state.RunState

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnRunComplete: func(rs *state.RunState, runDir string) {
			callbackState = rs
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-fail")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	if callbackState == nil {
		t.Fatal("expected OnRunComplete to be called on failure")
	}
	if callbackState.Status != statusFailed {
		t.Fatalf("expected status 'failed', got %q", callbackState.Status)
	}
}

func TestOnRunComplete_CalledOnCancellation(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-cancel",
		Name:    "Cancel Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	// Use sequentialRunner with no results — the pre-cancelled context
	// causes runLoop to return ErrRunCancelled before the runner executes.
	runner := &sequentialRunner{}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-cancel", workflow, nil)

	var callbackState *state.RunState

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnRunComplete: func(rs *state.RunState, runDir string) {
			callbackState = rs
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to trigger ErrRunCancelled

	_, err := eng.ResumeRun(ctx, "run-cancel")
	if err != ErrRunCancelled {
		t.Fatalf("expected ErrRunCancelled, got %v", err)
	}

	if callbackState == nil {
		t.Fatal("expected OnRunComplete to be called on cancellation")
	}
	if callbackState.Status != statusCancelled {
		t.Fatalf("expected status 'cancelled', got %q", callbackState.Status)
	}
}

func TestOnRunComplete_NilCallbackSafe(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-nil",
		Name:    "Nil Callback Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-1"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-nil", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnRunComplete:  nil,
	}

	_, err := eng.ResumeRun(context.Background(), "run-nil")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No panic means success
}
