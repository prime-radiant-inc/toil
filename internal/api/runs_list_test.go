package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestRunsList_FilterByCallbackURL(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Run 1: matches the caller callback prefix
	run1Dir := filepath.Join(runsDir, "run-caller")
	if err := os.MkdirAll(run1Dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rs1 := state.NewRunState("run-caller", "implement_spec", map[string]any{"key": "val"})
	rs1.CallbackURL = "http://caller:80/api/webhooks/toil"
	if err := state.SaveState(filepath.Join(run1Dir, "state.json"), rs1); err != nil {
		t.Fatalf("save state: %v", err)
	}

	// Run 2: does not match
	run2Dir := filepath.Join(runsDir, "run-other")
	if err := os.MkdirAll(run2Dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rs2 := state.NewRunState("run-other", "implement_spec", nil)
	rs2.CallbackURL = "http://other-service/webhook"
	if err := state.SaveState(filepath.Join(run2Dir, "state.json"), rs2); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs?callback_url=http://caller", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	runsRaw, ok := payload["runs"]
	if !ok {
		t.Fatal("expected 'runs' key in response")
	}
	runs, ok := runsRaw.([]any)
	if !ok {
		t.Fatalf("expected 'runs' to be array, got %T", runsRaw)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	run, ok := runs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected run to be object, got %T", runs[0])
	}
	if run["run_id"] != "run-caller" {
		t.Errorf("expected run_id=run-caller, got %v", run["run_id"])
	}
	if run["workflow_id"] != "implement_spec" {
		t.Errorf("expected workflow_id=implement_spec, got %v", run["workflow_id"])
	}
	if run["status"] != "running" {
		t.Errorf("expected status=running, got %v", run["status"])
	}
	if run["callback_url"] != "http://caller:80/api/webhooks/toil" {
		t.Errorf("expected callback_url=http://caller:80/api/webhooks/toil, got %v", run["callback_url"])
	}
	if _, ok := run["inputs"]; !ok {
		t.Error("expected inputs field in run summary")
	}
}

func TestRunsList_UnfilteredReturnsIDList(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("test-run", "implement_spec", nil)
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var ids []string
	if err := json.Unmarshal(rec.Body.Bytes(), &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d", len(ids))
	}
	if ids[0] != "test-run" {
		t.Errorf("expected test-run, got %v", ids[0])
	}
}

func TestRunsList_FilterReadDirError(t *testing.T) {
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: "/nonexistent/path/that/does/not/exist",
	}

	req := httptest.NewRequest(http.MethodGet, "/runs?callback_url=http://caller", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestRunsList_Filtered_IncludesRunTotal(t *testing.T) {
	runsDir := t.TempDir()
	runID := "r_total"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState(runID, "wf-x", nil)
	rs.Status = "completed"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}
	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_ = logger.Append(state.Event{
		Timestamp: time.Now().UTC(), Type: "node_output", RunID: runID, NodeID: "n",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5.4","usage":{"input_tokens":100,"output_tokens":50}}}`,
	})
	_ = logger.Close()

	server := &Server{RunsDir: runsDir}
	req := httptest.NewRequest(http.MethodGet, "/runs?workflow=wf-x", nil)
	rr := httptest.NewRecorder()
	server.handleRunsList(rr, req)

	var resp struct {
		Runs []struct {
			RunID    string `json:"run_id"`
			RunTotal *struct {
				Tokens struct{ Total int } `json:"tokens"`
			} `json:"run_total,omitempty"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(resp.Runs))
	}
	if resp.Runs[0].RunTotal == nil {
		t.Fatal("expected run_total on filtered response")
	}
	if resp.Runs[0].RunTotal.Tokens.Total != 150 {
		t.Errorf("run_total.tokens.total: got %d, want 150", resp.Runs[0].RunTotal.Tokens.Total)
	}
}

func TestRunsList_FilterNoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-other")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rs := state.NewRunState("run-other", "implement_spec", nil)
	rs.CallbackURL = "http://other-service/webhook"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs?callback_url=http://caller", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	runsRaw, ok := payload["runs"]
	if !ok {
		t.Fatal("expected 'runs' key in response")
	}
	runs, ok := runsRaw.([]any)
	if !ok {
		t.Fatalf("expected 'runs' to be array, got %T", runsRaw)
	}
	if len(runs) != 0 {
		t.Fatalf("expected 0 runs, got %d", len(runs))
	}
}
