package inspect

import (
	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("compare", func(rs *state.RunState) Processor { return NewCompareProcessor(rs) })
}

// CompareResult holds the side-by-side comparison of two runs.
type CompareResult struct {
	Runs       [2]string   `json:"runs"`
	Comparison CompareData `json:"comparison"`
}

// CompareData contains the computed deltas between two runs.
type CompareData struct {
	DurationS     CompareValue   `json:"duration_s"`
	TotalAttempts CompareValue   `json:"total_attempts"`
	Tokens        *CompareTokens `json:"tokens,omitempty"`
}

// CompareValue is a single metric comparison: A, B, delta (B-A), pct change.
type CompareValue struct {
	A     float64 `json:"a"`
	B     float64 `json:"b"`
	Delta float64 `json:"delta"`
	Pct   float64 `json:"pct"`
}

// CompareTokens holds token-level comparisons.
type CompareTokens struct {
	Total   CompareValue `json:"total"`
	CostUSD CompareValue `json:"cost_usd"`
}

type compareProcessor struct {
	rs      *state.RunState
	loader  RunLoader
	otherID string
	tokens  *tokensProcessor
	changed bool
}

func NewCompareProcessor(rs *state.RunState) *compareProcessor {
	return &compareProcessor{
		rs:     rs,
		tokens: NewTokensProcessor(rs),
	}
}

func (p *compareProcessor) SetLoader(loader RunLoader) {
	p.loader = loader
}

func (p *compareProcessor) SetOtherRunID(id string) {
	p.otherID = id
}

func (p *compareProcessor) ProcessEvent(event state.Event) {
	p.tokens.ProcessEvent(event)
	// Track our own changed flag: p.tokens.Result() (called below by
	// compareProcessor.Result) resets tokens.changed, so we can't
	// read it on the next Changed() call. Latch a local flag here
	// and clear it in Result.
	if p.tokens.Changed() {
		p.changed = true
	}
}

func (p *compareProcessor) Changed() bool {
	return p.changed
}

func (p *compareProcessor) Result() any {
	p.changed = false
	if p.otherID == "" {
		return map[string]string{keyError: "no other run ID specified for comparison"}
	}
	if p.loader == nil {
		return map[string]string{keyError: "no run loader available for comparison"}
	}

	otherRS, err := p.loader.LoadState(p.otherID)
	if err != nil {
		return map[string]string{keyError: "could not load run " + p.otherID + ": " + err.Error()}
	}

	// Load and process events for the other run
	otherTokens := NewTokensProcessor(otherRS)
	otherEvents, err := p.loader.LoadEvents(p.otherID)
	if err == nil {
		for _, e := range otherEvents {
			otherTokens.ProcessEvent(e)
		}
	}

	// Compute timing for both runs
	durationA := runDuration(p.rs)
	durationB := runDuration(otherRS)

	// Compute total attempts for both runs
	attemptsA := totalAttempts(p.rs)
	attemptsB := totalAttempts(otherRS)

	// Compute token comparison
	tokensA := p.tokens.Result().(TokensResult)
	tokensB := otherTokens.Result().(TokensResult)

	var tokenCompare *CompareTokens
	totalA := tokensA.Total.Input + tokensA.Total.Output
	totalB := tokensB.Total.Input + tokensB.Total.Output
	if totalA > 0 || totalB > 0 {
		tokenCompare = &CompareTokens{
			Total:   compareValue(float64(totalA), float64(totalB)),
			CostUSD: compareValue(tokensA.Total.EstimatedCostUSD, tokensB.Total.EstimatedCostUSD),
		}
	}

	return CompareResult{
		Runs: [2]string{p.rs.ID, p.otherID},
		Comparison: CompareData{
			DurationS:     compareValue(durationA, durationB),
			TotalAttempts: compareValue(float64(attemptsA), float64(attemptsB)),
			Tokens:        tokenCompare,
		},
	}
}

func runDuration(rs *state.RunState) float64 {
	if rs.FinishedAt != nil {
		return rs.FinishedAt.Sub(rs.StartedAt).Seconds()
	}
	return 0
}

func totalAttempts(rs *state.RunState) int {
	total := 0
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, n := range nodes {
			total += n.Attempts
		}
	})
	return total
}

func compareValue(a, b float64) CompareValue {
	delta := b - a
	var pct float64
	if a != 0 {
		pct = (delta / a) * 100.0
	}
	return CompareValue{
		A:     a,
		B:     b,
		Delta: delta,
		Pct:   pct,
	}
}
