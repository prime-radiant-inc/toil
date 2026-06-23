package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

type forEachItemState struct {
	ID             string
	ExpandedID     string
	Status         string // outcomeSucceeded, outcomeFailedHandled, outcomeFailed, statusSkipped
	Output         NodeOutput
	Err            error
	FailureContext map[string]any
	SkipReason     string
	// OriginatingFailure carries a back-reference for skipped items
	// whose skip was caused by a sibling's failure: the failed item's
	// id plus a one-line summary of its failure_context. Without this,
	// a "1 failed, 3 skipped" outcome forces a debugger to traverse
	// into the failed item's child run to find the root cause.
	// Nil for cancellation skips and for unhandled-failure cascades
	// upstream that have no failure_context yet. (PRI-1576)
	OriginatingFailure map[string]any
}

func (s *forEachItemState) toMap() map[string]any {
	m := map[string]any{
		"id":          s.ID,
		"expanded_id": s.ExpandedID,
		fieldStatus:   s.Status,
	}
	if s.Output.Decision != "" {
		m[fieldDecision] = s.Output.Decision
	}
	if s.Output.Message != "" {
		m["message"] = s.Output.Message
	}
	if len(s.Output.Data) > 0 {
		m["data"] = s.Output.Data
	}
	if len(s.Output.Artifacts) > 0 {
		m["artifacts"] = s.Output.Artifacts
	}
	if s.FailureContext != nil {
		m["failure_context"] = s.FailureContext
	}
	if s.SkipReason != "" {
		m["reason"] = s.SkipReason
	}
	if s.OriginatingFailure != nil {
		m["originating_failure"] = s.OriginatingFailure
	}
	return m
}

// summarizeForOriginatingFailure builds a compact failure pointer for
// downstream skipped items. Pulls the diagnostic-quality bits from a
// full failure_context (which can be large — decision_history, nested
// child summaries) into a single one-line summary. Returns nil if the
// failure_context is empty (e.g. a cancel cascade with no underlying
// failure to point at). (PRI-1576)
func summarizeForOriginatingFailure(itemID string, fc map[string]any) map[string]any {
	if fc == nil {
		return nil
	}
	out := map[string]any{"id": itemID}
	if s, ok := fc["error"].(string); ok && s != "" {
		out["error"] = truncateForSummary(s, 200)
	}
	if s, ok := fc["last_message"].(string); ok && s != "" {
		out["last_message"] = truncateForSummary(s, 200)
	}
	if s, ok := fc["last_decision"].(string); ok && s != "" {
		out["last_decision"] = s
	}
	if s, ok := fc["session_id"].(string); ok && s != "" {
		out["session_id"] = s
	}
	if len(out) == 1 { // only "id" — nothing useful to point at
		return nil
	}
	return out
}

func truncateForSummary(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// itemIDsForItems returns a per-item id string. For items that expose an "id"
// field (DAG mode) the id is used; otherwise it's the index as a string.
func itemIDsForItems(items []any) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = fmt.Sprintf("%d", i)
		if m, ok := item.(map[string]any); ok {
			if v, ok := m["id"].(string); ok && v != "" {
				out[i] = v
				continue
			}
		}
	}
	return out
}

// buildDependentsMap inverts the depends_on relation: for each item index,
// returns the list of indices that depend on it (transitively computed by
// the caller via walking the DAG).
func buildDependentsMap(items []dagItem) map[int][]int {
	idToIndex := map[string]int{}
	for _, it := range items {
		idToIndex[it.id] = it.index
	}
	out := map[int][]int{}
	for _, it := range items {
		for _, dep := range it.deps {
			from := idToIndex[dep]
			out[from] = append(out[from], it.index)
		}
	}
	return out
}

// isTransientErr reports whether err is one of the engine's transient
// "child still running" sentinel errors. Used by per-item collection loops
// to decide whether a higher-priority error (cancellation, genuine failure)
// should overwrite firstUnhandledErr.
func isTransientErr(err error) bool {
	return errors.Is(err, ErrApprovalPending) || errors.Is(err, ErrSubworkflowInProgress)
}

// markSkipped transitively marks all dependents of the given index as skipped
// with a reason referencing the failed item's id. Mirrors the skip into the
// expanded NodeState so persisted run state shows skipped items as skipped
// (not stuck in "pending"), emits a node_skipped event per marked child,
// and flushes state.json once at the end so a crash mid-ForEach preserves
// the terminal status.
//
// runDir/runID/logger may be zero/nil (tests without a logger context); in
// that case no events are emitted and no disk flush happens.
func markSkipped(states []forEachItemState, deps map[int][]int, failedIdx int, itemIDs []string, runState *state.RunState, expandedIDs []string, runDir string, runID string, logger *state.Logger, reason string, deferFlush bool, originating map[string]any) {
	queue := []int{failedIdx}
	if reason == "" {
		reason = fmt.Sprintf("dependency %s failed", itemIDs[failedIdx])
	}
	// Stamp EndedAt once so all dependents get the same terminal time and
	// timing displays don't leave them looking "still running".
	ended := time.Now().UTC()
	anyMutation := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range deps[cur] {
			if states[child].Status != "" {
				continue
			}
			// Compute the immediate-parent reason: if the parent of this
			// child has its own SkipReason (e.g. it was already marked by
			// a prior cancel), use "dependency <parent> cancelled/failed";
			// otherwise default to the root-level `reason`. This makes
			// transitive chains (A→B→C) point to the immediate upstream.
			childReason := reason
			if cur != failedIdx && states[cur].SkipReason != "" {
				// states[cur] was set by an earlier markSkipped pass — its
				// SkipReason mentions ITS upstream. For the child's reason,
				// label the immediate parent (cur), which makes the chain
				// traceable: walk SkipReason→parent ID→its SkipReason.
				if strings.Contains(reason, "cancelled") {
					childReason = fmt.Sprintf("dependency %s cancelled", itemIDs[cur])
				} else {
					childReason = fmt.Sprintf("dependency %s failed", itemIDs[cur])
				}
			}
			states[child].Status = statusSkipped
			states[child].SkipReason = childReason
			if originating != nil {
				states[child].OriginatingFailure = originating
			}
			if runState != nil && child < len(expandedIDs) {
				rsn := childReason
				runState.WithNode(expandedIDs[child], func(n *state.NodeState) {
					n.Status = statusSkipped
					n.Message = rsn
					n.EndedAt = &ended
				})
				if logger != nil {
					_ = logger.Append(state.Event{
						Type:   eventNodeSkipped,
						RunID:  runID,
						NodeID: expandedIDs[child],
						Data:   map[string]any{keyReason: rsn},
					})
				}
				anyMutation = true
			}
			queue = append(queue, child)
		}
	}
	if anyMutation && !deferFlush && runState != nil && runDir != "" {
		_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
	}
}

// forEachDecision returns the aggregate decision based on item statuses.
// Skipped items never count toward "all".
func forEachDecision(items []forEachItemState) string {
	if len(items) == 0 {
		return decisionAllSucceeded
	}
	succeeded := 0
	failed := 0
	for _, it := range items {
		switch it.Status {
		case outcomeSucceeded:
			succeeded++
		case outcomeFailedHandled, outcomeFailed:
			failed++
		}
	}
	if failed == 0 {
		return decisionAllSucceeded
	}
	if succeeded == 0 {
		return decisionAllFailed
	}
	return decisionSomeFailed
}

// forEachMessage returns a short summary of item counts by status.
// Items with empty Status are transient waits (still in flight on
// approval / subworkflow completion) — counted as "pending" so the
// summary doesn't undercount the total.
func forEachMessage(items []forEachItemState) string {
	var s, f, k, p int
	for _, it := range items {
		switch it.Status {
		case outcomeSucceeded:
			s++
		case outcomeFailedHandled, outcomeFailed:
			f++
		case statusSkipped:
			k++
		case "":
			p++
		}
	}
	if p > 0 {
		return fmt.Sprintf("%d succeeded, %d failed, %d skipped, %d pending", s, f, k, p)
	}
	return fmt.Sprintf("%d succeeded, %d failed, %d skipped", s, f, k)
}

