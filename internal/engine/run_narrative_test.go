package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestMaybeGenerateRunIntent_FallbackApplied(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{
		ID:          "test",
		Description: "A test workflow for unit testing.",
	}
	runState := state.NewRunState("test-run", "test", nil)
	runState.ParentRun = "parent-run-id"

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()

	maybeGenerateRunIntent(context.Background(), runDir, runState, workflow, logger)

	// Deterministic fallback is applied synchronously for all runs.
	if runState.Description == "" {
		t.Error("expected deterministic fallback to set description")
	}
	if runState.Description != "A test workflow for unit testing." {
		t.Errorf("expected workflow description as fallback, got %q", runState.Description)
	}
}

func TestMaybeGenerateRunSummary_FallbackApplied(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{ID: "test"}
	runState := state.NewRunState("test-run", "test", nil)
	runState.ParentRun = "parent-run-id"
	runState.Status = "completed"

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()

	maybeGenerateRunSummary(context.Background(), runDir, runState, workflow, logger)

	if runState.Summary != "Completed." {
		t.Errorf("expected deterministic fallback summary, got %q", runState.Summary)
	}
}

func TestMaybeGenerateRunSummary_FailedWithError(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{ID: "test"}
	runState := state.NewRunState("test-run", "test", nil)
	runState.Status = "failed"
	runState.Error = "node xyz crashed"

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()

	maybeGenerateRunSummary(context.Background(), runDir, runState, workflow, logger)

	if runState.Summary != "Failed: node xyz crashed" {
		t.Errorf("expected failed summary with error, got %q", runState.Summary)
	}
}

func TestMaybeGenerateRunIntent_ExistingDescriptionPreserved(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{ID: "test", Description: "Root workflow."}
	runState := state.NewRunState("test-run", "test", nil)
	runState.Description = "Already set."

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()

	maybeGenerateRunIntent(context.Background(), runDir, runState, workflow, logger)

	if runState.Description != "Already set." {
		t.Errorf("expected existing description preserved, got %q", runState.Description)
	}
}

func TestMaybeGenerateRunSummary_ExistingSummaryPreserved(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{ID: "test"}
	runState := state.NewRunState("test-run", "test", nil)
	runState.Status = "completed"
	runState.Summary = "Already summarized."

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()

	maybeGenerateRunSummary(context.Background(), runDir, runState, workflow, logger)

	if runState.Summary != "Already summarized." {
		t.Errorf("expected existing summary preserved, got %q", runState.Summary)
	}
}

func TestLlmcallAvailable_Cached(t *testing.T) {
	// Just verify it doesn't panic and returns a consistent result.
	a := llmcallAvailable()
	b := llmcallAvailable()
	if a != b {
		t.Error("llmcallAvailable should return consistent results")
	}
}
