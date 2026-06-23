package inspect

import (
	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("decisions", func(rs *state.RunState) Processor { return NewDecisionsProcessor(rs) })
}

type DecisionsResult struct {
	Decisions []DecisionEntry `json:"decisions"`
}

type DecisionEntry struct {
	Node     string         `json:"node"`
	Attempt  int            `json:"attempt"`
	Decision string         `json:"decision"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data,omitempty"`
}

type decisionsProcessor struct {
	rs        *state.RunState
	decisions []DecisionEntry
	attempts  map[string]int // nodeID -> current attempt number
	changed   bool
}

func NewDecisionsProcessor(rs *state.RunState) *decisionsProcessor {
	return &decisionsProcessor{
		rs:       rs,
		attempts: make(map[string]int),
	}
}

func (p *decisionsProcessor) ProcessEvent(event state.Event) {
	// Track attempt boundaries via node_started events
	if event.Type == eventNodeStarted {
		p.attempts[event.NodeID]++
		return
	}

	inner, ok := ParseRunnerEvent(event)
	if !ok {
		return
	}

	if inner.Communicate == nil {
		return
	}

	attempt := p.attempts[inner.NodeID]
	if attempt == 0 {
		attempt = 1 // default if no node_started was seen
	}

	p.decisions = append(p.decisions, DecisionEntry{
		Node:     inner.NodeID,
		Attempt:  attempt,
		Decision: inner.Communicate.Decision,
		Message:  inner.Communicate.Message,
		Data:     inner.Communicate.Data,
	})
	p.changed = true
}

func (p *decisionsProcessor) Changed() bool {
	return p.changed
}

func (p *decisionsProcessor) Result() any {
	p.changed = false
	decisions := p.decisions
	if decisions == nil {
		decisions = []DecisionEntry{}
	}
	return DecisionsResult{
		Decisions: decisions,
	}
}