// hasTemplateFailureEdge reports whether the workflow declares an EXPLICIT
// failure edge from the template node — an edge whose `when:` is an
// expression that evaluates true under a failed status. A default/empty
// `when:` edge does NOT count; without this restriction, any default routing
// edge from the template would silently absorb failures.
//
// runContext may be nil; when present, it enables resolution of expressions
// that reference node outputs (e.g. "status == 'failed' && node.X.data.Y").
// This keeps the validator/runtime evaluation paths consistent.
//
// Limitation: expressions referencing node outputs not yet in
// runContext.Outputs (e.g. peer/downstream nodes) resolve to nil and the
// expression evaluates to false. In practice upstream nodes complete before
// the orchestrator runs, so this is rarely hit; authors should avoid
// conjuncts against peer/downstream nodes in failure edges.
//
// A broken expression (tokenize/parse error) returns an error from
// EvalEdgeExpr and is treated as NOT a failure edge — the genuine failure
// then propagates up as unhandled rather than being silently absorbed.
// Runtime edge routing uses the same err == nil && ok rule, so the two
// paths stay consistent.
func hasTemplateFailureEdge(workflow *definitions.Workflow, templateID string, runContext *RunContext) bool {
	evalCtx := &EvalContext{Status: statusFailed}
	if runContext != nil {
		evalCtx.Resolve = runContext.Resolve
	}
	for _, edge := range workflow.Edges {
		if edge.From != templateID {
			continue
		}
		if !IsExpression(edge.When) {
			continue
		}
		ok, err := EvalEdgeExpr(edge.When, evalCtx)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func (engine *Engine) executeNode(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, edgePrompt string, fromNodeID string, edgePasses map[string]any, runContext *RunContext, logger *state.Logger, runState *state.RunState) (NodeOutput, error) {
	if node.ForEach != nil {
		return engine.executeForEach(ctx, runID, runDir, workflow, node, edgePrompt, fromNodeID, runContext, logger, runState)
	}
	return engine.executeSingle(ctx, runID, runDir, workflow, node, edgePrompt, edgePasses, runContext, logger, runState, nil, "")
}

func (engine *Engine) executeForEach(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, edgePrompt string, fromNodeID string, runContext *RunContext, logger *state.Logger, runState *state.RunState) (NodeOutput, error) {
	startTime := time.Now().UTC()

	listValue, err := runContext.Resolve(node.ForEach.List)
	if err != nil {
		return NodeOutput{}, err
	}
	items, err := toSlice(listValue)
	if err != nil {
		return NodeOutput{}, err
	}

	// Resolve the per-item template. Body is required (enforced by validation).
	template := definitions.FindNode(workflow, node.ForEach.Body)
	if template == nil {
		return NodeOutput{}, fmt.Errorf("for_each body %q (on orchestrator %q) not found", node.ForEach.Body, node.ID)
	}
	expandedPrefix := template.ID

	// Detect re-execution vs resume-after-crash:
	// - Re-execution: orchestrator's prior Status is terminal (completed or
	//   failed) — an upstream edge or retrigger fired it again. Expanded
	//   items from the prior pass hold stale outputs and must be cleaned.
	// - Resume-after-crash: orchestrator's prior Status == statusRunning
	//   and StartedAt is already set. Preserve StartedAt so duration
	//   reporting reflects total elapsed, not just the latest segment.
	var (
		reexecuting bool
		firstEntry  bool
	)
	runState.WithNode(node.ID, func(n *state.NodeState) {
		switch n.Status {
		case statusCompleted, statusFailed, statusRetrying:
			reexecuting = true
		}
		if n.StartedAt == nil || reexecuting {
			// Capture the address of the local startTime for the heap-escape
			// analysis; n.StartedAt retains a pointer to the heap-allocated
			// value even after this closure returns.
			n.StartedAt = &startTime
			firstEntry = true
		} else {
			// Preserve the original StartedAt so duration math is cumulative
			// across resume.
			startTime = *n.StartedAt
		}
		n.Status = statusRunning
		n.EndedAt = nil
		// On re-execution, clear prior-pass terminal-state fields so the
		// intermediate "running" state on disk isn't paired with stale
		// Decision/Data/Artifacts/Error/Message. The orphan-cleanup
		// SaveState below would otherwise capture status=running with
		// e.g. Message="3 succeeded, 0 failed, 0 skipped" from the prior
		// pass; a crash before the next state mutation would leave that
		// inconsistency on disk.
		if reexecuting {
			n.Decision = ""
			n.Data = nil
			n.Artifacts = nil
			n.Error = ""
			n.Message = ""
		}
	})
	if firstEntry {
		_ = logger.Append(state.Event{Type: eventNodeStarted, RunID: runID, NodeID: node.ID})
	}

	// Clean up orphaned expanded NodeStates BEFORE the empty-list check and
	// BEFORE registering new expansions.
	//
	// Scenario: a prior pass (or a retrigger, or a downstream-reset) left
	// template::N entries in state for indices that aren't in the current
	// items list (smaller or empty list this time). Delete them so they
	// don't pollute state.json, interview candidates, or inspect output.
	//
	// Run whenever ANY expanded NodeState under this template prefix exists
	// today — that's a reliable signal that a prior pass happened, even if
	// the orchestrator's own status is currently statusPending (as it is
	// after retrigger's resetNodeState on a downstream orchestrator).
	orphanPrefix := expandedPrefix + "::"
	orphansDeleted := false
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id := range nodes {
			if !strings.HasPrefix(id, orphanPrefix) {
				continue
			}
			idxStr := id[len(orphanPrefix):]
			idx, convErr := strconv.Atoi(idxStr)
			// Unparseable suffix or negative index can't have come from
			// our expansion logic (which always uses non-negative ints).
			// Treat them as orphans — they're either stale state from an
			// older engine version or manual edits, and either way we
			// shouldn't preserve them as live ForEach children.
			if convErr != nil || idx < 0 || idx >= len(items) {
				delete(nodes, id)
				orphansDeleted = true
			}
		}
	})
	// Persist so a crash between here and the next checkpoint can't
	// resurrect orphans from stale state.json.
	if orphansDeleted && runDir != "" {
		_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
	}

	// Empty list: complete immediately. Use the same direct-write pattern
	// as the non-empty success path below so the node_completed event
	// payload is shape-consistent (no items[] duplication; just a
	// decision/message/item_count summary in the event).
	if len(items) == 0 {
		// Honor prior cancellation: if the caller's context is already done,
		// don't record the orchestrator as completed with decision=all_succeeded
		// when operator intent was cancellation.
		if err := ctx.Err(); err != nil {
			ended := time.Now().UTC()
			// Use statusSkipped + node_skipped(reason=cancelled) to match
			// the per-item cancellation convention elsewhere in this file
			// — there's no node_cancelled event type, and pairing a
			// statusCancelled NodeState with a node_skipped event would
			// leave the two stores out of sync.
			//
			// Decision/Data/Artifacts/Error/Message were already cleared
			// by the reexecuting block above (when reexecuting=true). For
			// first-entry the orchestrator NodeState had nothing to begin
			// with. Either way, no extra clearing needed here.
			runState.WithNode(node.ID, func(n *state.NodeState) {
				n.Status = statusSkipped
				n.Message = reasonCancelled
				n.EndedAt = &ended
			})
			_ = logger.Append(state.Event{
				Type: eventNodeSkipped, RunID: runID, NodeID: node.ID,
				DurationMs: durationMs(startTime, ended),
				Data:       map[string]any{keyReason: reasonCancelled},
			})
			// Persist immediately so a crash before the run loop's next
			// flush doesn't lose the skipped status.
			if runDir != "" {
				_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
			}
			return NodeOutput{}, err
		}
		emptyOutput := NodeOutput{
			Decision: decisionAllSucceeded,
			Message:  "0 succeeded, 0 failed, 0 skipped",
			Data:     map[string]any{keyItems: []map[string]any{}},
		}
		ended := time.Now().UTC()
		runState.WithNode(node.ID, func(n *state.NodeState) {
			n.Status = statusCompleted
			n.Decision = emptyOutput.Decision
			n.Message = emptyOutput.Message
			n.Data = emptyOutput.Data
			n.EndedAt = &ended
		})
		_ = logger.Append(state.Event{
			Type: eventNodeCompleted, RunID: runID, NodeID: node.ID,
			DurationMs: durationMs(startTime, ended),
			Data: map[string]any{
				fieldDecision: emptyOutput.Decision,
				fieldMessage:  emptyOutput.Message,
				"item_count":  0,
			},
		})
		return emptyOutput, nil
	}

	// Register expanded nodes under the template prefix. On re-execution,
	// clear stale state so prior outputs aren't reused for potentially
	// different items.
	expandedIDs := make([]string, len(items))
	for i := range items {
		expandedIDs[i] = fmt.Sprintf("%s::%d", expandedPrefix, i)
		runState.WithNode(expandedIDs[i], func(n *state.NodeState) {
			if reexecuting {
				n.Status = statusPending
				n.Decision = ""
				n.Message = ""
				n.Data = nil
				n.Artifacts = nil
				n.Error = ""
				n.EndedAt = nil
				n.StartedAt = nil
				n.Attempts = 0
				n.NoProgressCount = 0
				// Keep SessionID: shell/role runners may reuse for context.
				// Keep LastDispatchHash/LastOutputHash: dispatch dedup.
				return
			}
			if n.Status == "" {
				n.Status = statusPending
			}
		})
	}

	var outputs []forEachItemState
	if node.ForEach.DependsOn != "" {
		outputs, err = engine.executeForEachDAG(ctx, runID, runDir, workflow, node, template, edgePrompt, fromNodeID, runContext, logger, runState, items, expandedIDs)
	} else {
		outputs, err = engine.executeForEachConcurrent(ctx, runID, runDir, workflow, node, template, edgePrompt, fromNodeID, runContext, logger, runState, items, expandedIDs)
	}
	if err != nil {
		// Don't mark the ForEach parent as failed for transient waits.
		// ErrSubworkflowInProgress and ErrApprovalPending mean a child
		// is still running — the parent should stay "running" so that
		// resume doesn't re-dispatch all ForEach items.
		if !errors.Is(err, ErrSubworkflowInProgress) && !errors.Is(err, ErrApprovalPending) {
			// Build items[] from the partial results so the orchestrator's
			// NodeState.Data surfaces per-item failure_context for any
			// recovery edge (and for resume consistency).
			var itemMaps []map[string]any
			var msg string
			if outputs != nil {
				itemMaps = make([]map[string]any, len(outputs))
				for i, st := range outputs {
					itemMaps[i] = st.toMap()
				}
				msg = forEachMessage(outputs)
			}
			ended := time.Now().UTC()
			runState.WithNode(node.ID, func(n *state.NodeState) {
				n.Status = statusFailed
				n.EndedAt = &ended
				if msg != "" {
					n.Message = msg
				}
				if itemMaps != nil {
					n.Data = map[string]any{keyItems: itemMaps}
				}
			})
			_ = logger.Append(state.Event{
				Type: eventNodeFailed, RunID: runID, NodeID: node.ID,
				DurationMs: durationMs(startTime, ended),
				Data:       map[string]any{keyError: err.Error()},
			})
		}
		return NodeOutput{}, err
	}

	decision := forEachDecision(outputs)
	message := forEachMessage(outputs)

	itemMaps := make([]map[string]any, len(outputs))
	for i, st := range outputs {
		itemMaps[i] = st.toMap()
	}

	finalOutput := NodeOutput{
		Decision: decision,
		Message:  message,
		Data:     map[string]any{keyItems: itemMaps},
	}
	// Write NodeState directly so state.json carries the full items[]
	// (for resume and downstream node.orch.data.items reads), but emit a
	// compact node_completed event with just the summary. A full items[]
	// in the event log duplicates state.json at every orchestrator
	// completion and bloats events.jsonl unboundedly with per-item counts.
	ended := time.Now().UTC()
	runState.WithNode(node.ID, func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = decision
		n.Message = message
		n.Data = finalOutput.Data
		n.EndedAt = &ended
	})
	_ = logger.Append(state.Event{
		Type: eventNodeCompleted, RunID: runID, NodeID: node.ID,
		DurationMs: durationMs(startTime, ended),
		Data: map[string]any{
			fieldDecision: decision,
			fieldMessage:  message,
			"item_count":  len(itemMaps),
		},
	})
	return finalOutput, nil
}

// indexedResult carries a per-item ForEach execution result. For freshly
// executed items it holds the NodeOutput and any error. For items resumed
// from persisted state it carries the reconstructed forEachItemState so
// the collector can adopt it directly without re-classifying.
type indexedResult struct {
	index   int
	output  NodeOutput
	err     error
	resumed *forEachItemState
}

func indexedResumedResult(index int, st forEachItemState) indexedResult {
	return indexedResult{index: index, resumed: &st}
}

// resumeItemFromState reconstructs a forEachItemState from persisted
// NodeState when the item is in a settled status. Returns (state, true)
// for succeeded (statusCompleted) or failed-handled items, (zero, false)
// otherwise (caller should run the item fresh).
func resumeItemFromState(runState *state.RunState, expandedID string, itemID string, status string) (forEachItemState, bool) {
	switch status {
	case statusCompleted:
		var st forEachItemState
		runState.WithNode(expandedID, func(n *state.NodeState) {
			st = forEachItemState{
				ID:         itemID,
				ExpandedID: expandedID,
				Status:     outcomeSucceeded,
				Output: NodeOutput{
					Decision:  n.Decision,
					Message:   n.Message,
					Artifacts: n.Artifacts,
					Data:      n.Data,
				},
			}
		})
		return st, true
	case statusFailedHandled:
		var st forEachItemState
		runState.WithNode(expandedID, func(n *state.NodeState) {
			// Surface failure_context as a top-level field and strip it from
			// Output.Data so serialized items[] doesn't carry it in two
			// places. Matches the freshly-classified shape where only the
			// top-level FailureContext is populated.
			var fc map[string]any
			data := n.Data
			if n.Data != nil {
				if stored, ok := n.Data["failure_context"].(map[string]any); ok {
					fc = stored
					dataCopy := make(map[string]any, len(n.Data)-1)
					for k, v := range n.Data {
						if k == "failure_context" {
							continue
						}
						dataCopy[k] = v
					}
					if len(dataCopy) == 0 {
						data = nil
					} else {
						data = dataCopy
					}
				}
			}
			st = forEachItemState{
				ID:         itemID,
				ExpandedID: expandedID,
				Status:     outcomeFailedHandled,
				Output: NodeOutput{
					Decision:  n.Decision,
					Message:   n.Message,
					Artifacts: n.Artifacts,
					Data:      data,
				},
				FailureContext: fc,
			}
		})
		return st, true
	case statusSkipped:
		// Skipped items (DAG dependent whose ancestor failed) persist their
		// skip reason in NodeState.Message. Round-trip them the same way as
		// other terminal states so resume doesn't re-launch them after a
		// crash mid-ForEach.
		var st forEachItemState
		runState.WithNode(expandedID, func(n *state.NodeState) {
			st = forEachItemState{
				ID:         itemID,
				ExpandedID: expandedID,
				Status:     statusSkipped,
				SkipReason: n.Message,
			}
		})
		return st, true
	}
	return forEachItemState{}, false
}

// persistFailedHandled records that an expanded item was absorbed as
// failed-handled. The status distinguishes it from a genuine failure so
// resume can short-circuit, and the failure_context is preserved under
// NodeState.Data so the aggregate items[] survives persistence. State is
// flushed to disk immediately so a crash mid-ForEach doesn't lose the
// distinction between genuine failure and absorbed failure.
//
// EndedAt is set since failed-handled is a terminal state — otherwise
// duration reporting would be incomplete for absorbed items.
func persistFailedHandled(runState *state.RunState, expandedID string, failureContext map[string]any, runDir string, runID string, logger *state.Logger) {
	ended := time.Now().UTC()
	runState.WithNode(expandedID, func(n *state.NodeState) {
		n.Status = statusFailedHandled
		n.EndedAt = &ended
		if n.Data == nil {
			n.Data = map[string]any{}
		}
		if failureContext != nil {
			n.Data["failure_context"] = failureContext
		}
	})
	if runDir != "" {
		_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
	}
	if logger != nil {
		_ = logger.Append(state.Event{
			Type:   "node_failed_handled",
			RunID:  runID,
			NodeID: expandedID,
			Data:   map[string]any{"failure_context": failureContext},
		})
	}
}

