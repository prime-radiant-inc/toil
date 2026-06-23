package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

const defaultNoProgressLimit = 3

func dispatchHashForNode(nodeID string, edgePrompt string, inputs map[string]any) (string, error) {
	payload := map[string]any{
		keyNodeID:     nodeID,
		"edge_prompt": edgePrompt,
		keyInputs:     inputs,
	}
	return hashStable(payload)
}

func outputHashForNode(output NodeOutput) (string, error) {
	payload := map[string]any{
		fieldDecision: output.Decision,
		fieldMessage:  output.Message,
		fieldData:     sanitizeOutputData(output.Data),
	}
	return hashStable(payload)
}

func hashStable(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sanitizeOutputData(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, nested := range typed {
			if key == dataKeyChildRun {
				continue
			}
			copy[key] = sanitizeOutputData(nested)
		}
		return copy
	case []any:
		copy := make([]any, 0, len(typed))
		for _, item := range typed {
			copy = append(copy, sanitizeOutputData(item))
		}
		return copy
	default:
		return value
	}
}

func noProgressLimit(workflow *definitions.Workflow) int {
	if workflow != nil && workflow.Limits != nil {
		if limit := workflow.Limits["max_no_progress_iterations"]; limit > 0 {
			return limit
		}
	}
	return defaultNoProgressLimit
}

func (engine *Engine) enforceCircuitBreaker(runID string, workflow *definitions.Workflow, node *definitions.Node, dispatchHash string, output NodeOutput, logger *state.Logger, runState *state.RunState, stateNodeID string) error {
	if stateNodeID == "" {
		stateNodeID = node.ID
	}

	outputHash, err := outputHashForNode(output)
	if err != nil {
		return err
	}

	limit := noProgressLimit(workflow)
	if limit <= 1 {
		return nil
	}

	noProgressCount := 0
	tripped := false
	message := ""
	runState.WithNode(stateNodeID, func(stateNode *state.NodeState) {
		sameDispatchAndOutput := stateNode.LastDispatchHash == dispatchHash && stateNode.LastOutputHash == outputHash
		madeToolCalls := output.ToolCalls > 0

		if sameDispatchAndOutput && !madeToolCalls {
			// Identical structured output with no tool calls = stuck.
			stateNode.NoProgressCount++
		} else {
			// Different output OR the agent did real work (tool calls)
			// even if the structured output looks the same.
			stateNode.NoProgressCount = 1
		}
		stateNode.LastDispatchHash = dispatchHash
		stateNode.LastOutputHash = outputHash
		noProgressCount = stateNode.NoProgressCount
		if noProgressCount >= limit {
			tripped = true
			message = fmt.Sprintf("circuit breaker tripped: identical dispatch/output repeated %d times (limit %d)", noProgressCount, limit)
			ended := time.Now().UTC()
			stateNode.Status = statusFailed
			stateNode.Message = message
			stateNode.EndedAt = &ended
		}
	})

	if !tripped {
		return nil
	}

	_ = logger.Append(state.Event{
		Type:   "circuit_breaker_tripped",
		RunID:  runID,
		NodeID: stateNodeID,
		Data: map[string]any{
			"dispatch_hash":        dispatchHash,
			"output_hash":          outputHash,
			"no_progress_count":    noProgressCount,
			"limit":                limit,
			"root_cause":           "identical_dispatch_and_output",
			"suggested_resolution": "provide new inputs or feedback before retrying",
		},
	})
	_ = logger.Append(state.Event{Type: eventNodeFailed, RunID: runID, NodeID: stateNodeID, Data: map[string]any{keyError: message}})
	return errors.New(message)
}
