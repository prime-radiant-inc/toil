package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/webhook"
)

const (
	statusRunning   = "running"
	statusPaused    = "paused"
	statusCancelled = "cancelled"
	statusPending   = "pending"
	statusCompleted = "completed"
	statusFailed    = "failed"

	keyChildRun = "child_run"
)

type Manager struct {
	Engine  *engine.Engine
	RunsDir string

	mu        sync.Mutex
	workers   map[string]*runWorker
	webhookFn func(callbackURL string, rs *state.RunState)
}

type runWorker struct {
	runID    string
	engine   *engine.Engine
	manager  *Manager
	ctx      context.Context
	cancel   context.CancelFunc
	resumeCh chan struct{}
	doneCh   chan struct{}

	mu     sync.Mutex
	failed error
}

func NewManager(engine *engine.Engine, runsDir string) *Manager {
	return &Manager{
		Engine:  engine,
		RunsDir: runsDir,
		workers: make(map[string]*runWorker),
	}
}

func (manager *Manager) StartRun(ctx context.Context, workflowID string, inputs map[string]any, env map[string]string, callbackURL string) (string, error) {
	runID, err := manager.Engine.CreateRun(workflowID, inputs, env, callbackURL)
	if err != nil {
		return "", err
	}
	worker := manager.ensureWorker(runID)
	worker.Signal()
	return runID, nil
}

func (manager *Manager) ResumeRun(ctx context.Context, runID string) error {
	worker := manager.ensureWorker(runID)
	worker.Signal()
	return nil
}

// RetriggerNode resets a node and its downstream cascade in an existing run,
// then resumes execution. If the run has a parent, the parent's subworkflow
// node is also reset so execution propagates back up the run tree.
func (manager *Manager) RetriggerNode(ctx context.Context, runID, nodeID string) error {
	if err := manager.Engine.RetriggerNode(runID, nodeID); err != nil {
		return err
	}
	manager.cascadeRetriggerToParent(runID)
	return manager.ResumeRun(ctx, runID)
}

// cascadeRetriggerToParent walks up the ParentRun chain and resets each
// parent's subworkflow node so the parent run re-enters "running" state
// and will pick up the child's eventual completion.
func (manager *Manager) cascadeRetriggerToParent(childRunID string) {
	parentID, err := manager.parentRunID(childRunID)
	if err != nil || parentID == "" {
		return
	}

	parentPath := filepath.Join(manager.RunsDir, parentID, "state.json")
	parentState, err := state.LoadState(parentPath)
	if err != nil {
		return
	}

	// Only cascade if the parent is in a terminal state.
	if parentState.Status != statusCompleted && parentState.Status != statusFailed {
		return
	}

	parentNodeID := findParentNodeForChild(parentState, childRunID)
	if parentNodeID == "" {
		return
	}

	// Reset the parent node that spawned this child.
	parentState.WithNode(parentNodeID, func(node *state.NodeState) {
		node.Status = statusPending
		node.Decision = ""
		node.Message = ""
		node.Artifacts = nil
		node.StartedAt = nil
		node.EndedAt = nil
		node.SessionID = ""
		node.Attempts = 0
		node.RetryCount = 0
		node.NoProgressCount = 0
		// Preserve Data — it contains the child_run pointer.
	})

	// Set parent run back to running.
	parentState.Status = statusRunning
	parentState.Totals = nil // retriggered parent's totals are stale until next terminal save
	parentState.Error = ""
	parentState.FinishedAt = nil
	parentState.Summary = ""
	parentState.Description = ""

	if err := state.SaveState(parentPath, parentState); err != nil {
		slog.Error("toil.cascade_retrigger.save_failed", "parent_run", parentID, "error", err)
		return
	}

	// Log cascade_retrigger event on the parent run.
	parentEventsPath := filepath.Join(manager.RunsDir, parentID, "events.jsonl")
	parentLogger, logErr := state.NewLogger(parentEventsPath)
	if logErr == nil {
		_ = parentLogger.Append(state.Event{
			Type:  "cascade_retrigger",
			RunID: parentID,
			Data: map[string]any{
				keyChildRun:   childRunID,
				"parent_node": parentNodeID,
			},
		})
		_ = parentLogger.Close()
	}

	slog.Info("toil.cascade_retrigger", keyChildRun, childRunID, "parent_run", parentID, "parent_node", parentNodeID)

	// Resume the parent worker so it picks up the reset node.
	_ = manager.ResumeRun(context.Background(), parentID)

	// Recurse up in case the parent also has a parent.
	manager.cascadeRetriggerToParent(parentID)
}