func (engine *Engine) executeForEachConcurrent(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, template *definitions.Node, edgePrompt string, fromNodeID string, runContext *RunContext, logger *state.Logger, runState *state.RunState, items []any, expandedIDs []string) ([]forEachItemState, error) {
	results := make(chan indexedResult, len(items))
	pending := 0
	itemIDs := itemIDsForItems(items)

	for i, item := range items {
		status, exists := runState.NodeStatus(expandedIDs[i])
		if exists {
			// Short-circuit resume: completed and failed-handled are both
			// settled terminal states. Reconstruct the item state from what
			// we persisted.
			if resumed, ok := resumeItemFromState(runState, expandedIDs[i], itemIDs[i], status); ok {
				results <- indexedResumedResult(i, resumed)
				pending++
				continue
			}
		}

		i, item := i, item
		pending++
		go func() {
			extra := map[string]any{
				node.ForEach.Item: item,
			}
			output, err := engine.executeSingle(ctx, runID, runDir, workflow, template, edgePrompt, nil, runContext, logger, runState, extra, expandedIDs[i])
			results <- indexedResult{index: i, output: output, err: err}
		}()
	}

	collected := make([]forEachItemState, len(items))
	var firstUnhandledErr error
	pendingFlush := false
	// Defer the SaveState so it fires even on panic mid-loop. Per-item
	// state mutations to runState happen in the loop body; without this
	// defer a panic between mutation and the end of the loop would leave
	// events.jsonl out of sync with state.json. Set pendingFlush in any
	// branch that mutates runState (cancel today; concurrent path doesn't
	// do markSkipped, so cancel is the only mutator here).
	defer func() {
		if pendingFlush && runDir != "" {
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
		}
	}()
	hasFailureEdge := hasTemplateFailureEdge(workflow, template.ID, runContext)
	var firstGenuineFailure bool // mirrors the DAG path's flag
	for i := 0; i < pending; i++ {
		r := <-results
		// Resumed items already carry their Status/FailureContext in the
		// indexedResult payload via the wrapper below; we only apply fresh
		// classification when this is a newly-run result.
		if r.resumed != nil {
			collected[r.index] = *r.resumed
			continue
		}
		st := forEachItemState{
			ID:         itemIDs[r.index],
			ExpandedID: expandedIDs[r.index],
			Output:     r.output,
			Err:        r.err,
		}
		if r.err != nil {
			// Transient waits (a child subworkflow still running or an
			// approval pending) must propagate unchanged — they aren't
			// failures and must not be absorbed by a failure edge.
			if errors.Is(r.err, ErrSubworkflowInProgress) || errors.Is(r.err, ErrApprovalPending) {
				if firstUnhandledErr == nil {
					firstUnhandledErr = r.err
				}
				collected[r.index] = st
				continue
			}
			switch {
			case errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded):
				// Run-level cancellation (or deadline expiration) propagated
				// into this item's goroutine. Don't treat as a failure to
				// absorb — record as skipped with a cancel reason so
				// state.json and items[] reflect intent. Batch the state
				// flush (single SaveState after the result loop) so 100
				// cancelled items don't trigger 100 full-run serializations.
				ended := time.Now().UTC()
				st.Status = statusSkipped
				st.SkipReason = reasonCancelled
				// Pre-dispatch ctx.Err short-circuit means no kind-handler
				// emitted node_started; synthesize one so the event log has
				// a paired started/terminator.
				var hadStartedAt bool
				runState.WithNode(expandedIDs[r.index], func(n *state.NodeState) {
					hadStartedAt = n.StartedAt != nil
					if !hadStartedAt {
						n.StartedAt = &ended
					}
					n.Status = statusSkipped
					n.Message = reasonCancelled
					n.EndedAt = &ended
				})
				if logger != nil {
					if !hadStartedAt {
						_ = logger.Append(state.Event{
							Type:   eventNodeStarted,
							RunID:  runID,
							NodeID: expandedIDs[r.index],
						})
					}
					_ = logger.Append(state.Event{
						Type:   "node_skipped",
						RunID:  runID,
						NodeID: expandedIDs[r.index],
						Data:   map[string]any{keyReason: reasonCancelled},
					})
				}
				pendingFlush = true
				// Cancellation outranks a prior transient wait — overwrite
				// it so the orchestrator surfaces context.Canceled (not a
				// sibling's ErrApprovalPending) up to executeForEach.
				if firstUnhandledErr == nil || isTransientErr(firstUnhandledErr) {
					firstUnhandledErr = r.err
				}
			case hasFailureEdge:
				st.Status = outcomeFailedHandled
				st.FailureContext = buildFailureContext(runState, expandedIDs[r.index], engine.RunsDir)
				persistFailedHandled(runState, expandedIDs[r.index], st.FailureContext, runDir, runID, logger)
			default:
				st.Status = outcomeFailed
				// Capture per-item failure context even when unhandled, so the
				// orchestrator's recovery edge (if any) can surface it.
				st.FailureContext = buildFailureContext(runState, expandedIDs[r.index], engine.RunsDir)
				// Mirror the DAG fix: a genuine failure must overwrite a
				// prior transient err so the orchestrator surfaces the
				// real failure (not a sibling's wait) up to executeForEach.
				if !firstGenuineFailure {
					firstGenuineFailure = true
					firstUnhandledErr = r.err
				}
			}
		} else {
			st.Status = outcomeSucceeded
		}
		collected[r.index] = st
	}
	return collected, firstUnhandledErr
}

func (engine *Engine) executeForEachDAG(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, template *definitions.Node, edgePrompt string, fromNodeID string, runContext *RunContext, logger *state.Logger, runState *state.RunState, items []any, expandedIDs []string) ([]forEachItemState, error) {
	dagItems, err := parseDAGItems(items, node.ForEach.DependsOn)
	if err != nil {
		return nil, fmt.Errorf("for_each DAG: %w", err)
	}
	d, err := buildDAG(dagItems)
	if err != nil {
		return nil, fmt.Errorf("for_each DAG: %w", err)
	}

	states := make([]forEachItemState, len(items))
	itemIDs := itemIDsForItems(items)
	for i := range items {
		states[i] = forEachItemState{ID: itemIDs[i], ExpandedID: expandedIDs[i]}
	}

	// Build the reverse dependency map early — the resume pre-resolution
	// below needs it to propagate skipped status to dependents of a
	// failed-handled or skipped resumed item.
	dependentsOf := buildDependentsMap(dagItems)

	var (
		firstUnhandledErr   error
		firstGenuineFailure bool // true once a non-transient, non-cancel failure has occurred
		pendingFlush        bool // any branch (cancel/genuine/absorbed) that mutates runState sets this
	)

	// Pre-resolve already-settled items (resume). Status-specific:
	//   - succeeded: d.resolve so dependents can launch
	//   - failed-handled: dependents must be marked skipped, NOT launched
	//     (the item didn't succeed even though its failure was absorbed)
	//   - skipped: dependents also skipped transitively
	// Without this distinction, a crash that left dependents unpersisted
	// would see them re-launched on resume even though their dependency
	// failed or was skipped.
	for i := range dagItems {
		status, exists := runState.NodeStatus(expandedIDs[i])
		if !exists {
			continue
		}
		resumed, ok := resumeItemFromState(runState, expandedIDs[i], itemIDs[i], status)
		if !ok {
			continue
		}
		states[i] = resumed
		switch resumed.Status {
		case outcomeSucceeded:
			d.resolve(i)
		case outcomeFailedHandled, statusSkipped:
			// Don't resolve — dependents gated on success shouldn't
			// launch. Mark transitive dependents skipped with a
			// dependency-flavored reason. deferFlush=true lets the
			// function-level defer batch the disk write.
			reason := ""
			var originating map[string]any
			switch resumed.Status {
			case statusSkipped:
				reason = fmt.Sprintf("dependency %s skipped", itemIDs[i])
			case outcomeFailedHandled:
				originating = summarizeForOriginatingFailure(itemIDs[i], resumed.FailureContext)
			}
			markSkipped(states, dependentsOf, i, itemIDs, runState, expandedIDs, runDir, runID, logger, reason, true, originating)
			pendingFlush = true
		}
	}
	// Register SaveState defer FIRST so it runs LAST in LIFO order —
	// AFTER dagCancel signals in-flight goroutines to stop. The defer
	// then flushes runState once, batching all per-item mutations from
	// the cancel, genuine-failure, and absorbed-failure branches into a
	// single serialization. Best-effort against the panic-mid-loop race:
	// goroutines may still be alive when the defer runs, but dagCancel
	// fires first and the per-mutation runState lock serializes writes.
	// See the matching defer in executeForEachConcurrent.
	defer func() {
		if pendingFlush && runDir != "" {
			_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
		}
	}()

	results := make(chan indexedResult, len(items))
	dagCtx, dagCancel := context.WithCancel(ctx)
	defer dagCancel()

	pending := 0
	launchItem := func(i int) {
		pending++
		idx, itm := i, items[i]
		go func() {
			extra := map[string]any{
				node.ForEach.Item: itm,
			}
			output, err := engine.executeSingle(dagCtx, runID, runDir, workflow, template, edgePrompt, nil, runContext, logger, runState, extra, expandedIDs[idx])
			results <- indexedResult{index: idx, output: output, err: err}
		}()
	}

	for _, i := range d.ready() {
		// Skip items already settled (from resume) in any terminal status.
		if states[i].Status != "" {
			continue
		}
		launchItem(i)
	}

	// dependentsOf was built earlier for resume-time dependent marking.
	hasFailureEdge := hasTemplateFailureEdge(workflow, template.ID, runContext)
	for pending > 0 {
		r := <-results
		pending--
		if r.err != nil {
			// Transient waits (a child subworkflow still running or an
			// approval pending) must propagate unchanged — they aren't
			// failures and must not be absorbed by a failure edge. They
			// must NOT call dagCancel either: sibling items should be
			// allowed to finish their own transient-error checks so each
			// can accumulate independently (a sibling waiting on its own
			// approval isn't "cancelled because of item A's wait").
			if errors.Is(r.err, ErrSubworkflowInProgress) || errors.Is(r.err, ErrApprovalPending) {
				if firstUnhandledErr == nil {
					firstUnhandledErr = r.err
				}
				states[r.index].Err = r.err
				continue
			}
			// Run-level cancellation (ctx.Done) or deadline-expiration takes
			// priority over the failure-edge branch: even if the template
			// declares a failure edge, we must NOT record cancelled items as
			// absorbed failures. State.json and items[] should reflect
			// operator intent (cancelled), not a synthetic absorbed-failure.
			if errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded) {
				// Idempotency guard: if a prior markSkipped already set
				// this item's status (it was an unlaunched dependent of a
				// previously-failed item), don't re-mark or re-emit
				// node_skipped — that would clobber the more-informative
				// "dependency X failed" reason with plain "cancelled".
				if states[r.index].Status != "" {
					continue
				}
				reason := reasonCancelled
				// Only say "after sibling failed" when a sibling genuinely
				// failed — a transient sibling waiting on approval didn't
				// "fail" and mustn't mislabel the cancellation cause.
				if firstGenuineFailure {
					reason = "cancelled after sibling failed"
				}
				ended := time.Now().UTC()
				states[r.index].Status = statusSkipped
				states[r.index].SkipReason = reason
				states[r.index].Err = r.err
				// Pre-dispatch ctx.Err short-circuits executeSingle before
				// any kind-handler runs, so no node_started was emitted.
				// Detect that case and synthesize a node_started so event
				// consumers always see a started/terminator pair.
				var hadStartedAt bool
				runState.WithNode(expandedIDs[r.index], func(n *state.NodeState) {
					hadStartedAt = n.StartedAt != nil
					if !hadStartedAt {
						n.StartedAt = &ended
					}
					n.Status = statusSkipped
					n.Message = reason
					n.EndedAt = &ended
				})
				if logger != nil {
					if !hadStartedAt {
						_ = logger.Append(state.Event{
							Type:   eventNodeStarted,
							RunID:  runID,
							NodeID: expandedIDs[r.index],
						})
					}
					_ = logger.Append(state.Event{
						Type:   "node_skipped",
						RunID:  runID,
						NodeID: expandedIDs[r.index],
						Data:   map[string]any{"reason": reason},
					})
				}
				pendingFlush = true
				// Mark transitive dependents as skipped too — they were
				// gated on this item's success and won't launch. Without
				// this, dependents stay statusPending in runState and
				// items[] accounting undercounts them. Use a cancel-flavored
				// reason so dependents don't get mislabeled as "dependency
				// X failed" when X was actually cancelled, not failed.
				dependentsReason := fmt.Sprintf("dependency %s cancelled", itemIDs[r.index])
				// Defer flush — the function-level cancel defer will
				// SaveState once after the loop, batching all cancelled
				// items + dependents into a single serialization.
				markSkipped(states, dependentsOf, r.index, itemIDs, runState, expandedIDs, runDir, runID, logger, dependentsReason, true, nil)
				// Propagate the cancellation up so executeForEach records
				// the orchestrator as failed (not completed / all_succeeded).
				// Without this, a fully-cancelled DAG orchestrator returns
				// (states, nil) and its NodeState says "completed" for a
				// cancelled operation.
				//
				// Cancellation is operator intent and outranks a transient
				// wait (a sibling that just happened to be waiting on
				// approval). Overwrite a prior transient firstUnhandledErr
				// so the orchestrator returns context.Canceled (not
				// ErrApprovalPending) up to executeForEach. A prior
				// genuine failure stays — that's even more informative
				// than the cancel.
				if firstUnhandledErr == nil || isTransientErr(firstUnhandledErr) {
					firstUnhandledErr = r.err
				}
				continue
			}
			if hasFailureEdge {
				states[r.index].Status = outcomeFailedHandled
				states[r.index].Err = r.err
				states[r.index].FailureContext = buildFailureContext(runState, expandedIDs[r.index], engine.RunsDir)
				persistFailedHandled(runState, expandedIDs[r.index], states[r.index].FailureContext, runDir, runID, logger)
				// Mark all transitive dependents as skipped. Defer the
				// flush — the function-level defer batches all skipped
				// dependents into one SaveState. (persistFailedHandled
				// above is unbatched; that's a separate optimization
				// opportunity.)
				originating := summarizeForOriginatingFailure(itemIDs[r.index], states[r.index].FailureContext)
				markSkipped(states, dependentsOf, r.index, itemIDs, runState, expandedIDs, runDir, runID, logger, "", true, originating)
				pendingFlush = true
				// Note: firstGenuineFailure is intentionally NOT set here.
				// An absorbed failure is a declared recovery path — the
				// author told us "this failure mode is expected." A later
				// cancellation isn't causally "after a sibling failed" in
				// the meaningful sense, so the cancel reason stays as
				// plain "cancelled" rather than "cancelled after sibling
				// failed".
				continue
			}
			// Unhandled genuine failure: record local item state, mark
			// dependents skipped, then cancel siblings. Gate dagCancel
			// on firstGenuineFailure (NOT firstUnhandledErr) — a prior
			// transient wait may have set firstUnhandledErr without
			// representing a real failure, and we must still cancel on
			// the first genuine failure. Also overwrite firstUnhandledErr
			// on the first genuine failure so the orchestrator propagates
			// the failure (not the transient wait) up to executeForEach.
			//
			// Note: failure_context here is written only to the local
			// states slice, not to runState — it becomes externally
			// visible only when executeForEach finishes and writes the
			// orchestrator's aggregate items[] to NodeState.Data. The
			// dagCancel-after-markSkipped order is a best-effort
			// improvement (siblings get marked skipped before they wake
			// from cancellation) but isn't a strict happens-before
			// guarantee for observers.
			isFirstGenuine := !firstGenuineFailure
			firstGenuineFailure = true
			if isFirstGenuine {
				firstUnhandledErr = r.err
			}
			states[r.index].Err = r.err
			states[r.index].Status = outcomeFailed
			states[r.index].FailureContext = buildFailureContext(runState, expandedIDs[r.index], engine.RunsDir)
			// Defer the flush — the function-level defer batches the
			// genuine failure's dependent-skip writes with any cancel
			// branches that fire later.
			originating := summarizeForOriginatingFailure(itemIDs[r.index], states[r.index].FailureContext)
			markSkipped(states, dependentsOf, r.index, itemIDs, runState, expandedIDs, runDir, runID, logger, "", true, originating)
			pendingFlush = true
			if isFirstGenuine {
				dagCancel()
			}
			continue
		}
		states[r.index].Output = r.output
		states[r.index].Status = outcomeSucceeded
		for _, unblocked := range d.resolve(r.index) {
			if states[unblocked].Status != "" {
				continue
			}
			launchItem(unblocked)
		}
	}

	return states, firstUnhandledErr
}

