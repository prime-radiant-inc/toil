package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

var ErrRunCancelled = errors.New("run cancelled")

type nodeResult struct {
	nodeID string
	output NodeOutput
	err    error
}

type readyNode struct {
	ID         string
	EdgePrompt string
	FromNodeID string         // source node that triggered this edge (empty for start/retry nodes)
	EdgeIndex  int            // position in workflow.Edges; -1 for synthesized (start/retry)
	Passes     map[string]any // evaluated edge.Passes for this edge, or nil
}

func (engine *Engine) runLoop(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, runState *state.RunState, runContext *RunContext, logger *state.Logger, ready []readyNode) (NodeOutput, error) {
	if len(ready) == 0 {
		ready = startNodes(workflow)
	}
	if runContext.Outputs == nil {
		runContext.Outputs = make(map[string]NodeOutput)
	}
	if runContext.Inputs == nil {
		runContext.Inputs = map[string]any{}
	}

	var lastOutput NodeOutput

	// Compute incoming edge counts for join nodes
	incomingEdgeCount := joinIncomingEdgeCount(workflow)

	// Initialize arrival tracking (may be pre-populated from persisted JoinState on resume)
	arrivedEdges := map[string]map[string]bool{}
	joinFired := map[string]bool{}
	if runState.JoinState != nil {
		for joinID, joinNode := range runState.JoinState {
			if joinNode == nil {
				continue
			}
			arrivedEdges[joinID] = state.ToSet(joinNode.Arrived)
		}
	}

	for len(ready) > 0 {
		if ctx.Err() != nil {
			cancelRunningNodes(runState)
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			return lastOutput, ErrRunCancelled
		}

		// Dedup ready queue — collapse duplicate node entries from fan-in convergence.
		// Convergent passes are merged by edge-index ASC (highest wins on overlap).
		ready = dedupReadyQueue(ready, logger, runID)

		wave := ready
		ready = nil

		approvalOutputs, timedOutNodeIDs, runnable, pending, err := engine.processApprovals(runID, runDir, workflow, wave, runContext, logger, runState)
		if err != nil {
			return lastOutput, err
		}

		for _, outcome := range approvalOutputs {
			if err := applyOutput(workflow, runContext, runState, outcome.nodeID, outcome.output, &lastOutput, &ready, arrivedEdges, incomingEdgeCount, joinFired, logger, runID); err != nil {
				return lastOutput, err
			}
		}

		for _, nodeID := range timedOutNodeIDs {
			if err := synthesizeMetaCompletion(
				runID, workflow, runContext, runState, logger,
				nodeID, MetaDecisionTimeout,
				&ready, arrivedEdges, incomingEdgeCount, joinFired,
			); err != nil {
				return lastOutput, err
			}
		}

		if pending {
			if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
				return lastOutput, err
			}
			return lastOutput, ErrApprovalPending
		}

		if len(runnable) == 0 {
			continue
		}

		var runnableFiltered []readyNode
		for _, rn := range runnable {
			node := definitions.FindNode(workflow, rn.ID)
			if node == nil {
				return lastOutput, fmt.Errorf("node not found: %s", rn.ID)
			}
			if limit := workflow.Limits["max_loop_iterations"]; limit > 0 {
				count, exhausted := getAndIncrementLoopIterations(runState, node.ID, limit)
				if exhausted {
					// Try meta-decision routing first. If an outgoing edge with
					// when: _loop_exhausted exists, synthesize the meta-decision
					// and route through the standard edge path.
					metaEdges := matchEdgesExplicit(workflow, node.ID, MetaDecisionLoopExhausted)
					if len(metaEdges) > 0 {
						if err := synthesizeMetaCompletion(
							runID, workflow, runContext, runState, logger,
							node.ID, MetaDecisionLoopExhausted,
							&ready, arrivedEdges, incomingEdgeCount, joinFired,
						); err != nil {
							return lastOutput, err
						}
						// Note: LoopIterations stays at the exhausted value;
						// lazy-reset happens at the start of the next dispatch (see
						// getAndIncrementLoopIterations).
						continue
					}

					// No graceful exhaustion handling — fail the run.
					var lastMsg, lastDecision string
					runState.WithNode(node.ID, func(n *state.NodeState) {
						lastMsg = n.Message
						lastDecision = n.Decision
					})
					_ = logger.Append(state.Event{
						Type: "loop_exhausted_failed", RunID: runID, NodeID: node.ID,
						Data: map[string]any{
							"executions":    count,
							"limit":         limit,
							"last_decision": lastDecision,
							"last_message":  lastMsg,
						},
					})
					return lastOutput, loopExhaustedError(node.ID, lastDecision, lastMsg)
				}
			}
			runnableFiltered = append(runnableFiltered, rn)
		}
		runnable = runnableFiltered

		if len(runnable) == 0 {
			continue
		}

		waveNodeIDs := make([]string, len(runnable))
		for i, rn := range runnable {
			waveNodeIDs[i] = rn.ID
		}
		_ = logger.Append(state.Event{
			Type: eventWaveStarted, RunID: runID,
			Data: map[string]any{keyNodeCount: len(runnable), "node_ids": waveNodeIDs},
		})
		waveStart := time.Now()

		results := engine.executeWave(ctx, runID, runDir, workflow, runnable, runContext, logger, runState)

		if ctx.Err() != nil {
			cancelRunningNodes(runState)
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			return lastOutput, ErrRunCancelled
		}

		succeeded := 0
		failed := 0
		var fatalErr error
		for _, result := range results {
			if result.err != nil {
				failed++

				// Check for retry exhaustion BEFORE legacy failure routing.
				// A node qualifies when it has retry.max > 1 AND consumed all
				// retries (RetryCount == Retry.Max, set by the final delay block
				// in executeSingle). Non-retryable first-attempt failures never
				// enter the delay block, so RetryCount stays 0 — they fall
				// through to the legacy `status == 'failed'` path unchanged.
				node := definitions.FindNode(workflow, result.nodeID)
				if node != nil && node.Retry != nil && node.Retry.Max > 1 {
					var retryCount int
					runState.WithNode(result.nodeID, func(n *state.NodeState) {
						retryCount = n.RetryCount
					})
					if retryCount >= node.Retry.Max {
						metaEdges := matchEdgesExplicit(workflow, result.nodeID, MetaDecisionRetryExhausted)
						if len(metaEdges) > 0 {
							if err := synthesizeMetaCompletion(
								runID, workflow, runContext, runState, logger,
								result.nodeID, MetaDecisionRetryExhausted,
								&ready, arrivedEdges, incomingEdgeCount, joinFired,
							); err != nil {
								fatalErr = fmt.Errorf("_retry_exhausted meta-decision for %s: %w", result.nodeID, err)
							}
							continue
						}
					}
				}

				failCtx := &EvalContext{Status: statusFailed, Resolve: runContext.Resolve}
				failureEdges := matchEdgesExpr(workflow, result.nodeID, failCtx)
				if len(failureEdges) == 0 {
					fatalErr = fmt.Errorf("node %s: %w", result.nodeID, result.err)
					continue
				}
				// Route failure: store synthetic output and follow failure edges
				targets := make([]string, len(failureEdges))
				for i, edge := range failureEdges {
					targets[i] = edge.To
				}
				_ = logger.Append(state.Event{
					Type: "node_failure_routed", RunID: runID, NodeID: result.nodeID,
					Data: map[string]any{keyError: result.err.Error(), "target_node": targets},
				})
				// Preserve existing NodeState.Data (e.g. a ForEach
				// orchestrator's items[]) so downstream recovery edges
				// can still read fields like `node.X.data.items`.
				// Overlay buildFailureContext on top — its fields
				// (node_id, session_id, failure_context, etc.) stay
				// top-level for back-compat with existing edge
				// expressions that read them directly.
				mergedData := map[string]any{}
				var failStatus string
				var failAttempts int
				runState.WithNode(result.nodeID, func(n *state.NodeState) {
					for k, v := range n.Data {
						mergedData[k] = v
					}
					failStatus = n.Status
					failAttempts = n.Attempts
				})
				for k, v := range buildFailureContext(runState, result.nodeID, engine.RunsDir) {
					mergedData[k] = v
				}
				syntheticOutput := NodeOutput{
					Decision: "",
					Message:  result.err.Error(),
					Data:     mergedData,
					Status:   failStatus,
					Attempts: failAttempts,
				}
				runContext.Outputs[result.nodeID] = syntheticOutput
				for _, edge := range failureEdges {
					edgeIdx := findEdgeIndex(workflow, edge)
					if err := routeEdge(workflow, runContext, runState, result.nodeID, edgeIdx, edge, &ready, arrivedEdges, incomingEdgeCount, joinFired); err != nil {
						fatalErr = fmt.Errorf("failure-edge passes for %s -> %s: %w", result.nodeID, edge.To, err)
					}
				}
				continue
			}
			succeeded++
			if err := applyOutput(workflow, runContext, runState, result.nodeID, result.output, &lastOutput, &ready, arrivedEdges, incomingEdgeCount, joinFired, logger, runID); err != nil {
				fatalErr = fmt.Errorf("node %s output routing: %w", result.nodeID, err)
			}
		}

		waveEnd := time.Now()
		_ = logger.Append(state.Event{
			Type: "wave_completed", RunID: runID, DurationMs: durationMs(waveStart, waveEnd),
			Data: map[string]any{keyNodeCount: len(runnable), outcomeSucceeded: succeeded, statusFailed: failed},
		})

		if fatalErr != nil {
			// Persist before returning so a single-item genuine failure
			// (where the only mutations are markNodeFailed in-memory)
			// survives a crash before the outer caller's next checkpoint.
			// SaveState is best-effort here — we always return fatalErr
			// regardless of save success.
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			return lastOutput, fatalErr
		}

		// Persist arrivedEdges to runState.JoinState
		for joinID, arrived := range arrivedEdges {
			runState.SetJoinState(joinID, state.SortedKeys(arrived))
		}

		if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
			return lastOutput, err
		}

		// Check goal gates when no more ready nodes remain
		if len(ready) == 0 {
			retryTargets, gateErr := checkGoalGates(workflow, runState, runID, logger)
			if gateErr != nil {
				return lastOutput, gateErr
			}
			if len(retryTargets) > 0 {
				// Reset arrivedEdges and joinFired for all join nodes reachable from retry targets
				resetJoinsForRetrigger(retryTargets, workflow, arrivedEdges, joinFired)
			}
			ready = retryTargets
		}

		// Check for join deadlocks when no ready nodes remain (after goal gate checks)
		if len(ready) == 0 {
			if err := checkJoinDeadlocks(workflow, runState, arrivedEdges, incomingEdgeCount); err != nil {
				return lastOutput, err
			}
		}
	}

	return lastOutput, nil
}

