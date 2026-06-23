package metrics

import (
	"encoding/json"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// End-to-end: ingest a realistic sequence of ASSISTANT_TEXT_END events
// through the Collector (as the orchestrator would), serialize to JSON
// (as the API handler would), and assert the numbers match hand-computed
// pricing against the LiteLLM catalog.

// makeEventFromPayload builds a state.Event with a raw JSON text payload
// shaped exactly like serf's runtime output.
func makeEventFromPayload(nodeID, kind string, data map[string]any) state.Event {
	raw := map[string]any{"kind": kind, "data": data}
	b, _ := json.Marshal(raw)
	return state.Event{Type: "node_output", NodeID: nodeID, Text: string(b)}
}

func TestE2E_MixedModels_OpenAI_and_Anthropic(t *testing.T) {
	c := NewCollector()

	// Turn 1: gpt-5 sees a 10k prompt with 7k cached, emits 500 tokens.
	// Serf normalized (Phase C14) so input_tokens = 3000 new uncached;
	// cache_read_tokens = 7000; output_tokens = 500.
	//
	// gpt-5 rates: $1.25/M in, $10/M out, $0.125/M cache_read.
	// Event 1 cost: 3000 * 1.25/M + 7000 * 0.125/M + 500 * 10/M
	//             = 0.00375 + 0.000875 + 0.005 = 0.009625
	c.ProcessEvent(makeEventFromPayload("planner", "ASSISTANT_TEXT_END", map[string]any{
		"model": "gpt-5",
		"usage": map[string]any{
			"input_tokens":      3000,
			"output_tokens":     500,
			"cache_read_tokens": 7000,
		},
	}))

	// Turn 2: claude-opus-4-5 sees a new 5k prompt, writes 4k to cache, emits 200.
	// input_tokens = 1000 uncached; cache_write_tokens = 4000 (5m TTL);
	// cache_read_tokens = 0; output_tokens = 200.
	//
	// claude-opus-4-5: $5/M in, $25/M out, cache_creation_5m $6.25/M.
	// Event 2 cost: 1000 * 5/M + 4000 * 6.25/M + 200 * 25/M
	//             = 0.005 + 0.025 + 0.005 = 0.035
	c.ProcessEvent(makeEventFromPayload("analyzer", "ASSISTANT_TEXT_END", map[string]any{
		"model": "claude-opus-4-5",
		"usage": map[string]any{
			"input_tokens":       1000,
			"output_tokens":      200,
			"cache_write_tokens": 4000,
		},
	}))

	// Turn 3: claude-opus-4-5 again, high cache-hit on prior write.
	// input_tokens = 500 uncached; cache_read_tokens = 4000; output_tokens = 100.
	// Rates: cache_read $0.50/M.
	// Event 3 cost: 500 * 5/M + 4000 * 0.50/M + 100 * 25/M
	//             = 0.0025 + 0.002 + 0.0025 = 0.007
	c.ProcessEvent(makeEventFromPayload("analyzer", "ASSISTANT_TEXT_END", map[string]any{
		"model": "claude-opus-4-5",
		"usage": map[string]any{
			"input_tokens":      500,
			"output_tokens":     100,
			"cache_read_tokens": 4000,
		},
	}))

	// Planner node: one event, gpt-5, total = $0.009625.
	plannerOwn, _, ok := c.NodeMetrics("planner")
	if !ok {
		t.Fatal("planner not tracked")
	}
	if plannerOwn.CostUSD == nil {
		t.Fatal("planner cost: nil, want ~0.009625")
	}
	if !approxEq(*plannerOwn.CostUSD, 0.009625) {
		t.Errorf("planner cost: got %f, want ~0.009625", *plannerOwn.CostUSD)
	}
	if plannerOwn.Tokens.Input != 3000 {
		t.Errorf("planner input: got %d, want 3000", plannerOwn.Tokens.Input)
	}
	if plannerOwn.Tokens.CacheRead != 7000 {
		t.Errorf("planner cache_read: got %d, want 7000", plannerOwn.Tokens.CacheRead)
	}
	// Total processed = 3000 uncached + 500 output + 7000 cache_read + 0 cache_write = 10500
	if plannerOwn.Tokens.Total != 10500 {
		t.Errorf("planner total: got %d, want 10500", plannerOwn.Tokens.Total)
	}

	// Analyzer node: two events, both claude-opus-4-5.
	// Tokens: input=1500, output=300, cache_read=4000, cache_write=4000
	// Costs: $0.035 + $0.007 = $0.042 (Go's %.4f of 0.042 is "0.0420")
	analyzerOwn, _, ok := c.NodeMetrics("analyzer")
	if !ok {
		t.Fatal("analyzer not tracked")
	}
	if analyzerOwn.Tokens.Input != 1500 {
		t.Errorf("analyzer input: got %d, want 1500", analyzerOwn.Tokens.Input)
	}
	if analyzerOwn.Tokens.CacheRead != 4000 {
		t.Errorf("analyzer cache_read: got %d, want 4000", analyzerOwn.Tokens.CacheRead)
	}
	if analyzerOwn.Tokens.CacheWrite != 4000 {
		t.Errorf("analyzer cache_write: got %d, want 4000", analyzerOwn.Tokens.CacheWrite)
	}
	if analyzerOwn.CostUSD == nil {
		t.Fatal("analyzer cost: nil")
	}
	if !approxEq(*analyzerOwn.CostUSD, 0.042) {
		t.Errorf("analyzer cost: got %f, want 0.042", *analyzerOwn.CostUSD)
	}

	// Run total across planner + analyzer leaves.
	// $0.009625 + $0.042 = $0.051625. Floats accumulate exactly (no string round-trip).
	total := c.RunTotal()
	if total.CostUSD == nil {
		t.Fatal("run total cost: nil")
	}
	if !approxEq(*total.CostUSD, 0.051625) {
		t.Errorf("run total cost: got %f, want 0.051625", *total.CostUSD)
	}
}

func approxEq(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-6
}

func TestE2E_RollupAcrossMixedModelChildren(t *testing.T) {
	// Parent "p" has two iteration children (ForEach-style) running on
	// different models. Rollup on p must sum per-child costs at each
	// child's correct rate, not apply one model's rate to combined tokens.
	c := NewCollector()

	// p::0 uses gpt-5: 100k in, 1k out → 100_000 * 1.25/M + 1_000 * 10/M = 0.125 + 0.01 = $0.135
	c.ProcessEvent(makeEventFromPayload("p::0", "ASSISTANT_TEXT_END", map[string]any{
		"model": "gpt-5",
		"usage": map[string]any{"input_tokens": 100_000, "output_tokens": 1000},
	}))

	// p::1 uses claude-opus-4-5: 100k in, 1k out → 100_000 * 5/M + 1_000 * 25/M = 0.5 + 0.025 = $0.525
	c.ProcessEvent(makeEventFromPayload("p::1", "ASSISTANT_TEXT_END", map[string]any{
		"model": "claude-opus-4-5",
		"usage": map[string]any{"input_tokens": 100_000, "output_tokens": 1000},
	}))

	// Auto-link B10 puts p as parent of p::0 and p::1.
	_, rollup, ok := c.NodeMetrics("p")
	if !ok {
		t.Fatal("parent p not tracked — ForEach auto-link failed")
	}
	// Expected rollup cost: $0.135 + $0.525 = $0.66
	if rollup.CostUSD == nil {
		t.Fatal("rollup cost: nil")
	}
	if !approxEq(*rollup.CostUSD, 0.66) {
		t.Errorf("rollup cost: got %f, want 0.66", *rollup.CostUSD)
	}
	// WRONG result if both priced at Opus rate: 200_000 * 5/M + 2000 * 25/M = $1.05
	// WRONG if both priced at gpt-5 rate: 200_000 * 1.25/M + 2000 * 10/M = $0.27
	// Our answer must be between — $0.66.
	if rollup.Tokens.Input != 200_000 {
		t.Errorf("rollup input: got %d, want 200_000", rollup.Tokens.Input)
	}
}

func TestE2E_UnpricedModelIsAbsentInJSON(t *testing.T) {
	c := NewCollector()
	c.ProcessEvent(makeEventFromPayload("n", "ASSISTANT_TEXT_END", map[string]any{
		"model": "made-up-model-xyz",
		"usage": map[string]any{"input_tokens": 1000, "output_tokens": 100},
	}))

	own, _, _ := c.NodeMetrics("n")
	// CostUSD has `json:"cost_usd,omitempty"`. An unpriceable model leaves it "",
	// so marshal should omit the field entirely.
	b, err := json.Marshal(own)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey := string(b); containsCostUSD(gotKey) {
		t.Errorf("unpriced model should omit cost_usd; got JSON: %s", gotKey)
	}
}

func containsCostUSD(s string) bool {
	for i := 0; i+len("cost_usd") <= len(s); i++ {
		if s[i:i+len("cost_usd")] == "cost_usd" {
			return true
		}
	}
	return false
}