func (engine *Engine) executeSingle(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, edgePrompt string, edgePasses map[string]any, runContext *RunContext, logger *state.Logger, runState *state.RunState, extraInputs map[string]any, stateNodeID string) (NodeOutput, error) {
	if stateNodeID == "" {
		stateNodeID = node.ID
	}
	// A cancelled context must propagate before any work (including test
	// hooks) so the DAG cancel branch sees context.Canceled, not an
	// unrelated error class.
	if err := ctx.Err(); err != nil {
		return NodeOutput{}, err
	}
	if engine.failOnItem != "" || engine.transientPendingOnItem != "" || engine.blockUntilCtxDoneOnItem != "" {
		var marker any
		if v, ok := extraInputs["item"]; ok {
			marker = v
			if m, ok := v.(map[string]any); ok {
				if inner, ok := m["item"]; ok {
					marker = inner
				}
			}
		}
		if engine.failOnItem != "" && marker == engine.failOnItem {
			return NodeOutput{}, fmt.Errorf("test-injected failure for item %v", marker)
		}
		if engine.transientPendingOnItem != "" && marker == engine.transientPendingOnItem {
			return NodeOutput{}, ErrApprovalPending
		}
		if engine.blockUntilCtxDoneOnItem != "" && marker == engine.blockUntilCtxDoneOnItem {
			engine.blockerEntered.Add(1)
			<-ctx.Done()
			return NodeOutput{}, ctx.Err()
		}
	}
	// Phase 1 context: clone runContext with extras (ForEach iteration item,
	// predecessor auto-injections) merged into Inputs so Phase 1 expressions
	// like ${workflow_input.task.id} can resolve. The legacy resolveInputs
	// did this implicitly; the new pipeline must do it explicitly.
	phase1Ctx := runContext
	if len(extraInputs) > 0 {
		cloned := *runContext
		mergedInputs := make(map[string]any, len(runContext.Inputs)+len(extraInputs))
		for k, v := range runContext.Inputs {
			mergedInputs[k] = v
		}
		for k, v := range extraInputs {
			mergedInputs[k] = v
		}
		cloned.Inputs = mergedInputs
		phase1Ctx = &cloned
	}
	resolvedNodeInputs, err := evaluatePhase1(phase1Ctx, node.Inputs)
	if err != nil {
		recordPreDispatchFailure(runState, logger, runID, stateNodeID, fmt.Errorf("resolve inputs: %w", err))
		return NodeOutput{}, err
	}
	// For join nodes, merge all per-edge passes from JoinState; for non-join
	// nodes, use the single edge's passes captured by applyOutputRouting.
	dispatchPasses := edgePasses
	if node.Join == joinAll {
		dispatchPasses = mergeJoinPasses(runState.JoinState[node.ID])
	}
	// Base of the merge is the same Inputs visible to Phase 1 (which includes
	// extras if any). Workflow inputs <  extras < node inputs < edge passes.
	// Layering extras into the merged map explicitly is redundant but cheap;
	// keep it for clarity that extras are top-precedence.
	inputs := mergeDispatchInputs(phase1Ctx.Inputs, resolvedNodeInputs, dispatchPasses)
	nodeInputs := resolvedNodeInputs
	if len(extraInputs) > 0 {
		for k, v := range extraInputs {
			inputs[k] = v
			nodeInputs[k] = v
		}
	}
	// Phase 4: clone runContext with merged Inputs so ${input.X} reads work
	// in everything below this point (prompts, session_id, workspace paths,
	// emit output, etc.). Reassign runContext so all downstream callers
	// (executeRole, executeShellRole, executeHuman, resolvedSessionID) see
	// the merged dispatch map. The outer caller's *RunContext is unchanged
	// (Go passes pointers by value); Outputs/Env/Tree share by reference.
	runContext = dispatchContext(runContext, inputs)

	promptInputsMode := resolvePromptInputsMode(workflow, node)
	promptDisplayInputs := buildPromptDisplayInputs(promptInputsMode, runContext.Inputs, nodeInputs)

	// Emit resolved inputs for inspect introspection.
	if len(inputs) > 0 {
		_ = logger.Append(state.Event{
			Type:   "node_inputs_resolved",
			RunID:  runID,
			NodeID: stateNodeID,
			Data:   inputs,
		})
	}

	// Emit edge prompt separately for inspect introspection.
	if edgePrompt != "" {
		_ = logger.Append(state.Event{
			Type:   "node_edge_prompt",
			RunID:  runID,
			NodeID: stateNodeID,
			Text:   edgePrompt,
		})
	}

	dispatchHash, err := dispatchHashForNode(node.ID, edgePrompt, inputs)
	if err != nil {
		recordPreDispatchFailure(runState, logger, runID, stateNodeID, fmt.Errorf("dispatch hash: %w", err))
		return NodeOutput{}, err
	}

	// Resolve node.SessionID expression (e.g. "${input.original_session}") so
	// that resolveSession sees the concrete session ID value. We use a local
	// variable to avoid mutating the shared *Node pointer (data race when
	// executeForEachConcurrent or executeForEachDAG calls executeSingle from
	// multiple goroutines).
	resolvedSessionID := node.SessionID
	if resolvedSessionID != "" && strings.Contains(resolvedSessionID, "${") {
		resolved, err := runContext.Resolve(resolvedSessionID)
		if err != nil {
			wrapped := fmt.Errorf("resolve session_id expression: %w", err)
			recordPreDispatchFailure(runState, logger, runID, stateNodeID, wrapped)
			return NodeOutput{}, wrapped
		}
		if resolved != nil {
			resolvedSessionID = fmt.Sprint(resolved)
		} else {
			resolvedSessionID = ""
		}
	}

	maxAttempts := 1
	if node.Retry != nil && node.Retry.Max > 0 {
		maxAttempts = node.Retry.Max + 1
	}

	var output NodeOutput
	var execErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := retryDelay(node.Retry, attempt-1)
			_ = logger.Append(state.Event{
				Type: "node_retry", RunID: runID, NodeID: stateNodeID,
				Data: map[string]any{keyAttempt: attempt, "delay": delay.String(), "max_attempts": maxAttempts},
			})
			runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
				stateNode.Status = statusRetrying
				stateNode.RetryCount = attempt - 1
			})
			if saveErr := state.SaveState(filepath.Join(runDir, "state.json"), runState); saveErr != nil {
				return NodeOutput{}, saveErr
			}
			time.Sleep(delay)
		}

		_ = logger.Append(state.Event{
			Type:   "node_attempt_started",
			RunID:  runID,
			NodeID: stateNodeID,
			Data:   map[string]any{keyAttempt: attempt},
		})

		switch node.Kind {
		case kindRole:
			output, execErr = engine.executeRole(ctx, runID, runDir, workflow, node, edgePrompt, inputs, promptDisplayInputs, dispatchHash, logger, runState, stateNodeID, resolvedSessionID, runContext, attempt)
		case kindHuman:
			output, execErr = engine.executeHuman(ctx, runID, runDir, workflow, node, edgePrompt, inputs, promptDisplayInputs, dispatchHash, logger, runState, stateNodeID, resolvedSessionID, runContext, attempt)
		case kindSystem:
			output, execErr = engine.executeSystem(runID, workflow, node, dispatchHash, logger, runState, stateNodeID)
		case kindSubworkflow:
			output, execErr = engine.executeSubworkflow(ctx, runID, workflow, node, inputs, dispatchHash, logger, runState, stateNodeID)
		case kindEmit:
			output, execErr = executeEmit(ctx, node, runContext, dispatchPasses)
		default:
			return NodeOutput{}, fmt.Errorf("unknown node kind: %s", node.Kind)
		}

		if execErr == nil {
			return output, nil
		}
		if !IsRetryable(execErr) || attempt == maxAttempts {
			return NodeOutput{}, execErr
		}
		_ = logger.Append(state.Event{
			Type:   "node_attempt_failed",
			RunID:  runID,
			NodeID: stateNodeID,
			Data: map[string]any{
				"attempt": attempt,
				"reason":  truncateForSummary(execErr.Error(), 200),
			},
		})
	}

	return NodeOutput{}, execErr
}

