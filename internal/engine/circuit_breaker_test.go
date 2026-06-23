package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestCircuitBreakerTripsOnRepeatedNoProgress(t *testing.T) {
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	engine := &Engine{}
	workflow := &definitions.Workflow{
		ID:     "wf",
		Limits: map[string]int{"max_no_progress_iterations": 3},
	}
	node := &definitions.Node{ID: "verify_code_meets_acceptance_criteria"}
	runState := state.NewRunState(testRunID1, "wf", nil)
	output := NodeOutput{
		Decision: "changes_requested",
		Message:  "spec has issues",
		Data: map[string]any{
			"issues": []any{"missing tests"},
		},
	}

	for i := 0; i < 2; i++ {
		if err := engine.enforceCircuitBreaker(testRunID1, workflow, node, "dispatch-a", output, logger, runState, ""); err != nil {
			t.Fatalf("unexpected error before limit: %v", err)
		}
	}
	if err := engine.enforceCircuitBreaker(testRunID1, workflow, node, "dispatch-a", output, logger, runState, ""); err == nil {
		t.Fatal("expected circuit breaker to trip")
	}

	stateNode := runState.Node("verify_code_meets_acceptance_criteria")
	if stateNode.Status != statusFailed {
		t.Fatalf("expected failed status, got %q", stateNode.Status)
	}
	if stateNode.NoProgressCount != 3 {
		t.Fatalf("expected no progress count 3, got %d", stateNode.NoProgressCount)
	}

	events := readFile(t, logPath)
	if !strings.Contains(events, "circuit_breaker_tripped") {
		t.Fatalf("expected circuit_breaker_tripped event, got %s", events)
	}
}

func TestCircuitBreakerResetsWhenOutputChanges(t *testing.T) {
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	engine := &Engine{}
	workflow := &definitions.Workflow{
		ID:     "wf",
		Limits: map[string]int{"max_no_progress_iterations": 3},
	}
	node := &definitions.Node{ID: "planner"}
	runState := state.NewRunState("run-2", "wf", nil)

	firstOutput := NodeOutput{Decision: "needs_clarification", Message: "question 1"}
	secondOutput := NodeOutput{Decision: "needs_clarification", Message: "question 2"}

	if err := engine.enforceCircuitBreaker("run-2", workflow, node, "dispatch-a", firstOutput, logger, runState, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := engine.enforceCircuitBreaker("run-2", workflow, node, "dispatch-a", firstOutput, logger, runState, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := engine.enforceCircuitBreaker("run-2", workflow, node, "dispatch-a", secondOutput, logger, runState, ""); err != nil {
		t.Fatalf("unexpected error after progress: %v", err)
	}

	stateNode := runState.Node("planner")
	if stateNode.NoProgressCount != 1 {
		t.Fatalf("expected no progress count reset to 1, got %d", stateNode.NoProgressCount)
	}
}

func TestOutputHashIgnoresChildRun(t *testing.T) {
	outputA := NodeOutput{
		Decision: testDecisionDefault,
		Message:  "ok",
		Data: map[string]any{
			"child_run": "run-a",
			"value":     "same",
		},
	}
	outputB := NodeOutput{
		Decision: testDecisionDefault,
		Message:  "ok",
		Data: map[string]any{
			"child_run": "run-b",
			"value":     "same",
		},
	}

	hashA, err := outputHashForNode(outputA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hashB, err := outputHashForNode(outputB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected hashes to match, got %s != %s", hashA, hashB)
	}
}

func newTestLogger(t *testing.T) (*state.Logger, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	logger, err := state.NewLoggerWithStdout(logPath, io.Discard)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return logger, logPath
}

func TestCircuitBreakerResetsWhenToolCallsMade(t *testing.T) {
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	engine := &Engine{}
	workflow := &definitions.Workflow{
		ID:     "wf",
		Limits: map[string]int{"max_no_progress_iterations": 3},
	}
	node := &definitions.Node{ID: "debugger"}
	runState := state.NewRunState("run-tools", "wf", nil)

	// Same structured output but with tool calls = progress.
	output := NodeOutput{Decision: "investigating", Message: "still looking", ToolCalls: 5}

	// Should never trip — tool calls indicate real work.
	for i := 0; i < 10; i++ {
		if err := engine.enforceCircuitBreaker("run-tools", workflow, node, "dispatch-a", output, logger, runState, ""); err != nil {
			t.Fatalf("circuit breaker should not trip when tool calls made, iteration %d: %v", i, err)
		}
	}
}

func TestCircuitBreakerTripsWithZeroToolCalls(t *testing.T) {
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	engine := &Engine{}
	workflow := &definitions.Workflow{
		ID:     "wf",
		Limits: map[string]int{"max_no_progress_iterations": 3},
	}
	node := &definitions.Node{ID: "debugger"}
	runState := state.NewRunState("run-no-tools", "wf", nil)

	// Same output, zero tool calls = stuck.
	output := NodeOutput{Decision: "investigating", Message: "still looking", ToolCalls: 0}

	for i := 0; i < 2; i++ {
		if err := engine.enforceCircuitBreaker("run-no-tools", workflow, node, "dispatch-a", output, logger, runState, ""); err != nil {
			t.Fatalf("unexpected error before limit: %v", err)
		}
	}
	if err := engine.enforceCircuitBreaker("run-no-tools", workflow, node, "dispatch-a", output, logger, runState, ""); err == nil {
		t.Fatal("expected circuit breaker to trip with zero tool calls")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	return string(data)
}
