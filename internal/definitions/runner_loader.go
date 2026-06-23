package definitions

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func LoadRunnerFile(path string) (*Runner, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var runner Runner
	if err := yaml.Unmarshal(data, &runner); err != nil {
		return nil, err
	}

	if runner.ID == "" {
		return nil, fmt.Errorf("runner id is required")
	}
	if runner.Type == "" {
		return nil, fmt.Errorf("runner type is required")
	}

	return &runner, nil
}

func LoadRunnersDir(dir string) (map[string]*Runner, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	runners := make(map[string]*Runner)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		runner, err := LoadRunnerFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if _, exists := runners[runner.ID]; exists {
			return nil, fmt.Errorf("duplicate runner id: %s", runner.ID)
		}
		runners[runner.ID] = runner
	}

	return runners, nil
}