// findParentNodeForChild scans a run's nodes for the one whose
// data.child_run matches the given child run ID.
func findParentNodeForChild(rs *state.RunState, childRunID string) string {
	var found string
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, node := range nodes {
			if node.Data == nil {
				continue
			}
			if cr, ok := node.Data[keyChildRun].(string); ok && cr == childRunID {
				found = id
				return
			}
		}
	})
	return found
}

func (manager *Manager) NotifyApproval(ctx context.Context, runID string) error {
	return manager.ResumeRun(ctx, runID)
}

func (manager *Manager) Restore(ctx context.Context) error {
	if !config.RestoreEnabled() {
		return nil
	}
	entries, err := os.ReadDir(manager.RunsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Only restore root runs (no parent). Child runs are triggered
	// naturally when their parent progresses. This prevents a restart
	// from spawning hundreds of concurrent runners.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		statePath := filepath.Join(manager.RunsDir, runID, "state.json")
		runState, err := state.LoadState(statePath)
		if err != nil {
			continue
		}
		if runState.Status != statusRunning && runState.Status != statusPaused {
			continue
		}
		if strings.TrimSpace(runState.ParentRun) != "" {
			continue
		}
		worker := manager.ensureWorker(runID)
		worker.Signal()
	}

	return nil
}

func (manager *Manager) CancelRun(runID string) error {
	// Walk up to the root run so cancelling any node in the tree
	// cancels the entire execution group.
	rootID := runID
	for {
		statePath := filepath.Join(manager.RunsDir, rootID, "state.json")
		rs, err := state.LoadState(statePath)
		if err != nil {
			break
		}
		if strings.TrimSpace(rs.ParentRun) == "" {
			break
		}
		rootID = rs.ParentRun
	}
	return manager.cancelSingle(rootID)
}

func (manager *Manager) cancelSingle(runID string) error {
	if _, err := manager.markCancelled(runID); err != nil {
		return err
	}
	return manager.cancelChildren(runID)
}

// markCancelled writes a cancelled state.json, emits a run_cancelled event,
// and cancels the worker's context. Returns the pre-mutation status so the
// caller can decide webhook responsibility (paused → orchestrator fires the
// webhook; running → engine's cancelled path handles it). Errors if the
// run isn't found or can't be cancelled from its current status.
func (manager *Manager) markCancelled(runID string) (priorStatus string, err error) {
	statePath := filepath.Join(manager.RunsDir, runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		return "", fmt.Errorf("load state: %w", err)
	}
	if rs.Status != statusRunning && rs.Status != statusPaused {
		return "", fmt.Errorf("cannot cancel run with status %q", rs.Status)
	}

	priorStatus = rs.Status
	now := time.Now().UTC()

	// Append run_cancelled BEFORE flipping state so the event log never
	// trails the state file. If append fails, leave state as-is and
	// return the error — caller retries, consumers never see status=
	// cancelled without the corresponding terminal event.
	eventsPath := filepath.Join(manager.RunsDir, runID, "events.jsonl")
	logger, lerr := state.NewLogger(eventsPath)
	if lerr != nil {
		return "", fmt.Errorf("open events log: %w", lerr)
	}
	if aerr := logger.Append(state.Event{
		Timestamp: now,
		Type:      "run_cancelled",
		RunID:     runID,
		Data: map[string]any{
			"workflow_id": rs.WorkflowID,
			"source":      "orchestrator",
		},
	}); aerr != nil {
		_ = logger.Close()
		return "", fmt.Errorf("append run_cancelled event: %w", aerr)
	}
	if cerr := logger.Close(); cerr != nil {
		return "", fmt.Errorf("close events log: %w", cerr)
	}

	rs.Status = statusCancelled
	rs.FinishedAt = &now
	_ = engine.FinalizeRunTotals(rs, filepath.Dir(statePath))
	if err := state.SaveState(statePath, rs); err != nil {
		return "", fmt.Errorf("save state: %w", err)
	}

	// Fire webhook for paused runs — no engine will handle it.
	if priorStatus == statusPaused {
		manager.maybeFireWebhook(rs)
	}

	// Cancel the worker's context to kill running subprocesses.
	manager.mu.Lock()
	if worker, ok := manager.workers[runID]; ok && worker.cancel != nil {
		worker.cancel()
	}
	manager.mu.Unlock()

	// Signal the parent's worker so a parent parked on
	// subworkflow_in_progress notices the child's terminal state and can
	// propagate upward. Noop when the parent is being cancelled in the
	// same cascade (its worker is about to be torn down anyway).
	if parentID := strings.TrimSpace(rs.ParentRun); parentID != "" {
		manager.mu.Lock()
		if pw, ok := manager.workers[parentID]; ok {
			pw.Signal()
		}
		manager.mu.Unlock()
	}

	return priorStatus, nil
}

