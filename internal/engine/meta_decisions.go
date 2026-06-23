package engine

import (
	"fmt"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// MetaDecisionLoopExhausted is emitted when a node's iteration counter
// hits the workflow's max_loop_iterations limit AND an outgoing edge
// with `when: _loop_exhausted` exists. The node's real
// Decision/Message/Data are preserved; LastRoutingDecision tracks the
// synthetic routing for crash-resume and observability.
const MetaDecisionLoopExhausted = "_loop_exhausted"

// MetaDecisionTimeout is emitted when an approval gate's TimeoutSec
// fires before resolution and an outgoing edge with `when: _timeout`
// exists.
const MetaDecisionTimeout = "_timeout"

// MetaDecisionRetryExhausted is emitted when a node's retry budget is
// reached without a successful attempt AND an outgoing edge with
// `when: _retry_exhausted` exists. Distinguishes "every retry failed"
// from a first-attempt failure (the latter still routes via
// `when: status == 'failed'` edges). Real Decision/Message/Data are
// preserved per the same crash-safety semantics as _loop_exhausted
// and _timeout.
const MetaDecisionRetryExhausted = "_retry_exhausted"

// synthesizeMetaCompletion records a meta-decision on NodeState
// (LastRoutingDecision + LastRoutingAt; Decision/Message/Data
// unchanged), emits a synthetic node_completed event, and fires
// downstream edges via applyOutputRouting. The synthetic decision
// routes through the standard edge-matching path so passes: blocks
// on meta-decision edges work uniformly.
//
// LoopIterations is NOT reset here — it stays at the exhausted value
// so meta-decision edge passes and downstream emit nodes reading
// ${node.X.loop_iterations} see the real count. Reset is deferred to
// the next dispatch's pre-increment when LastRoutingDecision ==
// MetaDecisionLoopExhausted (see runLoop's counter path).
func synthesizeMetaCompletion(
	runID string,
	workflow *definitions.Workflow,
	runContext *RunContext,
	runState *state.RunState,
	logger *state.Logger,
	nodeID string,
	metaDecision string,
	ready *[]readyNode,
	arrivedEdges map[string]map[string]bool,
	incomingEdgeCount map[string]int,
	joinFired map[string]bool,
) error {
	now := time.Now().UTC()
	var loopIterations int
	runState.WithNode(nodeID, func(n *state.NodeState) {
		n.LastRoutingDecision = metaDecision
		n.LastRoutingAt = &now
		loopIterations = n.LoopIterations
		// Decision/Message/Data/Artifacts intentionally unchanged.
		// LoopIterations intentionally unchanged (reset deferred to
		// next dispatch).
	})

	// Mirror the routing fields onto runContext.Outputs[nodeID] so the
	// resolver can surface ${node.X.last_routing_decision} and
	// ${node.X.loop_iterations} on downstream edge passes / emit outputs.
	// Decision/Message/Data/Artifacts continue to hold the last real envelope.
	if existing, ok := runContext.Outputs[nodeID]; ok {
		existing.LastRoutingDecision = metaDecision
		existing.LoopIterations = loopIterations
		runContext.Outputs[nodeID] = existing
	}

	_ = logger.Append(state.Event{
		Type: eventNodeCompleted, RunID: runID, NodeID: nodeID,
		Data: map[string]any{
			fieldDecision: metaDecision,
			"synthetic":   true,
			"meta":        metaDecision,
		},
	})
	if err := applyOutputRouting(workflow, runContext, runState, nodeID, metaDecision, ready, arrivedEdges, incomingEdgeCount, joinFired, logger, runID); err != nil {
		return fmt.Errorf("meta-decision %s on %s: %w", metaDecision, nodeID, err)
	}
	return nil
}
