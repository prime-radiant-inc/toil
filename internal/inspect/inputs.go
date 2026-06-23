package inspect

import "primeradiant.com/toil/internal/state"

func init() {
	Register("inputs", func(rs *state.RunState) Processor { return NewInputsProcessor(rs) })
}

// InputsResult holds the run-level inputs.
type InputsResult struct {
	Inputs map[string]any `json:"inputs"`
}

type inputsProcessor struct {
	rs *state.RunState
}

func NewInputsProcessor(rs *state.RunState) *inputsProcessor {
	return &inputsProcessor{rs: rs}
}

// ProcessEvent is a no-op; inputs are read directly from RunState.
func (p *inputsProcessor) ProcessEvent(event state.Event) {}

// Changed always returns false; inputs are static state.
func (p *inputsProcessor) Changed() bool { return false }

func (p *inputsProcessor) Result() any {
	inputs := p.rs.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	return InputsResult{Inputs: inputs}
}