// checkGoalGates verifies all goal gate nodes have completed. Returns retry
// target nodes if any gates are unsatisfied, or an error if no retry target
// is available.
func checkGoalGates(workflow *definitions.Workflow, runState *state.RunState, runID string, logger *state.Logger) ([]readyNode, error) {
	var retryTargets []readyNode
	seen := map[string]bool{}
	for _, node := range workflow.Nodes {
		if !node.GoalGate {
			continue
		}
		status, exists := runState.NodeStatus(node.ID)
		if exists && status == statusCompleted {
			_ = logger.Append(state.Event{
				Type: "goal_gate_satisfied", RunID: runID, NodeID: node.ID,
			})
			continue
		}
		target := node.RetryTarget
		if target == "" {
			target = workflow.RetryTarget
		}
		if target == "" {
			return nil, fmt.Errorf("goal gate unsatisfied: %s", node.ID)
		}
		_ = logger.Append(state.Event{
			Type: "goal_gate_unsatisfied", RunID: runID, NodeID: node.ID,
			Data: map[string]any{"retry_target": target},
		})
		if !seen[target] {
			retryTargets = append(retryTargets, readyNode{ID: target, EdgeIndex: -1})
			seen[target] = true
		}
	}
	return retryTargets, nil
}

func applyOutput(workflow *definitions.Workflow, runContext *RunContext, runState *state.RunState, nodeID string, output NodeOutput, lastOutput *NodeOutput, ready *[]readyNode, arrivedEdges map[string]map[string]bool, incomingEdgeCount map[string]int, joinFired map[string]bool, logger *state.Logger, runID string) error {
	runContext.Outputs[nodeID] = output
	*lastOutput = output
	return applyOutputRouting(workflow, runContext, runState, nodeID, output.Decision, ready, arrivedEdges, incomingEdgeCount, joinFired, logger, runID)
}

