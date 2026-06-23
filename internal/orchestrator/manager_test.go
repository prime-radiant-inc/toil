package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

const (
	testRunIDParent  = "parent"
	testStatusRun    = "running"
	testStatusCancel = "cancelled"
	testCallbackURL  = "http://test-callback/webhook"
)

func TestCancelRun_EmitsRunCancelledEvent(t *testing.T) {
	// Orchestrator's cancel path was writing state.Status=cancelled directly
	// to state.json without emitting a run_cancelled event. Consumers that
	// tail events.jsonl (including the metrics collector, inspect, and SSE
	// stream) never see the terminal transition. Verify the event is now
	// appended whenever cancelSingle fires.
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("r1", "wf", nil)
	rs.Status = testStatusRun
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(nil, runsDir)
	if err := manager.CancelRun("r1"); err != nil {
		t.Fatal(err)
	}

	events, err := state.ReadEvents(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var hasCancelled bool
	for _, e := range events {
		if e.Type == "run_cancelled" && e.RunID == "r1" {
			hasCancelled = true
			break
		}
	}
	if !hasCancelled {
		t.Error("expected run_cancelled event in events.jsonl; got none")
	}
}

func TestCancelRun_SignalsParentWorker(t *testing.T) {
	// Cancel cascade wasn't signaling the parent of a cancelled run, so a
	// parent parked on subworkflow_in_progress stayed stuck forever even
	// after its child became terminal. Verify that cancelling a child
	// signals the parent's worker resumeCh.
	runsDir := filepath.Join(t.TempDir(), "runs")

	parentDir := filepath.Join(runsDir, "parent")
	_ = os.MkdirAll(parentDir, 0o755)
	parent := state.NewRunState("parent", "wf", nil)
	parent.Status = testStatusRun
	_ = state.SaveState(filepath.Join(parentDir, "state.json"), parent)

	childDir := filepath.Join(runsDir, "child")
	_ = os.MkdirAll(childDir, 0o755)
	child := state.NewRunState("child", "wf", nil)
	child.Status = testStatusRun
	child.ParentRun = "parent"
	_ = state.SaveState(filepath.Join(childDir, "state.json"), child)

	manager := NewManager(nil, runsDir)

	// Install a fake parent worker so we can detect the signal. cancel
	// is set to a noop so cancelSingle's worker.cancel() call is safe.
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	parentWorker := &runWorker{
		runID:    "parent",
		ctx:      pctx,
		cancel:   pcancel,
		resumeCh: make(chan struct{}, 1),
		doneCh:   make(chan struct{}),
	}
	manager.workers["parent"] = parentWorker

	// Cancel the CHILD specifically. Walk-up takes CancelRun to the root
	// (parent), which cascades down to child. Along the way the parent's
	// state transitions too, but the worker should also be signaled so a
	// restarted parent process would re-run and notice the child's state.
	if err := manager.CancelRun("child"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-parentWorker.resumeCh:
		// ok
	default:
		t.Error("expected parent worker to be signaled after child was cancelled")
	}
}

func TestCancelRunSetsStatusCancelled(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("run-1", "wf", nil)
	rs.Status = testStatusRun
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	manager := NewManager(nil, runsDir)

	err := manager.CancelRun("run-1")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	loaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != testStatusCancel {
		t.Fatalf("expected cancelled, got %q", loaded.Status)
	}
	if loaded.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestCancelRunRejectsTerminalStatus(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("run-1", "wf", nil)
	rs.Status = "completed"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	manager := NewManager(nil, runsDir)
	err := manager.CancelRun("run-1")
	if err == nil {
		t.Fatal("expected error for completed run")
	}
}

func TestCancelRunCascadesToChildren(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")

	parentDir := filepath.Join(runsDir, testRunIDParent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	parent := state.NewRunState(testRunIDParent, "wf", nil)
	parent.Status = testStatusRun
	if err := state.SaveState(filepath.Join(parentDir, "state.json"), parent); err != nil {
		t.Fatalf("save state: %v", err)
	}

	childDir := filepath.Join(runsDir, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	child := state.NewRunState("child", "wf", nil)
	child.Status = testStatusRun
	child.ParentRun = testRunIDParent
	if err := state.SaveState(filepath.Join(childDir, "state.json"), child); err != nil {
		t.Fatalf("save state: %v", err)
	}

	manager := NewManager(nil, runsDir)
	err := manager.CancelRun(testRunIDParent)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	loaded, _ := state.LoadState(filepath.Join(childDir, "state.json"))
	if loaded.Status != testStatusCancel {
		t.Fatalf("expected child cancelled, got %q", loaded.Status)
	}
}

func TestParentRunIDReturnsParent(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	childDir := filepath.Join(runsDir, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	child := state.NewRunState("child", "wf", nil)
	child.ParentRun = testRunIDParent
	if err := state.SaveState(filepath.Join(childDir, "state.json"), child); err != nil {
		t.Fatalf("save state: %v", err)
	}

	manager := NewManager(nil, runsDir)
	parent, err := manager.parentRunID("child")
	if err != nil {
		t.Fatalf("parentRunID: %v", err)
	}
	if parent != testRunIDParent {
		t.Fatalf("expected parent run id %q, got %q", testRunIDParent, parent)
	}
}

func TestSignalParentRunSignalsExistingParentWorker(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")

	childDir := filepath.Join(runsDir, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	child := state.NewRunState("child", "wf", nil)
	child.ParentRun = testRunIDParent
	if err := state.SaveState(filepath.Join(childDir, "state.json"), child); err != nil {
		t.Fatalf("save state: %v", err)
	}

	manager := NewManager(nil, runsDir)
	parentWorker := &runWorker{
		runID:    testRunIDParent,
		resumeCh: make(chan struct{}, 1),
		doneCh:   make(chan struct{}),
	}
	manager.workers[testRunIDParent] = parentWorker

	manager.signalParentRun("child")

	select {
	case <-parentWorker.resumeCh:
	default:
		t.Fatal("expected parent worker to receive resume signal")
	}
}

func TestStartLearnRun_CreatesRun(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Minimal engine with "learn" workflow defined
	reg := runners.NewRegistry()
	eng := &engine.Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"learn": {
					ID:      "learn",
					Name:    "Post-Run Learning",
					Version: 1,
					Nodes:   []definitions.Node{{ID: "placeholder", Kind: "system"}},
				},
			},
		},
		RunnerRegistry: reg,
		RunsDir:        runsDir,
	}

	manager := NewManager(eng, runsDir)

	nodes := []engine.InterviewableNode{
		{NodeID: "agent-a", RoleID: "engineer", SessionID: "sess-1", Outcome: "succeeded", Attempts: 1},
	}

	runID, err := manager.StartLearnRun("parent-run", filepath.Join(runsDir, "parent-run"), nodes)
	if err != nil {
		t.Fatalf("StartLearnRun: %v", err)
	}
	if runID == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Wait for the background worker to finish (it will complete quickly since
	// the workflow has only a system node).
	manager.mu.Lock()
	worker, ok := manager.workers[runID]
	manager.mu.Unlock()
	if ok {
		<-worker.doneCh
	}

	// Verify state was created
	rs, err := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if rs.WorkflowID != "learn" {
		t.Fatalf("expected workflow 'learn', got %q", rs.WorkflowID)
	}
	// Verify inputs
	if rs.Inputs["run_id"] != "parent-run" {
		t.Fatalf("expected run_id input 'parent-run', got %v", rs.Inputs["run_id"])
	}
	if rs.Inputs["run_dir"] != filepath.Join(runsDir, "parent-run") {
		t.Fatalf("expected run_dir input, got %v", rs.Inputs["run_dir"])
	}
}

func TestWireInterviewTrigger_SetsCallback(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	eng := &engine.Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"learn": {
					ID:      "learn",
					Name:    "Post-Run Learning",
					Version: 1,
					Nodes:   []definitions.Node{{ID: "placeholder", Kind: "system"}},
				},
			},
		},
		RunsDir: runsDir,
	}

	manager := NewManager(eng, runsDir)
	manager.WireInterviewTrigger()

	if eng.OnInterviewCandidates == nil {
		t.Fatal("expected OnInterviewCandidates to be set after WireInterviewTrigger")
	}
}

func TestWireWebhookCallback_SetsOnRunComplete(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	eng := &engine.Engine{
		Definitions: &definitions.Bundle{},
		RunsDir:     runsDir,
	}

	manager := NewManager(eng, runsDir)
	manager.WireWebhookCallback()

	if eng.OnRunComplete == nil {
		t.Fatal("expected OnRunComplete to be set after WireWebhookCallback")
	}
}

func TestCancelRun_FiresWebhookForPausedRun(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-paused")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-paused", "wf", nil)
	rs.Status = statusPaused
	rs.CallbackURL = testCallbackURL
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	// webhookFn fires synchronously inside CancelRun — no mutex needed.
	var firedPayload *webhookCapture
	manager := NewManager(nil, runsDir)
	manager.webhookFn = func(callbackURL string, rs *state.RunState) {
		firedPayload = &webhookCapture{callbackURL: callbackURL, status: rs.Status}
	}

	err := manager.CancelRun("run-paused")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if firedPayload == nil {
		t.Fatal("expected webhook to fire for paused run")
	}
	if firedPayload.callbackURL != testCallbackURL {
		t.Fatalf("expected callback URL, got %q", firedPayload.callbackURL)
	}
	if firedPayload.status != testStatusCancel {
		t.Fatalf("expected cancelled status, got %q", firedPayload.status)
	}
}

