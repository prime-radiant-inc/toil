package document

// Registry resolves human-readable names from machine ids. The dashboard
// supplies a registry backed by the loaded workflow definitions; tests
// supply a fake.
type Registry interface {
	// WorkflowName returns the display name for a workflow id (e.g.
	// "build_component" → "Build Component"). Falls back to the id if
	// unknown.
	WorkflowName(workflowID string) string
	// RoleForNode returns the canonical role name for a node within a
	// workflow. For implement_task's "write_code" node this returns
	// "write_code". Falls back to the node id.
	RoleForNode(workflowID, nodeID string) string
	// RunnerForNode returns the runner id (e.g. "serf", "codex", "claude",
	// "shell", "human") configured for a node in a workflow, or "" if unknown.
	RunnerForNode(workflowID, nodeID string) string
	// NextNode returns the target node id for an edge whose `from` matches
	// nodeID and whose `when` matches the literal decision. Returns "" if no
	// such edge exists. Expression edges (when:"status == 'failed'") are
	// skipped — only exact `when: <decision>` matches.
	NextNode(workflowID, fromNodeID, decision string) string
	// PlanTaskDescription looks up the parent's plan.tasks[] for a task
	// description matching the child run. Returns "" if unknown.
	PlanTaskDescription(parentRunID, taskID string) string
}
