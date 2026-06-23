package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestHandleRunStream_BasicEvents(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "test-run-1"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []state.Event{
		{Timestamp: time.Now(), Type: "run_started", RunID: runID},
		{Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "step_1"},
		{Timestamp: time.Now(), Type: "node_completed", RunID: runID, NodeID: "step_1", DurationMs: int64Ptr(1500)},
	}

	f, err := os.Create(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		data, _ := json.Marshal(e)
		_, _ = f.Write(data)
		_, _ = f.WriteString("\n")
	}
	_ = f.Close()

	server := &Server{runsDir: tmpDir}
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server.templates = tmpl

	req := httptest.NewRequest("GET", "/runs/"+runID+"/stream", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	server.handleRunStream(rec, req, runID)

	body := rec.Body.String()

	if !strings.Contains(body, "event: timeline-refresh") {
		t.Error("expected timeline-refresh SSE events in response")
	}
	if !strings.Contains(body, "event: run-status") {
		t.Error("expected run-status SSE event in response")
	}
	if !strings.Contains(body, "event: graph-update") {
		t.Error("expected graph-update SSE event in response")
	}
	if !strings.Contains(body, "event: node-status") {
		t.Error("expected node-status SSE event in response")
	}
}

func TestHandleNodeTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "test-run-1"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []state.Event{
		{
			Timestamp: time.Now(), Type: "node_output", RunID: runID, NodeID: "step_1",
			Text: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from step 1"}]}}`,
		},
		{
			Timestamp: time.Now(), Type: "node_output", RunID: runID, NodeID: "step_2",
			Text: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from step 2"}]}}`,
		},
		{
			Timestamp: time.Now(), Type: "node_output", RunID: runID, NodeID: "step_1",
			Text: `{"type":"assistant","message":{"content":[{"type":"text","text":"More from step 1"}]}}`,
		},
	}

	f, err := os.Create(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		data, _ := json.Marshal(e)
		_, _ = f.Write(data)
		_, _ = f.WriteString("\n")
	}
	_ = f.Close()

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest("GET", "/runs/"+runID+"/nodes/step_1/transcript", nil)
	rec := httptest.NewRecorder()
	server.handleNodeTranscript(rec, req, runID, "nodes/step_1/transcript")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(body, "Hello from step 1") {
		t.Error("expected 'Hello from step 1' in transcript")
	}
	if !strings.Contains(body, "More from step 1") {
		t.Error("expected 'More from step 1' in transcript")
	}
	if strings.Contains(body, "Hello from step 2") {
		t.Error("should NOT contain step_2 content")
	}
}

