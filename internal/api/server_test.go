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
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestProjectDirFromInputs(t *testing.T) {
	tests := []struct {
		name   string
		inputs map[string]any
		want   string
	}{
		{"nil inputs", nil, ""},
		{"missing key", map[string]any{"other": "val"}, ""},
		{"non-string value", map[string]any{"project_dir": 42}, ""},
		{"valid string", map[string]any{"project_dir": "/tmp/proj"}, "/tmp/proj"},
		{"whitespace trimmed", map[string]any{"project_dir": "  /tmp/proj  "}, "/tmp/proj"},
		{"empty string", map[string]any{"project_dir": ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectDirFromInputs(tt.inputs)
			if got != tt.want {
				t.Errorf("projectDirFromInputs(%v) = %q, want %q", tt.inputs, got, tt.want)
			}
		})
	}
}

func TestIsTerminalRunStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"completed", true},
		{"failed", true},
		{"cancelled", true},
		{"running", false},
		{"paused", false},
		{"", false},
		{" completed ", true},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := isTerminalRunStatus(tt.status)
			if got != tt.want {
				t.Errorf("isTerminalRunStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestServeHTTP_UnknownPath(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestServeHTTP_WrongMethod(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodDelete, "/runs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunsList_UnfilteredReadDirError(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: "/nonexistent/path",
	}

	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestStatusRecorderFlush_WithFlusher(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner}
	rec.Flush()
}

func TestStatusRecorderFlush_WithoutFlusher(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: noFlushWriter{}}
	rec.Flush()
}

type noFlushWriter struct{ http.ResponseWriter }

func TestDocumentEndpointReturnsJSON(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "test-doc-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("test-doc-1", "implement_spec", nil)
	rs.Title = "Implement Spec · test"
	rs.WithNode("hello", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "default"
		n.Message = "ok"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/test-doc-1/document", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["root_run_id"] != "test-doc-1" {
		t.Fatalf("root_run_id: %v", got["root_run_id"])
	}
}

func TestRunJSONIncludesSessionID(t *testing.T) {
	t.Parallel()

	// Non-empty case: session_id appears with its value.
	rs := state.NewRunState("test-run-1", "implement_spec", nil)
	rs.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.SessionID = "01KRFTAQQ2X99FBNY4MN3AZ3VD"
	})
	body, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"session_id":"01KRFTAQQ2X99FBNY4MN3AZ3VD"`) {
		t.Fatalf("expected session_id with value in JSON; got: %s", string(body))
	}

	// Empty case: session_id must still appear (this is the actual point of
	// removing `,omitempty` — the renderer needs the field present even when
	// there's no LLM session for the attempt).
	rs2 := state.NewRunState("test-run-2", "implement_spec", nil)
	rs2.WithNode("shell_step", func(n *state.NodeState) {
		n.Status = "completed"
		// SessionID intentionally left as "".
	})
	body2, err := json.Marshal(rs2)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if !strings.Contains(string(body2), `"session_id":""`) {
		t.Fatalf("expected empty session_id field to be present; got: %s", string(body2))
	}
}
