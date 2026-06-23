package engine

import "primeradiant.com/toil/internal/definitions"

const (
	promptInputsModeAll      = "all"
	promptInputsModeDeclared = "declared"
	promptInputsModeNone     = "none"
)

func resolvePromptInputsMode(workflow *definitions.Workflow, node *definitions.Node) string {
	if node != nil && node.PromptInputsMode != "" {
		return node.PromptInputsMode
	}
	if workflow != nil && workflow.PromptInputsMode != "" {
		return workflow.PromptInputsMode
	}
	return promptInputsModeDeclared
}

func buildPromptDisplayInputs(mode string, runInputs map[string]any, nodeInputs map[string]any) map[string]any {
	switch mode {
	case promptInputsModeNone:
		return map[string]any{}
	case promptInputsModeAll:
		merged := make(map[string]any, len(runInputs)+len(nodeInputs))
		for key, value := range runInputs {
			merged[key] = value
		}
		for key, value := range nodeInputs {
			merged[key] = value
		}
		return merged
	default:
		declared := make(map[string]any, len(nodeInputs))
		for key, value := range nodeInputs {
			declared[key] = value
		}
		return declared
	}
}
