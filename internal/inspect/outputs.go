package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("outputs", func(rs *state.RunState) Processor { return NewOutputsProcessor(rs) })
}

// OutputsResult holds the output data for all nodes in the run.
type OutputsResult struct {
	Nodes []NodeOutput `json:"nodes"`
}

// NodeOutput holds the result data for a single node.
type NodeOutput struct {
	ID        string         `json:"id"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Artifacts []string       `json:"artifacts,omitempty"`
}

type outputsProcessor struct {
	rs *state.RunState
}

func NewOutputsProcessor(rs *state.RunState) *outputsProcessor {
	return &outputsProcessor{rs: rs}
}

// ProcessEvent is a no-op; outputs are read directly from RunState nodes.
func (p *outputsProcessor) ProcessEvent(event state.Event) {}

// Changed always returns false; outputs are read from static state.
func (p *outputsProcessor) Changed() bool { return false }

func (p *outputsProcessor) Result() any {
	var nodes []NodeOutput

	p.rs.WithNodes(func(ns map[string]*state.NodeState) {
		for _, n := range ns {
			nodes = append(nodes, NodeOutput{
				ID:        n.ID,
				Message:   n.Message,
				Data:      n.Data,
				Artifacts: n.Artifacts,
			})
		}
	})

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})

	if nodes == nil {
		nodes = []NodeOutput{}
	}

	return OutputsResult{Nodes: nodes}
}