// joinIncomingEdgeCount returns the number of incoming edges for each join: all node.
func joinIncomingEdgeCount(workflow *definitions.Workflow) map[string]int {
	counts := map[string]int{}
	for _, node := range workflow.Nodes {
		if node.Join == joinAll {
			for _, edge := range workflow.Edges {
				if edge.To == node.ID {
					counts[node.ID]++
				}
			}
		}
	}
	return counts
}

// resetJoinsForRetrigger resets arrivedEdges and joinFired for all join nodes
// reachable from the retry targets via BFS over workflow edges.
func resetJoinsForRetrigger(retryTargets []readyNode, workflow *definitions.Workflow, arrivedEdges map[string]map[string]bool, joinFired map[string]bool) {
	reachable := map[string]bool{}
	queue := make([]string, len(retryTargets))
	for i, rt := range retryTargets {
		queue[i] = rt.ID
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if reachable[cur] {
			continue
		}
		reachable[cur] = true
		for _, edge := range workflow.Edges {
			if edge.From == cur && !reachable[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}
	for nodeID := range reachable {
		delete(arrivedEdges, nodeID)
		delete(joinFired, nodeID)
	}
}

// checkJoinDeadlocks detects stalled join nodes after goal gate checks.
// A join is stalled when some predecessors will never arrive because they
// are dead (failed/completed without routing to the join) and cannot be
// reached from any alive node.
func checkJoinDeadlocks(workflow *definitions.Workflow, runState *state.RunState, arrivedEdges map[string]map[string]bool, incomingEdgeCount map[string]int) error {
	for _, node := range workflow.Nodes {
		if node.Join != joinAll {
			continue
		}
		arrived := arrivedEdges[node.ID]
		arrivedCount := len(arrived)
		if arrivedCount == 0 || arrivedCount >= incomingEdgeCount[node.ID] {
			continue // either not started or already complete
		}

		// Find missing predecessors
		var missing []string
		for _, edge := range workflow.Edges {
			if edge.To == node.ID && !arrived[edge.From] {
				missing = append(missing, edge.From)
			}
		}

		allDead := true
		for _, predID := range missing {
			if isAlive(predID, runState) {
				allDead = false
				break
			}
			if canBeReachedFromAlive(predID, workflow, runState) {
				allDead = false
				break
			}
			predNode := definitions.FindNode(workflow, predID)
			if predNode != nil && predNode.Join == joinAll {
				visited := map[string]bool{}
				if !isTransitivelyDead(predID, workflow, runState, arrivedEdges, incomingEdgeCount, visited) {
					allDead = false
					break
				}
			}
		}

		if allDead {
			arrivedList := state.SortedKeys(arrived)
			return fmt.Errorf("join node %q stalled: received from [%s], missing [%s]",
				node.ID, strings.Join(arrivedList, ", "), strings.Join(missing, ", "))
		}
	}
	return nil
}

// isAlive returns true if the node is alive (running, retrying, awaiting approval,
// pending, or not yet in RunState).
func isAlive(nodeID string, runState *state.RunState) bool {
	status, exists := runState.NodeStatus(nodeID)
	if !exists {
		return true // not in state = hasn't run yet = alive
	}
	switch status {
	case statusRunning, statusRetrying, statusAwaitingApproval, statusPending:
		return true
	}
	return false
}

// canBeReachedFromAlive returns true if the target node can be reached from
// any alive node via BFS over workflow edges. Expression edges are
// conservatively assumed reachable.
func canBeReachedFromAlive(target string, workflow *definitions.Workflow, runState *state.RunState) bool {
	// Collect alive nodes
	var alive []string
	for _, node := range workflow.Nodes {
		if isAlive(node.ID, runState) {
			alive = append(alive, node.ID)
		}
	}

	// BFS from alive nodes
	reachable := map[string]bool{}
	queue := alive
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if reachable[cur] {
			continue
		}
		reachable[cur] = true
		if cur == target {
			return true
		}
		for _, edge := range workflow.Edges {
			if edge.From == cur && !reachable[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}
	return false
}

// isTransitivelyDead returns true if a join node is transitively dead:
// all its missing predecessors are themselves dead (or transitively dead joins).
func isTransitivelyDead(nodeID string, workflow *definitions.Workflow, runState *state.RunState, arrivedEdges map[string]map[string]bool, incomingEdgeCount map[string]int, visited map[string]bool) bool {
	if visited[nodeID] {
		return true // cycle = dead
	}
	visited[nodeID] = true

	node := definitions.FindNode(workflow, nodeID)
	if node == nil || node.Join != joinAll {
		return !isAlive(nodeID, runState) && !canBeReachedFromAlive(nodeID, workflow, runState)
	}

	// It's a join node — check its missing predecessors
	arrived := arrivedEdges[nodeID]
	for _, edge := range workflow.Edges {
		if edge.To == nodeID && !arrived[edge.From] {
			if !isTransitivelyDead(edge.From, workflow, runState, arrivedEdges, incomingEdgeCount, visited) {
				return false
			}
		}
	}
	return true
}

// getAndIncrementLoopIterations atomically increments the per-dispatch loop
// counter on NodeState.LoopIterations and reports whether the new count
// exceeds the workflow's max_loop_iterations limit. Returns the new count
// (always >= 1).
//
// Unlike the old in-memory executions map, this counter survives crash-resume
// because it is written to the persisted NodeState.
func getAndIncrementLoopIterations(runState *state.RunState, nodeID string, maxLimit int) (newCount int, exhausted bool) {
	runState.WithNode(nodeID, func(n *state.NodeState) {
		// Lazy reset: if a prior dispatch ended via _loop_exhausted
		// meta-decision and we're being re-dispatched (e.g., the
		// meta-decision edge target eventually loops back here), the
		// counter must restart at 1, not continue from N. Clear the
		// routing decision atomically.
		if n.LastRoutingDecision == MetaDecisionLoopExhausted {
			n.LoopIterations = 0
			n.LastRoutingDecision = ""
			n.LastRoutingAt = nil
		}
		n.LoopIterations++
		newCount = n.LoopIterations
		exhausted = maxLimit > 0 && newCount > maxLimit
	})
	return
}

// loopExhaustedError builds an enriched error for fatal loop exhaustion,
// including the node's last decision and a truncated message (max 200 chars).
func loopExhaustedError(nodeID, decision, message string) error {
	truncated := message
	if len(truncated) > 200 {
		truncated = truncated[:200] + "..."
	}
	return fmt.Errorf("max loop iterations exceeded for %s (decision=%s): %s",
		nodeID, decision, truncated)
}

func cancelRunningNodes(runState *state.RunState) {
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, node := range nodes {
			if node.Status == statusRunning {
				node.Status = statusCancelled
			}
		}
	})
}

func (engine *Engine) executeWave(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, nodes []readyNode, runContext *RunContext, logger *state.Logger, runState *state.RunState) []nodeResult {
	results := make(chan nodeResult, len(nodes))
	for _, nodeInfo := range nodes {
		go func() {
			node := definitions.FindNode(workflow, nodeInfo.ID)
			if node == nil {
				results <- nodeResult{nodeID: nodeInfo.ID, err: fmt.Errorf("node not found: %s", nodeInfo.ID)}
				return
			}
			output, err := engine.executeNode(ctx, runID, runDir, workflow, node, nodeInfo.EdgePrompt, nodeInfo.FromNodeID, nodeInfo.Passes, runContext, logger, runState)
			results <- nodeResult{nodeID: node.ID, output: output, err: err}
		}()
	}

	collected := make([]nodeResult, 0, len(nodes))
	for i := 0; i < len(nodes); i++ {
		collected = append(collected, <-results)
	}
	return collected
}

// dedupReadyQueue collapses duplicate node entries from fan-in convergence.
// When multiple readyNode entries with the same ID arrive in one tick,
// their Passes maps are merged by edge-index ASC (highest EdgeIndex wins on
// overlapping keys; non-overlapping keys from both are preserved).
// The first entry's EdgePrompt is kept; a dedup_dropped event is logged if
// a later entry carries a different EdgePrompt.
func dedupReadyQueue(ready []readyNode, logger *state.Logger, runID string) []readyNode {
	seen := map[string]int{} // ID -> index in deduped slice
	keptPrompt := map[string]string{}
	var deduped []readyNode
	for _, rn := range ready {
		if idx, ok := seen[rn.ID]; ok {
			existing := &deduped[idx]
			if rn.EdgeIndex > existing.EdgeIndex {
				// Higher-index edge wins on overlap; merge existing underneath.
				merged := make(map[string]any, len(rn.Passes)+len(existing.Passes))
				for k, v := range existing.Passes {
					merged[k] = v
				}
				for k, v := range rn.Passes {
					merged[k] = v
				}
				existing.Passes = merged
				existing.EdgeIndex = rn.EdgeIndex
			} else {
				// Existing wins on collision; add only new (non-overlapping) keys.
				if existing.Passes == nil && len(rn.Passes) > 0 {
					existing.Passes = make(map[string]any, len(rn.Passes))
				}
				for k, v := range rn.Passes {
					if _, present := existing.Passes[k]; !present {
						existing.Passes[k] = v
					}
				}
			}
			if rn.EdgePrompt != keptPrompt[rn.ID] {
				_ = logger.Append(state.Event{
					Type: "dedup_dropped", RunID: runID, NodeID: rn.ID,
					Data: map[string]any{
						"kept_prompt":    keptPrompt[rn.ID],
						"dropped_prompt": rn.EdgePrompt,
					},
				})
			}
			continue
		}
		seen[rn.ID] = len(deduped)
		keptPrompt[rn.ID] = rn.EdgePrompt
		deduped = append(deduped, rn)
	}
	return deduped
}
