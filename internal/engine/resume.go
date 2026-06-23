package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func (engine *Engine) ResumeRun(ctx context.Context, runID string) (NodeOutput, error) {
	// Prevent concurrent execution of the same run. Without this lock,
	// multiple goroutines can load the same state, execute the same nodes,
	// and create duplicate child runs — causing exponential run explosion.
	mu := engine.lockRun(runID)
	mu.Lock()
	defer mu.Unlock()

	runDir := filepath.Join(engine.RunsDir, runID)
	runState, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return NodeOutput{}, err
	}
	if runState.Status == statusCompleted {
		output, _ := lastOutputFromState(runState)
		// Preserve cached output (so executeSubworkflow can read child
		// Decision via lastOutputFromState), but propagate the failure
		// sentinel on re-resume of a finalized-with-flag run.
		if runState.HasUnresolvedFailure {
			return output, ErrUnresolvedFailure
		}
		return output, nil
	}
	if runState.Status == statusCancelled {
		return NodeOutput{}, fmt.Errorf("run %s is cancelled", runID)
	}

	workflow, err := engine.loadWorkflowSnapshot(runDir, runState.WorkflowID)
	if err != nil {
		return NodeOutput{}, err
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), engine.eventStdout())
	if err != nil {
		return NodeOutput{}, err
	}
	defer func() { _ = logger.Close() }()

	// Restore in-memory secrets from the engine store (secrets are never
	// persisted to disk, so they must be carried forward in memory).
	if secrets := engine.loadRunSecrets(runID); len(secrets) > 0 {
		runState.Secrets = secrets
		logger.SetSecrets(secrets)
	}

	// Only save if status actually changed — avoids a redundant write when
	// createRun already saved the state as "running" moments ago.
	if runState.Status != statusRunning || runState.Error != "" {
		runState.Status = statusRunning
		runState.Totals = nil // resumed run's totals are stale until next terminal save
		runState.Error = ""
		if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
			return NodeOutput{}, err
		}
	}

	// Best-effort: generate a friendly title + one-line intended description for the run.
	maybeGenerateRunIntent(ctx, runDir, runState, workflow, logger)

	runStarted := time.Now()

	runContext := engine.NewRunContext(runState, workflow)
	ready := resumeReadyNodes(workflow, runState, runContext)
	lastOutput, err := engine.runLoop(ctx, runID, runDir, workflow, runState, runContext, logger, ready)
	if err != nil {
		// Use errors.Is so a wrapped sentinel (e.g. fmt.Errorf("node X: %w", ...))
		// still routes correctly. Plain `switch err` only matches by value,
		// which would silently fall through to the failed branch when the
		// run loop wraps the error with node context.
		switch {
		case errors.Is(err, ErrRunCancelled) || errors.Is(err, context.Canceled):
			runState.Status = statusCancelled
			runState.Error = ""
			now := time.Now().UTC()
			runState.FinishedAt = &now
			_ = logger.Append(state.Event{Type: "run_cancelled", RunID: runID, DurationMs: durationMs(runStarted, now), Data: map[string]any{
				keyWorkflowID: runState.WorkflowID,
			}})
			_ = FinalizeRunTotals(runState, runDir)
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			maybeGenerateRunSummary(ctx, runDir, runState, workflow, logger)
			if engine.OnRunComplete != nil {
				engine.OnRunComplete(runState, runDir)
			}
			return lastOutput, err

		case errors.Is(err, ErrApprovalPending):
			runState.Status = statusPaused
			runState.Error = ""
			_ = logger.Append(state.Event{Type: "run_paused", RunID: runID, Data: map[string]any{
				keyWorkflowID: runState.WorkflowID,
				keyReason:     "approval_pending",
			}})
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			return lastOutput, err

		case errors.Is(err, ErrSubworkflowInProgress):
			// Parent workflow is waiting on an already-running child run.
			// Keep status as running to avoid misleading "approval pending" UI.
			runState.Status = statusRunning
			runState.Totals = nil // resumed run's totals are stale until next terminal save
			runState.Error = ""
			_ = logger.Append(state.Event{Type: "subworkflow_pending", RunID: runID, Data: map[string]any{
				keyWorkflowID: runState.WorkflowID,
				keyReason:     "subworkflow_in_progress",
			}})
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			return lastOutput, err

		default:
			runState.Status = statusFailed
			runState.Error = err.Error()
			now := time.Now().UTC()
			runState.FinishedAt = &now
			_ = logger.Append(state.Event{Type: "run_failed", RunID: runID, DurationMs: durationMs(runStarted, now), Data: map[string]any{
				keyWorkflowID: runState.WorkflowID,
				keyError:      err.Error(),
			}})
			_ = FinalizeRunTotals(runState, runDir)
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			engine.maybeEmitInterviewCandidates(runID, runDir, runState, workflow, logger)
			maybeGenerateRunSummary(ctx, runDir, runState, workflow, logger)
			if engine.OnRunComplete != nil {
				engine.OnRunComplete(runState, runDir)
			}
			return NodeOutput{}, err
		}
	}

	// Compute unresolved-failure flag before finalizing status. The walk
	// reads NodeState.LastRoutingDecision, the workflow snapshot, and child
	// run state.json files; it's the engine's sole way of deriving run-level
	// failure from edge declarations.
	ComputeUnresolvedFailure(runState, workflow, runDir)

	runState.Status = statusCompleted
	runState.Error = ""
	finished := time.Now().UTC()
	runState.FinishedAt = &finished
	_ = FinalizeRunTotals(runState, runDir)
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
	_ = logger.Append(state.Event{Type: "run_completed", RunID: runID, DurationMs: durationMs(runStarted, finished), Data: map[string]any{
		keyWorkflowID:           runState.WorkflowID,
		keyNodeCount:            len(workflow.Nodes),
		keyHasUnresolvedFailure: runState.HasUnresolvedFailure,
	}})

	// IMPORTANT: Run post-completion hooks regardless of HasUnresolvedFailure.
	// These hooks (interview emission, narrative summary, OnRunComplete) need
	// to observe the flag — skipping them would defeat interview-trigger filter
	// changes and narrative-summary updates.
	engine.maybeEmitInterviewCandidates(runID, runDir, runState, workflow, logger)
	maybeGenerateRunSummary(ctx, runDir, runState, workflow, logger)
	if engine.OnRunComplete != nil {
		engine.OnRunComplete(runState, runDir)
	}

	// Return the sentinel after hooks have observed the flag. Callers (eval,
	// orchestrator) translate the sentinel into their own status semantics.
	if runState.HasUnresolvedFailure {
		return lastOutput, ErrUnresolvedFailure
	}
	return lastOutput, nil
}

