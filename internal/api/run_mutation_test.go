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
	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestRunCreate_BadJSON(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunCreate_MissingWorkflowID(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"inputs": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunCreate_UnknownWorkflow(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"workflow_id": "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunCreate_MissingRequiredInputs(t *testing.T) {
	wf := &definitions.Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
		Inputs:  map[string]string{"project_dir": "string"},
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"test": wf}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"workflow_id": "test", "inputs": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunCreate_ProjectDirConflict(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Existing active run for the same project dir
	existingDir := filepath.Join(runsDir, "existing-run")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingRS := state.NewRunState("existing-run", "test", map[string]any{"project_dir": "/home/proj"})
	existingRS.Status = "running"
	if err := state.SaveState(filepath.Join(existingDir, "state.json"), existingRS); err != nil {
		t.Fatal(err)
	}

	wf := &definitions.Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"test": wf}}

	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{
		"workflow_id": "test",
		"inputs":      map[string]any{"project_dir": "/home/proj"},
	})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["error"] != "run_conflict" {
		t.Errorf("expected error=run_conflict, got %v", payload["error"])
	}
	if payload["active_run_id"] != "existing-run" {
		t.Errorf("expected active_run_id=existing-run, got %v", payload["active_run_id"])
	}
}

func TestRunCancel_EmptyRunID(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodPost, "/runs//cancel", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunResume_EmptyRunID(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodPost, "/runs//resume", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunRetrigger_EmptyRunID(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"node_id": "test"})
	req := httptest.NewRequest(http.MethodPost, "/runs//retrigger", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRunRetrigger_MissingNodeID(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/retrigger", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if rec.Body.String() != "node_id is required" {
		t.Errorf("expected 'node_id is required', got %q", rec.Body.String())
	}
}

func TestRunRetrigger_BadJSON(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/retrigger", bytes.NewReader([]byte("bad")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func newTestManager(t *testing.T) (*orchestrator.Manager, string) {
	t.Helper()
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"test-wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	return orchestrator.NewManager(eng, runsDir), runsDir
}

func TestRunCancel_Success(t *testing.T) {
	manager, runsDir := newTestManager(t)

	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.Status = "running"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/cancel", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["status"] != "cancelled" {
		t.Errorf("expected status=cancelled, got %v", payload["status"])
	}
}

func TestRunCancel_CannotCancel(t *testing.T) {
	manager, runsDir := newTestManager(t)

	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.Status = "completed"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/cancel", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunCancel_NonexistentRun(t *testing.T) {
	manager, runsDir := newTestManager(t)

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/nonexistent/cancel", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunResume_Success(t *testing.T) {
	manager, runsDir := newTestManager(t)

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	req := httptest.NewRequest(http.MethodPost, "/runs/some-run/resume", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload runResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.RunID != "some-run" {
		t.Errorf("expected run_id=some-run, got %q", payload.RunID)
	}
}

func TestRunRetrigger_NonexistentRun(t *testing.T) {
	manager, runsDir := newTestManager(t)

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"node_id": "start"})
	req := httptest.NewRequest(http.MethodPost, "/runs/nonexistent/retrigger", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	// load state error doesn't contain "not found", goes to 500 path
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunRetrigger_NodeNotFound(t *testing.T) {
	manager, runsDir := newTestManager(t)

	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.Status = "failed"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	wfYAML := `id: test-wf
name: Test
version: 1
nodes:
  - id: start
    kind: role
    runner: shell
    prompt: echo hi
`
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wfYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"node_id": "nonexistent-node"})
	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/retrigger", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunRetrigger_NotTerminal(t *testing.T) {
	manager, runsDir := newTestManager(t)

	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.Status = "running"
	rs.WithNode("start", func(n *state.NodeState) {
		n.Status = "running"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	wfYAML := `id: test-wf
name: Test
version: 1
nodes:
  - id: start
    kind: role
    runner: shell
    prompt: echo hi
`
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wfYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"node_id": "start"})
	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/retrigger", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateRun_RejectedWhenPaused verifies that createRun returns HTTP 503
// with a helpful error body and a Retry-After header when the .paused marker
// file is present in the runs directory.
func TestCreateRun_RejectedWhenPaused(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Place the pause marker.
	markerPath := config.PausedMarkerPath(runsDir)
	f, err := os.Create(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	wf := &definitions.Workflow{
		ID:      "wf",
		Name:    "WF",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	srv := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"workflow_id": "wf", "inputs": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header to be set")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(".paused")) {
		t.Errorf("expected error body to mention .paused, got: %s", rec.Body.String())
	}
}

// TestCreateRun_AcceptedWhenNotPaused verifies that createRun proceeds normally
// when no .paused marker is present (it will fail downstream because the runs
// dir is empty, but the pause check must pass).
func TestCreateRun_AcceptedWhenNotPaused(t *testing.T) {
	t.Parallel()

	// Use os.MkdirTemp with explicit RemoveAll so the engine's run
	// subdirectories don't interfere with t.TempDir's cleanup.
	tmpDir, err := os.MkdirTemp("", "toil-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Ensure no marker file exists.
	markerPath := config.PausedMarkerPath(runsDir)
	_ = os.Remove(markerPath)

	wf := &definitions.Workflow{
		ID:      "wf",
		Name:    "WF",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	srv := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"workflow_id": "wf", "inputs": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// 503 would mean the pause check fired incorrectly.
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("got 503 (paused) but no marker was present: %s", rec.Body.String())
	}
}

// TestCreateRun_AcceptedAfterUnpause verifies the lifecycle: no marker → OK,
// place marker → 503, remove marker → OK again.
func TestCreateRun_AcceptedAfterUnpause(t *testing.T) {
	t.Parallel()

	// Use os.MkdirTemp with explicit RemoveAll so the engine's run
	// subdirectories don't interfere with t.TempDir's cleanup.
	tmpDir, err := os.MkdirTemp("", "toil-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := config.PausedMarkerPath(runsDir)

	wf := &definitions.Workflow{
		ID:      "wf",
		Name:    "WF",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	srv := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	doRequest := func() int {
		body, _ := json.Marshal(map[string]any{"workflow_id": "wf", "inputs": map[string]any{}})
		req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// Phase 1: no marker — must NOT be 503.
	if code := doRequest(); code == http.StatusServiceUnavailable {
		t.Fatal("phase1: paused but no marker present")
	}

	// Phase 2: place marker — must be 503.
	f, err := os.Create(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if code := doRequest(); code != http.StatusServiceUnavailable {
		t.Fatalf("phase2: expected 503 with marker, got %d", code)
	}

	// Phase 3: remove marker — must NOT be 503.
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if code := doRequest(); code == http.StatusServiceUnavailable {
		t.Fatalf("phase3: still paused after marker removal")
	}
}
