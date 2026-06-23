package engine

import (
	"fmt"
	"slices"
	"strings"

	"primeradiant.com/toil/internal/definitions"
)

type OutputValidationError struct {
	Messages []string
}

func (err OutputValidationError) Error() string {
	return fmt.Sprintf("node output validation failed: %s", strings.Join(err.Messages, "; "))
}

func validateNodeOutput(output NodeOutput, node *definitions.Node) error {
	var messages []string

	if strings.TrimSpace(output.Decision) == "" {
		messages = append(messages, `field "decision" is required and must be a non-empty string`)
	}
	if strings.TrimSpace(output.Message) == "" {
		messages = append(messages, `field "message" is required and must be a non-empty string`)
	}
	if node != nil && len(node.Decisions) > 0 && strings.TrimSpace(output.Decision) != "" {
		if !slices.Contains(node.Decisions.IDs(), output.Decision) {
			messages = append(messages, fmt.Sprintf(`field "decision" must be one of: %s`, strings.Join(node.Decisions.IDs(), ", ")))
		}
	}

	if len(messages) == 0 {
		return nil
	}
	return OutputValidationError{Messages: messages}
}

func outputValidationMessages(err error) []string {
	if err == nil {
		return nil
	}
	if validationErr, ok := err.(OutputValidationError); ok {
		return append([]string{}, validationErr.Messages...)
	}
	return []string{err.Error()}
}
