package inspect

import (
	"testing"

	"primeradiant.com/toil/internal/state"
)

// makeUsageEvent creates a node_output event with ASSISTANT_TEXT_END
// containing token usage, matching the real serf event format. Uses
// "gpt-5" which is in the LiteLLM catalog at $1.25/M input, $10/M output.
func makeUsageEvent(nodeID string, input, output, cacheRead, reasoning int) state.Event {
	return makeNodeOutputEvent(nodeID, map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"text":  "",
			"model": "gpt-5",
			"usage": map[string]any{
				"input_tokens":      input,
				"output_tokens":     output,
				"cache_read_tokens": cacheRead,
				"reasoning_tokens":  reasoning,
			},
		},
	})
}

func TestTokensProcessor_AggregatesUsage(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	// Two turns for node-a
	proc.ProcessEvent(makeUsageEvent("node-a", 100, 50, 80, 10))
	proc.ProcessEvent(makeUsageEvent("node-a", 200, 100, 150, 20))

	// One turn for node-b
	proc.ProcessEvent(makeUsageEvent("node-b", 300, 75, 200, 5))

	result := proc.Result().(TokensResult)

	// Total: input=600, output=225, cache_read=430, reasoning=35
	if result.Total.Input != 600 {
		t.Errorf("Total.Input: got %d, want 600", result.Total.Input)
	}
	if result.Total.Output != 225 {
		t.Errorf("Total.Output: got %d, want 225", result.Total.Output)
	}
	if result.Total.CacheRead != 430 {
		t.Errorf("Total.CacheRead: got %d, want 430", result.Total.CacheRead)
	}
	if result.Total.Reasoning != 35 {
		t.Errorf("Total.Reasoning: got %d, want 35", result.Total.Reasoning)
	}

	// CacheMiss is the new-uncached input tokens. Post-normalization, Input
	// already means uncached-new, so CacheMiss == Input.
	if result.Total.CacheMiss != 600 {
		t.Errorf("Total.CacheMiss: got %d, want 600", result.Total.CacheMiss)
	}

	// Per-node check
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 node entries, got %d", len(result.Nodes))
	}
}

func TestTokensProcessor_CacheHitRate(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	// 1000 uncached-new input, 750 cache-reads.
	proc.ProcessEvent(makeUsageEvent("node-a", 1000, 100, 750, 0))

	result := proc.Result().(TokensResult)

	// cache_hit_rate = cache_read / (cache_read + new_uncached) = 750 / 1750 ≈ 0.4286
	want := 750.0 / 1750.0
	if absF(result.Total.CacheHitRate-want) > 1e-9 {
		t.Errorf("CacheHitRate: got %f, want %f", result.Total.CacheHitRate, want)
	}
}

func absF(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

func TestTokensProcessor_CacheHitRateZeroInput(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	// No events, so input is 0
	result := proc.Result().(TokensResult)

	if result.Total.CacheHitRate != 0 {
		t.Errorf("CacheHitRate with zero input: got %f, want 0", result.Total.CacheHitRate)
	}
}

func TestTokensProcessor_ModelsFromUsageEvents(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	// ASSISTANT_TEXT_END events carry model in the data
	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"model": "gpt-5.4",
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		},
	}))
	proc.ProcessEvent(makeNodeOutputEvent("node-b", map[string]any{
		"kind": "ASSISTANT_TEXT_END",
		"data": map[string]any{
			"model": "claude-opus-4-5",
			"usage": map[string]any{
				"input_tokens":  200,
				"output_tokens": 75,
			},
		},
	}))

	result := proc.Result().(TokensResult)

	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(result.Models), result.Models)
	}

	modelSet := map[string]bool{}
	for _, m := range result.Models {
		modelSet[m] = true
	}
	if !modelSet["gpt-5.4"] {
		t.Error("expected model gpt-5.4")
	}
	if !modelSet["claude-opus-4-5"] {
		t.Error("expected model claude-opus-4-5")
	}
}

func TestTokensProcessor_SessionStartModels(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	proc.ProcessEvent(makeNodeOutputEvent("node-a", map[string]any{
		"kind": "SESSION_START",
		"data": map[string]any{
			"profile": "default",
			"model":   "claude-opus-4-5",
		},
	}))

	result := proc.Result().(TokensResult)

	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0] != "claude-opus-4-5" {
		t.Errorf("model: got %q, want claude-opus-4-5", result.Models[0])
	}
}

func TestTokensProcessor_DuplicateModels(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	proc.ProcessEvent(makeUsageEvent("node-a", 100, 50, 0, 0))
	proc.ProcessEvent(makeUsageEvent("node-b", 200, 75, 0, 0))

	result := proc.Result().(TokensResult)

	// Both events have model "gpt-5" from makeUsageEvent
	if len(result.Models) != 1 {
		t.Errorf("expected 1 unique model, got %d: %v", len(result.Models), result.Models)
	}
}

func TestTokensProcessor_EstimatedCost(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	// gpt-5: $1.25/M input, $10/M output.
	// 1M input + 1M output = $1.25 + $10 = $11.25
	proc.ProcessEvent(makeUsageEvent("node-a", 1_000_000, 1_000_000, 0, 0))

	result := proc.Result().(TokensResult)

	expectedCost := 11.25
	if result.Total.EstimatedCostUSD != expectedCost {
		t.Errorf("EstimatedCostUSD: got %f, want %f", result.Total.EstimatedCostUSD, expectedCost)
	}
}

func TestTokensProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false before any events")
	}

	proc.ProcessEvent(makeUsageEvent("node-a", 100, 50, 0, 0))

	if !proc.Changed() {
		t.Error("Changed() should be true after processing a usage event")
	}

	// After calling Result, Changed should reset
	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after calling Result()")
	}
}

func TestTokensProcessor_IgnoresNonRunnerEvents(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_started",
		NodeID: "node-a",
	})

	if proc.Changed() {
		t.Error("non-runner events should not trigger Changed()")
	}
}

func TestTokensProcessor_PerNodeCost(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewTokensProcessor(rs)

	proc.ProcessEvent(makeUsageEvent("node-a", 500_000, 100_000, 0, 0))
	proc.ProcessEvent(makeUsageEvent("node-b", 500_000, 900_000, 0, 0))

	result := proc.Result().(TokensResult)

	// gpt-5: $1.25/M input, $10/M output.
	// node-a: 0.5M * 1.25 + 0.1M * 10 = 0.625 + 1.0 = $1.625
	// node-b: 0.5M * 1.25 + 0.9M * 10 = 0.625 + 9.0 = $9.625
	// total: $11.25
	nodeA := findNodeTokens(result.Nodes, "node-a")
	if nodeA == nil {
		t.Fatal("node-a not found in results")
	}
	if nodeA.CostUSD != 1.625 {
		t.Errorf("node-a CostUSD: got %f, want 1.625", nodeA.CostUSD)
	}

	nodeB := findNodeTokens(result.Nodes, "node-b")
	if nodeB == nil {
		t.Fatal("node-b not found in results")
	}
	if nodeB.CostUSD != 9.625 {
		t.Errorf("node-b CostUSD: got %f, want 9.625", nodeB.CostUSD)
	}

	if result.Total.EstimatedCostUSD != 11.25 {
		t.Errorf("Total cost: got %f, want 11.25", result.Total.EstimatedCostUSD)
	}
}

func findNodeTokens(nodes []NodeTokens, id string) *NodeTokens {
	for _, n := range nodes {
		if n.ID == id {
			return &n
		}
	}
	return nil
}