func TestCancelRun_NoWebhookForRunningRun(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-active")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-active", "wf", nil)
	rs.Status = testStatusRun
	rs.CallbackURL = testCallbackURL
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	fired := false
	manager := NewManager(nil, runsDir)
	manager.webhookFn = func(callbackURL string, rs *state.RunState) {
		fired = true
	}

	err := manager.CancelRun("run-active")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if fired {
		t.Fatal("expected webhook NOT to fire for running run (engine handles it)")
	}
}

func TestCancelRun_NoWebhookWhenNoCallbackURL(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-no-url")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-no-url", "wf", nil)
	rs.Status = statusPaused
	// No CallbackURL set
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	fired := false
	manager := NewManager(nil, runsDir)
	manager.webhookFn = func(callbackURL string, rs *state.RunState) {
		fired = true
	}

	err := manager.CancelRun("run-no-url")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if fired {
		t.Fatal("expected webhook NOT to fire when no callback URL")
	}
}

type webhookCapture struct {
	callbackURL string
	status      string
}

func TestRunCountsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(nil, dir)
	active, total := m.RunCounts()
	if active != 0 {
		t.Fatalf("expected active=0, got %d", active)
	}
	if total != 0 {
		t.Fatalf("expected total=0, got %d", total)
	}
}

func TestRunCountsWithRunDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "run-1"), 0o755); err != nil {
		t.Fatalf("mkdir run-1: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "run-2"), 0o755); err != nil {
		t.Fatalf("mkdir run-2: %v", err)
	}

	m := NewManager(nil, dir)
	active, total := m.RunCounts()
	if active != 0 {
		t.Fatalf("expected active=0, got %d", active)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
}

func TestFindParentNodeForChild(t *testing.T) {
	rs := &state.RunState{
		ID:         "parent-run",
		WorkflowID: "implement_spec",
		Status:     "completed",
		Nodes: map[string]*state.NodeState{
			"build": {
				ID:     "build",
				Status: "completed",
			},
			"integrate": {
				ID:     "integrate",
				Status: "completed",
				Data: map[string]any{
					"child_run": "child-run-1",
				},
			},
			"review": {
				ID:     "review",
				Status: "completed",
			},
		},
	}

	nodeID := findParentNodeForChild(rs, "child-run-1")
	if nodeID != "integrate" {
		t.Fatalf("expected 'integrate', got %q", nodeID)
	}

	nodeID = findParentNodeForChild(rs, "nonexistent")
	if nodeID != "" {
		t.Fatalf("expected empty string for nonexistent child, got %q", nodeID)
	}
}

func TestCascadeRetriggerToParent(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")

	parentDir := filepath.Join(runsDir, "parent-run")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	childDir := filepath.Join(runsDir, "child-run")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Parent run: completed, with "integrate" node that spawned child-run.
	parentState := &state.RunState{
		ID:         "parent-run",
		WorkflowID: "implement_spec",
		Status:     "completed",
		Nodes: map[string]*state.NodeState{
			"build": {
				ID:     "build",
				Status: "completed",
			},
			"integrate": {
				ID:     "integrate",
				Status: "completed",
				Data: map[string]any{
					"child_run": "child-run",
				},
			},
		},
	}
	if err := state.SaveState(filepath.Join(parentDir, "state.json"), parentState); err != nil {
		t.Fatal(err)
	}

	// Child run: completed, with ParentRun back-pointer.
	childState := &state.RunState{
		ID:         "child-run",
		WorkflowID: "verify_integration",
		Status:     "completed",
		ParentRun:  "parent-run",
		Nodes: map[string]*state.NodeState{
			"run_e2e_tests": {
				ID:     "run_e2e_tests",
				Status: "completed",
			},
		},
	}
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childState); err != nil {
		t.Fatal(err)
	}

	eng := &engine.Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{},
		},
		RunsDir: runsDir,
	}
	manager := NewManager(eng, runsDir)

	manager.cascadeRetriggerToParent("child-run")

	// Give the spawned worker a moment to start (it will fail to find the
	// workflow but that's fine — we only care about the state mutation).
	manager.mu.Lock()
	if w, ok := manager.workers["parent-run"]; ok {
		manager.mu.Unlock()
		<-w.doneCh
	} else {
		manager.mu.Unlock()
	}

	// Verify parent state was updated.
	updatedParent, err := state.LoadState(filepath.Join(parentDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if updatedParent.Status != "running" {
		t.Errorf("expected parent status 'running', got %q", updatedParent.Status)
	}

	if updatedParent.FinishedAt != nil {
		t.Error("expected parent FinishedAt to be nil")
	}

	integrateNode := updatedParent.Nodes["integrate"]
	if integrateNode == nil {
		t.Fatal("integrate node not found in parent")
	}

	if integrateNode.Status != "pending" {
		t.Errorf("expected integrate node status 'pending', got %q", integrateNode.Status)
	}

	// Data should be preserved (child_run pointer).
	if cr, ok := integrateNode.Data["child_run"].(string); !ok || cr != "child-run" {
		t.Errorf("expected child_run preserved in Data, got %v", integrateNode.Data)
	}
}

func TestCascadeRetriggerNoParent(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	childDir := filepath.Join(runsDir, "orphan-run")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}

	childState := &state.RunState{
		ID:         "orphan-run",
		WorkflowID: "wf",
		Status:     "completed",
		Nodes:      map[string]*state.NodeState{},
	}
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childState); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(nil, runsDir)

	// Should be a no-op — no panic, no error.
	manager.cascadeRetriggerToParent("orphan-run")
}

