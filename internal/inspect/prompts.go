package inspect

import (
	"strings"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("prompts", func(rs *state.RunState) Processor { return NewPromptsProcessor(rs) })
}

// PromptsResult holds the collected prompts for all nodes in the run.
type PromptsResult struct {
	Nodes []NodePrompts `json:"nodes"`
}

// NodePrompts holds the prompt data for a single node.
type NodePrompts struct {
	ID           string `json:"id"`
	SystemPrompt string `json:"system_prompt"`
	EdgePrompt   string `json:"edge_prompt,omitempty"`
	FullPrompt   string `json:"full_prompt"`
}

type nodePromptData struct {
	fullPrompt string
	edgePrompt string
}

type promptsProcessor struct {
	rs      *state.RunState
	nodes   map[string]*nodePromptData
	order   []string // insertion order for deterministic output
	changed bool
}

func NewPromptsProcessor(rs *state.RunState) *promptsProcessor {
	return &promptsProcessor{
		rs:    rs,
		nodes: make(map[string]*nodePromptData),
	}
}

func (p *promptsProcessor) ProcessEvent(event state.Event) {
	switch event.Type {
	case eventNodePrompt:
		data := p.getOrCreateNode(event.NodeID)
		data.fullPrompt = event.Text
		p.changed = true

	case eventNodeEdgePrompt:
		data := p.getOrCreateNode(event.NodeID)
		data.edgePrompt = event.Text
		p.changed = true
	}
}

func (p *promptsProcessor) getOrCreateNode(nodeID string) *nodePromptData {
	if _, exists := p.nodes[nodeID]; !exists {
		p.nodes[nodeID] = &nodePromptData{}
		p.order = append(p.order, nodeID)
	}
	return p.nodes[nodeID]
}

func (p *promptsProcessor) Changed() bool {
	return p.changed
}

func (p *promptsProcessor) Result() any {
	p.changed = false

	nodes := make([]NodePrompts, 0, len(p.order))
	for _, id := range p.order {
		data := p.nodes[id]
		nodes = append(nodes, NodePrompts{
			ID:           id,
			SystemPrompt: computeSystemPrompt(data.fullPrompt, data.edgePrompt),
			EdgePrompt:   data.edgePrompt,
			FullPrompt:   data.fullPrompt,
		})
	}

	if nodes == nil {
		nodes = []NodePrompts{}
	}

	return PromptsResult{Nodes: nodes}
}

// computeSystemPrompt returns the system portion of the full prompt by removing
// the edge portion. If the edge text is not found in the full prompt, it returns
// the full prompt unchanged.
func computeSystemPrompt(fullPrompt, edgePrompt string) string {
	if edgePrompt == "" {
		return fullPrompt
	}
	idx := strings.Index(fullPrompt, edgePrompt)
	if idx < 0 {
		return fullPrompt
	}
	return fullPrompt[:idx] + fullPrompt[idx+len(edgePrompt):]
}
