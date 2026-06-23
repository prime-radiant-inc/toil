package engine

import (
	"fmt"
	"time"

	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

type approvalOutcome struct {
	nodeID string
	output NodeOutput
}

func (engine *Engine) processApprovals(runID string, runDir string, workflow *definitions.Workflow, nodeIDs []readyNode, runContext *RunContext, logger *state.Logger, runState *state.RunState) ([]approvalOutcome, []string, []readyNode, bool, error) {
	runnable := []readyNode{}
	outcomes := []approvalOutcome{}
	var timedOutNodeIDs []string
	pending := false

	for _, ready := range nodeIDs {
		node := definitions.FindNode(workflow, ready.ID)
		if node == nil {
			return nil, nil, nil, false, fmt.Errorf("node not found: %s", ready.ID)
		}
		if node.Gate != keyRequired {
			runnable = append(runnable, ready)
			continue
		}

		output, resolved, timedOut, err := engine.approvalOutput(runID, runDir, node, ready.EdgePrompt, ready.Passes, runContext, logger, runState)
		if err != nil {
			return nil, nil, nil, false, err
		}
		switch {
		case timedOut:
			timedOutNodeIDs = append(timedOutNodeIDs, ready.ID)
		case resolved:
			outcomes = append(outcomes, approvalOutcome{nodeID: ready.ID, output: output})
		default:
			pending = true
		}
	}

	return outcomes, timedOutNodeIDs, runnable, pending, nil
}

// approvalOutput checks the state of a pending approval gate and returns
// (output, resolved, timedOut, err). Exactly one of resolved and timedOut
// will be true when the gate has been handled; both are false when the
// approval is still pending and the caller should wait.
func (engine *Engine) approvalOutput(runID string, runDir string, node *definitions.Node, edgePrompt string, edgePasses map[string]any, runContext *RunContext, logger *state.Logger, runState *state.RunState) (NodeOutput, bool, bool, error) {
	status := ""
	attempts := 0
	firstRun := false
	runState.WithNode(node.ID, func(stateNode *state.NodeState) {
		status = stateNode.Status
		attempts = stateNode.Attempts
		firstRun = attempts == 0
	})
	if status == statusAwaitingApproval && attempts > 0 {
		approvalID := approvals.BuildID(runID, node.ID, attempts)
		approval, err := approvals.Load(runDir, approvalID)
		if err != nil {
			return NodeOutput{}, false, false, err
		}
		if approval.Status == statusPending {
			// Check timeout before trying the Approver.
			// On timeout, mark the node completed and return timedOut=true so the
			// caller can invoke synthesizeMetaCompletion(_timeout) for routing.
			timedOut, err := checkApprovalTimeout(approval, runDir)
			if err != nil {
				return NodeOutput{}, false, false, err
			}
			if timedOut {
				markApprovalNodeTimedOut(runID, node.ID, approval, logger, runState)
				return NodeOutput{}, false, true, nil
			}
			if resolved, output, err := engine.tryResolveApproval(runDir, runID, node.ID, approval, logger, runState); err != nil {
				return NodeOutput{}, false, false, err
			} else if resolved {
				return output, true, false, nil
			}
			return NodeOutput{}, false, false, nil
		}
		if approval.Status == approvalTimedOut {
			// Already timed out (e.g. resumed after a previous timeout cycle that
			// did not complete routing). Re-signal timedOut so the caller retries
			// synthesizeMetaCompletion(_timeout).
			return NodeOutput{}, false, true, nil
		}
		output, err := approvalToOutput(approval)
		if err != nil {
			return NodeOutput{}, false, false, err
		}
		applyApprovalOutput(runID, node.ID, output, approval, logger, runState)
		return output, true, false, nil
	}

	attempt := 0
	runState.WithNode(node.ID, func(stateNode *state.NodeState) {
		stateNode.Attempts++
		attempt = stateNode.Attempts
		stateNode.Status = statusAwaitingApproval
	})
	approvalID := approvals.BuildID(runID, node.ID, attempt)
	nodeInputs, err := evaluatePhase1(runContext, node.Inputs)
	if err != nil {
		return NodeOutput{}, false, false, err
	}
	// Phase 3: merge workflow inputs + node inputs + edge passes into the
	// dispatch map, mirroring the standard 5-phase pipeline so that
	// ${input.X} references in the approval question can read edge passes.
	merged := mergeDispatchInputs(runContext.Inputs, nodeInputs, edgePasses)
	question, err := engine.composeApprovalQuestion(node, edgePrompt, firstRun, merged)
	if err != nil {
		return NodeOutput{}, false, false, err
	}
	approval := &approvals.Approval{
		ID:         approvalID,
		RunID:      runID,
		NodeID:     node.ID,
		Attempt:    attempt,
		Status:     statusPending,
		Question:   question,
		Choices:    node.Decisions.IDs(),
		TimeoutSec: node.TimeoutSec,
	}
	if err := approvals.Create(runDir, approval); err != nil {
		return NodeOutput{}, false, false, err
	}
	_ = logger.Append(state.Event{Type: "approval_requested", RunID: runID, NodeID: node.ID, Data: map[string]any{keyApprovalID: approvalID, "question": question}})

	// Try immediate resolution via Approver
	if resolved, output, err := engine.tryResolveApproval(runDir, runID, node.ID, approval, logger, runState); err != nil {
		return NodeOutput{}, false, false, err
	} else if resolved {
		return output, true, false, nil
	}
	return NodeOutput{}, false, false, nil
}

func approvalToOutput(approval *approvals.Approval) (NodeOutput, error) {
	if approval.Decision == "" {
		return NodeOutput{}, fmt.Errorf("approval %s: decision required", approval.ID)
	}
	if approval.Message == "" {
		return NodeOutput{}, fmt.Errorf("approval %s: message required", approval.ID)
	}

	data := map[string]any{}
	if approval.Comment != "" {
		data["approval_comment"] = approval.Comment
	}

	return NodeOutput{
		Decision: approval.Decision,
		Message:  approval.Message,
		Data:     data,
	}, nil
}

// markApprovalNodeTimedOut updates NodeState to reflect that the approval gate
// timed out and will be routed via synthesizeMetaCompletion(_timeout). The node
// is marked completed (no Decision/Message — the meta-decision carries routing)
// and an approval_timed_out event is emitted. synthesizeMetaCompletion will
// then emit node_completed with decision=_timeout and fire downstream edges.
func markApprovalNodeTimedOut(runID string, nodeID string, approval *approvals.Approval, logger *state.Logger, runState *state.RunState) {
	ended := time.Now().UTC()
	runState.WithNode(nodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusCompleted
		stateNode.EndedAt = &ended
	})
	_ = logger.Append(state.Event{Type: "approval_timed_out", RunID: runID, NodeID: nodeID, Data: map[string]any{keyApprovalID: approval.ID}})
}

