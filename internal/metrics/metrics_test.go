package metrics

import (
	"fmt"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

// makeUsageEvent builds a node_output event carrying an ASSISTANT_TEXT_END
// usage payload in the exact shape serf emits.
func makeUsageEvent(nodeID string, input, output, cacheRead, reasoning int, model string) state.Event {
	return state.Event{
		Type:   "node_output",
		NodeID: nodeID,
		Text: fmt.Sprintf(
			`{"kind":"ASSISTANT_TEXT_END","data":{"text":"","model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_tokens":%d,"reasoning_tokens":%d}}}`,
			model, input, output, cacheRead, reasoning,
		),
	}
}

func TestCollector_OwnTokens_SingleNode(t *testing.T) {
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("draft_plan", 100, 50, 20, 5, "gpt-5"))
	c.ProcessEvent(makeUsageEvent("draft_plan", 200, 75, 30, 10, "gpt-5"))

	own, _, ok := c.NodeMetrics("draft_plan")
	if !ok {
		t.Fatal("NodeMetrics: not found")
	}
	if own.Tokens.Input != 300 {
		t.Errorf("Input: got %d, want 300", own.Tokens.Input)
	}
	if own.Tokens.Output != 125 {
		t.Errorf("Output: got %d, want 125", own.Tokens.Output)
	}
	if own.Tokens.CacheRead != 50 {
		t.Errorf("CacheRead: got %d, want 50", own.Tokens.CacheRead)
	}
	if own.Tokens.Reasoning != 15 {
		t.Errorf("Reasoning: got %d, want 15", own.Tokens.Reasoning)
	}
	// Total = Input + Output + CacheRead + CacheWrite = 300 + 125 + 50 + 0.
	// Reasoning is not added: it's a subset of Output on every provider.
	if own.Tokens.Total != 475 {
		t.Errorf("Total: got %d, want 475", own.Tokens.Total)
	}
	// gpt-5: $1.25/M input, $10/M output, $0.125/M cache-read.
	// = 300/1M*1.25 + 125/1M*10 + 50/1M*0.125 = 0.000375 + 0.00125 + 0.00000625 = 0.00163125
	if own.CostUSD == nil {
		t.Fatal("CostUSD: nil")
	}
	if !approxCostEq(*own.CostUSD, 0.00163125) {
		t.Errorf("CostUSD: got %f, want ~0.00163125", *own.CostUSD)
	}
}

func TestCollector_OwnTokens_UnknownModelNoCost(t *testing.T) {
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("draft_plan", 100, 50, 0, 0, "made-up-model-xyz-no-catalog"))
	own, _, _ := c.NodeMetrics("draft_plan")
	if own.CostUSD != nil {
		t.Errorf("CostUSD unknown model: got %f, want nil", *own.CostUSD)
	}
}

