package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

// writeRun lays down a runs/<id>/state.json with the given parent. Used
// to fabricate a synthetic family tree on disk for tree-walking tests.
func writeRun(t *testing.T, runsDir, id, workflow, status, parent string, started time.Time, finished *time.Time) {
	t.Helper()
	rs := &state.RunState{
		ID:         id,
		WorkflowID: workflow,
		Status:     status,
		StartedAt:  started,
		FinishedAt: finished,
		ParentRun:  parent,
		Inputs:     map[string]any{},
		Nodes:      map[string]*state.NodeState{},
	}
	dir := filepath.Join(runsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRenderRunTree(t *testing.T) {
	runsDir := t.TempDir()

	start := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	fin1 := start.Add(12*time.Minute + 34*time.Second)
	fin2 := start.Add(11*time.Minute + 45*time.Second)
	fin4 := start.Add(2*time.Minute + 11*time.Second)
	fin5 := start.Add(6*time.Minute + 20*time.Second)

	// Tree:
	//   root (implement_spec)
	//   └─ child-a (build_component)
	//      ├─ leaf-1 (implement_task)
	//      └─ leaf-2 (implement_task)
	writeRun(t, runsDir, "root", "implement_spec", "completed", "", start, &fin1)
	writeRun(t, runsDir, "child-a", "build_component", "completed", "root", start.Add(5*time.Second), &fin2)
	writeRun(t, runsDir, "leaf-1", "implement_task", "completed", "child-a", start.Add(15*time.Second), &fin4)
	writeRun(t, runsDir, "leaf-2", "implement_task", "completed", "child-a", start.Add(20*time.Second), &fin5)

	// Unrelated run that must NOT appear in the tree output.
	writeRun(t, runsDir, "unrelated", "other", "completed", "", start, &fin1)

	var buf bytes.Buffer
	if err := renderRunTree(&buf, runsDir, "root"); err != nil {
		t.Fatalf("renderRunTree: %v", err)
	}
	out := buf.String()

	// All four family members present.
	for _, id := range []string{"root", "child-a", "leaf-1", "leaf-2"} {
		if !strings.Contains(out, id) {
			t.Errorf("expected run %q in output:\n%s", id, out)
		}
	}
	// Unrelated run absent.
	if strings.Contains(out, "unrelated") {
		t.Errorf("unrelated run leaked into tree output:\n%s", out)
	}
	// Workflow IDs rendered alongside.
	for _, wf := range []string{"implement_spec", "build_component", "implement_task"} {
		if !strings.Contains(out, wf) {
			t.Errorf("expected workflow %q in output:\n%s", wf, out)
		}
	}

	// Last sibling under a parent uses the terminal └─ glyph; the
	// non-terminal sibling uses ├─. Both glyphs must show up at least
	// once given the fixture (leaf-1 then leaf-2 under child-a).
	if !strings.Contains(out, "├─") {
		t.Errorf("expected non-terminal branch glyph in output:\n%s", out)
	}
	if !strings.Contains(out, "└─") {
		t.Errorf("expected terminal leaf glyph in output:\n%s", out)
	}

	// Indentation: every child line must appear AFTER its parent line.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	pos := map[string]int{}
	for i, line := range lines {
		for _, id := range []string{"root", "child-a", "leaf-1", "leaf-2"} {
			if strings.Contains(line, id) {
				if _, seen := pos[id]; !seen {
					pos[id] = i
				}
			}
		}
	}
	parentOf := map[string]string{
		"child-a": "root",
		"leaf-1":  "child-a",
		"leaf-2":  "child-a",
	}
	for child, parent := range parentOf {
		if pos[child] <= pos[parent] {
			t.Errorf("child %q (line %d) should appear after parent %q (line %d):\n%s",
				child, pos[child], parent, pos[parent], out)
		}
	}

	// Root line must have no leading whitespace/glyphs — it's the
	// anchor of the tree.
	if strings.HasPrefix(lines[0], " ") || strings.HasPrefix(lines[0], "│") ||
		strings.HasPrefix(lines[0], "├") || strings.HasPrefix(lines[0], "└") {
		t.Errorf("root line should not start with tree glyph: %q", lines[0])
	}
}

func TestRenderRunTree_MissingRoot(t *testing.T) {
	runsDir := t.TempDir()
	var buf bytes.Buffer
	err := renderRunTree(&buf, runsDir, "nonexistent")
	if err == nil {
		t.Fatalf("expected error for missing root, got nil")
	}
}

func TestRenderRunTree_SingleNode(t *testing.T) {
	runsDir := t.TempDir()
	start := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	fin := start.Add(30 * time.Second)
	writeRun(t, runsDir, "solo", "wf", "completed", "", start, &fin)

	var buf bytes.Buffer
	if err := renderRunTree(&buf, runsDir, "solo"); err != nil {
		t.Fatalf("renderRunTree: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "solo") || !strings.Contains(out, "wf") {
		t.Errorf("expected solo run in output:\n%s", out)
	}
	if strings.Contains(out, "├") || strings.Contains(out, "└") {
		t.Errorf("single-node tree should not contain branch glyphs:\n%s", out)
	}
}
