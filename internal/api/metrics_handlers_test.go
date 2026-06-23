package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestMetricsHandler_ReturnsPerNodeAndRunTotal(t *testing.T) {
	runsDir := t.TempDir()
	runID := "r_test"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a run state and events with one node that emitted usage.
	rs := state.NewRunState(runID, "wf", nil)
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}
	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC()
	_ = logger.Append(state.Event{Timestamp: t0, Type: "node_started", RunID: runID, NodeID: "n"})
	_ = logger.Append(state.Event{
		Timestamp: t0.Add(500 * time.Millisecond), Type: "node_output", RunID: runID, NodeID: "n",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5","usage":{"input_tokens":100,"output_tokens":50}}}`,
	})
	_ = logger.Append(state.Event{Timestamp: t0.Add(2 * time.Second), Type: "node_completed", RunID: runID, NodeID: "n"})
	_ = logger.Close()

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/metrics", nil)
	rr := httptest.NewRecorder()
	server.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		RunID    string `json:"run_id"`
		RunTotal struct {
			DurationMs int64 `json:"duration_ms"`
			Tokens     struct {
				Input int `json:"input"`
				Total int `json:"total"`
			} `json:"tokens"`
			CostUSD *float64 `json:"cost_usd"`
		} `json:"run_total"`
		Nodes map[string]struct {
			Own struct {
				Tokens struct {
					Input int `json:"input"`
				} `json:"tokens"`
			} `json:"own"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RunID != runID {
		t.Errorf("run_id: got %q, want %q", resp.RunID, runID)
	}
	if resp.Nodes["n"].Own.Tokens.Input != 100 {
		t.Errorf("node n own input: got %d, want 100", resp.Nodes["n"].Own.Tokens.Input)
	}
	if resp.RunTotal.Tokens.Input != 100 {
		t.Errorf("run total input: got %d, want 100", resp.RunTotal.Tokens.Input)
	}
	if resp.RunTotal.Tokens.Total != 150 {
		t.Errorf("run total total: got %d, want 150", resp.RunTotal.Tokens.Total)
	}
	// Expected: gpt-5 at $1.25/M input + $10/M output.
	// 100 input * 1.25 + 50 output * 10 = 0.000125 + 0.0005 = 0.000625
	if resp.RunTotal.CostUSD == nil {
		t.Fatal("run total cost: nil")
	}
	diff := *resp.RunTotal.CostUSD - 0.000625
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-9 {
		t.Errorf("run total cost: got %f, want 0.000625", *resp.RunTotal.CostUSD)
	}
}

func TestMetricsHandler_FollowMode_StreamsUpdates(t *testing.T) {
	runsDir := t.TempDir()
	runID := "r_stream"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState(runID, "wf", nil)
	rs.Status = "running"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(runDir, "events.jsonl")
	logger, err := state.NewLogger(eventsPath)
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/metrics?follow=true", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleMetrics(rr, req)
		close(done)
	}()

	// Give the handler a moment to start tailing.
	time.Sleep(100 * time.Millisecond)

	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5","usage":{"input_tokens":100,"output_tokens":0}}}`,
	})
	_ = logger.Append(state.Event{Timestamp: time.Now().UTC(), Type: "run_completed", RunID: runID})
	_ = logger.Close()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("handler did not complete")
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"type":"metric-update"`) {
		t.Errorf("expected metric-update SSE event in body; got:\n%s", body)
	}
	// The payload shape is nodes: {"n": {...}}, so assert the node ID as a key.
	if !strings.Contains(body, `"n":{`) {
		t.Errorf("expected node key \"n\" in stream; got:\n%s", body)
	}
}

func TestMetricsHandler_FollowMode_StreamsAllCacheAndUnpricedFields(t *testing.T) {
	// Pins that the follow-mode SSE stream serializes every field the
	// dashboard now depends on: cache_write, cache_write_1h, and
	// unpriced_event_count. Without this a schema drift between the Go
	// handler and the JS consumer could pass unit tests silently.
	runsDir := t.TempDir()
	runID := "r_stream_full"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState(runID, "wf", nil)
	rs.Status = "running"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}
	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/metrics?follow=true", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleMetrics(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	// Anthropic-shaped event with both 5m and 1h cache writes, plus cache reads.
	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n1",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"claude-opus-4-5","usage":{"input_tokens":500,"output_tokens":100,"cache_read_tokens":2000,"cache_write_tokens":1500,"cache_write_1h_tokens":800}}}`,
	})
	// Unknown-model event to bump unpriced_event_count.
	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n2",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"made-up-unknown-xyz","usage":{"input_tokens":50,"output_tokens":10}}}`,
	})
	_ = logger.Append(state.Event{Timestamp: time.Now().UTC(), Type: "run_completed", RunID: runID})
	_ = logger.Close()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("handler did not complete")
	}

	body := rr.Body.String()

	// Parse the last data: line (the final flushed metric-update) as JSON so
	// we're asserting against structure, not raw substring matches.
	var last string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"metric-update"`) {
			last = strings.TrimPrefix(line, "data: ")
		}
	}
	if last == "" {
		t.Fatalf("no metric-update SSE event in body:\n%s", body)
	}
	var payload struct {
		RunID string `json:"run_id"`
		Nodes map[string]struct {
			Own struct {
				Tokens struct {
					Input        int `json:"input"`
					Output       int `json:"output"`
					CacheRead    int `json:"cache_read"`
					CacheWrite   int `json:"cache_write"`
					CacheWrite1h int `json:"cache_write_1h"`
					Total        int `json:"total"`
				} `json:"tokens"`
				CostUSD            *float64 `json:"cost_usd"`
				UnpricedEventCount int      `json:"unpriced_event_count"`
			} `json:"own"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(last), &payload); err != nil {
		t.Fatalf("parse SSE payload: %v\n%s", err, last)
	}

	n1 := payload.Nodes["n1"].Own
	if n1.Tokens.CacheWrite != 1500 {
		t.Errorf("n1 cache_write: got %d, want 1500", n1.Tokens.CacheWrite)
	}
	if n1.Tokens.CacheWrite1h != 800 {
		t.Errorf("n1 cache_write_1h: got %d, want 800", n1.Tokens.CacheWrite1h)
	}
	// total = input + output + cache_read + cache_write + cache_write_1h
	// = 500 + 100 + 2000 + 1500 + 800 = 4900
	if n1.Tokens.Total != 4900 {
		t.Errorf("n1 total: got %d, want 4900", n1.Tokens.Total)
	}
	if n1.CostUSD == nil {
		t.Error("n1 cost_usd: nil, want priced")
	}
	if n1.UnpricedEventCount != 0 {
		t.Errorf("n1 unpriced_event_count: got %d, want 0 (all priced)", n1.UnpricedEventCount)
	}

	n2 := payload.Nodes["n2"].Own
	if n2.UnpricedEventCount != 1 {
		t.Errorf("n2 unpriced_event_count: got %d, want 1 (unknown model)", n2.UnpricedEventCount)
	}
	if n2.CostUSD != nil {
		t.Errorf("n2 cost_usd: got %v, want nil (unknown model)", *n2.CostUSD)
	}
}