func (manager *Manager) cancelChildren(parentID string) error {
	entries, err := os.ReadDir(manager.RunsDir)
	if err != nil {
		return nil // no runs directory is fine
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childID := entry.Name()
		if childID == parentID {
			continue
		}
		statePath := filepath.Join(manager.RunsDir, childID, "state.json")
		rs, err := state.LoadState(statePath)
		if err != nil {
			continue
		}
		if rs.ParentRun != parentID {
			continue
		}
		if rs.Status != statusRunning && rs.Status != statusPaused {
			continue
		}
		// markCancelled writes state + emits run_cancelled event + cancels
		// worker ctx + signals the child's parent. In this cascade the
		// child's parent is parentID, whose worker we just cancelled —
		// the signal is harmless.
		if _, mcerr := manager.markCancelled(childID); mcerr != nil {
			continue
		}

		_ = manager.cancelChildren(childID)
	}
	return nil
}

// maybeFireWebhook fires the webhook if the run has a callback URL.
// Uses the injectable webhookFn for testability.
func (manager *Manager) maybeFireWebhook(rs *state.RunState) {
	if rs.CallbackURL == "" {
		return
	}
	if manager.webhookFn != nil {
		manager.webhookFn(rs.CallbackURL, rs)
		return
	}
	manager.deliverWebhook(rs.CallbackURL, rs)
}

func (manager *Manager) deliverWebhook(callbackURL string, rs *state.RunState) {
	payload := webhook.PayloadFromRunState(rs)
	if err := webhook.Deliver(callbackURL, payload); err != nil {
		slog.Error("toil.webhook.delivery_failed", "run_id", rs.ID, "callback_url", callbackURL, "error", err)
	} else {
		slog.Info("toil.webhook.delivered", "run_id", rs.ID, "callback_url", callbackURL)
	}
}

// WireWebhookCallback sets the engine's OnRunComplete callback to fire
// the webhook. Call this after creating the manager.
func (manager *Manager) WireWebhookCallback() {
	manager.Engine.OnRunComplete = func(runState *state.RunState, runDir string) {
		manager.maybeFireWebhook(runState)
	}
}

// StartLearnRun starts the "learn" workflow with inputs for interviewing the
// given nodes. It is designed to be called fire-and-forget from the engine's
// OnInterviewCandidates callback.
func (manager *Manager) StartLearnRun(parentRunID string, parentRunDir string, nodes []engine.InterviewableNode) (string, error) {
	// Convert InterviewableNode slice to []any for the workflow input.
	interviewable := make([]any, len(nodes))
	for i, n := range nodes {
		interviewable[i] = map[string]any{
			"node_id":    n.NodeID,
			"role_id":    n.RoleID,
			"session_id": n.SessionID,
			"outcome":    n.Outcome,
			"attempts":   n.Attempts,
		}
	}

	// Build a run context summary from the event log for the interviewer.
	runContext := buildRunContext(parentRunDir, nodes)

	inputs := map[string]any{
		"run_id":              parentRunID,
		"run_dir":             parentRunDir,
		"run_context":         runContext,
		"interviewable_nodes": interviewable,
	}

	return manager.StartRun(context.Background(), "learn", inputs, nil, "")
}

// buildRunContext produces a brief text summary of the run for interviewers.
func buildRunContext(runDir string, nodes []engine.InterviewableNode) string {
	var summary string
	summary += fmt.Sprintf("Run directory: %s\n", runDir)
	summary += fmt.Sprintf("Interviewable nodes: %d\n\n", len(nodes))
	for _, n := range nodes {
		summary += fmt.Sprintf("- %s (role: %s, outcome: %s, attempts: %d)\n", n.NodeID, n.RoleID, n.Outcome, n.Attempts)
	}
	return summary
}

// RunCounts returns the number of active workers and total run directories.
func (manager *Manager) RunCounts() (active int, total int) {
	manager.mu.Lock()
	active = len(manager.workers)
	manager.mu.Unlock()

	entries, err := os.ReadDir(manager.RunsDir)
	if err != nil {
		return active, 0
	}
	for _, e := range entries {
		if e.IsDir() {
			total++
		}
	}
	return active, total
}

// WireInterviewTrigger sets the engine's OnInterviewCandidates callback to
// spawn the learn workflow via this manager. Call this after creating the
// manager.
func (manager *Manager) WireInterviewTrigger() {
	manager.Engine.OnInterviewCandidates = func(runID string, runDir string, nodes []engine.InterviewableNode) {
		learnRunID, err := manager.StartLearnRun(runID, runDir, nodes)
		if err != nil {
			slog.Error("toil.interview.trigger_failed", "run_id", runID, "error", err)
		} else {
			slog.Info("toil.interview.triggered", "learn_run_id", learnRunID, "parent_run_id", runID, "candidates", len(nodes))
		}
	}
}

