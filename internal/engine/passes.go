package engine

import (
	"fmt"
	"sort"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// applyOutputRouting fires the standard routing path for a node's output:
// matches outgoing edges, evaluates each matched edge's passes block
// SYNCHRONOUSLY against the current runContext, and routes to either
// ready[] (non-join target) or JoinState.Passes[edgeIndex] (join target).
//
// This is the single seam for both real completions and meta-decision
// emissions; applyOutput (legacy) becomes a thin wrapper that calls here.
func applyOutputRouting(
	workflow *definitions.Workflow,
	runContext *RunContext,
	runState *state.RunState,
	nodeID string,
	decision string,
	ready *[]readyNode,
	arrivedEdges map[string]map[string]bool,
	incomingEdgeCount map[string]int,
	joinFired map[string]bool,
	logger *state.Logger,
	runID string,
) error {
	// ForEach expanded items carry IDs like "template::N". Edges are declared
	// against the template, so resolve the prefix before matching (mirrors
	// matchEdgesExpr behaviour).
	matchID := nodeID
	if idx := strings.Index(nodeID, "::"); idx > 0 {
		matchID = nodeID[:idx]
	}

	evalCtx := &EvalContext{Decision: decision, Status: statusCompleted, Resolve: runContext.Resolve}
	matchedAny := false

	// First pass: explicit matches (plain or expression when).
	for i, e := range workflow.Edges {
		if e.From != matchID {
			continue
		}
		var fires bool
		if IsExpression(e.When) {
			ok, err := EvalEdgeExpr(e.When, evalCtx)
			fires = err == nil && ok
		} else if e.When != decisionDefault && e.When != "" {
			fires = e.When == decision
		}
		// empty/default handled in second pass below
		if !fires {
			continue
		}
		matchedAny = true
		if err := routeEdge(workflow, runContext, runState, nodeID, i, e, ready, arrivedEdges, incomingEdgeCount, joinFired); err != nil {
			return err
		}
		if e.Failed != nil && *e.Failed && logger != nil {
			_ = logger.Append(state.Event{
				Type:   "failure_edge_fired",
				RunID:  runID,
				NodeID: e.From,
				Data: map[string]any{
					"to":          e.To,
					"when":        e.When,
					fieldDecision: evalCtx.Decision,
				},
			})
		}
	}

	if matchedAny {
		return nil
	}

	// Second pass: default/empty fallback — not for failed nodes.
	if decision == statusFailed {
		return nil
	}
	for i, e := range workflow.Edges {
		if e.From != matchID {
			continue
		}
		if e.When != decisionDefault && e.When != "" {
			continue
		}
		if err := routeEdge(workflow, runContext, runState, nodeID, i, e, ready, arrivedEdges, incomingEdgeCount, joinFired); err != nil {
			return err
		}
	}
	return nil
}

// routeEdge evaluates passes for a single matched edge and appends to
// ready or updates JoinState as appropriate.
func routeEdge(
	workflow *definitions.Workflow,
	runContext *RunContext,
	runState *state.RunState,
	fromNodeID string,
	edgeIdx int,
	e definitions.Edge,
	ready *[]readyNode,
	arrivedEdges map[string]map[string]bool,
	incomingEdgeCount map[string]int,
	joinFired map[string]bool,
) error {
	evaluated, err := evaluatePhase2(runContext, e.Passes)
	if err != nil {
		return fmt.Errorf("edge %d (%s -> %s): %w", edgeIdx, e.From, e.To, err)
	}
	targetNode := definitions.FindNode(workflow, e.To)
	if targetNode != nil && targetNode.Join == joinAll {
		if arrivedEdges[e.To] == nil {
			arrivedEdges[e.To] = map[string]bool{}
		}
		arrivedEdges[e.To][fromNodeID] = true
		runState.SetJoinPasses(e.To, edgeIdx, evaluated)
		if len(arrivedEdges[e.To]) >= incomingEdgeCount[e.To] && !joinFired[e.To] {
			joinFired[e.To] = true
			*ready = append(*ready, readyNode{
				ID:         e.To,
				EdgePrompt: e.Prompt,
				FromNodeID: fromNodeID,
				EdgeIndex:  edgeIdx,
				Passes:     evaluated,
			})
		}
		return nil
	}
	*ready = append(*ready, readyNode{
		ID:         e.To,
		EdgePrompt: e.Prompt,
		FromNodeID: fromNodeID,
		EdgeIndex:  edgeIdx,
		Passes:     evaluated,
	})
	return nil
}

// findEdgeIndex returns the position of edge e in workflow.Edges, or -1
// if not found. Identifies the edge by (From, To, When) since edges
// are unique per that triple in well-formed workflows.
func findEdgeIndex(workflow *definitions.Workflow, e definitions.Edge) int {
	for i, candidate := range workflow.Edges {
		if candidate.From == e.From && candidate.To == e.To && candidate.When == e.When {
			return i
		}
	}
	return -1
}

// mergeJoinPasses combines all per-edge passes maps into a single map
// ordered by edge index ASC (highest-index wins on key overlap). Used
// at dispatch time when the target is a join node, instead of the
// single-edge readyNode.Passes.
func mergeJoinPasses(js *state.JoinNodeState) map[string]any {
	if js == nil || len(js.Passes) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(js.Passes))
	for i := range js.Passes {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	merged := map[string]any{}
	for _, i := range indexes {
		for k, v := range js.Passes[i] {
			merged[k] = v
		}
	}
	return merged
}
