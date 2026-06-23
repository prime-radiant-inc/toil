package inspect

import (
	"fmt"

	"primeradiant.com/toil/internal/state"
)

type ProcessorFactory func(runState *state.RunState) Processor

var registry = map[string]ProcessorFactory{}

func Register(name string, factory ProcessorFactory) {
	if _, exists := registry[name]; exists {
		panic("inspect: duplicate aspect registration: " + name)
	}
	registry[name] = factory
}

func NewProcessor(name string, runState *state.RunState) (Processor, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown aspect: %q", name)
	}
	return factory(runState), nil
}

func Aspects() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