func (engine *Engine) executeRole(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, edgePrompt string, inputs map[string]any, promptDisplayInputs map[string]any, dispatchHash string, logger *state.Logger, runState *state.RunState, stateNodeID string, explicitSessionID string, runContext *RunContext, attempt int) (NodeOutput, error) {
	runnerID, err := ResolveRunnerID(node, workflow)
	if err != nil {
		return NodeOutput{}, err
	}

	runner, ok := engine.RunnerRegistry.Get(runnerID)
	if !ok {
		return NodeOutput{}, fmt.Errorf("runner not found: %s", runnerID)
	}

	// Detect shell runner by checking the runner definition type.
	runnerDef := engine.Definitions.Runners[runnerID]
	isShell := runnerDef != nil && runnerDef.Type == kindShell

	if isShell {
		return engine.executeShellRole(ctx, runID, runDir, workflow, node, inputs, dispatchHash, logger, runState, stateNodeID, runnerID, runner, runContext)
	}

	// Bump Dispatches only on the first attempt of a logical dispatch.
	// Internal retries (attempt > 1) reuse the same dispatch number and
	// render full inputs (see prompt-build branch below).
	var dispatchN int
	firstAttempt := attempt == 1
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		if firstAttempt {
			stateNode.Dispatches++
		}
		dispatchN = stateNode.Dispatches
	})
	dispatchInputsDir := filepath.Join(runDir, "dispatches", stateNodeID, fmt.Sprintf("%d", dispatchN), "inputs")

	// Materialize on EVERY attempt (idempotent — resolved inputs don't
	// change between retries within a single executeSingle invocation).
	// Required for crash-mid-materialization recovery: if attempt 1 wrote
	// some files but crashed, attempt 2 completes the dir.
	//
	// INVARIANT: promptDisplayInputs MUST NOT be mutated between attempts
	// in executeSingle's retry loop. Today enforced by the inputs
	// resolution happening once before the loop.
	//
	// On failure: mark the node failed (same pattern as the runner-error
	// path below) and wrap as Retryable so executeSingle's retry policy
	// applies.
	if err := MaterializeDispatchInputs(dispatchInputsDir, promptDisplayInputs); err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, time.Now().UTC(), err)
		return NodeOutput{}, Retryable(err)
	}

	sessionID, resume := resolveSession(runState, node, workflow, stateNodeID, explicitSessionID)
	now := time.Now().UTC()
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusRunning
		stateNode.Attempts++
		stateNode.StartedAt = &now
	})
	// Flush state before execution so resume can detect in-flight nodes.
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)

	// Use !resume rather than firstRun: a non-resumed session (fresh context
	// or first attempt) needs the full node prompt because there is no prior
	// conversation containing it.
	rolePrompt, nodePrompt := selectPrompts(!resume, "", node.Prompt, edgePrompt, node.PromptOnResume)
	rolePrompt = prependPreamble(rolePrompt, node, workflow, runState)

	// resumeDeltas semantics: nil = fresh rendering (full inputs).
	// Non-nil (possibly empty) = resume rendering (deltas; empty
	// renders explicit "no changes since prior turn" notice).
	// Gate on len(promptDisplayInputs) > 0: a node with prompt_inputs_mode:none
	// should see no inputs-related rendering at all — not even a "no changes"
	// notice — when re-dispatched.
	var resumeDeltas map[string]any
	if resume && firstAttempt && dispatchN > 1 && len(promptDisplayInputs) > 0 {
		priorInputsDir := filepath.Join(runDir, "dispatches", stateNodeID, fmt.Sprintf("%d", dispatchN-1), "inputs")
		resumeDeltas = DiffAgainstPriorDispatch(promptDisplayInputs, priorInputsDir)
	}

	prompt, err := ComposePromptWithInputViews(
		rolePrompt,
		nodePrompt,
		inputs,
		promptDisplayInputs,
		node.Decisions,
		dispatchInputsDir,
		resumeDeltas,
		dispatchN,
	)
	if err != nil {
		return NodeOutput{}, err
	}
	_ = logger.Append(state.Event{Type: eventNodePrompt, RunID: runID, NodeID: stateNodeID, Text: prompt})

	workspace, err := resolveWorkspace(runDir, workflow, node, runContext)
	if err != nil {
		return NodeOutput{}, err
	}

	schemaJSON, err := BuildRequestSchemaJSON(node)
	if err != nil {
		return NodeOutput{}, err
	}

	_ = logger.Append(state.Event{Type: eventNodeStarted, RunID: runID, NodeID: stateNodeID, Data: map[string]any{fieldSessionID: sessionID, "resume": resume}})
	engine.logUserPromptEcho(runID, stateNodeID, runnerID, prompt, logger)

	request := runners.Request{
		Prompt:           prompt,
		Workspace:        workspace,
		Decisions:        node.Decisions.IDs(),
		SessionID:        sessionID,
		Resume:           resume,
		MaxTurns:         node.MaxTurns,
		Env:              node.RunnerEnv,
		OutputSchemaJSON: schemaJSON,
	}
	result, err := engine.runWithResumeFallback(
		ctx, runID, stateNodeID, logger, runner, request,
		explicitSessionID != "",
		func() (string, error) {
			// Fresh-session prompt: includes node.Prompt AND edgePrompt.
			// The INITIAL fresh-session dispatch at line ~1340 drops edgePrompt
			// (selectPrompts ignores it on firstRun=true). The rebuild path
			// diverges intentionally: when degrading from resume to fresh, the
			// LLM in the fresh session needs the back-edge transition context
			// that would otherwise have come from its prior conversation memory.
			//
			// The "\n\n---\n\n" separator's newlines block ${input.x} regex
			// matches from spanning the concatenation boundary.
			freshRolePrompt := prependPreamble("", node, workflow, runState)
			freshNodePrompt := node.Prompt
			if edgePrompt != "" {
				freshNodePrompt = node.Prompt + "\n\n---\n\n" + edgePrompt
			}
			return ComposePromptWithInputViews(
				freshRolePrompt,
				freshNodePrompt,
				inputs,
				promptDisplayInputs,
				node.Decisions,
				dispatchInputsDir,
				nil, // no deltas — fresh session has no prior memory
				dispatchN,
			)
		},
		func(line runners.Line) {
			_ = logger.Append(state.Event{Type: eventNodeOutput, RunID: runID, NodeID: stateNodeID, Stream: line.Stream, Text: line.Text})
		},
	)
	if err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, now, err)
		return NodeOutput{}, Retryable(err)
	}

	if result.SessionID != "" {
		runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
			stateNode.SessionID = result.SessionID
		})
		sessionID = result.SessionID
	}

	output, err := engine.parseNodeOutputWithRepair(ctx, runID, stateNodeID, node, workspace, runner, logger, runState, result, schemaJSON)
	if err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, now, err)
		return NodeOutput{}, Retryable(err)
	}
	output.ToolCalls = result.ToolCalls

	normalizeOutputData(&output)
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		if stateNode.SessionID != "" {
			sessionID = stateNode.SessionID
		}
	})
	output.SessionID = sessionID

	artifacts, err := collectArtifacts(runDir, stateNodeID, workspace, output.Artifacts)
	if err != nil {
		missing := &ArtifactMissingError{}
		if errors.As(err, &missing) {
			repairedOutput, repairedArtifacts, repairErr := engine.repairMissingArtifacts(
				ctx,
				runID,
				runDir,
				stateNodeID,
				node,
				workspace,
				runner,
				logger,
				runState,
				output,
				sessionID,
				missing,
				schemaJSON,
			)
			if repairErr == nil {
				output = repairedOutput
				artifacts = repairedArtifacts
				err = nil
				// Repair may have produced a fresh runner session; pull the
				// canonical SessionID back from NodeState so the resolver
				// (`node.X.session_id`) sees the latest value.
				runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
					if stateNode.SessionID != "" {
						sessionID = stateNode.SessionID
					}
				})
				output.SessionID = sessionID
			} else {
				err = repairErr
			}
		}
		if err == nil {
			output.Artifacts = artifacts
		} else {
			markNodeFailed(runState, logger, runID, stateNodeID, now, err)
			return NodeOutput{}, err
		}
	}
	output.Artifacts = artifacts

	if err := engine.enforceCircuitBreaker(runID, workflow, node, dispatchHash, output, logger, runState, stateNodeID); err != nil {
		return NodeOutput{}, err
	}

	markNodeCompleted(runState, logger, runID, stateNodeID, node, now, &output)
	return output, nil
}

// buildShellEnv assembles the full environment for a shell node execution.
// Sources (in precedence order, highest first):
//  1. Node inputs — uppercased keys, complex types JSON-encoded
//  2. Secrets — from runState.Secrets
//  3. Run-level env — TOIL_ROOT, TOIL_CURRENT_WORKFLOW_DIR, PROJECT_DIR, etc.
//  4. TOIL_WORKFLOW_SCRIPT_DIR — derived from original workflow definition path
//  5. PATH — prepends $TOIL_ROOT/bin for tools like tgwm
func (engine *Engine) buildShellEnv(inputs map[string]any, runState *state.RunState, workflow *definitions.Workflow) map[string]string {
	env := make(map[string]string, len(inputs))

	// Node inputs as uppercased env vars.
	for k, v := range inputs {
		switch val := v.(type) {
		case string:
			env[strings.ToUpper(k)] = val
		default:
			if b, err := json.Marshal(val); err == nil {
				env[strings.ToUpper(k)] = string(b)
			} else {
				env[strings.ToUpper(k)] = fmt.Sprint(val)
			}
		}
	}

	// Secrets.
	mergeSecretsIntoEnv(env, runState.Secrets)

	// Run-level env vars. Input-derived vars take precedence.
	for k, v := range runState.Env {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}

	// Script directory for companion files co-located with the workflow YAML.
	if origWf, ok := engine.Definitions.Workflows[workflow.ID]; ok && origWf.SourcePath != "" {
		env["TOIL_WORKFLOW_SCRIPT_DIR"] = filepath.Join(filepath.Dir(origWf.SourcePath), origWf.ID)
	}

	// Prepend toil's bin directory to PATH.
	if engine.ToilRoot != "" {
		toilBin := filepath.Join(engine.ToilRoot, "bin")
		env["PATH"] = toilBin + ":" + os.Getenv("PATH")
	}

	return env
}

// executeShellRole handles role nodes backed by a shell runner. The prompt is
// the raw shell command — inputs are passed as uppercased environment variables
// (JSON-encoded for non-string types) and resolved by bash via $VAR syntax.
// By default, stdout becomes the output message with a "default" decision.
//
// If the node declares structured output requirements (node.decisions and/or
// node.outputs), stdout is parsed as the standard NodeOutput JSON object and
// validated against those requirements. This enables deterministic shell-backed
// nodes that emit machine-readable outputs.
func (engine *Engine) executeShellRole(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, inputs map[string]any, dispatchHash string, logger *state.Logger, runState *state.RunState, stateNodeID string, runnerID string, runner runners.Runner, runContext *RunContext) (NodeOutput, error) {
	if err := checkRequiredSecrets(runState, inputs); err != nil {
		recordPreDispatchFailure(runState, logger, runID, stateNodeID, err)
		return NodeOutput{}, err
	}

	now := time.Now().UTC()
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusRunning
		stateNode.Attempts++
		stateNode.StartedAt = &now
	})
	// Flush state before execution so resume can detect in-flight nodes.
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)

	// Shell prompt is the raw command — inputs are available as env vars.
	prompt := strings.TrimSpace(node.Prompt)
	_ = logger.Append(state.Event{Type: eventNodePrompt, RunID: runID, NodeID: stateNodeID, Text: prompt})

	workspace, err := resolveWorkspace(runDir, workflow, node, runContext)
	if err != nil {
		return NodeOutput{}, err
	}

	env := engine.buildShellEnv(inputs, runState, workflow)

	_ = logger.Append(state.Event{Type: eventNodeStarted, RunID: runID, NodeID: stateNodeID})
	engine.logUserPromptEcho(runID, stateNodeID, runnerID, prompt, logger)

	result, err := runner.Run(ctx, runners.Request{
		Prompt:    prompt,
		Workspace: workspace,
		Env:       env,
	}, func(line runners.Line) {
		_ = logger.Append(state.Event{Type: "node_output", RunID: runID, NodeID: stateNodeID, Stream: line.Stream, Text: line.Text})
	})
	if err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, now, err)
		return NodeOutput{}, Retryable(err)
	}

	// Non-zero exit code is a failure.
	if result.ExitCode != 0 {
		detail := lastLines(result.Stderr, 10)
		if detail == "" {
			detail = lastLines(result.Output, 10)
		}
		var failErr error
		if detail != "" {
			failErr = fmt.Errorf("shell command exited with code %d:\n%s", result.ExitCode, detail)
		} else {
			failErr = fmt.Errorf("shell command exited with code %d", result.ExitCode)
		}
		runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
			stateNode.Status = statusFailed
		})
		_ = logger.Append(state.Event{Type: eventNodeFailed, RunID: runID, NodeID: stateNodeID, DurationMs: durationMs(now, time.Now()), Data: map[string]any{keyError: failErr.Error(), "exit_code": result.ExitCode}})
		return NodeOutput{}, Retryable(failErr)
	}

	// Shell output: decision is "default", message is stdout (trimmed).
	output := NodeOutput{
		Decision: decisionDefault,
		Message:  strings.TrimSpace(result.Output),
	}

	// Structured output path: parse/validate JSON if node declares decisions.
	if len(node.Decisions) > 0 {
		parsed, parseErr := ParseNodeOutput(result.Output)
		if parseErr != nil {
			markNodeFailed(runState, logger, runID, stateNodeID, now, parseErr)
			return NodeOutput{}, Retryable(parseErr)
		}
		if validationErr := validateNodeOutput(parsed, node); validationErr != nil {
			markNodeFailed(runState, logger, runID, stateNodeID, now, validationErr)
			return NodeOutput{}, Retryable(validationErr)
		}
		normalizeOutputData(&parsed)

		artifacts, collectErr := collectArtifacts(runDir, stateNodeID, workspace, parsed.Artifacts)
		if collectErr != nil {
			markNodeFailed(runState, logger, runID, stateNodeID, now, collectErr)
			return NodeOutput{}, Retryable(collectErr)
		}
		parsed.Artifacts = artifacts
		output = parsed
	}

	if err := engine.enforceCircuitBreaker(runID, workflow, node, dispatchHash, output, logger, runState, stateNodeID); err != nil {
		return NodeOutput{}, err
	}

	markNodeCompleted(runState, logger, runID, stateNodeID, node, now, &output)
	return output, nil
}