func TestEstimateCost_KnownModel(t *testing.T) {
	// gpt-5: $1.25/M input, $10/M output. 1M in + 1M out = $11.25
	got, ok := EstimateCost("gpt-5", TokenUsage{UncachedInput: 1_000_000, Output: 1_000_000})
	if !ok {
		t.Fatal("EstimateCost returned false for known model")
	}
	if !approxCostEq(got, 11.25) {
		t.Errorf("EstimateCost: got %f, want 11.25", got)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	got, ok := EstimateCost("made-up-model-xyz-no-catalog", TokenUsage{UncachedInput: 1000, Output: 500})
	if ok {
		t.Errorf("EstimateCost unknown model: ok = true, want false")
	}
	if got != 0 {
		t.Errorf("EstimateCost unknown model: got %f, want 0", got)
	}
}

func TestEstimateCost_Zero(t *testing.T) {
	got, ok := EstimateCost("gpt-5", TokenUsage{})
	if !ok {
		t.Fatal("expected known model")
	}
	if got != 0 {
		t.Errorf("EstimateCost zero: got %f, want 0", got)
	}
}

func TestEstimateCost_DatedSnapshotMatchesFamily(t *testing.T) {
	got, ok := EstimateCost("gpt-5-2025-08-07", TokenUsage{UncachedInput: 1_000_000, Output: 1_000_000})
	if !ok {
		t.Fatal("dated snapshot should price")
	}
	if !approxCostEq(got, 11.25) {
		t.Errorf("EstimateCost dated snapshot: got %f, want 11.25", got)
	}
}

func TestEstimateCost_InputAndOutputUseDifferentRates(t *testing.T) {
	if got, _ := EstimateCost("gpt-5", TokenUsage{UncachedInput: 1_000_000}); !approxCostEq(got, 1.25) {
		t.Errorf("input-only: got %f, want 1.25", got)
	}
	if got, _ := EstimateCost("gpt-5", TokenUsage{Output: 1_000_000}); !approxCostEq(got, 10.0) {
		t.Errorf("output-only: got %f, want 10", got)
	}
}

func approxCostEq(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-6
}

func TestEstimateCost_CacheReadUsesCacheRate(t *testing.T) {
	// claude-opus-4-5: $5/M input, $25/M output, $0.50/M cache_read.
	// 1M cache_read = $0.50, NOT $5 (which it'd be if we over-billed cache).
	got, ok := EstimateCost("claude-opus-4-5", TokenUsage{CacheRead: 1_000_000})
	if !ok {
		t.Fatal("expected known model")
	}
	if !approxCostEq(got, 0.5) {
		t.Errorf("cache_read priced at cache rate: got %f, want 0.5", got)
	}
}

func TestEstimateCost_CacheCreationUsesCreationRate(t *testing.T) {
	// claude-opus-4-5: cache_creation_5m at $6.25/M.
	got, ok := EstimateCost("claude-opus-4-5", TokenUsage{CacheCreation5m: 1_000_000})
	if !ok {
		t.Fatal("expected known model")
	}
	if !approxCostEq(got, 6.25) {
		t.Errorf("cache_creation priced at creation rate: got %f, want 6.25", got)
	}
}

func TestCollector_ForEachIterationsAutoLink(t *testing.T) {
	// Events for "p::0" and "p::1" should auto-link to parent "p" without
	// the caller having to call SetParent. Rollup on "p" must include both
	// iterations even when no ancestor edge was explicitly registered.
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("p::0", 100, 50, 0, 0, "gpt-5"))
	c.ProcessEvent(makeUsageEvent("p::1", 200, 75, 0, 0, "gpt-5"))

	// p itself emits no events; only the iteration children do.
	_, rollup, ok := c.NodeMetrics("p")
	if !ok {
		t.Fatal("parent p not tracked — auto-link missed")
	}
	// Combined across iterations: input=300, output=125.
	if rollup.Tokens.Input != 300 {
		t.Errorf("rollup input: got %d, want 300", rollup.Tokens.Input)
	}
	if rollup.Tokens.Output != 125 {
		t.Errorf("rollup output: got %d, want 125", rollup.Tokens.Output)
	}
}

func TestCollector_TracksUnpricedEventCount(t *testing.T) {
	// One priced (gpt-5), two unpriced (made-up). The unpriced count
	// must surface on the node's own totals and on the run total so the
	// UI can show a warning badge.
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("n", 100, 50, 0, 0, "gpt-5"))
	c.ProcessEvent(makeUsageEvent("n", 200, 75, 0, 0, "unknown-made-up-model"))
	c.ProcessEvent(makeUsageEvent("n", 300, 90, 0, 0, "also-fake-model"))

	own, _, _ := c.NodeMetrics("n")
	if own.UnpricedEventCount != 2 {
		t.Errorf("own unpriced count: got %d, want 2", own.UnpricedEventCount)
	}
	// Cost is still set (from the one gpt-5 event); it's just incomplete.
	if own.CostUSD == nil {
		t.Error("CostUSD should reflect the priced event, not nil")
	}

	total := c.RunTotal()
	if total.UnpricedEventCount != 2 {
		t.Errorf("run total unpriced count: got %d, want 2", total.UnpricedEventCount)
	}
}

