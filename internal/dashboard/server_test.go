package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/orchestrator"
)

func TestServerRoutes(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))

	application, err := app.LoadForResume(root)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}

	tempRoot := t.TempDir()
	runsDir := filepath.Join(tempRoot, "runs")
	manager := orchestrator.NewManager(application.Engine, runsDir)
	server := NewServer(application, runsDir, manager, "/ui")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/workflows", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

func TestHandleNodeDetail_LeafDelegatesToTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"node_started","run_id":"r1","node_id":"dev","timestamp":"2026-04-20T12:00:00Z","data":{"session_id":"s1"}}
{"type":"node_output","run_id":"r1","node_id":"dev","stream":"stdout","text":"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"hello from dev\"}]}}","timestamp":"2026-04-20T12:00:01Z"}
{"type":"node_completed","run_id":"r1","node_id":"dev","timestamp":"2026-04-20T12:00:02Z","data":{"session_id":"s1"},"duration_ms":2000}
`
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := `id: test
name: Test
version: 1
nodes:
  - id: dev
    role: developer
`
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/r1/nodes/dev/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="leaf-role"`) {
		t.Errorf("body missing data-node-detail-kind=\"leaf-role\"; got: %s", body)
	}
	if !strings.Contains(body, "hello from dev") {
		t.Errorf("body missing transcript content; got: %s", body)
	}
}

func TestHandleNodeDetail_PendingRendersPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := `id: test
name: Test
version: 1
nodes:
  - id: future
    role: dev
    prompt: |
      # Future Node
      Do the thing.
`
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/r1/nodes/future/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="pending"`) {
		t.Errorf("expected pending kind marker; got: %s", body)
	}
	if !strings.Contains(body, "has not run yet") {
		t.Errorf("expected 'has not run yet' message; got: %s", body)
	}
	if !strings.Contains(body, "Do the thing") {
		t.Errorf("expected node prompt in placeholder; got: %s", body)
	}
}

func TestHandleNodeDetail_SkippedRendersPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "r1")
	_ = os.MkdirAll(runDir, 0o755)
	events := `{"type":"node_skipped","run_id":"r1","node_id":"check","timestamp":"2026-04-20T12:00:00Z","data":{"decision":"skip"}}
`
	_ = os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644)
	wf := `id: test
name: Test
version: 1
nodes:
  - id: check
    kind: system
    prompt: "Maybe skip"
`
	_ = os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644)

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/r1/nodes/check/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="skipped"`) {
		t.Errorf("expected skipped kind marker; got: %s", body)
	}
	if !strings.Contains(body, "skip") {
		t.Errorf("expected skip decision; got: %s", body)
	}
}

func TestHandleNodeDetail_SubworkflowListsChildRuns(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "parent-run")
	_ = os.MkdirAll(runDir, 0o755)
	childDir := filepath.Join(tmpDir, "child-1")
	_ = os.MkdirAll(childDir, 0o755)
	_ = os.WriteFile(filepath.Join(childDir, "events.jsonl"), []byte(`{"type":"run_started","run_id":"child-1","timestamp":"2026-04-20T12:00:00Z"}
{"type":"run_completed","run_id":"child-1","timestamp":"2026-04-20T12:00:05Z"}
`), 0o644)
	_ = os.WriteFile(filepath.Join(childDir, "state.json"), []byte(`{"id":"child-1","workflow_id":"sub","status":"completed","started_at":"2026-04-20T12:00:00Z","finished_at":"2026-04-20T12:00:05Z"}`), 0o644)

	parentEvents := `{"type":"subworkflow_started","run_id":"parent-run","node_id":"dispatch","timestamp":"2026-04-20T12:00:00Z","data":{"child_run":"child-1"}}
`
	_ = os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(parentEvents), 0o644)
	wf := `id: parent
name: Parent
version: 1
nodes:
  - id: dispatch
    kind: subworkflow
    workflow: sub
`
	_ = os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644)

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/parent-run/nodes/dispatch/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="subworkflow"`) {
		t.Errorf("expected subworkflow kind marker; got: %s", body)
	}
	if !strings.Contains(body, "child-1") {
		t.Errorf("expected child run ID; got: %s", body)
	}
}

func TestHandleNodeDetail_ForEachGroupsByIteration(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "p")
	_ = os.MkdirAll(runDir, 0o755)
	for _, cid := range []string{"c0", "c1"} {
		cd := filepath.Join(tmpDir, cid)
		_ = os.MkdirAll(cd, 0o755)
		_ = os.WriteFile(filepath.Join(cd, "events.jsonl"),
			[]byte(`{"type":"run_started","run_id":"`+cid+`","timestamp":"2026-04-20T12:00:00Z"}
`), 0o644)
		_ = os.WriteFile(filepath.Join(cd, "state.json"),
			[]byte(`{"id":"`+cid+`","workflow_id":"sub","status":"running","started_at":"2026-04-20T12:00:00Z"}`), 0o644)
	}
	parentEvents := `{"type":"subworkflow_started","run_id":"p","node_id":"dispatch::0","timestamp":"2026-04-20T12:00:00Z","data":{"child_run":"c0"}}
{"type":"subworkflow_started","run_id":"p","node_id":"dispatch::1","timestamp":"2026-04-20T12:00:01Z","data":{"child_run":"c1"}}
`
	_ = os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(parentEvents), 0o644)
	wf := `id: parent
name: Parent
version: 1
nodes:
  - id: dispatch
    for_each:
      list: input.items
      item: it
      body: dispatch_body
  - id: dispatch_body
    kind: subworkflow
    workflow: sub
`
	_ = os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644)

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/p/nodes/dispatch/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="subworkflow-foreach"`) {
		t.Errorf("expected subworkflow-foreach marker; got: %s", body)
	}
	idx0 := strings.Index(body, "iteration 0")
	idx1 := strings.Index(body, "iteration 1")
	if idx0 < 0 || idx1 < 0 {
		t.Errorf("expected both iteration labels; got: %s", body)
	}
	if idx0 > idx1 {
		t.Errorf("iteration 0 should come before iteration 1 in output")
	}
}

func TestHandleNodeDetail_ForeachIterationRendersTranscriptWithBreadcrumb(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "r1")
	_ = os.MkdirAll(runDir, 0o755)
	events := `{"type":"node_started","run_id":"r1","node_id":"build::0","timestamp":"2026-04-20T12:00:00Z","data":{"session_id":"s1"}}
{"type":"node_output","run_id":"r1","node_id":"build::0","stream":"stdout","text":"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"building\"}]}}","timestamp":"2026-04-20T12:00:01Z"}
{"type":"node_completed","run_id":"r1","node_id":"build::0","timestamp":"2026-04-20T12:00:02Z","data":{"session_id":"s1"}}
`
	_ = os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644)
	wf := `id: t
name: T
version: 1
nodes:
  - id: build
    for_each: {list: input.items, item: it, body: build_body}
  - id: build_body
    kind: subworkflow
    workflow: sub
`
	_ = os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(wf), 0o644)

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmpDir, templates: tmpl}

	req := httptest.NewRequest(http.MethodGet, "/runs/r1/nodes/build::0/detail", nil)
	w := httptest.NewRecorder()
	server.handleRunDetail(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-node-detail-kind="foreach-iteration"`) {
		t.Errorf("expected foreach-iteration marker; got: %s", body)
	}
	if !strings.Contains(body, "build") || !strings.Contains(body, "iteration 0") {
		t.Errorf("expected breadcrumb 'build → iteration 0'; got: %s", body)
	}
	if !strings.Contains(body, "building") {
		t.Errorf("expected transcript content 'building'; got: %s", body)
	}
}
