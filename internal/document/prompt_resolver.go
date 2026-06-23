package document

// PromptResolver returns the attempt-specific portion of a node's prompt.
// Implementations resolve the workflow template against the node's inputs
// and pass the result through ExtractLocalPrompt to return only the local part.
type PromptResolver interface {
	LocalPrompt(workflowID, nodeID string, runID string, attempt int) string
}