// NewRunContext builds a RunContext for the given run, wiring in the
// filesystem-backed TreeResolver using the engine's RunsDir. Prefer
// this helper over calling RunContextFromState directly — any new
// context-construction site should pick up `tree.*` expression support
// automatically, and this is the single place to keep tree wiring in
// sync with the engine. RunContextFromState remains exported for test
// fixtures that don't need (and can't provide) a runs directory.
func (engine *Engine) NewRunContext(runState *state.RunState, workflow *definitions.Workflow) *RunContext {
	ctx := RunContextFromState(runState, workflow)
	ctx.Tree = NewFilesystemTreeResolver(engine.RunsDir, runState.ID)
	return ctx
}

func RunContextFromState(runState *state.RunState, workflow *definitions.Workflow) *RunContext {
	ctx := &RunContext{
		RunID:          runState.ID,
		Inputs:         runState.Inputs,
		Outputs:        make(map[string]NodeOutput),
		OptionalInputs: optionalInputsFromWorkflow(workflow),
	}
	ctx.PopulateEnv(runState.Env)
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, node := range nodes {
			if node.Status != statusCompleted {
				continue
			}
			ctx.Outputs[id] = NodeOutput{
				Decision:            node.Decision,
				Message:             node.Message,
				Artifacts:           node.Artifacts,
				Data:                node.Data,
				SessionID:           node.SessionID,
				LoopIterations:      node.LoopIterations,
				LastRoutingDecision: node.LastRoutingDecision,
			}
		}
	})
	return ctx
}

func resumeReadyNodes(workflow *definitions.Workflow, runState *state.RunState, runContext *RunContext) []readyNode {
	ready := []readyNode{}
	seen := map[string]bool{}
	incomingCounts := joinIncomingEdgeCount(workflow)

	// Snapshot join arrival counts before entering WithNodes to avoid re-entrant lock.
	joinArrivalCount := map[string]int{}
	for nodeID := range incomingCounts {
		arrived := runState.GetJoinState(nodeID)
		joinArrivalCount[nodeID] = len(arrived)
	}

	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for nodeID, output := range runContext.Outputs {
			routingDecision := output.Decision
			if output.LastRoutingDecision != "" {
				routingDecision = output.LastRoutingDecision
			}
			for _, edge := range matchEdges(workflow, nodeID, routingDecision) {
				if shouldSkipResume(nodes, nodeID, edge.To) {
					continue
				}
				if seen[edge.To] {
					continue
				}
				// Join-aware: don't add join nodes to ready if arrival count incomplete
				targetNode := definitions.FindNode(workflow, edge.To)
				if targetNode != nil && targetNode.Join == joinAll {
					if joinArrivalCount[edge.To] < incomingCounts[edge.To] {
						continue // wait for remaining predecessors
					}
				}
				seen[edge.To] = true
				ready = append(ready, readyNode{ID: edge.To, EdgePrompt: edge.Prompt, FromNodeID: nodeID, EdgeIndex: -1})
			}
		}

		// Re-queue nodes interrupted mid-retry or mid-execution
		for _, node := range workflow.Nodes {
			if seen[node.ID] {
				continue
			}
			ns, exists := nodes[node.ID]
			if !exists {
				continue
			}
			if ns.Status == statusRetrying || ns.Status == statusRunning {
				seen[node.ID] = true
				ns.RetryCount = 0
				ready = append(ready, readyNode{ID: node.ID, EdgeIndex: -1})
			}
		}
	})

	return ready
}

func shouldSkipResume(nodes map[string]*state.NodeState, fromID string, toID string) bool {
	toNode, ok := nodes[toID]
	if !ok || toNode.Status != statusCompleted {
		return false
	}
	fromNode, ok := nodes[fromID]
	if !ok {
		return false
	}
	// If the downstream node is already completed and we cannot prove the
	// upstream completion is newer (missing timestamps are common for for_each
	// parent nodes), skip replay to avoid re-running completed edges on resume.
	return fromNode.EndedAt == nil || toNode.EndedAt == nil || !fromNode.EndedAt.After(*toNode.EndedAt)
}