// Shutdown flushes state for all in-flight runs so they can be
// restored on restart. Does not cancel runs — the process is about
// to exit and the OS will clean up child processes.
func (manager *Manager) Shutdown() {
	manager.mu.Lock()
	runIDs := make([]string, 0, len(manager.workers))
	for id := range manager.workers {
		runIDs = append(runIDs, id)
	}
	manager.mu.Unlock()

	for _, runID := range runIDs {
		statePath := filepath.Join(manager.RunsDir, runID, "state.json")
		rs, err := state.LoadState(statePath)
		if err != nil {
			continue
		}
		// Reset any nodes stuck in "running" back to "pending" so
		// resume re-executes them instead of skipping.
		rs.WithNodes(func(nodes map[string]*state.NodeState) {
			for _, n := range nodes {
				if n.Status == statusRunning {
					n.Status = statusPending
					n.SessionID = ""
				}
			}
		})
		_ = state.SaveState(statePath, rs)
	}
	slog.Info("toil.manager.shutdown_complete", "runs_saved", len(runIDs))
}

// WaitForRun blocks until the run's worker goroutine finishes.
func (manager *Manager) WaitForRun(runID string) {
	manager.mu.Lock()
	worker, ok := manager.workers[runID]
	manager.mu.Unlock()
	if ok {
		<-worker.doneCh
	}
}

func (manager *Manager) ensureWorker(runID string) *runWorker {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	worker, ok := manager.workers[runID]
	if ok {
		return worker
	}

	ctx, cancel := context.WithCancel(context.Background())
	worker = &runWorker{
		runID:    runID,
		engine:   manager.Engine,
		manager:  manager,
		ctx:      ctx,
		cancel:   cancel,
		resumeCh: make(chan struct{}, 1),
		doneCh:   make(chan struct{}),
	}
	manager.workers[runID] = worker
	go worker.Run()
	return worker
}

func (worker *runWorker) Signal() {
	select {
	case <-worker.doneCh:
		return
	default:
	}

	select {
	case worker.resumeCh <- struct{}{}:
	default:
	}
}

func (worker *runWorker) Run() {
	defer worker.cancel()
	for {
		select {
		case <-worker.ctx.Done():
			close(worker.doneCh)
			return
		case <-worker.resumeCh:
			err := worker.runOnce()
			if err == nil {
				worker.signalParent()
				close(worker.doneCh)
				return
			}
			if errors.Is(err, engine.ErrRunCancelled) || errors.Is(err, engine.ErrApprovalPending) || errors.Is(err, engine.ErrSubworkflowInProgress) {
				continue
			}
			if errors.Is(err, engine.ErrUnresolvedFailure) {
				// Run terminated with unresolved failure routing. Record as failed
				// for this worker. The run loop already completed all in-flight work;
				// no children to cancel.
				worker.mu.Lock()
				worker.failed = err
				worker.mu.Unlock()
				worker.signalParent()
				close(worker.doneCh)
				return
			}
			worker.mu.Lock()
			worker.failed = err
			worker.mu.Unlock()
			_ = worker.manager.cancelChildren(worker.runID)
			worker.signalParent()
			close(worker.doneCh)
			return
		case <-time.After(1 * time.Second):
			if worker.isDone() {
				return
			}
		}
	}
}

func (worker *runWorker) runOnce() error {
	_, err := worker.engine.ResumeRun(worker.ctx, worker.runID)
	return err
}

func (worker *runWorker) isDone() bool {
	select {
	case <-worker.doneCh:
		return true
	default:
		return false
	}
}

func (worker *runWorker) Error() error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.failed != nil {
		return fmt.Errorf("run %s failed: %w", worker.runID, worker.failed)
	}
	return nil
}

func (worker *runWorker) signalParent() {
	if worker.manager == nil {
		return
	}
	worker.manager.signalParentRun(worker.runID)
}

func (manager *Manager) signalParentRun(runID string) {
	parentRunID, err := manager.parentRunID(runID)
	if err != nil || parentRunID == "" {
		return
	}
	_ = manager.ResumeRun(context.Background(), parentRunID)
}

func (manager *Manager) parentRunID(runID string) (string, error) {
	runState, err := state.LoadState(filepath.Join(manager.RunsDir, runID, "state.json"))
	if err != nil {
		return "", err
	}
	return runState.ParentRun, nil
}