func TestLoadNodeTranscript_Dividers(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "test-run-dividers"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []state.Event{
		// Cycle 1: completes successfully
		{
			Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "review",
			Data: map[string]any{"session_id": "sess-001", "resume": false},
		},
		{
			Timestamp: time.Now(), Type: "node_output", RunID: runID, NodeID: "review",
			Text: `{"type":"assistant","message":{"content":[{"type":"text","text":"Review cycle 1"}]}}`,
		},
		{
			Timestamp: time.Now(), Type: "node_completed", RunID: runID, NodeID: "review",
			DurationMs: int64Ptr(5000), Data: map[string]any{"decision": "changes_requested", "session_id": "sess-001"},
		},
		// Cycle 2: graph routes back after write_code — this is a cycle, not a retry
		{
			Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "review",
			Data: map[string]any{"session_id": "sess-002", "resume": false},
		},
		{
			Timestamp: time.Now(), Type: "node_output", RunID: runID, NodeID: "review",
			Text: `{"type":"assistant","message":{"content":[{"type":"text","text":"Review cycle 2"}]}}`,
		},
		{
			Timestamp: time.Now(), Type: "node_completed", RunID: runID, NodeID: "review",
			DurationMs: int64Ptr(3000), Data: map[string]any{"decision": "approved", "session_id": "sess-002"},
		},
		// Unrelated node — should be excluded
		{Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "other"},
	}

	f, err := os.Create(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		data, _ := json.Marshal(e)
		_, _ = f.Write(data)
		_, _ = f.WriteString("\n")
	}
	_ = f.Close()

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	items, err := server.loadNodeTranscript("review", filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	// Expect: start-divider, message, end-divider, start-divider, message, end-divider
	if len(items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(items))
	}

	// First start divider — cycle (first execution is always a cycle)
	if items[0].Kind != transcriptKindDivider || items[0].Attempt != 1 || items[0].SessionID != "sess-001" {
		t.Errorf("item 0: expected start divider for execution 1 with sess-001, got kind=%s attempt=%d session=%s", items[0].Kind, items[0].Attempt, items[0].SessionID)
	}
	if !items[0].IsCycle {
		t.Error("item 0: first execution should be a cycle")
	}

	// First message
	if items[1].Kind != transcriptKindMessage || items[1].Text != "Review cycle 1" {
		t.Errorf("item 1: expected message 'Review cycle 1', got kind=%s text=%s", items[1].Kind, items[1].Text)
	}

	// First end divider
	if items[2].Kind != transcriptKindDivider || !items[2].IsEnd || items[2].Decision != "changes_requested" {
		t.Errorf("item 2: expected end divider with decision=changes_requested, got kind=%s isEnd=%v decision=%s", items[2].Kind, items[2].IsEnd, items[2].Decision)
	}
	if items[2].DurationMs != 5000 {
		t.Errorf("item 2: expected duration 5000, got %d", items[2].DurationMs)
	}

	// Second start divider — cycle (previous completed successfully)
	if items[3].Kind != transcriptKindDivider || items[3].Attempt != 2 || items[3].SessionID != "sess-002" {
		t.Errorf("item 3: expected start divider for execution 2 with sess-002, got kind=%s attempt=%d session=%s", items[3].Kind, items[3].Attempt, items[3].SessionID)
	}
	if !items[3].IsCycle {
		t.Error("item 3: execution after successful completion should be a cycle, not a retry")
	}

	// Second end divider
	if items[5].Kind != transcriptKindDivider || !items[5].IsEnd || items[5].Decision != "approved" {
		t.Errorf("item 5: expected end divider with decision=approved, got kind=%s isEnd=%v decision=%s", items[5].Kind, items[5].IsEnd, items[5].Decision)
	}
}

func TestLoadNodeTranscript_RetryAfterFailure(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "test-run-retry"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []state.Event{
		// Execution 1: fails
		{
			Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "build",
			Data: map[string]any{"session_id": "sess-001"},
		},
		{
			Timestamp: time.Now(), Type: "node_failed", RunID: runID, NodeID: "build",
			DurationMs: int64Ptr(2000), Data: map[string]any{"error": "timeout", "session_id": "sess-001"},
		},
		// Execution 2: retry after failure
		{
			Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "build",
			Data: map[string]any{"session_id": "sess-002"},
		},
		{
			Timestamp: time.Now(), Type: "node_completed", RunID: runID, NodeID: "build",
			DurationMs: int64Ptr(4000), Data: map[string]any{"decision": "done", "session_id": "sess-002"},
		},
		// Execution 3: cycle (previous completed successfully)
		{
			Timestamp: time.Now(), Type: "node_started", RunID: runID, NodeID: "build",
			Data: map[string]any{"session_id": "sess-003"},
		},
		{
			Timestamp: time.Now(), Type: "node_completed", RunID: runID, NodeID: "build",
			DurationMs: int64Ptr(3000), Data: map[string]any{"decision": "done", "session_id": "sess-003"},
		},
	}

	f, err := os.Create(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		data, _ := json.Marshal(e)
		_, _ = f.Write(data)
		_, _ = f.WriteString("\n")
	}
	_ = f.Close()

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	items, err := server.loadNodeTranscript("build", filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	// Expect: start, end(fail), start, end(ok), start, end(ok) = 6 items
	if len(items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(items))
	}

	// Execution 1: cycle (first execution)
	if !items[0].IsCycle {
		t.Error("item 0: first execution should always be a cycle")
	}

	// End 1: failed
	if items[1].Text != "failed" {
		t.Errorf("item 1: expected 'failed', got %q", items[1].Text)
	}

	// Execution 2: retry (previous failed)
	if items[2].IsCycle {
		t.Error("item 2: execution after failure should be a retry, not a cycle")
	}
	if items[2].Attempt != 2 {
		t.Errorf("item 2: expected attempt 2, got %d", items[2].Attempt)
	}

	// Execution 3: cycle (previous completed)
	if !items[4].IsCycle {
		t.Error("item 4: execution after success should be a cycle")
	}
	if items[4].Attempt != 3 {
		t.Errorf("item 4: expected attempt 3, got %d", items[4].Attempt)
	}
}

func int64Ptr(v int64) *int64 { return &v }
