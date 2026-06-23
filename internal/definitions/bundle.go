package definitions

import (
	"fmt"
	"path/filepath"
)

type Bundle struct {
	Runners   map[string]*Runner
	Workflows map[string]*Workflow
	Root      string
}

func LoadBundle(root string) (*Bundle, error) {
	return loadBundle(root, LoadWorkflowsDir)
}

func LoadBundleNoEnv(root string) (*Bundle, error) {
	return loadBundle(root, LoadWorkflowsDirSnapshot)
}

func loadBundle(root string, loadWorkflows func(string) (map[string]*Workflow, error)) (*Bundle, error) {
	runners, err := LoadRunnersDir(filepath.Join(root, "definitions", "runners"))
	if err != nil {
		return nil, err
	}
	workflows, err := loadWorkflows(filepath.Join(root, "definitions", "workflows"))
	if err != nil {
		return nil, err
	}

	bundle := &Bundle{
		Runners:   runners,
		Workflows: workflows,
		Root:      root,
	}

	if result := ValidateBundle(bundle); result.HasErrors() {
		return nil, fmt.Errorf("bundle validation: %s", result.Error())
	}

	return bundle, nil
}