func TestCollector_MixedModelsInOneNode_PricesPerEvent(t *testing.T) {
	// A node that emitted two events with different models must price each
	// event at that event's model's rate, not apply the last-seen model's
	// rate to the combined tokens.
	//
	// gpt-5:           $1.25/M in, $10/M out
	// claude-opus-4-5: $5/M in,    $25/M out
	//
	// Event 1 at gpt-5:           1M in + 1M out = 1.25 + 10 = $11.25
	// Event 2 at claude-opus-4-5: 1M in + 1M out = 5.00 + 25 = $30.00
	// Correct total: $41.25
	// Wrong (last-model applied to sum): 2M * 5 + 2M * 25 = $60
	// Wrong (first-model applied to sum): 2M * 1.25 + 2M * 10 = $22.50
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("mixed", 1_000_000, 1_000_000, 0, 0, "gpt-5"))
	c.ProcessEvent(makeUsageEvent("mixed", 1_000_000, 1_000_000, 0, 0, "claude-opus-4-5"))

	own, _, _ := c.NodeMetrics("mixed")
	if own.CostUSD == nil {
		t.Fatal("multi-model cost: nil")
	}
	if !approxCostEq(*own.CostUSD, 41.25) {
		t.Errorf("multi-model cost: got %f, want 41.25 (per-event pricing)", *own.CostUSD)
	}
}

func TestEstimateCost_AllBucketsTogether(t *testing.T) {
	// Realistic Anthropic call: 1k uncached new, 10k cache_read, 0 write, 500 output.
	// claude-opus-4-5: 1k * 5/M + 10k * 0.5/M + 500 * 25/M
	// = 0.005 + 0.005 + 0.0125 = 0.0225
	got, ok := EstimateCost("claude-opus-4-5", TokenUsage{
		UncachedInput: 1000, CacheRead: 10_000, Output: 500,
	})
	if !ok {
		t.Fatal("expected known model")
	}
	if !approxCostEq(got, 0.0225) {
		t.Errorf("mixed buckets: got %f, want 0.0225", got)
	}
}

func TestCollector_Duration_FromStartEnd(t *testing.T) {
	c := NewCollector()
	start := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	end := start.Add(2500 * time.Millisecond)
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: start})
	c.ProcessEvent(state.Event{Type: "node_completed", NodeID: "n", Timestamp: end})

	own, _, _ := c.NodeMetrics("n")
	if own.DurationMs != 2500 {
		t.Errorf("DurationMs: got %d, want 2500", own.DurationMs)
	}
}

func TestCollector_Duration_RunningNodeUsesNow(t *testing.T) {
	c := NewCollector()
	// started_at 10 seconds ago, no end event -> duration is "now - started".
	start := time.Now().Add(-10 * time.Second)
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: start})

	own, _, _ := c.NodeMetrics("n")
	// Wide window so a CI pause/suspend can't flake the assertion — the
	// real invariant is "near 10s and non-zero," not a precise match.
	if own.DurationMs < 8_000 || own.DurationMs > 20_000 {
		t.Errorf("DurationMs running: got %d, want ~10000 (wide tolerance)", own.DurationMs)
	}
}

func TestCollector_Duration_RetrySum(t *testing.T) {
	c := NewCollector()
	t0 := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	// Attempt 1: 1s
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: t0})
	c.ProcessEvent(state.Event{Type: "node_failed", NodeID: "n", Timestamp: t0.Add(1 * time.Second)})
	// Attempt 2: 2s
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: t0.Add(5 * time.Second)})
	c.ProcessEvent(state.Event{Type: "node_completed", NodeID: "n", Timestamp: t0.Add(7 * time.Second)})

	own, _, _ := c.NodeMetrics("n")
	if own.DurationMs != 3000 {
		t.Errorf("Retry sum duration: got %d, want 3000", own.DurationMs)
	}
}

func TestCollector_Duration_SkippedShowsZero(t *testing.T) {
	c := NewCollector()
	c.ProcessEvent(state.Event{Type: "node_skipped", NodeID: "n", Timestamp: time.Now()})
	own, _, ok := c.NodeMetrics("n")
	if !ok {
		t.Fatal("skipped node should be recorded")
	}
	if own.DurationMs != 0 {
		t.Errorf("Skipped DurationMs: got %d, want 0", own.DurationMs)
	}
	if own.Tokens.Total != 0 {
		t.Errorf("Skipped Tokens.Total: got %d, want 0", own.Tokens.Total)
	}
}

