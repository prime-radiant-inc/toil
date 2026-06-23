package engine

import (
	"log/slog"
	"time"

	"primeradiant.com/toil/internal/interviews"
	"primeradiant.com/toil/internal/state"
)

// createPendingInterviews writes a pending interview record for each candidate
// node into the source run's directory. Called from maybeEmitInterviewCandidates
// before the event/callback.
func createPendingInterviews(sourceRunDir, sourceRunID, workflowID string, nodes []InterviewableNode) {
	now := time.Now().UTC()
	for _, n := range nodes {
		if err := interviews.Create(sourceRunDir, &interviews.Interview{
			ID:                interviews.BuildID(sourceRunID, n.NodeID),
			RunID:             sourceRunID,
			NodeID:            n.NodeID,
			RoleID:            n.RoleID,
			WorkflowID:        workflowID,
			OriginalSessionID: n.SessionID,
			Status:            interviews.StatusPending,
			OriginalOutcome:   n.Outcome,
			OriginalAttempts:  n.Attempts,
			StartedAt:         &now,
		}); err != nil {
			slog.Error("toil.interview.record_create_failed", "run_id", sourceRunID, "node_id", n.NodeID, "error", err)
		}
	}
}

// maybeRecordInterviewResult updates (or creates) an interview record when
// a child subworkflow completes. Only acts when the child workflow is the
// "interview" workflow. Called from executeSubworkflow after successful
// completion.
func (engine *Engine) maybeRecordInterviewResult(parentRunState *state.RunState, childWorkflowID string, childRunID string, inputs map[string]any, output NodeOutput) {
	if childWorkflowID != "interview" {
		return
	}
	sourceRunDir, ok := parentRunState.Inputs["run_dir"].(string)
	if !ok || sourceRunDir == "" {
		return
	}
	nodeID, _ := inputs["node_id"].(string)
	if nodeID == "" {
		return
	}
	sourceRunID, _ := parentRunState.Inputs["run_id"].(string)

	now := time.Now().UTC()
	iv, err := interviews.Load(sourceRunDir, nodeID)
	if err != nil {
		// No pending record found; create one from what we have
		iv = &interviews.Interview{
			ID:     interviews.BuildID(sourceRunID, nodeID),
			RunID:  sourceRunID,
			NodeID: nodeID,
		}
	}
	iv.Status = interviews.StatusCompleted
	iv.InterviewSessionID = childRunID
	iv.CompletedAt = &now
	if output.Data != nil {
		if learnings, ok := output.Data[keyLearnings]; ok {
			iv.Responses = map[string]any{keyLearnings: learnings}
		}
	}
	if err != nil {
		// No existing record; Create ensures directory exists
		if createErr := interviews.Create(sourceRunDir, iv); createErr != nil {
			slog.Error("toil.interview.record_create_failed", "run_id", sourceRunID, "node_id", nodeID, "status", "completed", "error", createErr)
		}
	} else {
		if saveErr := interviews.Save(sourceRunDir, iv); saveErr != nil {
			slog.Error("toil.interview.record_save_failed", "run_id", iv.RunID, "node_id", nodeID, "status", "completed", "error", saveErr)
		}
	}
}

// maybeRecordInterviewFailure marks an interview record as failed when the
// interview subworkflow errors. Called from executeSubworkflow's error path.
func (engine *Engine) maybeRecordInterviewFailure(parentRunState *state.RunState, childWorkflowID string, inputs map[string]any, subworkflowErr error) {
	if childWorkflowID != "interview" {
		return
	}
	sourceRunDir, ok := parentRunState.Inputs["run_dir"].(string)
	if !ok || sourceRunDir == "" {
		return
	}
	nodeID, _ := inputs["node_id"].(string)
	if nodeID == "" {
		return
	}
	iv, loadErr := interviews.Load(sourceRunDir, nodeID)
	if loadErr != nil {
		// Unlike maybeRecordInterviewResult, we don't create a record from
		// scratch on failure — a failure record without the original context
		// (role, workflow, outcome) isn't useful for synthesis.
		return
	}
	iv.Status = interviews.StatusFailed
	iv.Error = subworkflowErr.Error()
	now := time.Now().UTC()
	iv.CompletedAt = &now
	if err := interviews.Save(sourceRunDir, iv); err != nil {
		slog.Error("toil.interview.record_save_failed", "run_id", iv.RunID, "node_id", nodeID, "status", "failed", "error", err)
	}
}
