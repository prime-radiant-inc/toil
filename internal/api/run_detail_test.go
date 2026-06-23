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

func TestRunDetail_ReturnsStateJSON(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-1", "test-wf", map[string]any{"key": "val"})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", rec.Header().Get("Content-Type"))
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload["id"] != "run-1" {
		t.Errorf("expected id=run-1, got %v", payload["id"])
	}
}

func TestRunDetail_NotFound(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_EmptyPath(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_Events(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := `{"type":"run_started","run_id":"run-1"}
{"type":"node_started","run_id":"run-1","node_id":"a"}
`
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/events", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != events {
		t.Errorf("expected events content, got %q", rec.Body.String())
	}
}

func TestRunDetail_Events_NotFound(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent/events", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_Meta(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.WithNode("build", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "pass"
		n.Error = "some error"
		n.Data = map[string]any{"output": "built"}
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/meta", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["run_id"] != "run-1" {
		t.Errorf("expected run_id=run-1, got %v", payload["run_id"])
	}
	if payload["workflow_id"] != "test-wf" {
		t.Errorf("expected workflow_id=test-wf, got %v", payload["workflow_id"])
	}

	nodes, ok := payload["nodes"].(map[string]any)
	if !ok {
		t.Fatal("expected nodes in response")
	}
	build, ok := nodes["build"].(map[string]any)
	if !ok {
		t.Fatal("expected build node")
	}
	if build["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", build["status"])
	}
	if build["decision"] != "pass" {
		t.Errorf("expected decision=pass, got %v", build["decision"])
	}
	if build["error"] != "some error" {
		t.Errorf("expected error='some error', got %v", build["error"])
	}
	data, ok := build["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data in build node")
	}
	if data["output"] != "built" {
		t.Errorf("expected data.output=built, got %v", data["output"])
	}
}

func TestRunDetail_Meta_NotFound(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent/meta", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_Graph(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-1", "test", nil)
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	workflowYAML := `id: test
name: Test
version: 1
nodes:
  - id: start
    kind: role
    runner: shell
    prompt: echo hello
`
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(workflowYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := payload["nodes"]; !ok {
		t.Error("expected nodes in graph response")
	}
}

func TestRunDetail_Graph_MissingState(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent/graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_Graph_MissingWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("run-1", "test", nil)
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1/graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRunDetail_CompoundGraph(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Root run
	rootDir := filepath.Join(runsDir, "root-run")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootRS := state.NewRunState("root-run", "main-wf", nil)
	if err := state.SaveState(filepath.Join(rootDir, "state.json"), rootRS); err != nil {
		t.Fatal(err)
	}
	rootWF := `id: main-wf
name: Main
version: 1
nodes:
  - id: step1
    kind: role
    runner: shell
    prompt: echo step1
`
	if err := os.WriteFile(filepath.Join(rootDir, "workflow.yaml"), []byte(rootWF), 0o644); err != nil {
		t.Fatal(err)
	}

	// Child run
	childDir := filepath.Join(runsDir, "child-run")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childRS := state.NewRunState("child-run", "sub-wf", nil)
	childRS.ParentRun = "root-run"
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childRS); err != nil {
		t.Fatal(err)
	}
	childWF := `id: sub-wf
name: Sub
version: 1
nodes:
  - id: substep
    kind: role
    runner: shell
    prompt: echo sub
`
	if err := os.WriteFile(filepath.Join(childDir, "workflow.yaml"), []byte(childWF), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/root-run/compound-graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes, ok := payload["nodes"].([]any)
	if !ok {
		t.Fatal("expected nodes array")
	}
	if len(nodes) < 2 {
		t.Errorf("expected at least 2 nodes (root + child run containers), got %d", len(nodes))
	}
}

func TestRunDetail_CompoundGraph_MissingRun(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent/compound-graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty graph), got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes, ok := payload["nodes"].([]any)
	if !ok {
		t.Fatal("expected nodes array")
	}
	if len(nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(nodes))
	}
}

