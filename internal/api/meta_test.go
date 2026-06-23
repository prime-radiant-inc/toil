package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

const statusCompleted = "completed"

func TestMetaEndpoint_IncludesNodes(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("test-run", "implement_spec", nil)
	rs.Status = statusCompleted
	rs.WithNode("implement", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "approved"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/test-run/meta", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	nodes, ok := payload["nodes"].(map[string]any)
	if !ok {
		t.Fatal("expected nodes in meta response")
	}
	impl, ok := nodes["implement"].(map[string]any)
	if !ok {
		t.Fatal("expected implement node in meta")
	}
	if impl["status"] != statusCompleted {
		t.Errorf("expected status=completed, got %v", impl["status"])
	}
	if impl["decision"] != "approved" {
		t.Errorf("expected decision=approved, got %v", impl["decision"])
	}
}

func TestMetaEndpoint_IncludesNodeMessage(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("test-run", "implement_spec", nil)
	rs.Status = statusCompleted
	rs.WithNode("reviewer", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "changes_requested"
		n.Message = "Rejected due to scope violation"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/test-run/meta", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	nodes := payload["nodes"].(map[string]any)
	reviewer := nodes["reviewer"].(map[string]any)
	if reviewer["message"] != "Rejected due to scope violation" {
		t.Errorf("expected message in meta node, got %v", reviewer["message"])
	}
}
