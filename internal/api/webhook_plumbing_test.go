package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestHandleRunCreate_PersistsCallbackURL(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test Workflow",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "placeholder", Kind: "system"}},
	}

	reg := runners.NewRegistry()
	eng := engine.NewEngine(
		&definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{"test-wf": workflow},
		},
		reg,
		runsDir,
		"",
	)
	manager := orchestrator.NewManager(eng, runsDir)

	server := &Server{
		App: &app.App{
			Definitions: eng.Definitions,
		},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{
		"workflow_id":  "test-wf",
		"inputs":       map[string]any{},
		"callback_url": "http://caller/api/webhooks/toil",
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp runResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RunID == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Wait for the background worker to finish
	manager.WaitForRun(resp.RunID)

	// Load state and verify CallbackURL was persisted
	rs, err := state.LoadState(filepath.Join(runsDir, resp.RunID, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if rs.CallbackURL != "http://caller/api/webhooks/toil" {
		t.Fatalf("expected callback_url 'http://caller/api/webhooks/toil', got %q", rs.CallbackURL)
	}
}
