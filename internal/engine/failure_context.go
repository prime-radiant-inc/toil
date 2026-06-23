package engine

import (
	"path/filepath"

	"primeradiant.com/toil/internal/state"
)

func buildFailureContext(runState *state.RunState, nodeID string, runsDir string) map[string]any {
	ctx := map[string]any{keyNodeID: nodeID}
	runState.WithNode(nodeID, func(n *state.NodeState) {
		ctx[fieldSessionID] = n.SessionID
		ctx["last_decision"] = n.Decision
		ctx["last_message"] = n.Message
		ctx[fieldAttempts] = n.Attempts
		ctx[keyError] = n.Error
		if childRun, ok := n.Data[dataKeyChildRun]; ok {
			ctx[dataKeyChildRun] = childRun
			if runID, ok := childRun.(string); ok {
				ctx["decision_history"] = extractDecisionHistory(runsDir, runID)
				ctx["failed_child"] = extractFailedChild(runsDir, runID)
			}
		}
	})
	return ctx
}

func extractDecisionHistory(runsDir string, runID string) []map[string]string {
	eventsPath := filepath.Join(runsDir, runID, "events.jsonl")
	events, err := state.ReadEvents(eventsPath)
	if err != nil {
		return nil
	}
	var history []map[string]string
	for _, e := range events {
		if e.Type != eventNodeCompleted {
			continue
		}
		decision, _ := e.Data[fieldDecision].(string)
		message, _ := e.Data[fieldMessage].(string)
		if decision == "" && message == "" {
			continue
		}
		msg := message
		if len(msg) > 200 {
			msg = msg[:200]
		}
		history = append(history, map[string]string{
			keyNode:       e.NodeID,
			fieldDecision: decision,
			fieldMessage:  msg,
		})
	}
	return history
}

func extractFailedChild(runsDir string, runID string) map[string]string {
	rs, err := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if err != nil {
		return nil
	}
	var result map[string]string
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, n := range nodes {
			if n.Status == statusFailed {
				result = map[string]string{
					keyNodeID:      n.ID,
					fieldMessage:   n.Message,
					keyError:       n.Error,
					fieldSessionID: n.SessionID,
				}
				break
			}
		}
	})
	return result
}
