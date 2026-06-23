package approvals

const decisionApproved = "approved"

// Approver resolves pending approvals.
type Approver interface {
	Resolve(approval *Approval) (*Resolution, error)
}

// Resolution holds the result of an approval decision.
type Resolution struct {
	Decision string
	Message  string
	Comment  string
}

// FileApprover is the default approver. It returns nil, leaving the approval
// pending for external resolution (e.g. a human editing the JSON file).
type FileApprover struct{}

func (f *FileApprover) Resolve(approval *Approval) (*Resolution, error) {
	return nil, nil
}

// AutoApprover selects the default or first choice automatically. Useful for
// CI pipelines and evaluation runs.
type AutoApprover struct{}

func (a *AutoApprover) Resolve(approval *Approval) (*Resolution, error) {
	var decision string
	switch {
	case approval.Default != "":
		decision = approval.Default
	case len(approval.Choices) > 0:
		decision = approval.Choices[0]
	default:
		decision = decisionApproved
	}
	return &Resolution{
		Decision: decision,
		Message:  "auto-approved",
		Comment:  "resolved by AutoApprover",
	}, nil
}

// CallbackApprover delegates resolution to a provided function.
type CallbackApprover struct {
	Fn func(*Approval) (*Resolution, error)
}

func (c *CallbackApprover) Resolve(approval *Approval) (*Resolution, error) {
	return c.Fn(approval)
}
