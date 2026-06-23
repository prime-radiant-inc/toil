package engine

import "primeradiant.com/toil/internal/definitions"

func optionalInputsFromWorkflow(workflow *definitions.Workflow) map[string]bool {
	if workflow == nil || len(workflow.InputSchema) == 0 {
		return nil
	}
	optional := map[string]bool{}
	for key, spec := range workflow.InputSchema {
		if spec.Optional {
			optional[key] = true
		}
	}
	if len(optional) == 0 {
		return nil
	}
	return optional
}
