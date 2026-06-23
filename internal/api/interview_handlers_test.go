package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/interviews"
)

func TestHandleRunInterviews_Empty(t *testing.T) {
	runsDir := t.TempDir()
	server := &Server{RunsDir: runsDir}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result []*interviews.Interview
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty list, got %d items", len(result))
	}
}

func TestHandleRunInterviews_WithData(t *testing.T) {
	runsDir := t.TempDir()
	runDir := runsDir + "/run-1"
	server := &Server{RunsDir: runsDir}

	iv := &interviews.Interview{
		ID:     "interview-run-1-build",
		RunID:  "run-1",
		NodeID: "build",
		RoleID: "builder",
		Status: interviews.StatusCompleted,
	}
	if err := interviews.Create(runDir, iv); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result []*interviews.Interview
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 interview, got %d", len(result))
	}
	if result[0].NodeID != "build" {
		t.Fatalf("expected node_id 'build', got %q", result[0].NodeID)
	}
}

func TestHandleRunInterview_Found(t *testing.T) {
	runsDir := t.TempDir()
	runDir := runsDir + "/run-1"
	server := &Server{RunsDir: runsDir}

	iv := &interviews.Interview{
		ID:     "interview-run-1-build",
		RunID:  "run-1",
		NodeID: "build",
		RoleID: "builder",
		Status: interviews.StatusPending,
	}
	if err := interviews.Create(runDir, iv); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews/build", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result interviews.Interview
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.NodeID != "build" {
		t.Fatalf("expected node_id 'build', got %q", result.NodeID)
	}
	if result.RoleID != "builder" {
		t.Fatalf("expected role_id 'builder', got %q", result.RoleID)
	}
}

func TestHandleRunInterview_NotFound(t *testing.T) {
	runsDir := t.TempDir()
	server := &Server{RunsDir: runsDir}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews/nonexistent", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleRunInterviews_EmptyRunID(t *testing.T) {
	server := &Server{RunsDir: t.TempDir()}

	req := httptest.NewRequest(http.MethodGet, "/runs//interviews", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRunInterview_EmptyNodeID(t *testing.T) {
	server := &Server{RunsDir: t.TempDir()}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRunInterviews_ListError(t *testing.T) {
	runsDir := t.TempDir()
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create "interviews" as a file to cause ListForRun to fail
	if err := os.WriteFile(filepath.Join(runDir, "interviews"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: runsDir}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleRunInterview_CorruptJSON(t *testing.T) {
	runsDir := t.TempDir()
	runDir := filepath.Join(runsDir, "run-1")
	interviewDir := filepath.Join(runDir, "interviews")
	if err := os.MkdirAll(interviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write corrupt JSON as the interview file
	if err := os.WriteFile(filepath.Join(interviewDir, "build.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: runsDir}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/interviews/build", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
