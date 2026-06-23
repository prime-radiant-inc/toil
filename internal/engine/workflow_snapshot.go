package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"primeradiant.com/toil/internal/definitions"
)

func (engine *Engine) loadWorkflowSnapshot(runDir string, workflowID string) (*definitions.Workflow, error) {
	snapshotPath := filepath.Join(runDir, "workflow.yaml")
	if _, err := os.Stat(snapshotPath); err == nil {
		return definitions.LoadWorkflowSnapshot(snapshotPath)
	}
	workflow, ok := engine.Definitions.Workflows[workflowID]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}
	return workflow, nil
}
