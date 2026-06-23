package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/interviews"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestCreatePendingInterviews(t *testing.T) {
	sourceRunDir := t.TempDir()
	sourceRunID := "run-abc"
	workflowID := "implement_task"
	nodes := []InterviewableNode{
		{NodeID: testNodeWriteCode, RoleID: "engineer", SessionID: testSessID1, Outcome: "succeeded", Attempts: 1},
		{NodeID: "write_tests", RoleID: "tester", SessionID: "sess-2", Outcome: "retried", Attempts: 3},
	}

	createPendingInterviews(sourceRunDir, sourceRunID, workflowID, nodes)

	// Verify both records are loadable
	for _, n := range nodes {
		iv, err := interviews.Load(sourceRunDir, n.NodeID)
		if err != nil {
			t.Fatalf("Load(%s): %v", n.NodeID, err)
		}
		expectedID := interviews.BuildID(sourceRunID, n.NodeID)
		if iv.ID != expectedID {
			t.Fatalf("ID: want %q, got %q", expectedID, iv.ID)
		}
		if iv.RunID != sourceRunID {
			t.Fatalf("RunID: want %q, got %q", sourceRunID, iv.RunID)
		}
		if iv.NodeID != n.NodeID {
			t.Fatalf("NodeID: want %q, got %q", n.NodeID, iv.NodeID)
		}
		if iv.RoleID != n.RoleID {
			t.Fatalf("RoleID: want %q, got %q", n.RoleID, iv.RoleID)
		}
		if iv.WorkflowID != workflowID {
			t.Fatalf("WorkflowID: want %q, got %q", workflowID, iv.WorkflowID)
		}
		if iv.OriginalSessionID != n.SessionID {
			t.Fatalf("OriginalSessionID: want %q, got %q", n.SessionID, iv.OriginalSessionID)
		}
		if iv.Status != interviews.StatusPending {
			t.Fatalf("Status: want %q, got %q", interviews.StatusPending, iv.Status)
		}
		if iv.OriginalOutcome != n.Outcome {
			t.Fatalf("OriginalOutcome: want %q, got %q", n.Outcome, iv.OriginalOutcome)
		}
		if iv.OriginalAttempts != n.Attempts {
			t.Fatalf("OriginalAttempts: want %d, got %d", n.Attempts, iv.OriginalAttempts)
		}
		if iv.StartedAt == nil {
			t.Fatal("StartedAt should be set")
		}
	}
}

func TestCreatePendingInterviews_EmptyNodes(t *testing.T) {
	sourceRunDir := t.TempDir()
	// Should not panic or error with empty slice
	createPendingInterviews(sourceRunDir, testRunID1, "wf", nil)

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interviews, got %d", len(list))
	}
}

func TestMaybeRecordInterviewResult_CompletesRecord(t *testing.T) {
	sourceRunDir := t.TempDir()
	sourceRunID := testRunIDSrc
	nodeID := testNodeWriteCode

	// Create a pending record first
	if err := interviews.Create(sourceRunDir, &interviews.Interview{
		ID:     interviews.BuildID(sourceRunID, nodeID),
		RunID:  sourceRunID,
		NodeID: nodeID,
		Status: interviews.StatusPending,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  sourceRunID,
	})
	inputs := map[string]any{
		"node_id": nodeID,
	}
	childRunID := "child-run-123"
	output := NodeOutput{
		Decision: testDecisionDone,
		Message:  "interview complete",
		Data:     map[string]any{"learnings": "use error handling consistently"},
	}

	eng.maybeRecordInterviewResult(parentRunState, "interview", childRunID, inputs, output)

	iv, err := interviews.Load(sourceRunDir, nodeID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if iv.Status != interviews.StatusCompleted {
		t.Fatalf("Status: want %q, got %q", interviews.StatusCompleted, iv.Status)
	}
	if iv.InterviewSessionID != childRunID {
		t.Fatalf("InterviewSessionID: want %q, got %q", childRunID, iv.InterviewSessionID)
	}
	if iv.CompletedAt == nil {
		t.Fatal("CompletedAt should be set")
	}
	if iv.Responses == nil {
		t.Fatal("Responses should not be nil")
	}
	if iv.Responses["learnings"] != "use error handling consistently" {
		t.Fatalf("Responses[learnings]: want %q, got %v", "use error handling consistently", iv.Responses["learnings"])
	}
}

func TestMaybeRecordInterviewResult_SkipsNonInterview(t *testing.T) {
	sourceRunDir := t.TempDir()

	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  testRunIDSrc,
	})

	// Should be a no-op when workflow is not "interview"
	eng.maybeRecordInterviewResult(parentRunState, "implement_task", "child-1", map[string]any{"node_id": "x"}, NodeOutput{})

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interviews, got %d", len(list))
	}
}

