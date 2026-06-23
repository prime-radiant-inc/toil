package state

// TokenBreakdown is a per-node or rollup token count across all categories.
//
//	Input is new uncached input tokens; Output includes reasoning tokens on
//	every supported provider; CacheRead/CacheWrite/CacheWrite1h track cache
//	tiers. Reasoning is metadata-only — DO NOT add it to Total, since every
//	supported provider already counts reasoning tokens inside Output.
//	ReasoningEstimated is a char-based estimate for providers that don't
//	report native reasoning counts; display-only.
type TokenBreakdown struct {
	Input              int `json:"input"`
	Output             int `json:"output"`
	CacheRead          int `json:"cache_read"`
	CacheWrite         int `json:"cache_write,omitempty"`
	CacheWrite1h       int `json:"cache_write_1h,omitempty"`
	Reasoning          int `json:"reasoning"`
	ReasoningEstimated int `json:"reasoning_estimated,omitempty"`
	Total              int `json:"total"`
}

// NodeTotals is a roll-up of duration, tokens, and cost. Used at the
// per-node level (rollups) and at the run level (sum across nodes).
//
//	CostUSD is *float64 so JSON can distinguish nil (unknown model),
//	&0 (priced, zero tokens), and non-zero. UnpricedEventCount > 0 means
//	CostUSD is an under-report — the collector saw events from a model
//	with no pricing entry.
//
// Stored as derived state on a RunState once the run reaches a terminal
// status, so callers don't need to replay events.jsonl on every read.
type NodeTotals struct {
	DurationMs         int64          `json:"duration_ms"`
	Tokens             TokenBreakdown `json:"tokens"`
	CostUSD            *float64       `json:"cost_usd,omitempty"`
	UnpricedEventCount int            `json:"unpriced_event_count,omitempty"`
}
