package metrics

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"primeradiant.com/serf/llm"
	"primeradiant.com/toil/internal/state"
)

const (
	eventNodeOutput    = "node_output"
	eventNodeStarted   = "node_started"
	eventNodeCompleted = "node_completed"
	eventNodeSkipped   = "node_skipped"
)

// TokenUsage is the bucketed input breakdown used for pricing. Every
// bucket represents tokens billed at a separate rate.
//
// Output counts total output tokens (including any reasoning tokens the
// provider billed there). Callers must not add ReasoningTokens to
// Output — reasoning is already inside OutputTokens per every supported
// provider.
type TokenUsage struct {
	UncachedInput   int // "new input," billed at the model's input rate
	CacheRead       int // cached input, billed at cache-read rate (or input rate if unknown)
	CacheCreation5m int // input written to 5-min-TTL cache (Anthropic)
	CacheCreation1h int // input written to 1-h-TTL cache (Anthropic). Serf does not yet populate this.
	Output          int // total output tokens; reasoning billed via this
}

// EstimateCost returns the estimated USD cost for a usage breakdown under
// the given model. Returns (cost, true) when the model is priceable via
// serf's embedded LiteLLM catalog; (0, false) for unknown models.
//
// Cache-tier semantics:
//   - CacheRead is priced at cache_read_input_token_cost if the catalog has
//     one for the model; otherwise at the input rate (safe over-estimate).
//   - CacheCreation5m is priced at cache_creation_input_token_cost if
//     available; otherwise at the input rate. CacheCreation1h falls back to
//     the 5m rate, then to input rate.
//   - Output (including reasoning) is always priced at the output rate.
func EstimateCost(model string, u TokenUsage) (float64, bool) {
	price, ok := llm.DefaultPrice(model)
	if !ok {
		return 0, false
	}
	cost := float64(u.UncachedInput)/1_000_000.0*price.InputPerM +
		float64(u.Output)/1_000_000.0*price.OutputPerM
	cost += float64(u.CacheRead) / 1_000_000.0 * firstNonNilOrFallback(price.CacheReadPerM, price.InputPerM)
	cost += float64(u.CacheCreation5m) / 1_000_000.0 * firstNonNilOrFallback(price.CacheCreation5mPerM, price.InputPerM)
	cost += float64(u.CacheCreation1h) / 1_000_000.0 * firstNonNilOrFallback(price.CacheCreation1hPerM, price.CacheCreation5mPerM, price.InputPerM)
	return cost, true
}

// firstNonNilOrFallback returns the first non-nil *float64 in order,
// dereferenced. If all are nil, returns the final float64 fallback.
func firstNonNilOrFallback(choices ...interface{}) float64 {
	for i, c := range choices {
		if i == len(choices)-1 {
			if f, ok := c.(float64); ok {
				return f
			}
			if p, ok := c.(*float64); ok && p != nil {
				return *p
			}
			return 0
		}
		if p, ok := c.(*float64); ok && p != nil {
			return *p
		}
	}
	return 0
}

// TokenBreakdown and NodeTotals live in internal/state to avoid an import
// cycle (state cannot import metrics; metrics already imports state).
// Callers reference them as state.TokenBreakdown and state.NodeTotals.
// Canonical docs are on the type definitions in internal/state/totals.go.

type nodeAccum struct {
	tokens          state.TokenBreakdown
	model           string  // last-seen model; used only for informational display
	costUSD         float64 // running sum of per-event costs (in USD)
	hasPricedEvent  bool    // true if at least one event was priced; drives CostUSD presence
	unpricedEvents  int     // count of events whose model was not in the catalog
	totalDurationMs int64
	lastStarted     time.Time // zero when not mid-attempt
	firstStart      time.Time // earliest start across all attempts
	lastEnd         time.Time // latest end across all attempts
	skipped         bool
}

// Collector accumulates per-node metrics from a stream of events.
// It is safe for concurrent use.
type Collector struct {
	mu       sync.RWMutex
	nodes    map[string]*nodeAccum
	parent   map[string]string // childID -> parentID
	changeCh chan []string
}

func NewCollector() *Collector {
	return &Collector{
		nodes:    make(map[string]*nodeAccum),
		parent:   make(map[string]string),
		changeCh: make(chan []string, 64),
	}
}