func (engine *Engine) executeHuman(ctx context.Context, runID string, runDir string, workflow *definitions.Workflow, node *definitions.Node, edgePrompt string, inputs map[string]any, promptDisplayInputs map[string]any, dispatchHash string, logger *state.Logger, runState *state.RunState, stateNodeID string, explicitSessionID string, runContext *RunContext, attempt int) (NodeOutput, error) {
	// Bump Dispatches only on the first attempt of a logical dispatch.
	// Internal retries (attempt > 1) reuse the same dispatch number and
	// render full inputs (see prompt-build branch below).
	var dispatchN int
	firstAttempt := attempt == 1
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		if firstAttempt {
			stateNode.Dispatches++
		}
		dispatchN = stateNode.Dispatches
	})
	dispatchInputsDir := filepath.Join(runDir, "dispatches", stateNodeID, fmt.Sprintf("%d", dispatchN), "inputs")

	// Materialize on EVERY attempt (idempotent — resolved inputs don't
	// change between retries within a single executeSingle invocation).
	// Required for crash-mid-materialization recovery: if attempt 1 wrote
	// some files but crashed, attempt 2 completes the dir.
	//
	// INVARIANT: promptDisplayInputs MUST NOT be mutated between attempts
	// in executeSingle's retry loop. Today enforced by the inputs
	// resolution happening once before the loop.
	//
	// On failure: mark the node failed (same pattern as the runner-error
	// path below) and wrap as Retryable so executeSingle's retry policy
	// applies.
	if err := MaterializeDispatchInputs(dispatchInputsDir, promptDisplayInputs); err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, time.Now().UTC(), err)
		return NodeOutput{}, Retryable(err)
	}

	sessionID, resume := resolveSession(runState, node, workflow, stateNodeID, explicitSessionID)
	firstRun := false
	now := time.Now().UTC()
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		firstRun = stateNode.Attempts == 0
		stateNode.Status = statusRunning
		stateNode.Attempts++
		stateNode.StartedAt = &now
	})
	// Flush state before execution so resume can detect in-flight nodes.
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)

	rolePrompt, nodePrompt := selectPrompts(firstRun, "", node.Prompt, edgePrompt, true)
	rolePrompt = prependPreamble(rolePrompt, node, workflow, runState)

	// resumeDeltas semantics: nil = fresh rendering (full inputs).
	// Non-nil (possibly empty) = resume rendering (deltas; empty
	// renders explicit "no changes since prior turn" notice).
	// Gate on len(promptDisplayInputs) > 0: a node with prompt_inputs_mode:none
	// should see no inputs-related rendering at all — not even a "no changes"
	// notice — when re-dispatched.
	var resumeDeltas map[string]any
	if resume && firstAttempt && dispatchN > 1 && len(promptDisplayInputs) > 0 {
		priorInputsDir := filepath.Join(runDir, "dispatches", stateNodeID, fmt.Sprintf("%d", dispatchN-1), "inputs")
		resumeDeltas = DiffAgainstPriorDispatch(promptDisplayInputs, priorInputsDir)
	}

	prompt, err := ComposePromptWithInputViews(
		rolePrompt,
		nodePrompt,
		inputs,
		promptDisplayInputs,
		node.Decisions,
		dispatchInputsDir,
		resumeDeltas,
		dispatchN,
	)
	if err != nil {
		return NodeOutput{}, err
	}
	_ = logger.Append(state.Event{Type: eventNodePrompt, RunID: runID, NodeID: stateNodeID, Text: prompt})

	runnerID, err := ResolveRunnerID(node, workflow)
	if err != nil {
		return NodeOutput{}, err
	}
	runner, ok := engine.RunnerRegistry.Get(runnerID)
	if !ok {
		return NodeOutput{}, fmt.Errorf("runner not found: %s", runnerID)
	}

	workspace, err := resolveWorkspace(runDir, workflow, node, runContext)
	if err != nil {
		return NodeOutput{}, err
	}

	schemaJSON, err := BuildRequestSchemaJSON(node)
	if err != nil {
		return NodeOutput{}, err
	}

	_ = logger.Append(state.Event{Type: eventNodeStarted, RunID: runID, NodeID: stateNodeID, Data: map[string]any{fieldSessionID: sessionID, "resume": resume}})
	engine.logUserPromptEcho(runID, stateNodeID, runnerID, prompt, logger)

	request := runners.Request{
		Prompt:           prompt,
		Workspace:        workspace,
		Decisions:        node.Decisions.IDs(),
		SessionID:        sessionID,
		Resume:           resume,
		MaxTurns:         node.MaxTurns,
		Env:              node.RunnerEnv,
		OutputSchemaJSON: schemaJSON,
	}
	result, err := engine.runWithResumeFallback(
		ctx, runID, stateNodeID, logger, runner, request,
		explicitSessionID != "",
		func() (string, error) {
			// Fresh-session prompt: includes node.Prompt AND edgePrompt.
			// The INITIAL fresh-session dispatch at line ~1340 drops edgePrompt
			// (selectPrompts ignores it on firstRun=true). The rebuild path
			// diverges intentionally: when degrading from resume to fresh, the
			// LLM in the fresh session needs the back-edge transition context
			// that would otherwise have come from its prior conversation memory.
			//
			// The "\n\n---\n\n" separator's newlines block ${input.x} regex
			// matches from spanning the concatenation boundary.
			freshRolePrompt := prependPreamble("", node, workflow, runState)
			freshNodePrompt := node.Prompt
			if edgePrompt != "" {
				freshNodePrompt = node.Prompt + "\n\n---\n\n" + edgePrompt
			}
			return ComposePromptWithInputViews(
				freshRolePrompt,
				freshNodePrompt,
				inputs,
				promptDisplayInputs,
				node.Decisions,
				dispatchInputsDir,
				nil, // no deltas — fresh session has no prior memory
				dispatchN,
			)
		},
		func(line runners.Line) {
			_ = logger.Append(state.Event{Type: eventNodeOutput, RunID: runID, NodeID: stateNodeID, Stream: line.Stream, Text: line.Text})
		},
	)
	if err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, now, err)
		return NodeOutput{}, Retryable(err)
	}

	if result.SessionID != "" {
		runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
			stateNode.SessionID = result.SessionID
		})
		sessionID = result.SessionID
	}

	output, err := engine.parseNodeOutputWithRepair(ctx, runID, stateNodeID, node, workspace, runner, logger, runState, result, schemaJSON)
	if err != nil {
		markNodeFailed(runState, logger, runID, stateNodeID, now, err)
		return NodeOutput{}, Retryable(err)
	}
	output.ToolCalls = result.ToolCalls
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		if stateNode.SessionID != "" {
			sessionID = stateNode.SessionID
		}
	})
	output.SessionID = sessionID

	if err := engine.enforceCircuitBreaker(runID, workflow, node, dispatchHash, output, logger, runState, stateNodeID); err != nil {
		return NodeOutput{}, err
	}

	markNodeCompleted(runState, logger, runID, stateNodeID, node, now, &output)
	return output, nil
}

func (engine *Engine) parseNodeOutputWithRepair(ctx context.Context, runID string, stateNodeID string, node *definitions.Node, workspace string, runner runners.Runner, logger *state.Logger, runState *state.RunState, result runners.Result, schemaJSON []byte) (NodeOutput, error) {
	output, parseErr := ParseNodeOutput(result.Output)
	if parseErr == nil {
		parseErr = validateNodeOutput(output, node)
	}
	if parseErr == nil {
		return output, nil
	}

	validationErrors := outputValidationMessages(parseErr)
	var repairPrompt string
	if strings.TrimSpace(result.Output) == "" {
		// Agent was likely interrupted (provider error, no result tool call, etc.).
		// Use the softer prompt that invites continued work rather than forcing
		// immediate classification. See kata bb03 for the postmortem.
		repairPrompt = buildIncompleteWorkPrompt(node.Decisions.IDs(), validationErrors)
	} else {
		// Agent emitted content but it doesn't match the contract — use the
		// harsher prompt to force the final classification.
		repairPrompt = buildRepairPrompt(node.Decisions.IDs(), validationErrors)
	}
	_ = logger.Append(state.Event{Type: "node_prompt", RunID: runID, NodeID: stateNodeID, Text: repairPrompt})

	repairRequests := []runners.Request{
		{
			Prompt:           repairPrompt,
			Workspace:        workspace,
			Decisions:        node.Decisions.IDs(),
			SessionID:        result.SessionID,
			Resume:           result.SessionID != "",
			MaxTurns:         node.MaxTurns,
			Env:              node.RunnerEnv,
			OutputSchemaJSON: schemaJSON,
		},
	}
	// Some providers reject resumed sessions if the previous turn ended with
	// tool_use/tool_result mismatch. Retry once in a fresh session.
	if result.SessionID != "" {
		repairRequests = append(repairRequests, runners.Request{
			Prompt:           repairPrompt,
			Workspace:        workspace,
			Decisions:        node.Decisions.IDs(),
			SessionID:        "",
			Resume:           false,
			MaxTurns:         node.MaxTurns,
			Env:              node.RunnerEnv,
			OutputSchemaJSON: schemaJSON,
		})
	}

	var lastRepairErr error
	for _, repairRequest := range repairRequests {
		repaired, repairErr := runner.Run(ctx, repairRequest, func(line runners.Line) {
			_ = logger.Append(state.Event{Type: eventNodeOutput, RunID: runID, NodeID: stateNodeID, Stream: line.Stream, Text: line.Text})
		})
		if repairErr != nil {
			lastRepairErr = repairErr
			continue
		}

		if repaired.SessionID != "" {
			runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
				stateNode.SessionID = repaired.SessionID
			})
		}

		repairedOutput, repairedParseErr := ParseNodeOutput(repaired.Output)
		if repairedParseErr == nil {
			repairedParseErr = validateNodeOutput(repairedOutput, node)
		}
		if repairedParseErr == nil {
			return repairedOutput, nil
		}
		lastRepairErr = repairedParseErr
	}

	finalErr := lastRepairErr
	if finalErr == nil {
		finalErr = parseErr
	}
	// When the runner produced no usable output, surface stderr diagnostics
	// so the real cause (e.g. missing API keys) is visible in the error.
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" && strings.TrimSpace(result.Output) == "" {
		return NodeOutput{}, fmt.Errorf("runner produced no output (stderr: %s)", lastStderrLine(stderr))
	}
	return NodeOutput{}, finalErr
}

