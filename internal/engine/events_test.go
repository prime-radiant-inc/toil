package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// failingRunner is a test runner that always returns an error.
type failingRunner struct{}

func (r *failingRunner) Run(_ context.Context, _ runners.Request, _ runners.LineHandler) (runners.Result, error) {
	return runners.Result{}, fmt.Errorf("simulated runner failure")
}

// parseEvents reads events.jsonl and returns all events.
func parseEvents(t *testing.T, path string) []state.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open events file: %v", err)
	}
	defer func() { _ = f.Close() }()

	var events []state.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("failed to parse event: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return events
}

// findEvent returns the first event matching the given type.
func findEvent(events []state.Event, eventType string) *state.Event {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}

// findEvents returns all events matching the given type.
func findEvents(events []state.Event, eventType string) []state.Event {
	var matched []state.Event
	for _, event := range events {
		if event.Type == eventType {
			matched = append(matched, event)
		}
	}
	return matched
}

// setupRunForResume creates a run directory with state.json and workflow.yaml
// so that ResumeRun can pick it up.
func setupRunForResume(t *testing.T, runsDir string, runID string, workflow *definitions.Workflow, inputs map[string]any) {
	t.Helper()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("failed to create run dir: %v", err)
	}

	runState := state.NewRunState(runID, workflow.ID, inputs)
	runState.Status = statusRunning
	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	// Create initial events.jsonl with run_started
	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), io.Discard)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	_ = logger.Append(state.Event{Type: "run_started", RunID: runID, Data: map[string]any{"workflow_id": workflow.ID}})
	_ = logger.Close()

	// Snapshot the workflow
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
	}
	if err := eng.snapshotWorkflow(runDir, workflow); err != nil {
		t.Fatalf("failed to snapshot workflow: %v", err)
	}
}

// --- Tests ---

func TestEvent_RunCompleted_HasWorkflowIDAndDuration(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, testRunID1, "events.jsonl"))
	event := findEvent(events, "run_completed")
	if event == nil {
		t.Fatal("expected run_completed event")
	}
	if event.Data == nil {
		t.Fatal("expected run_completed to have data")
	}
	if event.Data["workflow_id"] != "test-wf" {
		t.Fatalf("expected workflow_id 'test-wf', got %v", event.Data["workflow_id"])
	}
	if event.DurationMs == nil {
		t.Fatal("expected run_completed to have duration_ms")
	}
	if *event.DurationMs < 0 {
		t.Fatalf("expected non-negative duration_ms, got %d", *event.DurationMs)
	}
}

func TestEvent_RunFailed_HasWorkflowIDErrorAndDuration(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "fail-wf",
		Name:    "Fail Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "role", Runner: "fail-runner"},
		},
	}

	setupRunForResume(t, runsDir, "run-fail", workflow, nil)

	registry := runners.NewRegistry()
	_ = registry.Register("fail-runner", &failingRunner{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-fail")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-fail", "events.jsonl"))
	event := findEvent(events, "run_failed")
	if event == nil {
		t.Fatal("expected run_failed event")
	}
	if event.Data == nil {
		t.Fatal("expected run_failed to have data")
	}
	if event.Data["workflow_id"] != "fail-wf" {
		t.Fatalf("expected workflow_id 'fail-wf', got %v", event.Data["workflow_id"])
	}
	if _, ok := event.Data["error"]; !ok {
		t.Fatal("expected run_failed to have error in data")
	}
	if event.DurationMs == nil {
		t.Fatal("expected run_failed to have duration_ms")
	}
	if *event.DurationMs < 0 {
		t.Fatalf("expected non-negative duration_ms, got %d", *event.DurationMs)
	}

	loaded, err := state.LoadState(filepath.Join(runsDir, "run-fail", "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.Status != statusFailed {
		t.Fatalf("expected status failed, got %q", loaded.Status)
	}
	if loaded.Error == "" {
		t.Fatal("expected run state to persist error message")
	}
}

