package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleLearnings(t *testing.T) {
	tempRoot := t.TempDir()
	runsDir := filepath.Join(tempRoot, "runs")

	// Create a learn run with proposals
	learnDir := filepath.Join(runsDir, "learn-run-1")
	if err := os.MkdirAll(learnDir, 0o755); err != nil {
		t.Fatal(err)
	}

	learnState := map[string]any{
		"id":          "learn-run-1",
		"workflow_id": "learn",
		"status":      "completed",
		"started_at":  "2026-03-11T00:00:00Z",
		"inputs": map[string]any{
			"run_id": "source-run-1",
		},
		"nodes": map[string]any{
			"synthesize": map[string]any{
				"status":   "completed",
				"decision": "learnings_synthesized",
				"data": map[string]any{
					"proposals": []any{
						"target_file: roles/engineer.md | summary: Add TDD guidance | proposed_text: Always write tests first.",
					},
				},
				"message": "Synthesized 1 proposal.",
			},
		},
	}
	learnJSON, _ := json.Marshal(learnState)
	if err := os.WriteFile(filepath.Join(learnDir, "state.json"), learnJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create the source run so we can resolve its workflow_id
	sourceDir := filepath.Join(runsDir, "source-run-1")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceState := map[string]any{
		"id":          "source-run-1",
		"workflow_id": "implement_task",
		"status":      "completed",
		"title":       "Implement Task: widget",
		"started_at":  "2026-03-10T00:00:00Z",
	}
	sourceJSON, _ := json.Marshal(sourceState)
	if err := os.WriteFile(filepath.Join(sourceDir, "state.json"), sourceJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{runsDir: runsDir, basePath: "/ui"}
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	server.templates = tmpl

	req := httptest.NewRequest(http.MethodGet, "/learnings", nil)
	rec := httptest.NewRecorder()
	server.handleLearnings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !containsAll(body, "implement_task", "Add TDD guidance", "roles/engineer.md") {
		t.Fatalf("expected proposal details in response, got:\n%s", body[:min(500, len(body))])
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
