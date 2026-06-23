package runners

import "fmt"

type Registry struct {
	runners map[string]Runner
}

func NewRegistry() *Registry {
	return &Registry{runners: make(map[string]Runner)}
}

func (registry *Registry) Register(id string, runner Runner) error {
	if id == "" {
		return fmt.Errorf("runner id is required")
	}
	if _, exists := registry.runners[id]; exists {
		return fmt.Errorf("runner already registered: %s", id)
	}
	registry.runners[id] = runner
	return nil
}

func (registry *Registry) Get(id string) (Runner, bool) {
	runner, ok := registry.runners[id]
	return runner, ok
}