// Changes returns a channel that emits lists of changed node IDs after each
// ProcessEvent call. Each batch contains the touched leaf plus all its
// ancestors (most-specific first). The channel is buffered (size 64); when
// full, the oldest batch is dropped to make room for the new one.
func (c *Collector) Changes() <-chan []string {
	return c.changeCh
}

// emitChange sends a batch of affected node IDs — the leaf plus all ancestors
// — to changeCh. If the buffer is full, drops the oldest batch and retries.
// Caller must hold c.mu (read or write is fine since only writing to channel).
func (c *Collector) emitChange(leafID string) {
	ids := []string{leafID}
	for cur := leafID; ; {
		p, ok := c.parent[cur]
		if !ok {
			break
		}
		ids = append(ids, p)
		cur = p
	}
	select {
	case c.changeCh <- ids:
	default:
		// Buffer full — drop oldest to make room.
		select {
		case <-c.changeCh:
		default:
		}
		select {
		case c.changeCh <- ids:
		default:
		}
	}
}

// SetParent registers a parent/child relationship for rollup computation.
// Call once per compound-node-to-child edge before or during event processing.
// Callers derive this from the workflow definition and from ForEach
// iteration-ID namespacing (child::N has parent `child`).
func (c *Collector) SetParent(child, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parent[child] = parent
	// Ensure the parent has a slot even if it never emits its own events.
	if _, ok := c.nodes[parent]; !ok {
		c.nodes[parent] = &nodeAccum{}
	}
}

// linkForEachIteration auto-registers foo::N -> foo parent relationships
// the first time an iteration node is observed. Caller must hold c.mu.Lock.
// Noop if nodeID already has a parent (explicit SetParent wins) or lacks
// the "::" suffix.
func (c *Collector) linkForEachIteration(nodeID string) {
	if _, already := c.parent[nodeID]; already {
		return
	}
	i := strings.LastIndex(nodeID, "::")
	if i <= 0 {
		return
	}
	parent := nodeID[:i]
	c.parent[nodeID] = parent
	if _, ok := c.nodes[parent]; !ok {
		c.nodes[parent] = &nodeAccum{}
	}
}

// accum returns the accumulator for nodeID, creating it if absent.
// Caller must hold c.mu.Lock.
func (c *Collector) accum(nodeID string) *nodeAccum {
	a, ok := c.nodes[nodeID]
	if !ok {
		a = &nodeAccum{}
		c.nodes[nodeID] = a
	}
	return a
}

// ProcessEvent updates the collector's state from a single event.
func (c *Collector) ProcessEvent(ev state.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ev.NodeID == "" {
		return
	}
	a := c.accum(ev.NodeID)
	c.linkForEachIteration(ev.NodeID)
	changed := true
	switch ev.Type {
	case eventNodeOutput:
		c.consumeRunnerEvent(ev)
	case eventNodeStarted:
		a.lastStarted = ev.Timestamp
		if a.firstStart.IsZero() || ev.Timestamp.Before(a.firstStart) {
			a.firstStart = ev.Timestamp
		}
	case eventNodeCompleted, "node_failed":
		if !a.lastStarted.IsZero() {
			a.totalDurationMs += ev.Timestamp.Sub(a.lastStarted).Milliseconds()
			a.lastStarted = time.Time{}
		}
		if a.lastEnd.IsZero() || ev.Timestamp.After(a.lastEnd) {
			a.lastEnd = ev.Timestamp
		}
	case eventNodeSkipped:
		// Cancellation is emitted as node_skipped(reason=cancelled); if the
		// node was running when cancelled, close out the in-flight attempt
		// so duration doesn't tick forever. Plain skips (lastStarted zero)
		// are no-ops.
		if !a.lastStarted.IsZero() {
			a.totalDurationMs += ev.Timestamp.Sub(a.lastStarted).Milliseconds()
			a.lastStarted = time.Time{}
			if a.lastEnd.IsZero() || ev.Timestamp.After(a.lastEnd) {
				a.lastEnd = ev.Timestamp
			}
		}
		a.skipped = true
	default:
		changed = false
	}
	if changed {
		c.emitChange(ev.NodeID)
	}
}