func TestFindActiveRootRunForProjectDir(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Active root run with project_dir
	activeDir := filepath.Join(runsDir, "active-run")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	activeRS := state.NewRunState("active-run", "wf", map[string]any{"project_dir": "/home/proj"})
	activeRS.Status = "running"
	if err := state.SaveState(filepath.Join(activeDir, "state.json"), activeRS); err != nil {
		t.Fatal(err)
	}

	// Terminal run with same project_dir
	termDir := filepath.Join(runsDir, "done-run")
	if err := os.MkdirAll(termDir, 0o755); err != nil {
		t.Fatal(err)
	}
	termRS := state.NewRunState("done-run", "wf", map[string]any{"project_dir": "/home/proj"})
	termRS.Status = "completed"
	if err := state.SaveState(filepath.Join(termDir, "state.json"), termRS); err != nil {
		t.Fatal(err)
	}

	// Child run (should be skipped)
	childDir := filepath.Join(runsDir, "child-run")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childRS := state.NewRunState("child-run", "wf", map[string]any{"project_dir": "/home/proj"})
	childRS.Status = "running"
	childRS.ParentRun = "active-run"
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childRS); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	t.Run("finds active root run", func(t *testing.T) {
		id, status := server.findActiveRootRunForProjectDir("/home/proj")
		if id != "active-run" {
			t.Errorf("expected active-run, got %q", id)
		}
		if status != "running" {
			t.Errorf("expected running, got %q", status)
		}
	})

	t.Run("no match for different dir", func(t *testing.T) {
		id, status := server.findActiveRootRunForProjectDir("/home/other")
		if id != "" || status != "" {
			t.Errorf("expected empty, got %q %q", id, status)
		}
	})

	t.Run("empty project dir", func(t *testing.T) {
		id, status := server.findActiveRootRunForProjectDir("")
		if id != "" || status != "" {
			t.Errorf("expected empty, got %q %q", id, status)
		}
	})

	t.Run("whitespace project dir", func(t *testing.T) {
		id, status := server.findActiveRootRunForProjectDir("   ")
		if id != "" || status != "" {
			t.Errorf("expected empty, got %q %q", id, status)
		}
	})
}

func TestFindActiveRootRunForProjectDir_RunWithNoProjectDir(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Active run without project_dir input
	runDir := filepath.Join(runsDir, "run-no-pd")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-no-pd", "wf", map[string]any{"other_key": "val"})
	rs.Status = "running"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	id, status := server.findActiveRootRunForProjectDir("/home/proj")
	if id != "" || status != "" {
		t.Errorf("expected no match for run without project_dir, got %q %q", id, status)
	}
}

func TestFindActiveRootRunForProjectDir_NoRunsDir(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: "/nonexistent/path",
	}

	id, status := server.findActiveRootRunForProjectDir("/home/proj")
	if id != "" || status != "" {
		t.Errorf("expected empty for nonexistent runs dir, got %q %q", id, status)
	}
}

func TestRunDetail_CompoundGraph_ReadDirError(t *testing.T) {
	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: "/nonexistent/path",
	}

	req := httptest.NewRequest(http.MethodGet, "/runs/some-run/compound-graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty graph), got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes := payload["nodes"].([]any)
	if len(nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(nodes))
	}
}

func TestRunDetail_CompoundGraph_WithParentChain(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")

	// Grandparent run
	gpDir := filepath.Join(runsDir, "gp-run")
	if err := os.MkdirAll(gpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gpRS := state.NewRunState("gp-run", "gp-wf", nil)
	if err := state.SaveState(filepath.Join(gpDir, "state.json"), gpRS); err != nil {
		t.Fatal(err)
	}
	gpWF := `id: gp-wf
name: GP
version: 1
nodes:
  - id: step1
    kind: role
    runner: shell
    prompt: echo gp
`
	if err := os.WriteFile(filepath.Join(gpDir, "workflow.yaml"), []byte(gpWF), 0o644); err != nil {
		t.Fatal(err)
	}

	// Parent run
	parentDir := filepath.Join(runsDir, "parent-run")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentRS := state.NewRunState("parent-run", "parent-wf", nil)
	parentRS.ParentRun = "gp-run"
	if err := state.SaveState(filepath.Join(parentDir, "state.json"), parentRS); err != nil {
		t.Fatal(err)
	}
	parentWF := `id: parent-wf
name: Parent
version: 1
nodes:
  - id: step1
    kind: role
    runner: shell
    prompt: echo parent
`
	if err := os.WriteFile(filepath.Join(parentDir, "workflow.yaml"), []byte(parentWF), 0o644); err != nil {
		t.Fatal(err)
	}

	// Child run
	childDir := filepath.Join(runsDir, "child-run")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childRS := state.NewRunState("child-run", "child-wf", nil)
	childRS.ParentRun = "parent-run"
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childRS); err != nil {
		t.Fatal(err)
	}
	childWF := `id: child-wf
name: Child
version: 1
nodes:
  - id: step1
    kind: role
    runner: shell
    prompt: echo child
`
	if err := os.WriteFile(filepath.Join(childDir, "workflow.yaml"), []byte(childWF), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		App:     &app.App{Definitions: &definitions.Bundle{Workflows: map[string]*definitions.Workflow{}}},
		RunsDir: runsDir,
	}

	// Request compound graph from child — should walk up to grandparent
	req := httptest.NewRequest(http.MethodGet, "/runs/child-run/compound-graph", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes := payload["nodes"].([]any)
	// Should include all 3 runs as parent nodes, plus their child step nodes
	if len(nodes) < 3 {
		t.Errorf("expected at least 3 nodes (3 run containers), got %d", len(nodes))
	}
}
