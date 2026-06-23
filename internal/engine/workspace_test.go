package engine

import (
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestResolveWorkspaceProjectCreatesDir(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{
			Mode: "project",
			Path: projectPath,
		},
	}
	node := &definitions.Node{ID: "node-1"}

	resolved, err := resolveWorkspace(root, workflow, node, nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if resolved != projectPath {
		t.Fatalf("unexpected workspace: %s", resolved)
	}
	info, err := os.Stat(projectPath)
	if err != nil {
		t.Fatalf("project dir missing: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("project path not a dir: %s", projectPath)
	}
}

// TestResolveWorkspaceResolvesInputExpression pins the new contract:
// `workspace.path` is run through the run-context expression resolver
// before env-var expansion, so a YAML path of `${input.task_worktree}`
// resolves to whatever value the workflow received as input.task_worktree.
func TestResolveWorkspaceResolvesInputExpression(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "tasks", "task-0")
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{
			Mode: "project",
			Path: "${input.task_worktree}",
		},
	}
	node := &definitions.Node{ID: "node-1"}
	runContext := &RunContext{
		Inputs: map[string]any{"task_worktree": target},
	}

	resolved, err := resolveWorkspace(root, workflow, node, runContext)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if resolved != target {
		t.Fatalf("workspace = %q, want %q", resolved, target)
	}
}

// TestResolveWorkspaceResolvesEnvNamespace covers the primary contract:
// workflows that declare `${env.PROJECT_DIR}` in workspace_defaults resolve
// the env key from runState.Env via PopulateEnv → RunContext.Env.
func TestResolveWorkspaceResolvesEnvNamespace(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "env-project")
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{
			Mode: "project",
			Path: "${env.PROJECT_DIR}",
		},
	}
	node := &definitions.Node{ID: "node-1"}
	env := map[string]string{"PROJECT_DIR": target}
	rc := &RunContext{}
	rc.PopulateEnv(env)

	resolved, err := resolveWorkspace(root, workflow, node, rc)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if resolved != target {
		t.Fatalf("workspace = %q, want %q", resolved, target)
	}
}
