package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

// runUpdate is a per-run batch of changed node IDs emitted by the
// execution-group follow-mode stream's tail goroutines.
type runUpdate struct {
	runID   string
	changed map[string]bool
}

// runMetricsResponse is the JSON shape for GET /runs/{id}/metrics.
type runMetricsResponse struct {
	RunID       string                      `json:"run_id"`
	GeneratedAt time.Time                   `json:"generated_at"`
	RunTotal    state.NodeTotals            `json:"run_total"`
	Nodes       map[string]nodeMetricDetail `json:"nodes"`
}

type nodeMetricDetail struct {
	Status      string           `json:"status,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	EndedAt     *time.Time       `json:"ended_at,omitempty"`
	Attempts    int              `json:"attempts,omitempty"`
	Dispatches  int              `json:"dispatches,omitempty"`
	Own         state.NodeTotals `json:"own"`
	Rollup      state.NodeTotals `json:"rollup"`
	ChildRunIDs []string         `json:"child_run_ids,omitempty"`
}

func (server *Server) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/metrics")
	if runID == "" || strings.Contains(runID, "/") {
		http.Error(writer, "bad run id", http.StatusBadRequest)
		return
	}

	runDir := filepath.Join(server.RunsDir, runID)
	statePath := filepath.Join(runDir, "state.json")
	eventsPath := filepath.Join(runDir, "events.jsonl")

	rs, err := state.LoadState(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(writer, "run not found", http.StatusNotFound)
			return
		}
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	events, _, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil && !os.IsNotExist(err) {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	c := metrics.NewCollector()
	wireParents(c, rs)
	for _, ev := range events {
		c.ProcessEvent(ev)
	}

	resp := runMetricsResponse{
		RunID:       runID,
		GeneratedAt: time.Now().UTC(),
		RunTotal:    c.RunTotal(),
		Nodes:       map[string]nodeMetricDetail{},
	}
	// Collect per-node detail. Use the state's node map as primary source
	// (it carries status, attempts, timestamps), and fall back to the
	// collector's known IDs for any node that the events recorded but the
	// state snapshot doesn't include.
	stateNodeIDs := map[string]bool{}
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, ns := range nodes {
			stateNodeIDs[id] = true
			own, rollup, _ := c.NodeMetrics(id)
			var childRuns []string
			if ns.Data != nil {
				if cr, ok := ns.Data["child_run"].(string); ok && cr != "" {
					childRuns = []string{cr}
				}
			}
			resp.Nodes[id] = nodeMetricDetail{
				Status:      ns.Status,
				StartedAt:   ns.StartedAt,
				EndedAt:     ns.EndedAt,
				Attempts:    ns.Attempts,
				Dispatches:  ns.Dispatches,
				Own:         own,
				Rollup:      rollup,
				ChildRunIDs: childRuns,
			}
		}
	})
	for _, id := range c.AllNodeIDs() {
		if stateNodeIDs[id] {
			continue
		}
		own, rollup, _ := c.NodeMetrics(id)
		resp.Nodes[id] = nodeMetricDetail{
			Own:    own,
			Rollup: rollup,
		}
	}

	follow := request.URL.Query().Get("follow") == queryFollowTrue
	if !follow {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(resp)
		return
	}

	// SSE follow mode.
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Initial snapshot.
	initial, _ := json.Marshal(resp)
	fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", initial)
	flusher.Flush()

	// If run is already terminal, nothing more to stream.
	if isTerminalRunStatus(rs.Status) {
		return
	}

	// Tail new events from the current byte offset.
	ctx := request.Context()
	_, initialOffset, _ := state.ReadEventsWithOffset(eventsPath)
	tail := state.TailEvents(ctx, eventsPath, initialOffset)

	// Coalesce changes into 500ms batches before flushing.
	coalesceTimer := time.NewTimer(time.Hour)
	if !coalesceTimer.Stop() {
		<-coalesceTimer.C
	}
	pending := map[string]bool{}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		update := buildMetricUpdate(c, runID, pending)
		updateBytes, _ := json.Marshal(update)
		fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", updateBytes)
		flusher.Flush()
		pending = map[string]bool{}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case ev, ok := <-tail:
			if !ok {
				flush()
				return
			}
			c.ProcessEvent(ev)
			if ev.Type == eventTypeRunCompleted || ev.Type == eventTypeRunFailed {
				drainChanges(c, pending)
				flush()
				return
			}
			drainChanges(c, pending)
			if len(pending) > 0 {
				_ = coalesceTimer.Reset(500 * time.Millisecond)
			}
		case <-coalesceTimer.C:
			flush()
		}
	}
}

// drainChanges performs a non-blocking read of all pending change notifications
// from the collector's Changes channel, adding each affected node ID to into.
func drainChanges(c *metrics.Collector, into map[string]bool) {
	for {
		select {
		case ids := <-c.Changes():
			for _, id := range ids {
				into[id] = true
			}
		default:
			return
		}
	}
}

// metricUpdate is the shape of each metric-update SSE event payload.
type metricUpdate struct {
	Type     string                     `json:"type"`
	RunID    string                     `json:"run_id,omitempty"`
	Nodes    map[string]nodeMetricShort `json:"nodes"`
	RunTotal state.NodeTotals           `json:"run_total"`
}

// nodeMetricShort is the per-node payload in a metric-update event.
type nodeMetricShort struct {
	Own    state.NodeTotals `json:"own"`
	Rollup state.NodeTotals `json:"rollup"`
}

// buildMetricUpdate assembles a metricUpdate for the given set of changed node IDs.
func buildMetricUpdate(c *metrics.Collector, runID string, ids map[string]bool) metricUpdate {
	out := metricUpdate{
		Type:     "metric-update",
		RunID:    runID,
		Nodes:    map[string]nodeMetricShort{},
		RunTotal: c.RunTotal(),
	}
	for id := range ids {
		own, rollup, _ := c.NodeMetrics(id)
		out.Nodes[id] = nodeMetricShort{Own: own, Rollup: rollup}
	}
	return out
}

// wireParents registers parent/child relationships on the collector from
// ForEach iteration node IDs (e.g. "child::0" gets parent "child") so
// rollups compute correctly.
func wireParents(c *metrics.Collector, rs *state.RunState) {
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id := range nodes {
			if idx := strings.Index(id, "::"); idx > 0 {
				c.SetParent(id, id[:idx])
			}
		}
	})
}

// executionGroupMetricsResponse is the JSON shape for GET /runs/{id}/execution-group/metrics.
type executionGroupMetricsResponse struct {
	RootRunID  string                            `json:"root_run_id"`
	GroupTotal state.NodeTotals                  `json:"group_total"`
	Runs       map[string]executionGroupRunEntry `json:"runs"`
}

// executionGroupRunEntry is the per-run section of the group metrics response.
type executionGroupRunEntry struct {
	ParentRunID string                      `json:"parent_run_id,omitempty"`
	RunTotal    state.NodeTotals            `json:"run_total"`
	Nodes       map[string]nodeMetricDetail `json:"nodes"`
}

func (server *Server) handleExecutionGroupMetrics(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	path = strings.TrimSuffix(path, "/execution-group/metrics")
	rootRunID := strings.TrimSpace(path)
	if rootRunID == "" || strings.Contains(rootRunID, "/") {
		http.Error(writer, "bad run id", http.StatusBadRequest)
		return
	}

	runIDs, err := server.collectGroupRunIDs(rootRunID)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := executionGroupMetricsResponse{
		RootRunID: rootRunID,
		Runs:      map[string]executionGroupRunEntry{},
	}
	var group state.NodeTotals

	for _, id := range runIDs {
		entry, err := server.buildSingleRunMetrics(id)
		if err != nil {
			continue
		}
		resp.Runs[id] = entry
		addInto(&group, entry.RunTotal)
	}
	resp.GroupTotal = group

	if request.URL.Query().Get("follow") != queryFollowTrue {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(resp)
		return
	}
	server.streamExecutionGroupMetrics(writer, request, rootRunID, runIDs, resp)
}

// streamExecutionGroupMetrics serves the follow-mode SSE stream. Each active
// run in the group gets its own tail + Collector; metric-update events carry
// the source run_id so the frontend can qualify node IDs as runID::nodeID.
func (server *Server) streamExecutionGroupMetrics(
	writer http.ResponseWriter,
	request *http.Request,
	rootRunID string,
	runIDs []string,
	initial executionGroupMetricsResponse,
) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	// Initial snapshot — one metric-update event per run so the frontend
	// can populate each run's nodes before streams attach.
	for id, entry := range initial.Runs {
		snap := metricUpdate{
			Type:     "metric-update",
			RunID:    id,
			Nodes:    map[string]nodeMetricShort{},
			RunTotal: entry.RunTotal,
		}
		for nid, nd := range entry.Nodes {
			snap.Nodes[nid] = nodeMetricShort{Own: nd.Own, Rollup: nd.Rollup}
		}
		b, _ := json.Marshal(snap)
		_, _ = fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", b)
	}
	flusher.Flush()

	// Spin one goroutine per run to tail its events and feed a collector.
	// Updates funnel through a shared channel; the writer coalesces at 500ms.
	updates := make(chan runUpdate, 32)
	collectors := map[string]*metrics.Collector{}
	ctx := request.Context()

	for _, id := range runIDs {
		rs, err := state.LoadState(filepath.Join(server.RunsDir, id, "state.json"))
		if err != nil {
			continue
		}
		if isTerminalRunStatus(rs.Status) {
			continue // no further events expected; initial snapshot suffices
		}
		c := buildRunCollector(server.RunsDir, rs)
		if c == nil {
			continue
		}
		collectors[id] = c
		eventsPath := filepath.Join(server.RunsDir, id, "events.jsonl")
		_, offset, _ := state.ReadEventsWithOffset(eventsPath)
		go tailRunForStream(ctx, id, eventsPath, offset, c, updates)
	}

	coalesce := time.NewTimer(time.Hour)
	if !coalesce.Stop() {
		<-coalesce.C
	}
	pending := map[string]map[string]bool{}
	flush := func() {
		for id, ids := range pending {
			c := collectors[id]
			if c == nil || len(ids) == 0 {
				continue
			}
			update := buildMetricUpdate(c, id, ids)
			if b, err := json.Marshal(update); err == nil {
				_, _ = fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", b)
			}
		}
		pending = map[string]map[string]bool{}
		flusher.Flush()
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case u, ok := <-updates:
			if !ok {
				flush()
				return
			}
			if pending[u.runID] == nil {
				pending[u.runID] = map[string]bool{}
			}
			for id := range u.changed {
				pending[u.runID][id] = true
			}
			_ = coalesce.Reset(500 * time.Millisecond)
		case <-coalesce.C:
			flush()
		}
	}
}

// tailRunForStream tails one run's events.jsonl, feeds them into the
// collector, and emits runUpdate messages when the collector reports
// changes. Terminates on context cancel or run_completed/run_failed.
func tailRunForStream(
	ctx context.Context,
	runID, eventsPath string,
	offset int64,
	c *metrics.Collector,
	out chan<- runUpdate,
) {
	tail := state.TailEvents(ctx, eventsPath, offset)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-tail:
			if !ok {
				return
			}
			c.ProcessEvent(ev)
			changed := map[string]bool{}
			for {
				select {
				case ids := <-c.Changes():
					for _, id := range ids {
						changed[id] = true
					}
				default:
					goto done
				}
			}
		done:
			if len(changed) > 0 {
				select {
				case out <- runUpdate{runID: runID, changed: changed}:
				case <-ctx.Done():
					return
				}
			}
			if ev.Type == eventTypeRunCompleted || ev.Type == eventTypeRunFailed {
				return
			}
		}
	}
}

// collectGroupRunIDs walks the parent/child run tree starting at rootRunID,
// scanning the runs directory for state files that reference a parent.
func (server *Server) collectGroupRunIDs(rootRunID string) ([]string, error) {
	entries, err := os.ReadDir(server.RunsDir)
	if err != nil {
		return nil, err
	}
	// Index parent relationships.
	parent := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(server.RunsDir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		parent[rs.ID] = rs.ParentRun
	}
	// BFS from rootRunID through the reversed relationship.
	children := map[string][]string{}
	for c, p := range parent {
		if p != "" {
			children[p] = append(children[p], c)
		}
	}
	var out []string
	queue := []string{rootRunID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		queue = append(queue, children[cur]...)
	}
	return out, nil
}

// buildSingleRunMetrics computes per-run metrics for one run.
// Factored out to be reusable by handleExecutionGroupMetrics.
func (server *Server) buildSingleRunMetrics(runID string) (executionGroupRunEntry, error) {
	runDir := filepath.Join(server.RunsDir, runID)
	rs, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return executionGroupRunEntry{}, err
	}
	events, _, _ := state.ReadEventsWithOffset(filepath.Join(runDir, "events.jsonl"))
	c := metrics.NewCollector()
	wireParents(c, rs)
	for _, ev := range events {
		c.ProcessEvent(ev)
	}

	entry := executionGroupRunEntry{
		ParentRunID: rs.ParentRun,
		RunTotal:    c.RunTotal(),
		Nodes:       map[string]nodeMetricDetail{},
	}
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, ns := range nodes {
			own, rollup, _ := c.NodeMetrics(id)
			entry.Nodes[id] = nodeMetricDetail{
				Status:     ns.Status,
				Attempts:   ns.Attempts,
				Dispatches: ns.Dispatches,
				Own:        own,
				Rollup:     rollup,
			}
		}
	})
	return entry, nil
}

// addInto accumulates b into *a, field by field.
// DurationMs takes the max of the two (group wall time reflects the longest run).
func addInto(a *state.NodeTotals, b state.NodeTotals) {
	a.Tokens.Input += b.Tokens.Input
	a.Tokens.Output += b.Tokens.Output
	a.Tokens.CacheRead += b.Tokens.CacheRead
	a.Tokens.CacheWrite += b.Tokens.CacheWrite
	a.Tokens.CacheWrite1h += b.Tokens.CacheWrite1h
	a.Tokens.Reasoning += b.Tokens.Reasoning
	a.Tokens.Total = a.Tokens.Input + a.Tokens.Output + a.Tokens.CacheRead +
		a.Tokens.CacheWrite + a.Tokens.CacheWrite1h
	if b.CostUSD != nil {
		if a.CostUSD == nil {
			v := *b.CostUSD
			a.CostUSD = &v
		} else {
			v := *a.CostUSD + *b.CostUSD
			a.CostUSD = &v
		}
	}
	a.UnpricedEventCount += b.UnpricedEventCount
	if b.DurationMs > a.DurationMs {
		a.DurationMs = b.DurationMs
	}
}