func TestMaybeRecordInterviewResult_SkipsWhenNoRunDir(t *testing.T) {
	eng := &Engine{}
	// No run_dir in inputs
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_id": testRunIDSrc,
	})

	// Should not panic; just a no-op
	eng.maybeRecordInterviewResult(parentRunState, "interview", "child-1", map[string]any{"node_id": "x"}, NodeOutput{})
}

func TestMaybeRecordInterviewResult_SkipsWhenNoNodeID(t *testing.T) {
	eng := &Engine{}
	sourceRunDir := t.TempDir()
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  testRunIDSrc,
	})

	// No node_id in inputs
	eng.maybeRecordInterviewResult(parentRunState, "interview", "child-1", map[string]any{}, NodeOutput{})

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interviews, got %d", len(list))
	}
}

func TestMaybeRecordInterviewResult_CreatesRecordIfMissing(t *testing.T) {
	sourceRunDir := t.TempDir()
	sourceRunID := testRunIDSrc
	nodeID := testNodeWriteCode

	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  sourceRunID,
	})
	inputs := map[string]any{
		"node_id": nodeID,
	}
	childRunID := "child-run-456"
	output := NodeOutput{
		Decision: testDecisionDone,
		Message:  "interview complete",
		Data:     map[string]any{"learnings": "always validate inputs"},
	}

	// No pending record exists; should create one
	eng.maybeRecordInterviewResult(parentRunState, "interview", childRunID, inputs, output)

	iv, err := interviews.Load(sourceRunDir, nodeID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if iv.Status != interviews.StatusCompleted {
		t.Fatalf("Status: want %q, got %q", interviews.StatusCompleted, iv.Status)
	}
	if iv.RunID != sourceRunID {
		t.Fatalf("RunID: want %q, got %q", sourceRunID, iv.RunID)
	}
	if iv.InterviewSessionID != childRunID {
		t.Fatalf("InterviewSessionID: want %q, got %q", childRunID, iv.InterviewSessionID)
	}
	if iv.Responses["learnings"] != "always validate inputs" {
		t.Fatalf("Responses[learnings]: want %q, got %v", "always validate inputs", iv.Responses["learnings"])
	}
}