func TestEvent_WaveStarted_HasNodeCountAndIDs(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = logger.Close()

	events := parseEvents(t, logPath)
	waveStartedEvents := findEvents(events, "wave_started")
	if len(waveStartedEvents) == 0 {
		t.Fatal("expected at least one wave_started event")
	}

	// First wave should have node "a"
	first := waveStartedEvents[0]
	if first.Data == nil {
		t.Fatal("expected wave_started to have data")
	}
	nodeCount, ok := first.Data["node_count"]
	if !ok {
		t.Fatal("expected wave_started to have node_count")
	}
	// JSON numbers are float64
	if nc, ok := nodeCount.(float64); !ok || nc < 1 {
		t.Fatalf("expected node_count >= 1, got %v", nodeCount)
	}
	if _, ok := first.Data["node_ids"]; !ok {
		t.Fatal("expected wave_started to have node_ids")
	}
}

func TestEvent_WaveCompleted_HasCountsAndDuration(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = logger.Close()

	events := parseEvents(t, logPath)
	waveCompletedEvents := findEvents(events, "wave_completed")
	if len(waveCompletedEvents) == 0 {
		t.Fatal("expected at least one wave_completed event")
	}

	first := waveCompletedEvents[0]
	if first.Data == nil {
		t.Fatal("expected wave_completed to have data")
	}
	if _, ok := first.Data["node_count"]; !ok {
		t.Fatal("expected wave_completed to have node_count")
	}
	if _, ok := first.Data["succeeded"]; !ok {
		t.Fatal("expected wave_completed to have succeeded count")
	}
	if _, ok := first.Data["failed"]; !ok {
		t.Fatal("expected wave_completed to have failed count")
	}
	if first.DurationMs == nil {
		t.Fatal("expected wave_completed to have duration_ms")
	}
	if *first.DurationMs < 0 {
		t.Fatalf("expected non-negative duration_ms, got %d", *first.DurationMs)
	}
}

func TestEvent_NodeCompleted_HasDuration(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = logger.Close()

	events := parseEvents(t, logPath)
	event := findEvent(events, "node_completed")
	if event == nil {
		t.Fatal("expected node_completed event")
	}
	if event.DurationMs == nil {
		t.Fatal("expected node_completed to have duration_ms")
	}
	if *event.DurationMs < 0 {
		t.Fatalf("expected non-negative duration_ms, got %d", *event.DurationMs)
	}
}

func TestEvent_NodeFailed_HasDuration(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	// Use a role node with a registered runner that always fails.
	// This triggers the node_failed event path inside executeRole after
	// StartedAt is set.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "role", Runner: "fail-runner"},
		},
	}

	registry := runners.NewRegistry()
	_ = registry.Register("fail-runner", &failingRunner{})

	eng := &Engine{
		Definitions:    &definitions.Bundle{},
		RunnerRegistry: registry,
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, _ = eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	_ = logger.Close()

	events := parseEvents(t, logPath)
	event := findEvent(events, "node_failed")
	if event == nil {
		t.Fatal("expected node_failed event")
	}
	if event.DurationMs == nil {
		t.Fatal("expected node_failed to have duration_ms")
	}
	if *event.DurationMs < 0 {
		t.Fatalf("expected non-negative duration_ms, got %d", *event.DurationMs)
	}
}

func TestEvent_DurationMs_NilForNonCompletionEvents(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = logger.Close()

	events := parseEvents(t, logPath)
	for _, event := range events {
		switch event.Type {
		case "wave_started", "node_started":
			if event.DurationMs != nil {
				t.Fatalf("expected DurationMs to be nil for %s event, got %d", event.Type, *event.DurationMs)
			}
		}
	}
}

