package approvals

import (
	"path/filepath"
	"time"

	"primeradiant.com/toil/internal/state"
)

const statusResolved = "resolved"

type ResolveInput struct {
	Decision string
	Message  string
	Comment  string
}

func Resolve(root string, approvalID string, input ResolveInput) (*Approval, error) {
	approval, runDir, err := Find(root, approvalID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	approval.Status = statusResolved
	approval.Decision = input.Decision
	approval.Message = input.Message
	approval.Comment = input.Comment
	approval.ResolvedAt = &now

	if err := Save(runDir, approval); err != nil {
		return nil, err
	}

	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err == nil {
		_ = logger.Append(state.Event{Type: "approval_resolved", RunID: approval.RunID, NodeID: approval.NodeID, Data: map[string]any{"approval_id": approval.ID}})
		_ = logger.Close()
	}

	return approval, nil
}
