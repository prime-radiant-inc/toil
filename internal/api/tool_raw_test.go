package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/app"
)

func TestHandleToolRaw_Found(t *testing.T) {
	dir := t.TempDir()
	runID := "run-abc"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"tool_call","run_id":"run-abc","data":{"tool_id":"tool-1","name":"bash"}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"tool_result","run_id":"run-abc","data":{"tool_id":"tool-1","is_error":false,"content":"hello world"}}`,
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir, App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-abc/tools/tool-1/raw", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload["tool_id"] != "tool-1" {
		t.Errorf("expected tool_id=tool-1, got %v", payload["tool_id"])
	}
	if payload["content"] != "hello world" {
		t.Errorf("expected content='hello world', got %v", payload["content"])
	}
}

func TestHandleToolRaw_NotFound_ToolID(t *testing.T) {
	dir := t.TempDir()
	runID := "run-xyz"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"tool_result","run_id":"run-xyz","data":{"tool_id":"tool-1","is_error":false,"content":"hi"}}`,
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir, App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-xyz/tools/nonexistent/raw", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleToolRaw_NotFound_MissingRun(t *testing.T) {
	server := &Server{RunsDir: t.TempDir(), App: &app.App{}}

	req := httptest.NewRequest(http.MethodGet, "/runs/no-such-run/tools/tool-1/raw", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