func TestCollector_Duration_CancelledWhileRunningClosesOut(t *testing.T) {
	// Cancellation is emitted as node_skipped(reason=cancelled). If the
	// node was already running, the in-flight attempt must be closed so
	// duration doesn't tick forever.
	c := NewCollector()
	t0 := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: t0})
	c.ProcessEvent(state.Event{Type: "node_skipped", NodeID: "n", Timestamp: t0.Add(3 * time.Second)})

	own, _, _ := c.NodeMetrics("n")
	if own.DurationMs != 3000 {
		t.Errorf("cancelled-while-running duration: got %d, want 3000", own.DurationMs)
	}
}

func TestCollector_Rollup_Simple(t *testing.T) {
	c := NewCollector()
	// Parent "p" with leaf children "p::0" and "p::1" (ForEach shape).
	c.SetParent("p::0", "p")
	c.SetParent("p::1", "p")

	c.ProcessEvent(makeUsageEvent("p::0", 100, 50, 0, 0, "gpt-5.4"))
	c.ProcessEvent(makeUsageEvent("p::1", 200, 75, 0, 0, "gpt-5.4"))

	_, rollup, ok := c.NodeMetrics("p")
	if !ok {
		t.Fatal("parent should be present with rollup")
	}
	if rollup.Tokens.Input != 300 {
		t.Errorf("rollup Input: got %d, want 300", rollup.Tokens.Input)
	}
	if rollup.Tokens.Output != 125 {
		t.Errorf("rollup Output: got %d, want 125", rollup.Tokens.Output)
	}
	if rollup.Tokens.Total != 425 {
		t.Errorf("rollup Total: got %d, want 425", rollup.Tokens.Total)
	}
}

func TestCollector_Rollup_WallTime(t *testing.T) {
	c := NewCollector()
	c.SetParent("child_a", "p")
	c.SetParent("child_b", "p")
	t0 := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	// child_a runs 0-3s, child_b runs 2-5s. Wall time for p is 0-5s = 5000ms.
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "child_a", Timestamp: t0})
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "child_b", Timestamp: t0.Add(2 * time.Second)})
	c.ProcessEvent(state.Event{Type: "node_completed", NodeID: "child_a", Timestamp: t0.Add(3 * time.Second)})
	c.ProcessEvent(state.Event{Type: "node_completed", NodeID: "child_b", Timestamp: t0.Add(5 * time.Second)})

	_, rollup, _ := c.NodeMetrics("p")
	if rollup.DurationMs != 5000 {
		t.Errorf("rollup wall-time: got %d, want 5000", rollup.DurationMs)
	}
}

func TestCollector_RunTotal(t *testing.T) {
	c := NewCollector()
	c.ProcessEvent(makeUsageEvent("a", 100, 50, 0, 0, "gpt-5.4"))
	c.ProcessEvent(makeUsageEvent("b", 200, 75, 0, 0, "gpt-5.4"))

	rt := c.RunTotal()
	if rt.Tokens.Total != 425 {
		t.Errorf("RunTotal.Total: got %d, want 425", rt.Tokens.Total)
	}
}

func TestCollector_Changes_EmitsAffectedNodes(t *testing.T) {
	c := NewCollector()
	c.SetParent("child", "parent")

	ch := c.Changes()

	c.ProcessEvent(makeUsageEvent("child", 100, 50, 0, 0, "gpt-5.4"))

	select {
	case ids := <-ch:
		// Should include both the leaf and its ancestor.
		have := map[string]bool{}
		for _, id := range ids {
			have[id] = true
		}
		if !have["child"] {
			t.Errorf("expected \"child\" in Changes; got %v", ids)
		}
		if !have["parent"] {
			t.Errorf("expected \"parent\" (ancestor) in Changes; got %v", ids)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Changes() did not emit within 100ms")
	}
}