func TestMaybeRecordInterviewResult_NilOutputData(t *testing.T) {
	sourceRunDir := t.TempDir()
	sourceRunID := testRunIDSrc
	nodeID := testNodeWriteCode

	if err := interviews.Create(sourceRunDir, &interviews.Interview{
		ID:     interviews.BuildID(sourceRunID, nodeID),
		RunID:  sourceRunID,
		NodeID: nodeID,
		Status: interviews.StatusPending,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  sourceRunID,
	})
	output := NodeOutput{Decision: testDecisionDone, Message: "ok"}

	eng.maybeRecordInterviewResult(parentRunState, "interview", "child-1", map[string]any{"node_id": nodeID}, output)

	iv, err := interviews.Load(sourceRunDir, nodeID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if iv.Status != interviews.StatusCompleted {
		t.Fatalf("Status: want %q, got %q", interviews.StatusCompleted, iv.Status)
	}
	if iv.Responses != nil {
		t.Fatalf("Responses should be nil when output.Data is nil, got %v", iv.Responses)
	}
}

func TestInterviewCandidates_CreatesPendingRecords(t *testing.T) {
	// Integration test: verify that a failed workflow with on_failure interview
	// mode creates pending interview records in the run directory.
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-pending-records",
		Name:      "Pending Records Workflow",
		Version:   1,
		Interview: definitions.InterviewOnFailure,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
			{ID: "agent-fail", Kind: "role", Runner: "test-runner"},
		},
		Edges: []definitions.Edge{
			{From: "agent", To: "agent-fail"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-rec"},
		},
		errs: []error{nil, fmt.Errorf("simulated failure")},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-pending-rec", workflow, nil)

	// Use a channel to wait for the async callback
	done := make(chan struct{})
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnInterviewCandidates: func(runID string, runDir string, nodes []InterviewableNode) {
			close(done)
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-pending-rec")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	// Wait for the callback to confirm candidates were emitted
	<-done

	runDir := filepath.Join(runsDir, "run-pending-rec")
	list, err := interviews.ListForRun(runDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 pending interview record, got %d", len(list))
	}

	iv := list[0]
	if iv.NodeID != "agent" {
		t.Fatalf("NodeID: want %q, got %q", "agent", iv.NodeID)
	}
	if iv.RoleID != "" {
		t.Fatalf("RoleID: want empty, got %q", iv.RoleID)
	}
	if iv.Status != interviews.StatusPending {
		t.Fatalf("Status: want %q, got %q", interviews.StatusPending, iv.Status)
	}
	if iv.OriginalSessionID != "sess-rec" {
		t.Fatalf("OriginalSessionID: want %q, got %q", "sess-rec", iv.OriginalSessionID)
	}
	if iv.WorkflowID != "wf-pending-records" {
		t.Fatalf("WorkflowID: want %q, got %q", "wf-pending-records", iv.WorkflowID)
	}
}

func TestInterviewCandidates_NoPendingRecordsWhenDisabled(t *testing.T) {
	// When interviews are disabled, no records should be created.
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-no-records",
		Name:      "No Records Workflow",
		Version:   1,
		Interview: definitions.InterviewNever,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-xyz"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-no-records", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-no-records")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Brief yield to let any goroutines run (shouldn't be any)
	runtime.Gosched()

	runDir := filepath.Join(runsDir, "run-no-records")
	list, err := interviews.ListForRun(runDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interview records when disabled, got %d", len(list))
	}
}

func TestMaybeRecordInterviewFailure_MarksRecordFailed(t *testing.T) {
	sourceRunDir := t.TempDir()
	sourceRunID := testRunIDSrc
	nodeID := testNodeWriteCode

	// Create a pending record first
	if err := interviews.Create(sourceRunDir, &interviews.Interview{
		ID:     interviews.BuildID(sourceRunID, nodeID),
		RunID:  sourceRunID,
		NodeID: nodeID,
		Status: interviews.StatusPending,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  sourceRunID,
	})
	inputs := map[string]any{"node_id": nodeID}
	subErr := fmt.Errorf("interview agent crashed")

	eng.maybeRecordInterviewFailure(parentRunState, "interview", inputs, subErr)

	iv, err := interviews.Load(sourceRunDir, nodeID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if iv.Status != interviews.StatusFailed {
		t.Fatalf("Status: want %q, got %q", interviews.StatusFailed, iv.Status)
	}
	if iv.Error != "interview agent crashed" {
		t.Fatalf("Error: want %q, got %q", "interview agent crashed", iv.Error)
	}
	if iv.CompletedAt == nil {
		t.Fatal("CompletedAt should be set")
	}
}

func TestMaybeRecordInterviewFailure_SkipsNonInterview(t *testing.T) {
	eng := &Engine{}
	sourceRunDir := t.TempDir()
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
	})

	eng.maybeRecordInterviewFailure(parentRunState, "implement_task", map[string]any{"node_id": "x"}, fmt.Errorf("boom"))

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}
}

func TestMaybeRecordInterviewFailure_SkipsWhenNoRunDir(t *testing.T) {
	eng := &Engine{}
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{})

	// Should not panic
	eng.maybeRecordInterviewFailure(parentRunState, "interview", map[string]any{"node_id": "x"}, fmt.Errorf("boom"))
}

func TestMaybeRecordInterviewFailure_SkipsWhenNoNodeID(t *testing.T) {
	eng := &Engine{}
	sourceRunDir := t.TempDir()
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
	})

	eng.maybeRecordInterviewFailure(parentRunState, "interview", map[string]any{}, fmt.Errorf("boom"))

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}
}

func TestMaybeRecordInterviewFailure_SilentWhenNoPendingRecord(t *testing.T) {
	eng := &Engine{}
	sourceRunDir := t.TempDir()
	parentRunState := state.NewRunState("run-parent", "learn", map[string]any{
		"run_dir": sourceRunDir,
		"run_id":  testRunIDSrc,
	})

	// No pending record exists — should silently return
	eng.maybeRecordInterviewFailure(parentRunState, "interview", map[string]any{"node_id": "missing"}, fmt.Errorf("boom"))

	list, err := interviews.ListForRun(sourceRunDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}
}
