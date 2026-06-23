package definitions

import (
	"fmt"
	"sort"
	"strings"
)

// inputTypeString is the declared input type whose empty-after-trim value
// counts as missing.
const inputTypeString = "string"

func MissingRequiredInputs(workflow *Workflow, inputs map[string]any) []string {
	if workflow == nil || len(workflow.Inputs) == 0 {
		return nil
	}
	missing := []string{}
	for key, inputType := range workflow.Inputs {
		if workflow.InputSchema != nil {
			if spec, ok := workflow.InputSchema[key]; ok && spec.Optional {
				continue
			}
		}
		value, ok := inputs[key]
		if !ok || value == nil {
			missing = append(missing, key)
			continue
		}
		if inputType == inputTypeString {
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return missing
}

func ValidateInputs(workflow *Workflow, inputs map[string]any) error {
	missing := MissingRequiredInputs(workflow, inputs)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required inputs: %s", strings.Join(missing, ", "))
}
