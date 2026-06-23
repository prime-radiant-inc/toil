package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("tokens", func(rs *state.RunState) Processor { return NewTokensProcessor(rs) })
}

// TokensResult, TokenSummary, NodeTokens remain in their current shapes —
// the /inspect/tokens endpoint has external consumers and the shape is stable.

type TokensResult struct {
	Total  *TokenSummary `json:"total"`
	Models []string      `json:"models"`
	Nodes  []NodeTokens  `json:"nodes"`
}

type TokenSummary struct {
	Input            int     `json:"input"`
	Output           int     `json:"output"`
	CacheRead        int     `json:"cache_read"`
	CacheMiss        int     `json:"cache_miss"`
	Reasoning        int     `json:"reasoning"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type NodeTokens struct {
	ID        string  `json:"id"`
	Input     int     `json:"input"`
	Output    int     `json:"output"`
	CacheRead int     `json:"cache_read"`
	Reasoning int     `json:"reasoning"`
	CostUSD   float64 `json:"cost_usd"`
}

type tokensProcessor struct {
	rs        *state.RunState
	collector *metrics.Collector
	models    map[string]bool
	changed   bool
}

func NewTokensProcessor(rs *state.RunState) *tokensProcessor {
	return &tokensProcessor{
		rs:        rs,
		collector: metrics.NewCollector(),
		models:    make(map[string]bool),
	}
}

func (p *tokensProcessor) ProcessEvent(event state.Event) {
	inner, ok := ParseRunnerEvent(event)
	if !ok {
		return
	}

	switch {
	case inner.Usage != nil:
		p.collector.ProcessEvent(event)
		p.changed = true
		if inner.Usage.Model != "" {
			p.models[inner.Usage.Model] = true
		}

	case inner.SessionStart != nil:
		if inner.SessionStart.Model != "" {
			p.models[inner.SessionStart.Model] = true
			p.changed = true
		}
	}
}

func (p *tokensProcessor) Changed() bool { return p.changed }

func (p *tokensProcessor) Result() any {
	p.changed = false

	// Iterate collector's per-node data in sorted order.
	nodes := p.collector.AllNodeIDs()
	sort.Strings(nodes)

	var totalInput, totalOutput, totalCacheRead, totalReasoning int
	nodeList := make([]NodeTokens, 0, len(nodes))
	for _, id := range nodes {
		own, _, _ := p.collector.NodeMetrics(id)
		var cost float64
		if own.CostUSD != nil {
			cost = *own.CostUSD
		}
		nodeList = append(nodeList, NodeTokens{
			ID:        id,
			Input:     own.Tokens.Input,
			Output:    own.Tokens.Output,
			CacheRead: own.Tokens.CacheRead,
			Reasoning: own.Tokens.Reasoning,
			CostUSD:   cost,
		})
		totalInput += own.Tokens.Input
		totalOutput += own.Tokens.Output
		totalCacheRead += own.Tokens.CacheRead
		totalReasoning += own.Tokens.Reasoning
	}

	// Input counts are already normalized to "new uncached input" across
	// providers (see serf's llm.Usage invariant). cache_miss is simply the
	// uncached count. hit_rate is cached / (cached + miss).
	cacheMiss := totalInput
	var cacheHitRate float64
	totalInputProcessed := totalCacheRead + totalInput
	if totalInputProcessed > 0 {
		cacheHitRate = float64(totalCacheRead) / float64(totalInputProcessed)
	}

	models := make([]string, 0, len(p.models))
	for m := range p.models {
		models = append(models, m)
	}
	sort.Strings(models)

	// Run-total cost: sum the per-node costs already priced by the Collector
	// with each node's model. This is mix-correct: nodes using Opus are
	// priced at Opus rates, Haiku nodes at Haiku rates, etc.
	var totalCost float64
	for _, n := range nodeList {
		totalCost += n.CostUSD
	}

	return TokensResult{
		Total: &TokenSummary{
			Input:            totalInput,
			Output:           totalOutput,
			CacheRead:        totalCacheRead,
			CacheMiss:        cacheMiss,
			Reasoning:        totalReasoning,
			CacheHitRate:     cacheHitRate,
			EstimatedCostUSD: totalCost,
		},
		Models: models,
		Nodes:  nodeList,
	}
}
