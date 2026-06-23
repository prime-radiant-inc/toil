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
	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/runners"
)

func TestApprovalsList_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(tmpDir, "runs"))
	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Root: tmpDir, Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result []*approvals.Approval
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected 0 approvals, got %d", len(result))
	}
}

func TestApprovalsList_WithApprovals(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(tmpDir, "runs"))
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	appr := &approvals.Approval{
		ID:     "run-1-build-1",
		RunID:  "run-1",
		NodeID: "build",
		Status: "pending",
	}
	if err := approvals.Create(runDir, appr); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Root: tmpDir, Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result []*approvals.Approval
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(result))
	}
	if result[0].ID != "run-1-build-1" {
		t.Errorf("expected id=run-1-build-1, got %q", result[0].ID)
	}
	if result[0].NodeID != "build" {
		t.Errorf("expected node_id=build, got %q", result[0].NodeID)
	}
}

func TestApprovalResolve_BadJSON(t *testing.T) {
	server := &Server{
		App:     &app.App{Root: t.TempDir(), Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodPost, "/approvals/some-id/resolve", bytes.NewReader([]byte("bad")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestApprovalResolve_MissingDecision(t *testing.T) {
	server := &Server{
		App:     &app.App{Root: t.TempDir(), Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"message": "ok"})
	req := httptest.NewRequest(http.MethodPost, "/approvals/some-id/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestApprovalResolve_MissingMessage(t *testing.T) {
	server := &Server{
		App:     &app.App{Root: t.TempDir(), Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"decision": "approved"})
	req := httptest.NewRequest(http.MethodPost, "/approvals/some-id/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestApprovalResolve_EmptyApprovalID(t *testing.T) {
	server := &Server{
		App:     &app.App{Root: t.TempDir(), Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	body, _ := json.Marshal(map[string]any{"decision": "approved", "message": "ok"})
	req := httptest.NewRequest(http.MethodPost, "/approvals//resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestApprovalResolve_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(tmpDir, "runs"))
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	appr := &approvals.Approval{
		ID:     "run-1-build-1",
		RunID:  "run-1",
		NodeID: "build",
		Status: "pending",
	}
	if err := approvals.Create(runDir, appr); err != nil {
		t.Fatal(err)
	}

	wf := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "build", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"test-wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	server := &Server{
		App:     &app.App{Root: tmpDir, Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{
		"decision": "approved",
		"message":  "looks good",
		"comment":  "no issues",
	})
	req := httptest.NewRequest(http.MethodPost, "/approvals/run-1-build-1/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result approvals.Approval
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != "resolved" {
		t.Errorf("expected status=resolved, got %q", result.Status)
	}
	if result.Decision != "approved" {
		t.Errorf("expected decision=approved, got %q", result.Decision)
	}
	if result.Comment != "no issues" {
		t.Errorf("expected comment='no issues', got %q", result.Comment)
	}
}

func TestApprovalsList_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(tmpDir, "runs"))
	// Create a file called "runs" instead of a directory to cause ReadDir to fail
	if err := os.WriteFile(filepath.Join(tmpDir, "runs"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Root: tmpDir, Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: filepath.Join(tmpDir, "runs"),
	}

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestApprovalResolve_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(tmpDir, "runs"))
	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	wf := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes:   []definitions.Node{{ID: "build", Kind: "role", Runner: "shell", Prompt: "echo hi"}},
	}
	bundle := &definitions.Bundle{Workflows: map[string]*definitions.Workflow{"test-wf": wf}}
	reg := runners.NewRegistry()
	eng := engine.NewEngine(bundle, reg, runsDir, "")
	manager := orchestrator.NewManager(eng, runsDir)

	server := &Server{
		App:     &app.App{Root: tmpDir, Definitions: bundle},
		RunsDir: runsDir,
		Manager: manager,
	}

	body, _ := json.Marshal(map[string]any{"decision": "approved", "message": "ok"})
	req := httptest.NewRequest(http.MethodPost, "/approvals/nonexistent/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
