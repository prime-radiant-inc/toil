package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("overview", func(rs *state.RunState) Processor { return NewOverviewProcessor(rs) })
}

type OverviewResult struct {
	RunID      string        `json:"run_id"`
	WorkflowID string        `json:"workflow_id"`
	Status     string        `json:"status"`
	DurationS  float64       `json:"duration_s"`
	StartedAt  string        `json:"started_at"`
	FinishedAt string        `json:"finished_at,omitempty"`
	Models     []string      `json:"models"`
	Tokens     *TokenSummary `json:"tokens,omitempty"`
	Nodes      []NodeSummary `json:"nodes"`
}

type NodeSummary struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	Dispatches int     `json:"dispatches,omitempty"`
	Decision   string  `json:"decision,omitempty"`
	DurationS  float64 `json:"duration_s"`
	ChildRun   string  `json:"child_run,omitempty"`
}

type overviewProcessor struct {
	rs     *state.RunState
	tokens *tokensProcessor
}

func NewOverviewProcessor(rs *state.RunState) *overviewProcessor {
	return &overviewProcessor{
		rs:     rs,
		tokens: NewTokensProcessor(rs),
	}
}

func (p *overviewProcessor) ProcessEvent(event state.Event) {
	p.tokens.ProcessEvent(event)
}

func (p *overviewProcessor) Changed() bool {
	return p.tokens.Changed()
}

func (p *overviewProcessor) Result() any {
	var durationS float64
	var finishedAt string
	if p.rs.FinishedAt != nil {
		durationS = p.rs.FinishedAt.Sub(p.rs.StartedAt).Seconds()
		finishedAt = p.rs.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}

	tokensResult := p.tokens.Result().(TokensResult)

	// Only include tokens if any were recorded
	var tokenSummary *TokenSummary
	if tokensResult.Total.Input > 0 || tokensResult.Total.Output > 0 {
		tokenSummary = tokensResult.Total
	}

	type nodeSortEntry struct {
		summary NodeSummary
		startNs int64 // for sorting
	}
	var entries []nodeSortEntry

	p.rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, n := range nodes {
			var durS float64
			var startNs int64
			if n.StartedAt != nil && n.EndedAt != nil {
				durS = n.EndedAt.Sub(*n.StartedAt).Seconds()
				startNs = n.StartedAt.UnixNano()
			} else if n.StartedAt != nil {
				startNs = n.StartedAt.UnixNano()
			}
			entries = append(entries, nodeSortEntry{
				summary: NodeSummary{
					ID:         n.ID,
					Status:     n.Status,
					Attempts:   n.Attempts,
					Dispatches: n.Dispatches,
					Decision:   n.Decision,
					DurationS:  durS,
					ChildRun:   ChildRun(n),
				},
				startNs: startNs,
			})
		}
	})

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].startNs != entries[j].startNs {
			return entries[i].startNs < entries[j].startNs
		}
		return entries[i].summary.ID < entries[j].summary.ID
	})

	nodes := make([]NodeSummary, len(entries))
	for i, e := range entries {
		nodes[i] = e.summary
	}

	return OverviewResult{
		RunID:      p.rs.ID,
		WorkflowID: p.rs.WorkflowID,
		Status:     p.rs.Status,
		DurationS:  durationS,
		StartedAt:  p.rs.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		FinishedAt: finishedAt,
		Models:     tokensResult.Models,
		Tokens:     tokenSummary,
		Nodes:      nodes,
	}
}
