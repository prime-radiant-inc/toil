package engine

import (
	"io"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestCaptureEnvUsesInputProjectDir(t *testing.T) {
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Path: "${PROJECT_DIR}"},
	}
	t.Setenv("PROJECT_DIR", "/env/project")
	inputs := map[string]any{
		"project_dir": "/input/project",
	}
	env := captureEnv(workflow, nil, inputs)
	if env == nil {
		t.Fatal("expected env to be populated")
	}
	if env["PROJECT_DIR"] != "/input/project" {
		t.Fatalf("expected PROJECT_DIR from inputs, got %q", env["PROJECT_DIR"])
	}
}

func TestCaptureEnvPrefersOverride(t *testing.T) {
	workflow := &definitions.Workflow{
		WorkspaceDefaults: &definitions.Workspace{Path: "${PROJECT_DIR}"},
	}
	t.Setenv("PROJECT_DIR", "/env/project")
	inputs := map[string]any{
		"project_dir": "/input/project",
	}
	override := map[string]string{
		"PROJECT_DIR": "/override/project",
	}
	env := captureEnv(workflow, override, inputs)
	if env == nil {
		t.Fatal("expected env to be populated")
	}
	if env["PROJECT_DIR"] != "/override/project" {
		t.Fatalf("expected PROJECT_DIR from override, got %q", env["PROJECT_DIR"])
	}
}

func TestCaptureEnvIncludesProjectDirWithoutReferences(t *testing.T) {
	workflow := &definitions.Workflow{}
	inputs := map[string]any{
		"project_dir": "/input/project",
	}
	env := captureEnv(workflow, nil, inputs)
	if env == nil {
		t.Fatal("expected env to be populated")
	}
	if env["PROJECT_DIR"] != "/input/project" {
		t.Fatalf("expected PROJECT_DIR from inputs, got %q", env["PROJECT_DIR"])
	}
}

func TestChildEnvForSubworkflowOverridesProjectDirFromInputs(t *testing.T) {
	parent := map[string]string{
		"PROJECT_DIR": "/parent/project",
		"OTHER":       "value",
	}
	inputs := map[string]any{
		"project_dir": "/child/worktree",
	}

	child := childEnvForSubworkflow(parent, inputs)
	if child == nil {
		t.Fatal("expected child env")
	}
	if child["PROJECT_DIR"] != "/child/worktree" {
		t.Fatalf("expected PROJECT_DIR override, got %q", child["PROJECT_DIR"])
	}
	if child["OTHER"] != "value" {
		t.Fatalf("expected OTHER to be preserved, got %q", child["OTHER"])
	}
}

func TestChildEnvForSubworkflowKeepsParentProjectDirWhenInputMissing(t *testing.T) {
	parent := map[string]string{
		"PROJECT_DIR": "/parent/project",
	}

	child := childEnvForSubworkflow(parent, map[string]any{})
	if child == nil {
		t.Fatal("expected child env")
	}
	if child["PROJECT_DIR"] != "/parent/project" {
		t.Fatalf("expected PROJECT_DIR to remain parent value, got %q", child["PROJECT_DIR"])
	}
}

func TestChildEnvForSubworkflowOverridesWorkflowDirFromInputs(t *testing.T) {
	parent := map[string]string{
		"TOIL_CURRENT_WORKFLOW_DIR": "/parent/workflow",
	}
	inputs := map[string]any{
		"workflow_dir": "/child/worktree",
	}

	child := childEnvForSubworkflow(parent, inputs)
	if child["TOIL_CURRENT_WORKFLOW_DIR"] != "/child/worktree" {
		t.Fatalf("expected TOIL_CURRENT_WORKFLOW_DIR override, got %q", child["TOIL_CURRENT_WORKFLOW_DIR"])
	}
}

func TestChildEnvForSubworkflowKeepsParentWorkflowDirWhenInputMissing(t *testing.T) {
	parent := map[string]string{
		"TOIL_CURRENT_WORKFLOW_DIR": "/parent/workflow",
	}

	child := childEnvForSubworkflow(parent, map[string]any{})
	if child["TOIL_CURRENT_WORKFLOW_DIR"] != "/parent/workflow" {
		t.Fatalf("expected TOIL_CURRENT_WORKFLOW_DIR to remain parent value, got %q", child["TOIL_CURRENT_WORKFLOW_DIR"])
	}
}

func TestCaptureEnvSeparatesSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	t.Setenv("PROJECT_DIR", "/some/project")

	inputs := map[string]any{
		"secret_keys": []any{"GITHUB_TOKEN"},
		"project_dir": "/some/project",
	}

	env, secrets := captureEnvWithSecrets(&definitions.Workflow{}, nil, inputs)

	if _, ok := env["GITHUB_TOKEN"]; ok {
		t.Fatal("GITHUB_TOKEN should not be in env")
	}
	if secrets["GITHUB_TOKEN"] != "ghp_test123" {
		t.Fatalf("expected GITHUB_TOKEN in secrets, got %q", secrets["GITHUB_TOKEN"])
	}
	if env["PROJECT_DIR"] != "/some/project" {
		t.Fatalf("expected PROJECT_DIR in env, got %q", env["PROJECT_DIR"])
	}
}

