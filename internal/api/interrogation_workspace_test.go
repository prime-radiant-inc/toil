package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestResolveInterrogationWorkspace_ProjectModeUsesDeclaredPath(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Mode: "project", Path: dir},
	}
	node := &definitions.Node{ID: "x"}

	ws, err := resolveInterrogationWorkspace(workflow, node, nil, "/unused/runs", "run-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws != dir {
		t.Errorf("workspace = %q, want %q", ws, dir)
	}
}

// PRI-1573: non-project workspace modes (none, shared) used to hard-reject
// with a 400. Fall back to the run's runs-dir directory so interrogation
// works for workflows like `learn` whose nodes have no project workspace.
func TestResolveInterrogationWorkspace_NoneModeFallsBackToRunsDir(t *testing.T) {
	runsDir := t.TempDir()
	runID := "shadow-mosaic-pivot"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Mode: "none"},
	}
	node := &definitions.Node{ID: "synthesize"}

	ws, err := resolveInterrogationWorkspace(workflow, node, nil, runsDir, runID)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if ws != runDir {
		t.Errorf("workspace = %q, want fallback %q", ws, runDir)
	}
}

func TestResolveInterrogationWorkspace_NoWorkspaceDefaultFallsBackToRunsDir(t *testing.T) {
	runsDir := t.TempDir()
	runID := "no-workspace-run"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := &definitions.Workflow{} // no WorkspaceDefaults at all
	node := &definitions.Node{ID: "x"}

	ws, err := resolveInterrogationWorkspace(workflow, node, nil, runsDir, runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws != runDir {
		t.Errorf("workspace = %q, want fallback %q", ws, runDir)
	}
}

func TestResolveInterrogationWorkspace_FallbackErrorNamesConstraint(t *testing.T) {
	// Neither project nor fallback workable — the error must name the
	// actual constraint and suggest a path forward.
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Mode: "none"},
	}
	node := &definitions.Node{ID: "x"}

	_, err := resolveInterrogationWorkspace(workflow, node, nil, "/nonexistent/runs/dir", "missing-run")
	if err == nil {
		t.Fatal("expected error when fallback missing")
	}
	msg := err.Error()
	for _, expected := range []string{"none", "workspace_defaults.mode: project", "missing-run"} {
		if !strings.Contains(msg, expected) {
			t.Errorf("error should mention %q; got %q", expected, msg)
		}
	}
}

func TestResolveInterrogationWorkspace_ProjectMissingPathStillErrors(t *testing.T) {
	// Project mode with a path that doesn't exist must NOT silently fall
	// back to runs-dir — the project workspace is load-bearing for
	// project-mode interrogation.
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Mode: "project", Path: "/nonexistent/project/path"},
	}
	node := &definitions.Node{ID: "x"}

	_, err := resolveInterrogationWorkspace(workflow, node, nil, t.TempDir(), "run-x")
	if err == nil {
		t.Fatal("expected error when project path missing")
	}
	if !strings.Contains(err.Error(), "workspace no longer exists") {
		t.Errorf("error should mention 'workspace no longer exists', got %q", err.Error())
	}
}