func TestEvent_RunPaused_HasWorkflowIDAndReason(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "pause-wf",
		Name:    "Pause Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: testNodeGate, Kind: "system", Gate: "required"},
		},
	}

	setupRunForResume(t, runsDir, "run-pause", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-pause")
	if err != ErrApprovalPending {
		t.Fatalf("expected ErrApprovalPending, got: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-pause", "events.jsonl"))
	event := findEvent(events, "run_paused")
	if event == nil {
		t.Fatal("expected run_paused event")
	}
	if event.Data == nil {
		t.Fatal("expected run_paused to have data")
	}
	if event.Data["workflow_id"] != "pause-wf" {
		t.Fatalf("expected workflow_id 'pause-wf', got %v", event.Data["workflow_id"])
	}
	if _, ok := event.Data["reason"]; !ok {
		t.Fatal("expected run_paused to have reason in data")
	}
}

func TestEvent_RunCompleted_HasNodeCount(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Test Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, testRunID1, "events.jsonl"))
	event := findEvent(events, "run_completed")
	if event == nil {
		t.Fatal("expected run_completed event")
	}
	nodeCount, ok := event.Data["node_count"]
	if !ok {
		t.Fatal("expected run_completed to have node_count")
	}
	if nc, ok := nodeCount.(float64); !ok || nc != 2 {
		t.Fatalf("expected node_count 2, got %v", nodeCount)
	}
}

func TestEvent_RunStarted_HasWorkflowID(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "started-wf",
		Name:    "Started Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	runID, _, err := eng.RunWorkflow(context.Background(), "started-wf", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, runID, "events.jsonl"))
	event := findEvent(events, "run_started")
	if event == nil {
		t.Fatal("expected run_started event")
	}
	if event.Data == nil {
		t.Fatal("expected run_started to have data")
	}
	if event.Data["workflow_id"] != "started-wf" {
		t.Fatalf("expected workflow_id 'started-wf', got %v", event.Data["workflow_id"])
	}
}

func TestEvent_GoalGateSatisfied_Emitted(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system", GoalGate: true},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = logger.Close()

	events := parseEvents(t, logPath)
	event := findEvent(events, "goal_gate_satisfied")
	if event == nil {
		t.Fatal("expected goal_gate_satisfied event")
	}
	if event.NodeID != "a" {
		t.Fatalf("expected goal_gate_satisfied for node 'a', got %q", event.NodeID)
	}
}

func TestEvent_NodeRetry_HasMaxAttempts(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	// Use a role with a registered runner that always fails. The failure is
	// wrapped with Retryable inside executeRole, so retries occur.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "role", Runner: "fail-runner", Retry: &definitions.RetryPolicy{
				Max:          2,
				Backoff:      "fixed",
				InitialDelay: "0s",
				MaxDelay:     "0s",
			}},
		},
	}

	registry := runners.NewRegistry()
	_ = registry.Register("fail-runner", &failingRunner{})

	eng := &Engine{
		Definitions:    &definitions.Bundle{},
		RunnerRegistry: registry,
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, _ = eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	_ = logger.Close()

	events := parseEvents(t, logPath)
	retryEvents := findEvents(events, "node_retry")
	if len(retryEvents) == 0 {
		t.Fatal("expected node_retry events")
	}
	for _, event := range retryEvents {
		if event.Data == nil {
			t.Fatal("expected node_retry to have data")
		}
		if _, ok := event.Data["max_attempts"]; !ok {
			t.Fatal("expected node_retry to have max_attempts in data")
		}
	}
}

func TestEvent_NodeFailureRouted_HasTargetNode(t *testing.T) {
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: "status == 'failed'"},
		},
	}

	eng := &Engine{
		Definitions:    &definitions.Bundle{},
		RunnerRegistry: runners.NewRegistry(),
	}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, _ = eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	_ = logger.Close()

	events := parseEvents(t, logPath)
	event := findEvent(events, "node_failure_routed")
	if event == nil {
		t.Fatal("expected node_failure_routed event")
	}
	if event.Data == nil {
		t.Fatal("expected node_failure_routed to have data")
	}
	if _, ok := event.Data["target_node"]; !ok {
		t.Fatal("expected node_failure_routed to have target_node")
	}
}