// consumeRunnerEvent parses one node_output event's JSON envelope and
// accumulates tokens when the event carries an ASSISTANT_TEXT_END usage
// payload. Caller must hold c.mu.Lock.
func (c *Collector) consumeRunnerEvent(ev state.Event) {
	text := strings.TrimSpace(ev.Text)
	if text == "" || text[0] != '{' {
		return
	}
	var raw struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return
	}
	if raw.Kind != "ASSISTANT_TEXT_END" {
		return
	}
	var ate struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadTokens          int `json:"cache_read_tokens"`
			CacheWriteTokens         int `json:"cache_write_tokens"`
			CacheWrite1hTokens       int `json:"cache_write_1h_tokens"`
			ReasoningTokens          int `json:"reasoning_tokens"`
			ReasoningTokensEstimated int `json:"reasoning_tokens_estimated"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw.Data, &ate); err != nil {
		return
	}
	a := c.accum(ev.NodeID)
	a.tokens.Input += ate.Usage.InputTokens
	a.tokens.Output += ate.Usage.OutputTokens
	a.tokens.CacheRead += ate.Usage.CacheReadTokens
	a.tokens.CacheWrite += ate.Usage.CacheWriteTokens
	a.tokens.CacheWrite1h += ate.Usage.CacheWrite1hTokens
	a.tokens.Reasoning += ate.Usage.ReasoningTokens
	a.tokens.ReasoningEstimated += ate.Usage.ReasoningTokensEstimated
	// Total counts every token the provider processed. Reasoning is a subset of
	// Output on every supported provider (see TokenBreakdown doc), so it is not
	// added separately.
	a.tokens.Total = a.tokens.Input + a.tokens.Output + a.tokens.CacheRead +
		a.tokens.CacheWrite + a.tokens.CacheWrite1h
	if ate.Model != "" {
		a.model = ate.Model
	}
	// Price this event at its own model's rate, then accumulate. A node that
	// called multiple models is priced correctly as the sum of per-event costs
	// rather than one model's rate applied to the combined tokens.
	eventUsage := TokenUsage{
		UncachedInput:   ate.Usage.InputTokens,
		CacheRead:       ate.Usage.CacheReadTokens,
		CacheCreation5m: ate.Usage.CacheWriteTokens,
		CacheCreation1h: ate.Usage.CacheWrite1hTokens,
		Output:          ate.Usage.OutputTokens,
	}
	if cost, ok := EstimateCost(ate.Model, eventUsage); ok {
		a.costUSD += cost
		a.hasPricedEvent = true
	} else {
		a.unpricedEvents++
	}
}

// NodeMetrics returns the node's own totals and a rollup over its descendants.
func (c *Collector) NodeMetrics(nodeID string) (own state.NodeTotals, rollup state.NodeTotals, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	a, exists := c.nodes[nodeID]
	if !exists {
		return state.NodeTotals{}, state.NodeTotals{}, false
	}
	own = c.totalsFromAccum(a)
	rollup = c.computeRollup(nodeID, a)
	return own, rollup, true
}

func (c *Collector) totalsFromAccum(a *nodeAccum) state.NodeTotals {
	t := state.NodeTotals{Tokens: a.tokens}
	t.DurationMs = a.totalDurationMs
	if !a.lastStarted.IsZero() {
		t.DurationMs += time.Since(a.lastStarted).Milliseconds()
	}
	if a.hasPricedEvent {
		cost := a.costUSD
		t.CostUSD = &cost
	}
	t.UnpricedEventCount = a.unpricedEvents
	return t
}

// descendantsOf returns all nodes whose chain of parents passes through root.
// Excludes root itself. Caller must hold at least c.mu.RLock.
func (c *Collector) descendantsOf(root string) []string {
	children := map[string][]string{}
	for child, par := range c.parent {
		children[par] = append(children[par], child)
	}
	var out []string
	var walk func(id string)
	walk = func(id string) {
		for _, child := range children[id] {
			out = append(out, child)
			walk(child)
		}
	}
	walk(root)
	return out
}

// computeRollup returns a state.NodeTotals that sums tokens and cost over all
// descendants of nodeID, and computes wall-time as maxEnd - minStart across
// those descendants. Caller must hold c.mu.RLock.
func (c *Collector) computeRollup(nodeID string, own *nodeAccum) state.NodeTotals {
	rollup := c.totalsFromAccum(own)
	var minStart, maxEnd time.Time
	for _, descID := range c.descendantsOf(nodeID) {
		d := c.nodes[descID]
		if d == nil {
			continue
		}
		rollup.Tokens.Input += d.tokens.Input
		rollup.Tokens.Output += d.tokens.Output
		rollup.Tokens.CacheRead += d.tokens.CacheRead
		rollup.Tokens.CacheWrite += d.tokens.CacheWrite
		rollup.Tokens.CacheWrite1h += d.tokens.CacheWrite1h
		rollup.Tokens.Reasoning += d.tokens.Reasoning
		rollup.Tokens.ReasoningEstimated += d.tokens.ReasoningEstimated
		if d.hasPricedEvent {
			rollup.CostUSD = addCosts(rollup.CostUSD, d.costUSD)
		}
		rollup.UnpricedEventCount += d.unpricedEvents
		ds, de := d.firstStart, d.lastEnd
		if !ds.IsZero() && (minStart.IsZero() || ds.Before(minStart)) {
			minStart = ds
		}
		if !de.IsZero() && (maxEnd.IsZero() || de.After(maxEnd)) {
			maxEnd = de
		}
	}
	rollup.Tokens.Total = rollup.Tokens.Input + rollup.Tokens.Output +
		rollup.Tokens.CacheRead + rollup.Tokens.CacheWrite + rollup.Tokens.CacheWrite1h
	if !minStart.IsZero() && !maxEnd.IsZero() {
		rollup.DurationMs = maxEnd.Sub(minStart).Milliseconds()
	}
	return rollup
}

// AllNodeIDs returns every node ID the collector has observed.
func (c *Collector) AllNodeIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.nodes))
	for id := range c.nodes {
		ids = append(ids, id)
	}
	return ids
}

// RunTotal returns the sum across all leaf nodes in the collector.
// A leaf is any node that is not a parent in the parent map.
func (c *Collector) RunTotal() state.NodeTotals {
	c.mu.RLock()
	defer c.mu.RUnlock()
	parents := map[string]bool{}
	for _, p := range c.parent {
		parents[p] = true
	}
	var total state.NodeTotals
	var minStart, maxEnd time.Time
	for nodeID, a := range c.nodes {
		if parents[nodeID] {
			continue
		}
		total.Tokens.Input += a.tokens.Input
		total.Tokens.Output += a.tokens.Output
		total.Tokens.CacheRead += a.tokens.CacheRead
		total.Tokens.CacheWrite += a.tokens.CacheWrite
		total.Tokens.CacheWrite1h += a.tokens.CacheWrite1h
		total.Tokens.Reasoning += a.tokens.Reasoning
		total.Tokens.ReasoningEstimated += a.tokens.ReasoningEstimated
		if a.hasPricedEvent {
			total.CostUSD = addCosts(total.CostUSD, a.costUSD)
		}
		total.UnpricedEventCount += a.unpricedEvents
		if !a.firstStart.IsZero() && (minStart.IsZero() || a.firstStart.Before(minStart)) {
			minStart = a.firstStart
		}
		if !a.lastEnd.IsZero() && (maxEnd.IsZero() || a.lastEnd.After(maxEnd)) {
			maxEnd = a.lastEnd
		}
	}
	total.Tokens.Total = total.Tokens.Input + total.Tokens.Output +
		total.Tokens.CacheRead + total.Tokens.CacheWrite + total.Tokens.CacheWrite1h
	if !minStart.IsZero() && !maxEnd.IsZero() {
		total.DurationMs = maxEnd.Sub(minStart).Milliseconds()
	}
	return total
}

// addCosts adds an accumulator (nullable, nil = unknown) and an
// additional known cost. If the accumulator was nil, the result points
// at a copy of the new cost; otherwise the result points at the sum.
func addCosts(acc *float64, delta float64) *float64 {
	if acc == nil {
		v := delta
		return &v
	}
	v := *acc + delta
	return &v
}
