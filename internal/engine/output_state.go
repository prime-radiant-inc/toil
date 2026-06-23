package engine

import "primeradiant.com/toil/internal/state"

func lastOutputFromState(runState *state.RunState) (NodeOutput, bool) {
	var lastNode *state.NodeState
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, node := range nodes {
			if node.Status != statusCompleted || node.EndedAt == nil {
				continue
			}
			if lastNode == nil || node.EndedAt.After(*lastNode.EndedAt) {
				lastNode = node
			}
		}
	})
	if lastNode == nil {
		return NodeOutput{}, false
	}

	return NodeOutput{
		Decision:  lastNode.Decision,
		Message:   lastNode.Message,
		Artifacts: lastNode.Artifacts,
		Data:      lastNode.Data,
		SessionID: lastNode.SessionID,
	}, true
}
