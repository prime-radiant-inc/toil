package engine

import (
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestNewRunIDUsesThreeUniqueWords(t *testing.T) {
	for i := 0; i < 32; i++ {
		runID := newRunID()
		parts := strings.Split(runID, "-")
		if len(parts) != 3 {
			t.Fatalf("expected 3 words, got %q", runID)
		}
		seen := map[string]struct{}{}
		for _, part := range parts {
			if part == "" {
				t.Fatalf("expected non-empty word in run id %q", runID)
			}
			if _, exists := seen[part]; exists {
				t.Fatalf("expected unique words in run id %q", runID)
			}
			seen[part] = struct{}{}
		}
	}
}

func TestBuildRunTitleUsesWorkflowAndInputs(t *testing.T) {
	workflow := &definitions.Workflow{
		ID:   "implement_task",
		Name: "Implement Task",
	}
	inputs := map[string]any{
		"task": map[string]any{
			"name": "Build Todo CLI",
		},
	}
	title := buildRunTitle(workflow, inputs)
	if title != "Implement Task: Build Todo CLI" {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestBuildRunTitleFallsBackToWorkflowID(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "implement_task",
	}
	title := buildRunTitle(workflow, nil)
	if title != "implement_task" {
		t.Fatalf("unexpected title: %q", title)
	}
}

// TestBuildRunTitlePrefersDomainSubjectOverProductSlug guards against the
// regression where product_slug — inherited downward through every
// subworkflow's inputs — drowns out the per-iteration subject and turns every
// descendant run's label into a duplicate (e.g. "Implement Task: tetris" for
// every task in the implement_spec chain). The per-iteration subject (task,
// component, story…) must win so sibling runs are distinguishable in the
// Execution Group graph.
func TestBuildRunTitlePrefersDomainSubjectOverProductSlug(t *testing.T) {
	workflow := &definitions.Workflow{
		ID:   "implement_task",
		Name: "Implement Task",
	}
	inputs := map[string]any{
		"product_slug": "tetris",
		"task": map[string]any{
			"id":   "task-3",
			"name": "Terminal adapter for rendering and keyboard translation",
		},
	}
	got := buildRunTitle(workflow, inputs)
	want := "Implement Task: Terminal adapter for rendering and keyboard translation"
	if got != want {
		t.Fatalf("buildRunTitle = %q, want %q", got, want)
	}
}

// TestBuildRunTitleUsesProductSlugWhenNoDomainSubject keeps the top-level
// implement_spec label ("Implement Spec: tetris") working — when a run's
// inputs carry no per-iteration subject, product_slug remains the best
// human-readable label.
func TestBuildRunTitleUsesProductSlugWhenNoDomainSubject(t *testing.T) {
	workflow := &definitions.Workflow{
		ID:   "implement_spec",
		Name: "Implement Spec",
	}
	inputs := map[string]any{
		"product_slug": "tetris",
		"project_dir":  "/tmp/dr",
		"spec":         "A simple terminal based Tetris.",
	}
	got := buildRunTitle(workflow, inputs)
	want := "Implement Spec: tetris"
	if got != want {
		t.Fatalf("buildRunTitle = %q, want %q", got, want)
	}
}

// TestBuildRunTitleCombinesProductSlugAndSprint preserves the existing
// "<product> / <sprint>" combined subject for top-level runs that carry a
// sprint input alongside product_slug.
func TestBuildRunTitleCombinesProductSlugAndSprint(t *testing.T) {
	workflow := &definitions.Workflow{
		ID:   "grind",
		Name: "Grind",
	}
	inputs := map[string]any{
		"product_slug": "tetris",
		"sprint": map[string]any{
			"id":    "sprint-7",
			"title": "Playable Core",
		},
	}
	got := buildRunTitle(workflow, inputs)
	want := "Grind: tetris / Playable Core"
	if got != want {
		t.Fatalf("buildRunTitle = %q, want %q", got, want)
	}
}

// TestBuildRunTitleProjectDirFallback — project_dir remains a last-resort
// subject for exotic workflows that have nothing else identifying.
func TestBuildRunTitleProjectDirFallback(t *testing.T) {
	workflow := &definitions.Workflow{
		ID:   "exotic",
		Name: "Exotic",
	}
	inputs := map[string]any{
		"project_dir": "/tmp/dr",
	}
	got := buildRunTitle(workflow, inputs)
	want := "Exotic: /tmp/dr"
	if got != want {
		t.Fatalf("buildRunTitle = %q, want %q", got, want)
	}
}
