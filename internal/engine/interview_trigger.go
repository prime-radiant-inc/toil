package engine

import (
	"sort"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// InterviewableNode represents a workflow node that is a candidate for
// post-run interview. Only nodes backed by an agent session (i.e. those
// with a SessionID in their runtime state) qualify.
type InterviewableNode struct {
	NodeID    string `json:"node_id"`
	RoleID    string `json:"role_id"`
	SessionID string `json:"session_id"`
	Outcome   string `json:"outcome"`
	Attempts  int    `json:"attempts"`
}

// collectInterviewableNodes walks the workflow's nodes, checks each one's
// runtime state, and returns those that have an agent session. The result
// is sorted by priority: failed > retried (attempts > 1) > succeeded.
func collectInterviewableNodes(runState *state.RunState, workflow *definitions.Workflow) []InterviewableNode {
	var candidates []InterviewableNode

	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, wfNode := range workflow.Nodes {
			ns, ok := nodes[wfNode.ID]
			if !ok || ns.SessionID == "" {
				continue
			}

			outcome := outcomeFor(ns)
			candidates = append(candidates, InterviewableNode{
				NodeID:    wfNode.ID,
				RoleID:    wfNode.Role,
				SessionID: ns.SessionID,
				Outcome:   outcome,
				Attempts:  ns.Attempts,
			})
		}
	})

	// Second pass: collect per-item sessions for role-kind ForEach templates.
	// Expanded state IDs use the form "<templateID>::<index>" and are not
	// covered by the loop above (which only looks up declared node IDs).
	roleTemplates := map[string]*definitions.Node{}
	for i := range workflow.Nodes {
		n := &workflow.Nodes[i]
		if n.Kind != kindRole {
			continue
		}
		for _, other := range workflow.Nodes {
			if other.ForEach != nil && other.ForEach.Body == n.ID {
				roleTemplates[n.ID] = n
				break
			}
		}
	}

	if len(roleTemplates) > 0 {
		runState.WithNodes(func(nodes map[string]*state.NodeState) {
			for id, ns := range nodes {
				idx := strings.Index(id, "::")
				if idx <= 0 {
					continue
				}
				prefix := id[:idx]
				tmpl, ok := roleTemplates[prefix]
				if !ok || ns.SessionID == "" {
					continue
				}
				candidates = append(candidates, InterviewableNode{
					NodeID:    id,
					RoleID:    tmpl.Role,
					SessionID: ns.SessionID,
					Outcome:   outcomeFor(ns),
					Attempts:  ns.Attempts,
				})
			}
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return outcomePriority(candidates[i].Outcome) < outcomePriority(candidates[j].Outcome)
	})

	return candidates
}

// hasRetriedNodes returns true if any candidate node required more than one attempt.
func hasRetriedNodes(nodes []InterviewableNode) bool {
	for _, n := range nodes {
		if n.Attempts > 1 {
			return true
		}
	}
	return false
}

// outcomeFor classifies a node's result for interview prioritization.
func outcomeFor(ns *state.NodeState) string {
	if ns.Status == statusFailed || ns.Status == statusFailedHandled {
		return outcomeFailed
	}
	if ns.Attempts > 1 {
		return outcomeRetried
	}
	return outcomeSucceeded
}

// outcomePriority returns a sort key: lower = higher priority for interviews.
func outcomePriority(outcome string) int {
	switch outcome {
	case outcomeFailed:
		return 0
	case outcomeRetried:
		return 1
	default:
		return 2
	}
}

// hasDirectFailedNode returns true if any non-subworkflow node has Status = failed.
// Used to distinguish "structural failure routing only" (no candidates needed)
// from "actual node-level failure" (candidates appropriate).
func hasDirectFailedNode(runState *state.RunState, workflow *definitions.Workflow) bool {
	nodeByID := make(map[string]*definitions.Node, len(workflow.Nodes))
	for i := range workflow.Nodes {
		nodeByID[workflow.Nodes[i].ID] = &workflow.Nodes[i]
	}
	var found bool
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for nodeID, ns := range nodes {
			if ns.Status != statusFailed {
				continue
			}
			wfNode := nodeByID[strippedNodeID(nodeID)]
			if wfNode != nil && wfNode.Kind != kindSubworkflow {
				found = true
				return
			}
		}
	})
	return found
}

// failureCausedBySubworkflow returns true if the run's failure was caused
// entirely by subworkflow nodes failing (i.e. the failure propagated up from
// child runs). In that case the child runs handle their own interviews and
// the parent should not duplicate them.
func failureCausedBySubworkflow(runState *state.RunState, workflow *definitions.Workflow) bool {
	hasFailedSubworkflow := false
	hasFailedDirect := false

	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, ns := range nodes {
			if ns.Status != statusFailed {
				continue
			}
			wfNode := definitions.FindNode(workflow, ns.ID)
			if wfNode != nil && wfNode.Kind == kindSubworkflow {
				hasFailedSubworkflow = true
			} else {
				hasFailedDirect = true
			}
		}
	})

	return hasFailedSubworkflow && !hasFailedDirect
}

// maybeEmitInterviewCandidates checks the workflow's interview mode and,
// if triggered, collects interviewable nodes and emits an
// "interview_candidates" event. If the engine's OnInterviewCandidates
// callback is set, it is invoked fire-and-forget to spawn the learn workflow.
// Called after run completion or failure.
func (engine *Engine) maybeEmitInterviewCandidates(runID string, runDir string, runState *state.RunState, workflow *definitions.Workflow, logger *state.Logger) {
	mode := workflow.InterviewMode()
	if mode == definitions.InterviewNever {
		return
	}

	// Skip interviews on parent workflows when the failure was caused by a
	// child subworkflow. The child run triggers its own interviews at the
	// level where the actual failure occurred.
	if failureCausedBySubworkflow(runState, workflow) {
		return
	}

	nodes := collectInterviewableNodes(runState, workflow)
	if len(nodes) == 0 {
		return
	}

	// Filter based on interview mode. Each mode's suppression logic is
	// independent — don't apply a blanket guard before the switch.
	switch mode {
	case definitions.InterviewOnFailure:
		if runState.Status != statusFailed && !runState.HasUnresolvedFailure {
			return
		}
		// Suppress interview emission when failure is purely from structural
		// routing (no direct node failed). Interview candidates are meant to
		// investigate actual node-level failures, not edge-level "we gave up"
		// routings. This guard applies only to on_failure mode; on_issue mode
		// must still fire for retried nodes regardless of HasUnresolvedFailure.
		if runState.HasUnresolvedFailure && !hasDirectFailedNode(runState, workflow) {
			return
		}
	case definitions.InterviewOnIssue:
		// on_issue fires when any node failed, had to retry, or there is an
		// unresolved-failure flag set. HasUnresolvedFailure alone (without a
		// direct failed node) should not suppress the retry-triggered path.
		if runState.Status != statusFailed && !runState.HasUnresolvedFailure && !hasRetriedNodes(nodes) {
			return
		}
	}
	createPendingInterviews(runDir, runID, workflow.ID, nodes)
	_ = logger.Append(state.Event{
		Type:  "interview_candidates",
		RunID: runID,
		Data: map[string]any{
			"nodes":       nodes,
			keyWorkflowID: workflow.ID,
		},
	})
	if engine.OnInterviewCandidates != nil {
		go engine.OnInterviewCandidates(runID, runDir, nodes)
	}
}