func TestWorkerFailureCancelsChildren(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")

	// Parent run: running, references a nonexistent workflow so ResumeRun
	// will fail with a fatal error ("workflow not found").
	parentDir := filepath.Join(runsDir, testRunIDParent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := state.NewRunState(testRunIDParent, "nonexistent-workflow", nil)
	parent.Status = testStatusRun
	if err := state.SaveState(filepath.Join(parentDir, "state.json"), parent); err != nil {
		t.Fatal(err)
	}

	// Child run: running, with ParentRun pointing to parent.
	childDir := filepath.Join(runsDir, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := state.NewRunState("child", "wf", nil)
	child.Status = testStatusRun
	child.ParentRun = testRunIDParent
	if err := state.SaveState(filepath.Join(childDir, "state.json"), child); err != nil {
		t.Fatal(err)
	}

	// Create engine with empty workflow definitions so ResumeRun fails fatally.
	eng := &engine.Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
	}

	manager := NewManager(eng, runsDir)
	if err := manager.ResumeRun(context.Background(), testRunIDParent); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	// Wait for the worker to finish (it will hit a fatal error).
	manager.WaitForRun(testRunIDParent)

	// The child run should have been cancelled when the parent worker failed.
	loaded, err := state.LoadState(filepath.Join(childDir, "state.json"))
	if err != nil {
		t.Fatalf("load child state: %v", err)
	}
	if loaded.Status != testStatusCancel {
		t.Fatalf("expected child status %q after parent failure, got %q", testStatusCancel, loaded.Status)
	}
}

func TestRestoreSkipsWhenDisabled(t *testing.T) {
	t.Setenv("TOIL_DISABLE_RESTORE", "1")
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-1", "wf", nil)
	rs.Status = testStatusRun
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(nil, runsDir)
	if err := manager.Restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(manager.workers) != 0 {
		t.Fatalf("expected no workers when restore disabled, got %d", len(manager.workers))
	}
}

func TestShutdownSavesStateForRestore(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runsDir, "run-shutdown")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-shutdown", "wf", nil)
	rs.Status = testStatusRun
	rs.WithNode("step1", func(n *state.NodeState) {
		n.Status = "running"
		n.SessionID = "sess-123"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(nil, runsDir)
	// Manually add a worker entry so Shutdown finds it.
	manager.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.workers["run-shutdown"] = &runWorker{
		runID:  "run-shutdown",
		ctx:    ctx,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}
	manager.mu.Unlock()

	manager.Shutdown()

	// State should still be "running" (not cancelled) so restore picks it up.
	loaded, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.Status != testStatusRun {
		t.Errorf("expected status %q after shutdown, got %q", testStatusRun, loaded.Status)
	}

	// In-flight nodes should be reset to pending for re-execution.
	loaded.WithNode("step1", func(n *state.NodeState) {
		if n.Status != "pending" {
			t.Errorf("expected node status pending after shutdown, got %q", n.Status)
		}
		if n.SessionID != "" {
			t.Errorf("expected session cleared, got %q", n.SessionID)
		}
	})
}

func TestCancelRunWalksUpToRoot(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")

	// Create root → child → grandchild
	for _, r := range []struct {
		id     string
		parent string
	}{
		{"root", ""},
		{"child", "root"},
		{"grandchild", "child"},
	} {
		dir := filepath.Join(runsDir, r.id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rs := state.NewRunState(r.id, "wf", nil)
		rs.Status = testStatusRun
		rs.ParentRun = r.parent
		if err := state.SaveState(filepath.Join(dir, "state.json"), rs); err != nil {
			t.Fatal(err)
		}
	}

	manager := NewManager(nil, runsDir)

	// Cancel the grandchild — should cancel root and cascade to all
	if err := manager.CancelRun("grandchild"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	for _, id := range []string{"root", "child", "grandchild"} {
		rs, err := state.LoadState(filepath.Join(runsDir, id, "state.json"))
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if rs.Status != testStatusCancel {
			t.Errorf("%s: expected cancelled, got %q", id, rs.Status)
		}
	}
}
