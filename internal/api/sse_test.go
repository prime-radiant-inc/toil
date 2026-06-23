package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/state"
)

// flushRecorder wraps httptest.ResponseRecorder to satisfy http.Flusher.
type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

func TestSSEStreamEmitsNamedEvents(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"run_started","run_id":"test-run"}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"node_started","run_id":"test-run","node_id":"a"}`,
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir, App: &app.App{}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/runs/test-run/events/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleRunEventsStream(rec, req)
	}()
	<-done

	body := rec.Body.String()

	if !strings.Contains(body, "event: run_started\n") {
		t.Errorf("expected 'event: run_started' line in SSE output, got:\n%s", body)
	}
	if !strings.Contains(body, "event: node_started\n") {
		t.Errorf("expected 'event: node_started' line in SSE output, got:\n%s", body)
	}
	if !strings.Contains(body, `data: {"timestamp":`) {
		t.Errorf("expected data lines in SSE output, got:\n%s", body)
	}
}

func TestSSEStream_EmptyRunID(t *testing.T) {
	server := &Server{RunsDir: t.TempDir(), App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs//events/stream", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSSEStream_MissingEventsFile(t *testing.T) {
	server := &Server{RunsDir: t.TempDir(), App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent/events/stream", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSSEStream_NonFlusher(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(`{"type":"run_started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir, App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs/test-run/events/stream", nil)
	rec := httptest.NewRecorder()
	// Wrap in a ResponseWriter that doesn't implement Flusher
	server.handleRunEventsStream(noFlushResponseWriter{rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

type noFlushResponseWriter struct{ http.ResponseWriter }

func TestSSEStreamEmitsUntypedEvents(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Event without a "type" field
	events := `{"timestamp":"2026-01-01T00:00:00Z","run_id":"test-run","message":"no type"}
`
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir, App: &app.App{}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/runs/test-run/events/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleRunEventsStream(rec, req)
	}()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "data: {") {
		t.Errorf("expected data line in output, got:\n%s", body)
	}
	// The stream always emits an initial metric-update event; the untyped input
	// event itself must NOT produce a named event line.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "event: metric-update") {
			t.Errorf("unexpected named event line for untyped input event: %q", line)
		}
	}
}

func TestRunEventsStream_EmitsMetricUpdateEvents(t *testing.T) {
	runsDir := t.TempDir()
	runID := "r_sse"
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
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/events/stream", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rr := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		server.handleRunEventsStream(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	// Append a usage event; the stream should emit a metric-update line.
	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5.4","usage":{"input_tokens":100,"output_tokens":50}}}`,
	})
	// Terminate the stream.
	_ = logger.Append(state.Event{Timestamp: time.Now().UTC(), Type: "run_completed", RunID: runID})
	_ = logger.Close()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		cancel()
	}

	body := rr.Body.String()
	if !strings.Contains(body, "event: metric-update") {
		t.Errorf("expected \"event: metric-update\" in SSE body; got:\n%s", body)
	}
}

func TestExtractEventType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal event", `{"timestamp":"2026-01-01T00:00:00Z","type":"run_started","run_id":"x"}`, "run_started"},
		{"node event", `{"timestamp":"2026-01-01T00:00:00Z","type":"node_completed","run_id":"x","node_id":"a"}`, "node_completed"},
		{"space after colon", `{"timestamp":"2026-01-01T00:00:00Z","type": "run_failed","run_id":"x"}`, "run_failed"},
		{"no type field", `{"timestamp":"2026-01-01T00:00:00Z","run_id":"x"}`, ""},
		{"empty line", ``, ""},
		{"malformed json", `{"type":"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEventType(tt.input)
			if got != tt.want {
				t.Errorf("extractEventType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
