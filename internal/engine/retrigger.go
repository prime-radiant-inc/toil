package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// downstreamNodes computes the set of all nodes reachable from startNodeID
// (inclusive) via BFS over the workflow's edges. It handles cycles safely.
func downstreamNodes(workflow *definitions.Workflow, startNodeID string) map[string]bool {
	reachable := map[string]bool{}
	queue := []string{startNodeID}
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
	return reachable
}

// resetNodeState clears execution state on a node while preserving its Data.
func resetNodeState(node *state.NodeState) {
	node.Status = statusPending
	node.Decision = ""
	node.Message = ""
	node.Error = ""
	node.Artifacts = nil
	node.StartedAt = nil
	node.EndedAt = nil
	node.SessionID = ""
	node.LastDispatchHash = ""
	node.LastOutputHash = ""
	node.Attempts = 0
	node.RetryCount = 0
	node.NoProgressCount = 0
	node.LoopIterations = 0
	node.LastRoutingDecision = ""
	node.LastRoutingAt = nil
	delete(node.Data, "child_run")
	delete(node.Data, "failure_context")
}

// forEachParentID extracts the parent node ID from a ForEach expanded child
// ID (e.g. "process::0" → "process"). Returns empty string if the ID is not
// a ForEach child.
func forEachParentID(nodeID string) string {
	if idx := strings.Index(nodeID, "::"); idx >= 0 {
		return nodeID[:idx]
	}
	return ""
}

// RetriggerNode resets a target node and all its downstream dependents so the
// run can be resumed from that point. The run must be in a terminal state
// (failed or completed) and the node must exist in the workflow definition.
func (engine *Engine) RetriggerNode(runID, nodeID string) error {
	runDir := filepath.Join(engine.RunsDir, runID)
	runState, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Run must be in a terminal state to retrigger.
	if runState.Status != statusFailed && runState.Status != statusCompleted {
		return fmt.Errorf("cannot retrigger run %s: status %q is not terminal (must be failed or completed)", runID, runState.Status)
	}

	workflow, err := engine.loadWorkflowSnapshot(runDir, runState.WorkflowID)
	if err != nil {
		return fmt.Errorf("load workflow: %w", err)
	}

	if definitions.FindNode(workflow, nodeID) == nil {
		return fmt.Errorf("node %q not found in workflow %q", nodeID, workflow.ID)
	}

	// Compute the set of nodes that need to be reset.
	downstream := downstreamNodes(workflow, nodeID)

	// Open event logger.
	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), engine.eventStdout())
	if err != nil {
		return fmt.Errorf("open event logger: %w", err)
	}
	defer func() { _ = logger.Close() }()

	// Build a map from template node ID -> orchestrator ID so that expanded
	// children (e.g. "process_item::0") can be reset when their orchestrator
	// ("process") is downstream.
	templateToOrch := map[string]string{}
	for _, n := range workflow.Nodes {
		if n.ForEach != nil && n.ForEach.Body != "" {
			templateToOrch[n.ForEach.Body] = n.ID
		}
	}

	// Reset all downstream nodes and their ForEach children.
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, node := range nodes {
			inDownstream := downstream[id]
			// Also reset ForEach expanded children whose orchestrator is downstream.
			// Expanded IDs use the template prefix (e.g. "process_item::0").
			if !inDownstream {
				if prefix := forEachParentID(id); prefix != "" {
					if orch := templateToOrch[prefix]; orch != "" && downstream[orch] {
						inDownstream = true
					}
				}
			}
			if !inDownstream {
				continue
			}
			resetNodeState(node)
			if id == nodeID {
				node.Status = statusRetrying
			}
		}
	})

	// Reset join state for downstream nodes.
	for id := range downstream {
		if arrivals := runState.GetJoinState(id); len(arrivals) > 0 {
			runState.SetJoinState(id, nil)
		}
	}

	// Log retrigger event.
	_ = logger.Append(state.Event{
		Type:   "node_retriggered",
		RunID:  runID,
		NodeID: nodeID,
		Data: map[string]any{
			"downstream_count": len(downstream),
		},
	})

	// Set run back to running and clear stale narrative fields.
	runState.Status = statusRunning
	runState.Totals = nil // retriggered run's totals are stale until next terminal save
	runState.StartedAt = time.Now().UTC()
	runState.Error = ""
	runState.FinishedAt = nil
	runState.Summary = ""
	runState.Description = ""

	// Emit run_resumed so SSE clients update the status badge.
	_ = logger.Append(state.Event{
		Type:  "run_resumed",
		RunID: runID,
	})

	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	return nil
}