func applyApprovalOutput(runID string, nodeID string, output NodeOutput, approval *approvals.Approval, logger *state.Logger, runState *state.RunState) {
	ended := time.Now().UTC()
	runState.WithNode(nodeID, func(stateNode *state.NodeState) {
		stateNode.Status = statusCompleted
		stateNode.Decision = output.Decision
		stateNode.Message = output.Message
		stateNode.Data = output.Data
		stateNode.EndedAt = &ended
	})
	_ = logger.Append(state.Event{Type: "approval_resolved", RunID: runID, NodeID: nodeID, Data: map[string]any{keyApprovalID: approval.ID, fieldDecision: output.Decision}})
	_ = logger.Append(state.Event{Type: eventNodeCompleted, RunID: runID, NodeID: nodeID, Data: map[string]any{fieldDecision: output.Decision}})
}

// checkApprovalTimeout checks if a pending approval has exceeded its timeout.
// Returns true if the approval has timed out (fire-condition: TimeoutSec > 0
// and elapsed time >= TimeoutSec). The approval is persisted with status
// "timed_out" so the caller can invoke synthesizeMetaCompletion(_timeout)
// for routing. Decision/Message are intentionally left empty — meta-decision
// routing does not require a normal approval resolution.
func checkApprovalTimeout(approval *approvals.Approval, runDir string) (bool, error) {
	if approval.TimeoutSec <= 0 {
		return false, nil
	}
	if approval.CreatedAt.IsZero() {
		return false, nil
	}
	elapsed := time.Since(approval.CreatedAt)
	if elapsed < time.Duration(approval.TimeoutSec)*time.Second {
		return false, nil
	}
	// Re-read from disk to guard against concurrent resolution (e.g., dashboard).
	current, err := approvals.Load(runDir, approval.ID)
	if err != nil {
		return false, err
	}
	if current.Status != statusPending {
		// Already resolved externally — do not override with timeout.
		*approval = *current
		return false, nil
	}
	// Build timed-out copy — don't mutate until Save succeeds.
	timedOut := *approval
	now := time.Now().UTC()
	timedOut.Status = approvalTimedOut
	timedOut.Comment = fmt.Sprintf("timed out after %ds", approval.TimeoutSec)
	timedOut.ResolvedAt = &now
	if err := approvals.Save(runDir, &timedOut); err != nil {
		return false, err
	}
	*approval = timedOut
	return true, nil
}

func (engine *Engine) tryResolveApproval(runDir string, runID string, nodeID string, approval *approvals.Approval, logger *state.Logger, runState *state.RunState) (bool, NodeOutput, error) {
	if engine.Approver == nil {
		return false, NodeOutput{}, nil
	}
	resolution, err := engine.Approver.Resolve(approval)
	if err != nil {
		return false, NodeOutput{}, err
	}
	if resolution == nil {
		return false, NodeOutput{}, nil
	}
	// Re-read from disk to guard against concurrent resolution (e.g., dashboard).
	current, err := approvals.Load(runDir, approval.ID)
	if err != nil {
		return false, NodeOutput{}, err
	}
	if current.Status != statusPending {
		// Already resolved externally — use that resolution instead.
		*approval = *current
		output, err := approvalToOutput(approval)
		if err != nil {
			return false, NodeOutput{}, err
		}
		applyApprovalOutput(runID, nodeID, output, approval, logger, runState)
		return true, output, nil
	}
	// Build resolved copy — don't mutate until Save succeeds.
	resolved := *approval
	now := time.Now().UTC()
	resolved.Status = "resolved"
	resolved.Decision = resolution.Decision
	resolved.Message = resolution.Message
	resolved.Comment = resolution.Comment
	resolved.ResolvedAt = &now
	if err := approvals.Save(runDir, &resolved); err != nil {
		return false, NodeOutput{}, err
	}
	*approval = resolved
	output, err := approvalToOutput(approval)
	if err != nil {
		return false, NodeOutput{}, err
	}
	applyApprovalOutput(runID, nodeID, output, approval, logger, runState)
	return true, output, nil
}

func (engine *Engine) composeApprovalQuestion(node *definitions.Node, edgePrompt string, firstRun bool, inputs map[string]any) (string, error) {
	rolePrompt, nodePrompt := selectPrompts(firstRun, "", node.Prompt, edgePrompt, true)
	return ComposeQuestion(rolePrompt, nodePrompt, inputs, node.Decisions)
}
