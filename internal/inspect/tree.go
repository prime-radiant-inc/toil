package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("tree", func(rs *state.RunState) Processor { return NewTreeProcessor(rs) })
}

// TreeResult represents the hierarchical run tree starting from the root run.
type TreeResult struct {
	RunID      string     `json:"run_id"`
	WorkflowID string     `json:"workflow_id"`
	Status     string     `json:"status"`
	DurationS  float64    `json:"duration_s"`
	Children   []TreeNode `json:"children,omitempty"`
}

// TreeNode represents a child run discovered via child_run links in node data.
type TreeNode struct {
	NodeID     string     `json:"node_id"`
	RunID      string     `json:"run_id"`
	WorkflowID string     `json:"workflow_id"`
	Status     string     `json:"status"`
	DurationS  float64    `json:"duration_s"`
	Children   []TreeNode `json:"children,omitempty"`
}

type treeProcessor struct {
	rs     *state.RunState
	loader RunLoader
}

func NewTreeProcessor(rs *state.RunState) *treeProcessor {
	return &treeProcessor{rs: rs}
}

func (p *treeProcessor) SetLoader(loader RunLoader) {
	p.loader = loader
}

func (p *treeProcessor) ProcessEvent(event state.Event) {
	// Tree is computed from RunState, not events.
}

func (p *treeProcessor) Changed() bool {
	return false
}

func (p *treeProcessor) Result() any {
	var durationS float64
	if p.rs.FinishedAt != nil {
		durationS = p.rs.FinishedAt.Sub(p.rs.StartedAt).Seconds()
	}

	visited := map[string]bool{p.rs.ID: true}
	return TreeResult{
		RunID:      p.rs.ID,
		WorkflowID: p.rs.WorkflowID,
		Status:     p.rs.Status,
		DurationS:  durationS,
		Children:   p.buildChildren(p.rs, visited),
	}
}

// buildChildren finds all nodes with child_run links and recursively loads them.
// visited tracks seen run IDs to prevent infinite recursion from circular references.
func (p *treeProcessor) buildChildren(rs *state.RunState, visited map[string]bool) []TreeNode {
	if p.loader == nil {
		return nil
	}

	type childRef struct {
		nodeID   string
		childRun string
	}

	var refs []childRef
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, n := range nodes {
			if cr := ChildRun(n); cr != "" {
				refs = append(refs, childRef{nodeID: n.ID, childRun: cr})
			}
		}
	})

	if len(refs) == 0 {
		return nil
	}

	// Sort for deterministic output
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].nodeID < refs[j].nodeID
	})

	children := make([]TreeNode, 0, len(refs))
	for _, ref := range refs {
		if visited[ref.childRun] {
			// Cycle detected — stop recursion
			children = append(children, TreeNode{
				NodeID: ref.nodeID,
				RunID:  ref.childRun,
				Status: "cycle",
			})
			continue
		}

		childRS, err := p.loader.LoadState(ref.childRun)
		if err != nil {
			// Graceful degradation: include what we know
			children = append(children, TreeNode{
				NodeID: ref.nodeID,
				RunID:  ref.childRun,
				Status: "unknown",
			})
			continue
		}

		visited[ref.childRun] = true
		var dur float64
		if childRS.FinishedAt != nil {
			dur = childRS.FinishedAt.Sub(childRS.StartedAt).Seconds()
		}

		children = append(children, TreeNode{
			NodeID:     ref.nodeID,
			RunID:      childRS.ID,
			WorkflowID: childRS.WorkflowID,
			Status:     childRS.Status,
			DurationS:  dur,
			Children:   p.buildChildren(childRS, visited),
		})
	}

	return children
}
