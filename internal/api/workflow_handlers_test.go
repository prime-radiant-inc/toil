package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/visualize"
)

func TestWorkflowsList(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"build":  {ID: "build", Name: "Build"},
			"deploy": {ID: "deploy", Name: "Deploy"},
		},
	}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var ids []string
	if err := json.Unmarshal(rec.Body.Bytes(), &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sort.Strings(ids)
	if len(ids) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(ids))
	}
	if ids[0] != "build" || ids[1] != "deploy" {
		t.Errorf("expected [build, deploy], got %v", ids)
	}
}

func TestWorkflowsList_Empty(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var ids []string
	if err := json.Unmarshal(rec.Body.Bytes(), &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 workflows, got %d", len(ids))
	}
}

func TestWorkflowShow(t *testing.T) {
	tmpDir := t.TempDir()
	yamlContent := "id: test\nname: Test\nversion: 1\n"
	sourcePath := filepath.Join(tmpDir, "test.yaml")
	if err := os.WriteFile(sourcePath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"test": {ID: "test", Name: "Test", SourcePath: sourcePath},
		},
	}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/test", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("expected Content-Type text/plain, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != yamlContent {
		t.Errorf("expected body %q, got %q", yamlContent, rec.Body.String())
	}
}

func TestWorkflowShow_NotFound(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/nonexistent", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWorkflowShow_NoSourcePath(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"test": {ID: "test", Name: "Test", SourcePath: ""},
		},
	}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/test", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestWorkflowGraph(t *testing.T) {
	wf := &definitions.Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "role", Runner: "shell", Prompt: "echo hello"},
			{ID: "end", Kind: "role", Runner: "shell", Prompt: "echo done"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "end"},
		},
	}
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{"test": wf},
	}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/test/graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var graph visualize.TopologyGraph
	if err := json.Unmarshal(rec.Body.Bytes(), &graph); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(graph.Edges))
	}
}

func TestWorkflowGraph_NotFound(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/nonexistent/graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWorkflowShow_SourceFileDeleted(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"test": {ID: "test", Name: "Test", SourcePath: "/nonexistent/deleted.yaml"},
		},
	}
	server := &Server{
		App:     &app.App{Definitions: bundle},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/test", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestWorkflowShow_EmptyPath(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWorkflowGraph_EmptyPath(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/workflows//graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