func TestCaptureEnvAutoClassifiesSecretPatterns(t *testing.T) {
	t.Setenv("AWS_SECRET_KEY", "wJalrXUtnFEMI/K7MDENG")
	t.Setenv("MY_PASSWORD", "supersecure1")
	t.Setenv("API_TOKEN", "tok-abc123xyz")
	t.Setenv("NORMAL_VAR", "hello")

	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{Prompt: "${env.AWS_SECRET_KEY} ${env.MY_PASSWORD} ${env.API_TOKEN} ${env.NORMAL_VAR}"},
		},
	}
	inputs := map[string]any{}

	env, secrets := captureEnvWithSecrets(workflow, nil, inputs)

	if _, ok := env["AWS_SECRET_KEY"]; ok {
		t.Fatal("AWS_SECRET_KEY should be classified as secret")
	}
	if _, ok := secrets["AWS_SECRET_KEY"]; !ok {
		t.Fatal("AWS_SECRET_KEY should be in secrets")
	}

	if _, ok := env["MY_PASSWORD"]; ok {
		t.Fatal("MY_PASSWORD should be classified as secret")
	}
	if _, ok := secrets["MY_PASSWORD"]; !ok {
		t.Fatal("MY_PASSWORD should be in secrets")
	}

	if _, ok := env["API_TOKEN"]; ok {
		t.Fatal("API_TOKEN should be classified as secret")
	}
	if _, ok := secrets["API_TOKEN"]; !ok {
		t.Fatal("API_TOKEN should be in secrets")
	}

	if env["NORMAL_VAR"] != "hello" {
		t.Fatalf("NORMAL_VAR should stay in env, got %q", env["NORMAL_VAR"])
	}
	if _, ok := secrets["NORMAL_VAR"]; ok {
		t.Fatal("NORMAL_VAR should not be in secrets")
	}
}

func TestCreateRunOverridesStalePerRunEnvForTopLevelRun(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:                "test-wf",
		Name:              "Test",
		Version:           1,
		WorkspaceDefaults: &definitions.Workspace{Path: "${TOIL_CURRENT_WORKFLOW_DIR}"},
		Nodes:             []definitions.Node{{ID: "step1", Kind: "role", Runner: "shell"}},
		Edges:             []definitions.Edge{},
	}
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	// Simulate a re-run: pass env from a previous run that has stale
	// per-run directories.
	staleEnv := map[string]string{
		"TOIL_CURRENT_WORKFLOW_DIR": "/old/run/workflow",
		"TOIL_WORKFLOW_OUTPUTS":     "/old/run/outputs",
		"TOIL_RUN_ID":               "old-run-id",
	}
	runID, err := eng.createRun(workflow.ID, nil, "" /* top-level */, staleEnv, "")
	if err != nil {
		t.Fatalf("createRun: %v", err)
	}

	rs, err := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Per-run env vars must point to the NEW run's directories, not the
	// stale values from the old run.
	wantWorkflowDir := filepath.Join(runsDir, runID, "workflow")
	if rs.Env["TOIL_CURRENT_WORKFLOW_DIR"] != wantWorkflowDir {
		t.Errorf("TOIL_CURRENT_WORKFLOW_DIR = %q, want %q", rs.Env["TOIL_CURRENT_WORKFLOW_DIR"], wantWorkflowDir)
	}
	wantOutputsDir := filepath.Join(runsDir, runID, "outputs")
	if rs.Env["TOIL_WORKFLOW_OUTPUTS"] != wantOutputsDir {
		t.Errorf("TOIL_WORKFLOW_OUTPUTS = %q, want %q", rs.Env["TOIL_WORKFLOW_OUTPUTS"], wantOutputsDir)
	}
	if rs.Env["TOIL_RUN_ID"] != runID {
		t.Errorf("TOIL_RUN_ID = %q, want %q", rs.Env["TOIL_RUN_ID"], runID)
	}
}

func TestCreateRunPreservesParentWorkflowDirForSubworkflow(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:                "test-wf",
		Name:              "Test",
		Version:           1,
		WorkspaceDefaults: &definitions.Workspace{Path: "${TOIL_CURRENT_WORKFLOW_DIR}"},
		Nodes:             []definitions.Node{{ID: "step1", Kind: "role", Runner: "shell"}},
		Edges:             []definitions.Edge{},
	}
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	// Subworkflow: parent's workflow dir should be preserved.
	parentEnv := map[string]string{
		"TOIL_CURRENT_WORKFLOW_DIR": "/parent/worktree",
	}
	runID, err := eng.createRun(workflow.ID, nil, "parent-run" /* subworkflow */, parentEnv, "")
	if err != nil {
		t.Fatalf("createRun: %v", err)
	}

	rs, err := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Subworkflow should inherit parent's workflow dir.
	if rs.Env["TOIL_CURRENT_WORKFLOW_DIR"] != "/parent/worktree" {
		t.Errorf("TOIL_CURRENT_WORKFLOW_DIR = %q, want %q", rs.Env["TOIL_CURRENT_WORKFLOW_DIR"], "/parent/worktree")
	}
}