func (engine *Engine) repairMissingArtifacts(
	ctx context.Context,
	runID string,
	runDir string,
	stateNodeID string,
	node *definitions.Node,
	workspace string,
	runner runners.Runner,
	logger *state.Logger,
	runState *state.RunState,
	previousOutput NodeOutput,
	sessionID string,
	missing *ArtifactMissingError,
	schemaJSON []byte,
) (NodeOutput, []string, error) {
	// Hold the per-session lock across every runner.Run below so a concurrent
	// sibling resuming the same SessionID via runWithResumeFallback cannot
	// overlap with artifact repair and corrupt the transcript.
	release, err := runners.AcquireSession(ctx, sessionID)
	if err != nil {
		return NodeOutput{}, nil, err
	}
	defer release()

	missingPaths := append([]string{}, missing.Missing...)
	repairRequests := []runners.Request{
		{
			Workspace:        workspace,
			Decisions:        node.Decisions.IDs(),
			SessionID:        sessionID,
			Resume:           sessionID != "",
			MaxTurns:         node.MaxTurns,
			Env:              node.RunnerEnv,
			OutputSchemaJSON: schemaJSON,
		},
	}
	// Same resilience pattern as output-repair: if resume fails due provider
	// session mismatch, retry once in a fresh session.
	if sessionID != "" {
		repairRequests = append(repairRequests, runners.Request{
			Workspace:        workspace,
			Decisions:        node.Decisions.IDs(),
			SessionID:        "",
			Resume:           false,
			MaxTurns:         node.MaxTurns,
			Env:              node.RunnerEnv,
			OutputSchemaJSON: schemaJSON,
		})
	}

	var lastErr error = missing
	previousDecision := previousOutput.Decision

	for _, repairRequest := range repairRequests {
		repairRequest.Prompt = buildArtifactRepairPrompt(missingPaths, previousDecision, node.Decisions.IDs())
		_ = logger.Append(state.Event{Type: "node_prompt", RunID: runID, NodeID: stateNodeID, Text: repairRequest.Prompt})

		repairedResult, repairErr := runner.Run(ctx, repairRequest, func(line runners.Line) {
			_ = logger.Append(state.Event{Type: eventNodeOutput, RunID: runID, NodeID: stateNodeID, Stream: line.Stream, Text: line.Text})
		})
		if repairErr != nil {
			lastErr = repairErr
			continue
		}

		if repairedResult.SessionID != "" {
			runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
				stateNode.SessionID = repairedResult.SessionID
			})
		}

		repairedOutput, parseErr := engine.parseNodeOutputWithRepair(ctx, runID, stateNodeID, node, workspace, runner, logger, runState, repairedResult, schemaJSON)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}
		normalizeOutputData(&repairedOutput)

		artifacts, collectErr := collectArtifacts(runDir, stateNodeID, workspace, repairedOutput.Artifacts)
		if collectErr == nil {
			repairedOutput.Artifacts = artifacts
			return repairedOutput, artifacts, nil
		}

		missingErr := &ArtifactMissingError{}
		if errors.As(collectErr, &missingErr) {
			missingPaths = append([]string{}, missingErr.Missing...)
			previousDecision = repairedOutput.Decision
			lastErr = collectErr
			continue
		}

		return NodeOutput{}, nil, collectErr
	}

	if lastErr != nil {
		return NodeOutput{}, nil, lastErr
	}
	return NodeOutput{}, nil, fmt.Errorf("artifact repair failed")
}

func (engine *Engine) logUserPromptEcho(runID string, nodeID string, runnerID string, prompt string, logger *state.Logger) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	if !engine.shouldInjectPromptEcho(runnerID) {
		return
	}
	payload, err := buildUserPromptEcho(prompt)
	if err != nil || payload == "" {
		return
	}
	_ = logger.Append(state.Event{
		Type:   eventNodeOutput,
		RunID:  runID,
		NodeID: nodeID,
		Stream: keyStdout,
		Text:   payload,
	})
}

func (engine *Engine) runWithResumeFallback(
	ctx context.Context,
	runID string,
	nodeID string,
	logger *state.Logger,
	runner runners.Runner,
	request runners.Request,
	intendedYAMLResume bool,
	rebuildPromptForFreshSession func() (string, error),
	handler runners.LineHandler,
) (runners.Result, error) {
	release, err := runners.AcquireSession(ctx, request.SessionID)
	if err != nil {
		return runners.Result{}, err
	}
	defer release()

	result, err := runner.Run(ctx, request, handler)
	if err == nil {
		return result, nil
	}
	if request.Resume && request.SessionID != "" && shouldRetryFreshSession(err) {
		// PRI-1575: emit a structured event so debuggers + dashboards
		// can spot when a node that intended to resume silently
		// degraded to a fresh session. `intended` distinguishes a
		// YAML-author-declared resume (true — plan_tasks-as-judge style;
		// the workflow author was relying on prior context) from a
		// loop-iteration auto-resume (false — fine to silently
		// degrade; the loop just continues).
		if logger != nil {
			_ = logger.Append(state.Event{
				Type:   "node_resume_degraded",
				RunID:  runID,
				NodeID: nodeID,
				Data: map[string]any{
					"original_session_id": request.SessionID,
					keyError:              err.Error(),
					"intended":            intendedYAMLResume,
				},
			})
		}
		if rebuildPromptForFreshSession != nil {
			freshPrompt, rebuildErr := rebuildPromptForFreshSession()
			if rebuildErr != nil {
				return runners.Result{}, fmt.Errorf("rebuild fresh-session prompt: %w", rebuildErr)
			}
			request.Prompt = freshPrompt
		}
		request.Resume = false
		request.SessionID = ""
		return runner.Run(ctx, request, handler)
	}
	return runners.Result{}, err
}

func shouldRetryFreshSession(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tool_use") && strings.Contains(message, "tool_result")
}

func (engine *Engine) shouldInjectPromptEcho(runnerID string) bool {
	if runnerID == "" {
		return true
	}
	definition, ok := engine.Definitions.Runners[runnerID]
	if !ok || definition == nil {
		return true
	}
	switch definition.Type {
	case "claude", runnerTypeSerf:
		return false
	}
	return true
}

func buildUserPromptEcho(prompt string) (string, error) {
	payload := map[string]any{
		keyType:     "user",
		"synthetic": true,
		fieldMessage: map[string]any{
			kindRole: "user",
			keyContent: []map[string]string{
				{
					keyType: keyText,
					keyText: prompt,
				},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (engine *Engine) executeSystem(runID string, workflow *definitions.Workflow, node *definitions.Node, dispatchHash string, logger *state.Logger, runState *state.RunState, stateNodeID string) (NodeOutput, error) {
	if stateNodeID == "" {
		stateNodeID = node.ID
	}
	now := time.Now().UTC()
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusRunning
		stateNode.StartedAt = &now
	})

	output := NodeOutput{
		Decision: decisionDefault,
		Message:  "system node completed",
	}
	if err := engine.enforceCircuitBreaker(runID, workflow, node, dispatchHash, output, logger, runState, stateNodeID); err != nil {
		return NodeOutput{}, err
	}

	markNodeCompleted(runState, logger, runID, stateNodeID, node, now, &output)
	return output, nil
}

func (engine *Engine) executeSubworkflow(ctx context.Context, runID string, workflow *definitions.Workflow, node *definitions.Node, inputs map[string]any, dispatchHash string, logger *state.Logger, runState *state.RunState, stateNodeID string) (NodeOutput, error) {
	if stateNodeID == "" {
		stateNodeID = node.ID
	}
	if node.Workflow == "" {
		return NodeOutput{}, fmt.Errorf("subworkflow id required")
	}

	now := time.Now().UTC()
	childRunID := ""
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		// Detect loop re-entry: node completed previously and came back via back-edge.
		// Crash resume shows status "running" or "retrying", so this is safe.
		if stateNode.Status == statusCompleted && stateNode.Data != nil {
			if old, ok := stateNode.Data[dataKeyChildRun].(string); ok {
				_ = logger.Append(state.Event{
					Type: "subworkflow_reentry", RunID: runID, NodeID: stateNodeID,
					Data: map[string]any{"previous_child_run": old},
				})
				delete(stateNode.Data, dataKeyChildRun)
			}
		}
		stateNode.Status = statusRunning
		stateNode.StartedAt = &now
		if stateNode.Data != nil {
			if stored, ok := stateNode.Data[dataKeyChildRun].(string); ok {
				childRunID = stored
			}
		}
	})

	var output NodeOutput
	var err error
	var childHadUnresolvedFailure bool
	if childRunID == "" {
		// Create the child run first so we can emit a subworkflow_started event that includes child_run.
		childRunID, err = engine.createRun(node.Workflow, inputs, runID, childEnvForSubworkflow(runState.Env, inputs), "")
		if err == nil {
			engine.storeRunSecrets(childRunID, runState.Secrets)
			runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
				if stateNode.Data == nil {
					stateNode.Data = map[string]any{}
				}
				stateNode.Data[dataKeyChildRun] = childRunID
			})
			_ = logger.Append(state.Event{Type: "subworkflow_started", RunID: runID, NodeID: stateNodeID, Data: map[string]any{
				dataKeyChildRun:  childRunID,
				"child_workflow": node.Workflow,
			}})
			// Persist the parent's child_run pointer BEFORE the blocking
			// child ResumeRun. Without this, a crash anywhere in the
			// synchronous child chain loses the pointer: on restart the
			// parent re-enters executeSubworkflow with empty Data and
			// dispatches a NEW child, which collides with any
			// parent-ID-keyed external state (e.g. worktrees) the orphan
			// first child already created. Oversight from commit daa3292
			// which added the same save-before-block pattern to the role,
			// shell, and human paths but missed this one.
			_ = state.SaveState(filepath.Join(engine.RunsDir, runID, "state.json"), runState)
			if engine.beforeChildResume != nil {
				engine.beforeChildResume(runID, childRunID)
			}
			output, err = engine.ResumeRun(ctx, childRunID)
		}
	} else {
		_ = logger.Append(state.Event{Type: "subworkflow_started", RunID: runID, NodeID: stateNodeID, Data: map[string]any{
			dataKeyChildRun:  childRunID,
			"child_workflow": node.Workflow,
		}})
		childRunState, loadErr := state.LoadState(filepath.Join(engine.RunsDir, childRunID, "state.json"))
		if loadErr == nil {
			switch childRunState.Status {
			case statusCompleted:
				if recovered, ok := lastOutputFromState(childRunState); ok {
					output = recovered
					childHadUnresolvedFailure = childRunState.HasUnresolvedFailure
				} else {
					output, err = engine.ResumeRun(ctx, childRunID)
				}
			case statusRunning, statusPaused, statusAwaitingApproval:
				runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
					if stateNode.Data == nil {
						stateNode.Data = map[string]any{}
					}
					stateNode.Data[dataKeyChildRun] = childRunID
				})
				return NodeOutput{}, ErrSubworkflowInProgress
			default:
				output, err = engine.ResumeRun(ctx, childRunID)
			}
		} else {
			output, err = engine.ResumeRun(ctx, childRunID)
		}
	}
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		if stateNode.Data == nil {
			stateNode.Data = map[string]any{}
		}
		if childRunID != "" {
			stateNode.Data[dataKeyChildRun] = childRunID
		}
	})
	if err != nil {
		if errors.Is(err, ErrApprovalPending) || errors.Is(err, ErrSubworkflowInProgress) {
			return NodeOutput{}, err
		}
		if errors.Is(err, ErrUnresolvedFailure) {
			// Child terminated with Status=completed but HasUnresolvedFailure=true.
			// Surface the child's terminal Decision via the standard success-tail
			// so the parent's existing business edges (when: escalate, etc.) fire
			// normally. The parent's own finalization will rediscover the flag via
			// ComputeUnresolvedFailure's recursion into child state.json.
			childHadUnresolvedFailure = true
			err = nil
			// Intentional fall-through to success path below.
		}
	}
	if err != nil {
		ended := time.Now().UTC()
		runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
			stateNode.Status = statusFailed
			stateNode.Message = err.Error()
			// PRI-1582: also set Error so buildFailureContext.error
			// surfaces the subworkflow failure. Previously only Message
			// was set, which left .error empty in the parent's
			// failure_context — debug_task_failure failures had no error text
			// at the implement_task level.
			stateNode.Error = err.Error()
			stateNode.EndedAt = &ended
			if stateNode.Data == nil {
				stateNode.Data = map[string]any{}
			}
			stateNode.Data[dataKeyChildRun] = childRunID
			// Extract failed child node info for failure routing. The
			// preferred source is a child node with Status=failed; when
			// the child failed at orchestrator level (max loop iterations,
			// no_progress, etc.) no individual node has that status, so
			// fall back to the child's terminal node-by-message and the
			// outer error string. (PRI-1582)
			childRS, loadErr := state.LoadState(filepath.Join(engine.RunsDir, childRunID, "state.json"))
			if loadErr == nil {
				foundFailedNode := false
				childRS.WithNodes(func(nodes map[string]*state.NodeState) {
					for _, cn := range nodes {
						if cn.Status == statusFailed {
							stateNode.Data["failed_child_node"] = cn.ID
							stateNode.Data["failed_child_message"] = cn.Message
							stateNode.Data["failed_child_session"] = cn.SessionID
							foundFailedNode = true
							break
						}
					}
				})
				if !foundFailedNode {
					// Orchestrator-level failure: no child node has
					// Status=failed. Surface the outer error and the
					// most-recently-active node so the parent's
					// failure_context still names a concrete child node.
					stateNode.Data["failed_child_error"] = err.Error()
					var latestNode *state.NodeState
					childRS.WithNodes(func(nodes map[string]*state.NodeState) {
						for _, cn := range nodes {
							if cn.EndedAt == nil {
								continue
							}
							if latestNode == nil || cn.EndedAt.After(*latestNode.EndedAt) {
								latestNode = cn
							}
						}
					})
					if latestNode != nil {
						stateNode.Data["failed_child_node"] = latestNode.ID
						stateNode.Data["failed_child_message"] = latestNode.Message
						stateNode.Data["failed_child_session"] = latestNode.SessionID
						stateNode.Data["failed_child_decision"] = latestNode.Decision
					}
				}
			}
		})
		_ = logger.Append(state.Event{Type: "subworkflow_failed", RunID: runID, NodeID: stateNodeID, DurationMs: durationMs(now, ended), Data: map[string]any{
			keyError:        err.Error(),
			dataKeyChildRun: childRunID,
		}})
		engine.maybeRecordInterviewFailure(runState, node.Workflow, inputs, err)
		return NodeOutput{}, err
	}

	if output.Data == nil {
		output.Data = map[string]any{}
	}
	output.Data[dataKeyChildRun] = childRunID

	if err := engine.enforceCircuitBreaker(runID, workflow, node, dispatchHash, output, logger, runState, stateNodeID); err != nil {
		return NodeOutput{}, err
	}

	ended := time.Now().UTC()
	_ = logger.Append(state.Event{Type: "subworkflow_completed", RunID: runID, NodeID: stateNodeID, DurationMs: durationMs(now, ended), Data: map[string]any{
		dataKeyChildRun:         childRunID,
		keyHasUnresolvedFailure: childHadUnresolvedFailure,
	}})
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusCompleted
		stateNode.Decision = output.Decision
		stateNode.Message = output.Message
		stateNode.Data = output.Data
		stateNode.Artifacts = output.Artifacts
		stateNode.EndedAt = &ended
	})

	engine.maybeRecordInterviewResult(runState, node.Workflow, childRunID, inputs, output)

	return output, nil
}

