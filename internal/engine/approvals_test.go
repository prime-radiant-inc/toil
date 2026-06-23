package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestApproverNil_ApprovalStaysPending(t *testing.T) {
	// With nil Approver, approvals remain pending (file-based polling only).
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := testRunID1
	nodeID := testNodeGate
	attempt := 1
	approvalID := approvals.BuildID(runID, nodeID, attempt)
	approval := &approvals.Approval{
		ID:       approvalID,
		RunID:    runID,
		NodeID:   nodeID,
		Attempt:  attempt,
		Status:   testStatusPending,
		Question: "Approve?",
		Choices:  []string{testDecisionApproved, "rejected"},
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	eng := &Engine{} // Approver is nil
	runState := state.NewRunState(runID, "wf", nil)

	resolved, _, err := eng.tryResolveApproval(tmpDir, runID, nodeID, approval, logger, runState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved {
		t.Fatal("expected approval to stay pending with nil Approver")
	}

	// Verify the approval file was NOT modified
	loaded, err := approvals.Load(tmpDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != testStatusPending {
		t.Fatalf("expected approval status 'pending', got %q", loaded.Status)
	}
}

func TestAutoApprover_ResolvesImmediately(t *testing.T) {
	// With AutoApprover, approval resolves in a single call.
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := testRunID1
	nodeID := testNodeGate
	attempt := 1
	approvalID := approvals.BuildID(runID, nodeID, attempt)
	approval := &approvals.Approval{
		ID:       approvalID,
		RunID:    runID,
		NodeID:   nodeID,
		Attempt:  attempt,
		Status:   testStatusPending,
		Question: "Approve?",
		Choices:  []string{testDecisionApproved, "rejected"},
		Default:  testDecisionApproved,
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	eng := &Engine{
		Approver: &approvals.AutoApprover{},
	}
	runState := state.NewRunState(runID, "wf", nil)

	resolved, output, err := eng.tryResolveApproval(tmpDir, runID, nodeID, approval, logger, runState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolved {
		t.Fatal("expected approval to be resolved by AutoApprover")
	}
	if output.Decision != testDecisionApproved {
		t.Fatalf("expected decision 'approved', got %q", output.Decision)
	}

	// Verify approval file is updated
	loaded, err := approvals.Load(tmpDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != testStatusResolved {
		t.Fatalf("expected approval status 'resolved', got %q", loaded.Status)
	}
	if loaded.Decision != testDecisionApproved {
		t.Fatalf("expected decision 'approved' in file, got %q", loaded.Decision)
	}
	if loaded.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}

	// Verify events were emitted
	_ = logger.Close()
	events := parseEvents(t, logPath)
	resolvedEvent := findEvent(events, "approval_resolved")
	if resolvedEvent == nil {
		t.Fatal("expected approval_resolved event")
	}
	completedEvent := findEvent(events, "node_completed")
	if completedEvent == nil {
		t.Fatal("expected node_completed event")
	}

	// Verify run state was updated
	status, _ := runState.NodeStatus(nodeID)
	if status != statusCompleted {
		t.Fatalf("expected node status 'completed', got %q", status)
	}
}

func TestCallbackApprover_ResolvesWithCustomLogic(t *testing.T) {
	// CallbackApprover passes the approval to the callback and uses its result.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := testRunID1
	nodeID := "review"
	attempt := 1
	approvalID := approvals.BuildID(runID, nodeID, attempt)
	approval := &approvals.Approval{
		ID:       approvalID,
		RunID:    runID,
		NodeID:   nodeID,
		Attempt:  attempt,
		Status:   testStatusPending,
		Question: "Review the changes?",
		Choices:  []string{testDecisionApproved, "rejected", "needs_changes"},
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	var receivedApproval *approvals.Approval
	eng := &Engine{
		Approver: &approvals.CallbackApprover{
			Fn: func(a *approvals.Approval) (*approvals.Resolution, error) {
				receivedApproval = a
				return &approvals.Resolution{
					Decision: "needs_changes",
					Message:  "Please fix the formatting.",
					Comment:  "automated review",
				}, nil
			},
		},
	}
	runState := state.NewRunState(runID, "wf", nil)

	resolved, output, err := eng.tryResolveApproval(tmpDir, runID, nodeID, approval, logger, runState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolved {
		t.Fatal("expected approval to be resolved by CallbackApprover")
	}

	// Verify the callback received the right approval
	if receivedApproval == nil {
		t.Fatal("callback was not called")
	}
	if receivedApproval.NodeID != nodeID {
		t.Fatalf("expected callback to receive node ID %q, got %q", nodeID, receivedApproval.NodeID)
	}

	// Verify output reflects the callback's decision
	if output.Decision != "needs_changes" {
		t.Fatalf("expected decision 'needs_changes', got %q", output.Decision)
	}
	if output.Message != "Please fix the formatting." {
		t.Fatalf("expected message 'Please fix the formatting.', got %q", output.Message)
	}

	// Verify saved approval
	loaded, err := approvals.Load(tmpDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Comment != "automated review" {
		t.Fatalf("expected comment 'automated review', got %q", loaded.Comment)
	}
}

func TestApproverReturnsNil_ApprovalStaysPending(t *testing.T) {
	// When Approver.Resolve returns nil, the approval stays pending.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := testRunID1
	nodeID := testNodeGate
	attempt := 1
	approvalID := approvals.BuildID(runID, nodeID, attempt)
	approval := &approvals.Approval{
		ID:       approvalID,
		RunID:    runID,
		NodeID:   nodeID,
		Attempt:  attempt,
		Status:   testStatusPending,
		Question: "Approve?",
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	eng := &Engine{
		Approver: &approvals.FileApprover{}, // always returns nil
	}
	runState := state.NewRunState(runID, "wf", nil)

	resolved, _, err := eng.tryResolveApproval(tmpDir, runID, nodeID, approval, logger, runState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved {
		t.Fatal("expected approval to stay pending when Approver returns nil")
	}

	// Verify approval file unchanged
	loaded, err := approvals.Load(tmpDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != testStatusPending {
		t.Fatalf("expected approval status 'pending', got %q", loaded.Status)
	}
}

func TestApproverError_PropagatesError(t *testing.T) {
	// When Approver.Resolve returns an error, it propagates up.
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := testRunID1
	nodeID := testNodeGate
	attempt := 1
	approvalID := approvals.BuildID(runID, nodeID, attempt)
	approval := &approvals.Approval{
		ID:       approvalID,
		RunID:    runID,
		NodeID:   nodeID,
		Attempt:  attempt,
		Status:   testStatusPending,
		Question: "Approve?",
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	expectedErr := fmt.Errorf("approval service unavailable")
	eng := &Engine{
		Approver: &approvals.CallbackApprover{
			Fn: func(a *approvals.Approval) (*approvals.Resolution, error) {
				return nil, expectedErr
			},
		},
	}
	runState := state.NewRunState(runID, "wf", nil)

	_, _, err := eng.tryResolveApproval(tmpDir, runID, nodeID, approval, logger, runState)
	if err == nil {
		t.Fatal("expected error from Approver")
	}
	if err != expectedErr {
		t.Fatalf("expected error %q, got %q", expectedErr, err)
	}
}

func TestApprover_IntegrationWithApprovalOutput(t *testing.T) {
	// Integration test: approvalOutput calls tryResolveApproval for new approvals.
	// With an AutoApprover, the approval should resolve immediately when first created.
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Test Approval Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: testNodeGate, Kind: "system", Gate: "required", Decisions: definitions.StringDecisions(testDecisionApproved, "rejected")},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunsDir:     runsDir,
		Approver:    &approvals.AutoApprover{},
		EventStdout: io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != nil {
		t.Fatalf("expected no error with AutoApprover, got: %v", err)
	}

	// Verify the gate node completed
	runState, err := state.LoadState(filepath.Join(runsDir, testRunID1, "state.json"))
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	status, _ := runState.NodeStatus(testNodeGate)
	if status != statusCompleted {
		t.Fatalf("expected gate node status 'completed', got %q", status)
	}

	// Verify approval file is resolved
	approvalID := approvals.BuildID(testRunID1, testNodeGate, 1)
	loaded, err := approvals.Load(filepath.Join(runsDir, testRunID1), approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != testStatusResolved {
		t.Fatalf("expected approval status 'resolved', got %q", loaded.Status)
	}
}

func TestApprover_ExistingPendingApproval_ResolvedByApprover(t *testing.T) {
	// When resuming with a pending approval, the Approver should resolve it.
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Test Pending Approval Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: testNodeGate, Kind: "system", Gate: "required", Decisions: definitions.StringDecisions(testDecisionApproved, "rejected")},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	// First run without Approver — creates the pending approval
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != ErrApprovalPending {
		t.Fatalf("expected ErrApprovalPending, got: %v", err)
	}

	// Now set an Approver and resume
	eng.Approver = &approvals.AutoApprover{}
	_, err = eng.ResumeRun(context.Background(), testRunID1)
	if err != nil {
		t.Fatalf("expected no error after setting Approver, got: %v", err)
	}

	// Verify the gate completed
	runState, err := state.LoadState(filepath.Join(runsDir, testRunID1, "state.json"))
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	status, _ := runState.NodeStatus(testNodeGate)
	if status != statusCompleted {
		t.Fatalf("expected gate node status 'completed', got %q", status)
	}
}

func TestApprovalTimeout_AutoResolves(t *testing.T) {
	// Under the v7 spec, checkApprovalTimeout fires on TimeoutSec > 0 alone.
	// The approval is marked "timed_out" (not "resolved") with no Decision set.
	// The caller (run_loop.go) invokes synthesizeMetaCompletion(_timeout) for routing.
	tmpDir := t.TempDir()
	approval := &approvals.Approval{
		ID:         "run-1-gate-1",
		RunID:      testRunID1,
		NodeID:     testNodeGate,
		Attempt:    1,
		Status:     testStatusPending,
		Question:   "Approve?",
		TimeoutSec: 1,
		CreatedAt:  time.Now().Add(-2 * time.Second),
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	timedOut, err := checkApprovalTimeout(approval, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !timedOut {
		t.Fatal("expected approval to be timed out after timeout")
	}
	if approval.Status != "timed_out" {
		t.Fatalf("expected status 'timed_out', got %q", approval.Status)
	}
	if approval.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}
	if approval.Comment == "" {
		t.Fatal("expected Comment to be set with timeout message")
	}

	// Verify the file was updated on disk
	loaded, err := approvals.Load(tmpDir, approval.ID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != "timed_out" {
		t.Fatalf("expected saved status 'timed_out', got %q", loaded.Status)
	}
	// Decision is intentionally empty — meta-decision routing handles output.
	if loaded.Decision != "" {
		t.Fatalf("expected no Decision set (meta-decision routes instead), got %q", loaded.Decision)
	}
}

func TestApprovalTimeout_NotExpired(t *testing.T) {
	tmpDir := t.TempDir()
	approval := &approvals.Approval{
		ID:         "run-1-gate-1",
		RunID:      testRunID1,
		NodeID:     testNodeGate,
		Attempt:    1,
		Status:     testStatusPending,
		Question:   "Approve?",
		TimeoutSec: 3600,
		Default:    testDecisionApproved,
		CreatedAt:  time.Now(),
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	timedOut, err := checkApprovalTimeout(approval, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timedOut {
		t.Fatal("expected approval to NOT be auto-resolved before timeout")
	}
	if approval.Status != testStatusPending {
		t.Fatalf("expected status 'pending', got %q", approval.Status)
	}
}

func TestApprovalTimeout_NoDefault(t *testing.T) {
	// Under the v7 spec, Default is no longer required to fire a timeout.
	// TimeoutSec > 0 alone is sufficient — the _timeout meta-decision routes instead.
	tmpDir := t.TempDir()
	approval := &approvals.Approval{
		ID:         "run-1-gate-1",
		RunID:      testRunID1,
		NodeID:     testNodeGate,
		Attempt:    1,
		Status:     testStatusPending,
		Question:   "Approve?",
		TimeoutSec: 1,
		Default:    "",
		CreatedAt:  time.Now().Add(-2 * time.Second),
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	timedOut, err := checkApprovalTimeout(approval, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !timedOut {
		t.Fatal("expected approval to time out with TimeoutSec > 0 even without Default")
	}
	if approval.Status != "timed_out" {
		t.Fatalf("expected status 'timed_out', got %q", approval.Status)
	}
}

func TestApprovalTimeout_ZeroTimeoutSec(t *testing.T) {
	tmpDir := t.TempDir()
	approval := &approvals.Approval{
		ID:         "run-1-gate-1",
		RunID:      testRunID1,
		NodeID:     testNodeGate,
		Attempt:    1,
		Status:     testStatusPending,
		Question:   "Approve?",
		TimeoutSec: 0,
		Default:    testDecisionApproved,
		CreatedAt:  time.Now().Add(-2 * time.Second),
	}
	if err := approvals.Create(tmpDir, approval); err != nil {
		t.Fatalf("failed to create approval: %v", err)
	}

	timedOut, err := checkApprovalTimeout(approval, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timedOut {
		t.Fatal("expected approval to stay pending with zero timeout_sec")
	}
	if approval.Status != testStatusPending {
		t.Fatalf("expected status 'pending', got %q", approval.Status)
	}
}

func TestApprovalTimeout_Integration(t *testing.T) {
	// Integration test: an approval gate with TimeoutSec that was created in the
	// past should emit _timeout meta-decision and route through a "when: _timeout"
	// edge to a downstream node, completing the run.
	runsDir := t.TempDir()
	failedTrue := true
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Timeout Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: testNodeGate, Kind: "human", Gate: "required", Decisions: definitions.StringDecisions(testDecisionApproved, "rejected"), TimeoutSec: 1},
			{ID: "after_timeout", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: testNodeGate, To: "after_timeout", When: MetaDecisionTimeout, Failed: &failedTrue},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	// First run without Approver — creates the pending approval
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != ErrApprovalPending {
		t.Fatalf("expected ErrApprovalPending, got: %v", err)
	}

	// Backdate the approval's CreatedAt so it appears timed out
	approvalID := approvals.BuildID(testRunID1, testNodeGate, 1)
	runDir := filepath.Join(runsDir, testRunID1)
	approval, err := approvals.Load(runDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	approval.CreatedAt = time.Now().Add(-2 * time.Second)
	if err := approvals.Save(runDir, approval); err != nil {
		t.Fatalf("failed to save backdated approval: %v", err)
	}

	// Resume — the engine should detect the timeout, emit _timeout meta-decision,
	// and route through the _timeout edge. Because the _timeout edge is marked
	// failed:true, ComputeUnresolvedFailure sets HasUnresolvedFailure and the
	// run returns ErrUnresolvedFailure (status stays "completed").
	_, err = eng.ResumeRun(context.Background(), testRunID1)
	if !errors.Is(err, ErrUnresolvedFailure) {
		t.Fatalf("expected ErrUnresolvedFailure after timeout routing on failed:true edge, got: %v", err)
	}

	// Verify the gate is completed
	runState, err := state.LoadState(filepath.Join(runsDir, testRunID1, "state.json"))
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	nodeStatus, _ := runState.NodeStatus(testNodeGate)
	if nodeStatus != statusCompleted {
		t.Fatalf("expected gate node status 'completed', got %q", nodeStatus)
	}

	// Verify the _timeout meta-decision was captured on the gate node
	var lastRoutingDecision string
	runState.WithNode(testNodeGate, func(n *state.NodeState) {
		lastRoutingDecision = n.LastRoutingDecision
	})
	if lastRoutingDecision != MetaDecisionTimeout {
		t.Fatalf("expected LastRoutingDecision %q, got %q", MetaDecisionTimeout, lastRoutingDecision)
	}

	// Verify approval file is marked timed_out (not resolved)
	loaded, err := approvals.Load(runDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != "timed_out" {
		t.Fatalf("expected approval status 'timed_out', got %q", loaded.Status)
	}
	if loaded.Decision != "" {
		t.Fatalf("expected no decision on timed_out approval (meta-decision routes instead), got %q", loaded.Decision)
	}
}

func TestApprovalTimeoutEmitsMetaDecision(t *testing.T) {
	// Narrow unit test: processApprovals returns the timed-out node ID so the
	// run-loop caller can invoke synthesizeMetaCompletion(_timeout).
	// The approval has TimeoutSec=1 and no Default set — verifying fire-condition
	// is TimeoutSec > 0 alone.
	runsDir := t.TempDir()
	failedTrue := true
	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "MetaDecision Timeout Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: testNodeGate, Kind: "human", Gate: "required", Decisions: definitions.StringDecisions(testDecisionApproved, "rejected"), TimeoutSec: 1},
			{ID: "done_node", Kind: "role"},
			{ID: "timeout_handler", Kind: "role"},
		},
		Edges: []definitions.Edge{
			{From: testNodeGate, To: "done_node", When: testDecisionApproved},
			{From: testNodeGate, To: "timeout_handler", When: "_timeout", Failed: &failedTrue},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	// First resume: creates the pending approval, returns ErrApprovalPending.
	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err != ErrApprovalPending {
		t.Fatalf("expected ErrApprovalPending, got: %v", err)
	}

	// Backdate the approval's CreatedAt so timeout fires.
	approvalID := approvals.BuildID(testRunID1, testNodeGate, 1)
	runDir := filepath.Join(runsDir, testRunID1)
	approval, err := approvals.Load(runDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	approval.CreatedAt = time.Now().Add(-2 * time.Second)
	if err := approvals.Save(runDir, approval); err != nil {
		t.Fatalf("failed to save backdated approval: %v", err)
	}

	// Call processApprovals directly to inspect the returned timedOutNodeIDs.
	runState, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runContext := &RunContext{
		RunID:   testRunID1,
		Outputs: map[string]NodeOutput{},
		Inputs:  map[string]any{},
	}
	wave := []readyNode{{ID: testNodeGate}}

	_, timedOutNodeIDs, _, _, err := eng.processApprovals(testRunID1, runDir, workflow, wave, runContext, logger, runState)
	if err != nil {
		t.Fatalf("processApprovals returned error: %v", err)
	}
	if len(timedOutNodeIDs) != 1 || timedOutNodeIDs[0] != testNodeGate {
		t.Fatalf("expected timedOutNodeIDs=[%q], got %v", testNodeGate, timedOutNodeIDs)
	}

	// Verify approval was marked timed_out on disk.
	loaded, err := approvals.Load(runDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != "timed_out" {
		t.Fatalf("expected approval status 'timed_out', got %q", loaded.Status)
	}
	if loaded.Decision != "" {
		t.Fatalf("expected no Decision on timed_out approval, got %q", loaded.Decision)
	}
}

// TestApprovalGate_EdgePassesInQuestion verifies that edge passes forwarded to
// an approval gate are available as ${input.X} in the composed question.
//
// Scenario:
//
//	write_code --[passes: {summary: "..."}]--> human_approval
//
// The human_approval prompt references ${input.summary}. The passes block is
// evaluated by routeEdge (evaluatePhase2) before reaching processApprovals, so
// readyNode.Passes already contains the resolved string. This test calls
// processApprovals directly with a pre-evaluated Passes map to verify that the
// approval question correctly incorporates the edge-pass value.
func TestApprovalGate_EdgePassesInQuestion(t *testing.T) {
	runsDir := t.TempDir()
	const summaryValue = "code looks good to me"

	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Edge Passes Approval Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "write_code", Kind: "role"},
			{
				ID:        testNodeGate,
				Kind:      "human",
				Gate:      "required",
				Prompt:    "Please review this change: ${input.summary}",
				Decisions: definitions.StringDecisions(testDecisionApproved, "rejected"),
			},
		},
		Edges: []definitions.Edge{
			{
				From: "write_code",
				To:   testNodeGate,
				Passes: map[string]any{
					"summary": "${node.write_code.message}",
				},
			},
		},
	}

	setupRunForResume(t, runsDir, testRunID1, workflow, nil)
	runDir := filepath.Join(runsDir, testRunID1)

	runState, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}

	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runContext := &RunContext{
		RunID:   testRunID1,
		Outputs: map[string]NodeOutput{},
		Inputs:  map[string]any{},
	}

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	// Simulate: edge passes were already evaluated by routeEdge (evaluatePhase2)
	// before being placed on the readyNode. Pass the pre-resolved map directly.
	wave := []readyNode{
		{
			ID:         testNodeGate,
			EdgeIndex:  0,
			FromNodeID: "write_code",
			Passes: map[string]any{
				"summary": summaryValue, // already resolved
			},
		},
	}

	// processApprovals with no Approver: creates the pending approval, returns pending=true.
	_, _, _, pending, err := eng.processApprovals(testRunID1, runDir, workflow, wave, runContext, logger, runState)
	if err != nil {
		t.Fatalf("processApprovals returned error: %v", err)
	}
	if !pending {
		t.Fatal("expected pending=true (no Approver to resolve immediately)")
	}

	// Load the created approval and verify the question contains the edge-pass value.
	approvalID := approvals.BuildID(testRunID1, testNodeGate, 1)
	loaded, err := approvals.Load(runDir, approvalID)
	if err != nil {
		t.Fatalf("failed to load approval: %v", err)
	}
	if loaded.Status != testStatusPending {
		t.Fatalf("expected approval status 'pending', got %q", loaded.Status)
	}
	if !strings.Contains(loaded.Question, summaryValue) {
		t.Fatalf("expected approval question to contain edge-pass value %q, got question:\n%s", summaryValue, loaded.Question)
	}
}
