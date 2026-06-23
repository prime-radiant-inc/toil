package tgwm

import (
	"fmt"
	"os"
)

func ToilRoot() (string, error) {
	v := os.Getenv("TOIL_ROOT")
	if v == "" {
		return "", fmt.Errorf("TOIL_ROOT is not set")
	}
	return v, nil
}

func WorkflowDir() (string, error) {
	v := os.Getenv("TOIL_CURRENT_WORKFLOW_DIR")
	if v == "" {
		return "", fmt.Errorf("TOIL_CURRENT_WORKFLOW_DIR is not set")
	}
	return v, nil
}