func childEnvForSubworkflow(parentEnv map[string]string, inputs map[string]any) map[string]string {
	var childEnv map[string]string
	if len(parentEnv) > 0 {
		childEnv = make(map[string]string, len(parentEnv))
		for key, value := range parentEnv {
			childEnv[key] = value
		}
	}
	if projectDir, ok := inputEnvValue("PROJECT_DIR", inputs); ok {
		if childEnv == nil {
			childEnv = map[string]string{}
		}
		childEnv["PROJECT_DIR"] = projectDir
	}
	if workflowDir, ok := inputs["workflow_dir"]; ok {
		if s, ok := workflowDir.(string); ok && s != "" {
			if childEnv == nil {
				childEnv = map[string]string{}
			}
			childEnv["TOIL_CURRENT_WORKFLOW_DIR"] = s
		}
	}
	return childEnv
}

// lastStderrLine returns the last non-empty line from stderr output.
func lastStderrLine(stderr string) string {
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return stderr
}

// ResolveRunnerID determines which runner to use for a node. The priority chain is:
// 1. Node's explicit runner attribute
// 2. Tag-based override from workflow.RunnerOverrides (first matching tag wins)
//
// Every node must have a runner configured — returns an error if none is found.
func ResolveRunnerID(node *definitions.Node, workflow *definitions.Workflow) (string, error) {
	if node.Runner != "" {
		return node.Runner, nil
	}
	if len(node.Tags) > 0 && len(workflow.RunnerOverrides) > 0 {
		for _, tag := range node.Tags {
			if override, ok := workflow.RunnerOverrides[tag]; ok {
				return override, nil
			}
		}
	}
	return "", fmt.Errorf("node %q has no runner configured", node.ID)
}

// resolveSession reads and optionally clears the session ID based on the
// effective context mode. "fresh", "compact", and "summary" clear the session
// so the runner starts a new conversation. When explicitSessionID is non-empty
// (e.g. from a resolved expression), it overrides the state's session ID for
// "full" or default context modes.
func resolveSession(runState *state.RunState, node *definitions.Node, workflow *definitions.Workflow, stateNodeID string, explicitSessionID string) (sessionID string, resume bool) {
	mode := resolveContextMode(node, workflow)
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		switch mode {
		case contextModeFresh, contextModeCompact, contextModeSummary:
			stateNode.SessionID = ""
			sessionID = ""
			resume = false
		default:
			if explicitSessionID != "" {
				sessionID = explicitSessionID
			} else {
				sessionID = stateNode.SessionID
			}
			resume = sessionID != ""
		}
	})
	return
}

// evaluatePhase1 resolves a node's Inputs block against the run context.
// Returns the resolved key->value map. ${input.X} references are
// rejected at load time (Task 12), so we don't re-check here. Phase 1
// has no access to the merged map (it doesn't exist yet).
func evaluatePhase1(ctx *RunContext, inputs map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(inputs))
	for key, raw := range inputs {
		v, err := resolveValue(ctx, raw)
		if err != nil {
			return nil, fmt.Errorf("inputs.%s: %w", key, err)
		}
		resolved[key] = v
	}
	return resolved, nil
}

// evaluatePhase2 resolves an edge's Passes block against the run context.
// Same shape as evaluatePhase1: ${input.X} forbidden at load time, not
// re-checked here.
func evaluatePhase2(ctx *RunContext, passes map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(passes))
	for key, raw := range passes {
		v, err := resolveValue(ctx, raw)
		if err != nil {
			return nil, fmt.Errorf("passes.%s: %w", key, err)
		}
		resolved[key] = v
	}
	return resolved, nil
}

// resolveValue dispatches on the raw value's type: strings go through
// the resolver (which handles ${...} substitution + literal fallback);
// other types pass through unchanged. Nested maps are walked
// recursively (for emit Output.Data).
func resolveValue(ctx *RunContext, raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return v, nil
		}
		return ctx.Resolve(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, vv := range v {
			rv, err := resolveValue(ctx, vv)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = rv
		}
		return out, nil
	default:
		return raw, nil
	}
}

// mergeDispatchInputs implements Phase 3: build the merged input map
// for a dispatch. Order: workflow-top-level inputs (base), node inputs
// (overlay, may shadow), triggering edge passes (overlay, may shadow).
// Returns a new map; doesn't mutate inputs.
func mergeDispatchInputs(workflowInputs, nodeInputs, edgePasses map[string]any) map[string]any {
	merged := make(map[string]any, len(workflowInputs)+len(nodeInputs)+len(edgePasses))
	for k, v := range workflowInputs {
		merged[k] = v
	}
	for k, v := range nodeInputs {
		merged[k] = v
	}
	for k, v := range edgePasses {
		merged[k] = v
	}
	return merged
}

// dispatchContext returns a RunContext clone whose Inputs is replaced
// by the merged dispatch map. Used for Phase 5 evaluations (prompts,
// emit output, edge prompt template) so ${input.X} reads the merged
// map. The original base context is unchanged; Outputs, Env, Tree, etc.
// share by reference.
func dispatchContext(base *RunContext, merged map[string]any) *RunContext {
	cloned := *base
	cloned.Inputs = merged
	return &cloned
}

func isOptionalResolveError(err error) bool {
	return errors.Is(err, ErrInputNotFound) || errors.Is(err, ErrNodeOutputNotFound) || errors.Is(err, ErrNodeDataNotFound)
}

func resolveWorkspace(runDir string, workflow *definitions.Workflow, node *definitions.Node, runContext *RunContext) (string, error) {
	workspace := node.Workspace
	if workspace == nil {
		workspace = workflow.WorkspaceDefaults
	}
	if workspace == nil {
		return ensureDir(filepath.Join(runDir, "workspaces", "nodes", node.ID))
	}
	switch workspace.Mode {
	case workspaceModeShared:
		return ensureDir(filepath.Join(runDir, "workspaces", workspaceModeShared))
	case "group":
		if workspace.Group == "" {
			return "", fmt.Errorf("workspace group required")
		}
		return ensureDir(filepath.Join(runDir, "workspaces", "groups", workspace.Group))
	case workspaceModeProject:
		if workspace.Path == "" {
			return "", fmt.Errorf("workspace path required")
		}
		path, err := resolveWorkspacePath(workspace.Path, runContext)
		if err != nil {
			return "", err
		}
		return ensureDir(path)
	default:
		return ensureDir(filepath.Join(runDir, "workspaces", "nodes", node.ID))
	}
}

// resolveWorkspacePath turns a workspace.path string into a concrete
// filesystem path. The path is run through the run-context expression
// resolver so ${env.X}, ${input.X}, and other namespaced expressions
// all resolve correctly at dispatch time.
func resolveWorkspacePath(raw string, runContext *RunContext) (string, error) {
	if raw == "" {
		return "", nil
	}
	if runContext == nil {
		return raw, nil
	}
	v, err := runContext.Resolve(raw)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("workspace path must resolve to a string, got %T", v)
	}
	return s, nil
}

// mergeSecretsIntoEnv copies secret values into the env map so they are
// available as environment variables in shell subprocesses.
func mergeSecretsIntoEnv(env, secrets map[string]string) {
	for k, v := range secrets {
		env[k] = v
	}
}

// checkRequiredSecrets verifies that all keys listed in inputs["secret_keys"]
// are present in the run state's Secrets map. Returns an error listing any
// missing keys.
func checkRequiredSecrets(rs *state.RunState, inputs map[string]any) error {
	raw, ok := inputs["secret_keys"]
	if !ok {
		return nil
	}
	keys, ok := raw.([]any)
	if !ok {
		return nil
	}
	var missing []string
	for _, k := range keys {
		key, ok := k.(string)
		if !ok {
			continue
		}
		if rs.Secrets == nil || rs.Secrets[key] == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required secrets: %s", strings.Join(missing, ", "))
	}
	return nil
}

// recordPreDispatchFailure records a node failure that occurs before the node
// is dispatched (e.g. missing required secrets). Without this, the node would
// never appear in the nodes map and no node_failed event would be emitted.
func recordPreDispatchFailure(runState *state.RunState, logger *state.Logger, runID, stateNodeID string, err error) {
	now := time.Now().UTC()
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusFailed
		stateNode.Error = err.Error()
		stateNode.StartedAt = &now
	})
	_ = logger.Append(state.Event{
		Type: eventNodeFailed, RunID: runID, NodeID: stateNodeID,
		Data: map[string]any{keyError: err.Error()},
	})
}

// markNodeFailed sets the node to "failed" status and logs a node_failed event.
func markNodeFailed(runState *state.RunState, logger *state.Logger, runID, stateNodeID string, startTime time.Time, err error) {
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusFailed
		stateNode.Error = err.Error()
	})
	_ = logger.Append(state.Event{
		Type: eventNodeFailed, RunID: runID, NodeID: stateNodeID,
		DurationMs: durationMs(startTime, time.Now()),
		Data:       map[string]any{keyError: err.Error()},
	})
}

// markNodeCompleted sets the node to completed status, copies output
// fields to state, materializes the matched decision's Tags onto
// NodeState, and logs a node_completed event with the tags included.
//
// Tags are the cross-cutting signal used by dashboards, inspect, and
// `tree.tagged.<name>` expressions — materializing them at emit time
// lets downstream consumers read NodeState.Tags directly without
// having to re-resolve the matched decision from the workflow def.
// Live SSE consumers that want to react to specific tags filter
// node_completed events by event.data.tags.
func markNodeCompleted(runState *state.RunState, logger *state.Logger, runID, stateNodeID string, node *definitions.Node, startTime time.Time, output *NodeOutput) {
	ended := time.Now().UTC()
	var sessionID string
	var attempts int
	// Resolve the matched decision back to its full definition so we
	// can copy its Tags onto the node. nil decision here means the
	// node declares no decisions list (generic node) or the runner
	// returned a decision that wasn't declared — the latter is normally
	// caught earlier by output validation, so we degrade gracefully
	// by recording the node with no tags.
	var decisionTags []string
	if node != nil {
		if decision, ok := node.Decisions.Find(output.Decision); ok {
			decisionTags = append([]string(nil), decision.Tags...)
		}
	}

	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusCompleted
		stateNode.Decision = output.Decision
		stateNode.Message = output.Message
		stateNode.Data = output.Data
		stateNode.Artifacts = output.Artifacts
		stateNode.EndedAt = &ended
		stateNode.Tags = decisionTags
		sessionID = stateNode.SessionID
		attempts = stateNode.Attempts
	})
	// Copy engine-managed metadata onto the output so downstream
	// `node.X.<field>` resolver expressions surface it. (PRI-1574)
	output.Tags = decisionTags
	output.Status = statusCompleted
	output.Attempts = attempts
	eventData := map[string]any{fieldDecision: output.Decision, fieldSessionID: sessionID}
	if output.Message != "" {
		eventData[fieldMessage] = output.Message
	}
	if len(output.Data) > 0 {
		eventData[fieldData] = output.Data
	}
	if len(decisionTags) > 0 {
		eventData[fieldTags] = decisionTags
	}
	_ = logger.Append(state.Event{
		Type: eventNodeCompleted, RunID: runID, NodeID: stateNodeID,
		DurationMs: durationMs(startTime, ended),
		Data:       eventData,
	})
}

func ensureDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// lastLines returns the last n non-empty lines from s, trimmed.
func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:], "\n")
}