func TestExecutionGroupMetrics_AggregatesParentAndChildRuns(t *testing.T) {
	runsDir := t.TempDir()

	// Parent run.
	parent := "r_parent"
	setupRunWithOneNodeUsage(t, runsDir, parent, "", 100, 50) // $0.0025

	// Child run (parent_run set to parent).
	child := "r_child"
	setupRunWithOneNodeUsage(t, runsDir, child, parent, 200, 100) // $0.005

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs/"+parent+"/execution-group/metrics", nil)
	rr := httptest.NewRecorder()
	server.handleExecutionGroupMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		RootRunID  string `json:"root_run_id"`
		GroupTotal struct {
			Tokens  struct{ Total int } `json:"tokens"`
			CostUSD *float64            `json:"cost_usd"`
		} `json:"group_total"`
		Runs map[string]struct {
			ParentRunID string `json:"parent_run_id,omitempty"`
			RunTotal    struct {
				Tokens struct{ Total int } `json:"tokens"`
			} `json:"run_total"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RootRunID != parent {
		t.Errorf("root_run_id: got %q, want %q", resp.RootRunID, parent)
	}
	// parent: 100 in + 50 out = 150 total; child: 200 in + 100 out = 300 total; group = 450.
	if resp.GroupTotal.Tokens.Total != 450 {
		t.Errorf("group total tokens: got %d, want 450", resp.GroupTotal.Tokens.Total)
	}
	if resp.Runs[child].ParentRunID != parent {
		t.Errorf("child parent_run_id: got %q, want %q", resp.Runs[child].ParentRunID, parent)
	}
}

// setupRunWithOneNodeUsage writes a minimal run directory with a state file and
// one node_output event carrying token usage. parentRun may be empty for root runs.
func setupRunWithOneNodeUsage(t *testing.T, runsDir, runID, parentRun string, input, output int) {
	t.Helper()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState(runID, "wf", nil)
	rs.ParentRun = parentRun
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}
	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()
	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n",
		Text: fmt.Sprintf(
			`{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5","usage":{"input_tokens":%d,"output_tokens":%d}}}`,
			input, output,
		),
	})
}

func TestMetricsHandler_NotFound_ReturnsStatus404(t *testing.T) {
	server := &Server{RunsDir: t.TempDir()}
	req := httptest.NewRequest(http.MethodGet, "/runs/does-not-exist/metrics", nil)
	rr := httptest.NewRecorder()
	server.handleMetrics(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestMetricsHandler_BadRunID_ReturnsStatus400(t *testing.T) {
	server := &Server{RunsDir: t.TempDir()}
	// Empty run id between the /runs/ prefix and /metrics suffix.
	req := httptest.NewRequest(http.MethodGet, "/runs//metrics", nil)
	rr := httptest.NewRecorder()
	server.handleMetrics(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestExecutionGroupMetrics_FollowMode_EmitsPerRunSnapshots(t *testing.T) {
	runsDir := t.TempDir()
	parent := "r_eg_parent"
	child := "r_eg_child"
	setupRunWithOneNodeUsage(t, runsDir, parent, "", 100, 50)
	setupRunWithOneNodeUsage(t, runsDir, child, parent, 200, 100)

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs/"+parent+"/execution-group/metrics?follow=true", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleExecutionGroupMetrics(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after ctx timeout")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "event: metric-update") {
		t.Errorf("expected metric-update SSE events; got:\n%s", body)
	}
	if !strings.Contains(body, `"run_id":"`+parent+`"`) {
		t.Errorf("expected parent run snapshot in stream; got:\n%s", body)
	}
	if !strings.Contains(body, `"run_id":"`+child+`"`) {
		t.Errorf("expected child run snapshot in stream; got:\n%s", body)
	}
}
