package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupEvalRoot creates a minimal toil root directory with a shell runner
// and a simple echo workflow suitable for eval integration tests.
func setupEvalRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Shell runner.
	runnersDir := filepath.Join(root, "definitions", "runners")
	if err := os.MkdirAll(runnersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runnersDir, "shell.yaml"), []byte(`
id: shell
type: shell
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Minimal echo workflow.
	workflowsDir := filepath.Join(root, "definitions", "workflows")
	if err := os.MkdirAll(workflowsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowsDir, "echo.yaml"), []byte(`
id: echo
name: Echo
version: 1
nodes:
  - id: echo
    kind: role
    runner: shell
    prompt: "mkdir -p $PROJECT_DIR && echo hello"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Runs directory — set TOIL_RUNS_DIR so runs stay under the test root.
	runsDir := filepath.Join(root, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOIL_RUNS_DIR", runsDir)

	return root
}

func TestRun_MissingProjectDir(t *testing.T) {
	spec := &Spec{
		ID:         "test",
		WorkflowID: "echo",
		Inputs:     map[string]any{},
	}

	_, err := Run(context.Background(), t.TempDir(), spec)
	if err == nil {
		t.Fatal("expected error for missing project_dir")
	}
}

func TestRun_AppLoadError(t *testing.T) {
	// root with no definitions/ at all.
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	spec := &Spec{
		ID:         "test",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
	}

	_, err := Run(context.Background(), root, spec)
	if err == nil {
		t.Fatal("expected error when app.Load fails")
	}
}

func TestRun_PassingWorkflow(t *testing.T) {
	root := setupEvalRoot(t)
	// projectDir must be separate from root since cleanEvalArtifacts removes it.
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-1",
		Name:       "Echo Test",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("Status = %q, want %q", result.Status, "passed")
	}
	if result.RunID == "" {
		t.Fatal("RunID should not be empty")
	}
	if result.ID != "eval-1" {
		t.Fatalf("ID = %q, want %q", result.ID, "eval-1")
	}

	// Verify eval.json was saved.
	evalPath := filepath.Join(root, "runs", result.RunID, "eval.json")
	if _, err := os.Stat(evalPath); err != nil {
		t.Fatalf("eval.json not saved: %v", err)
	}
}

func TestRun_WithVerifyCommand_Passing(t *testing.T) {
	root := setupEvalRoot(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-2",
		Name:       "Echo Verify",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		Verify:     VerifySpec{Command: "true"},
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("Status = %q, want %q", result.Status, "passed")
	}
}

func TestRun_WithVerifyCommand_Failing(t *testing.T) {
	root := setupEvalRoot(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-3",
		Name:       "Echo Verify Fail",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		Verify:     VerifySpec{Command: "false"},
	}

	result, err := Run(context.Background(), root, spec)
	if err == nil {
		t.Fatal("expected error for failing verify command")
	}
	if result.Status != "failed" {
		t.Fatalf("Status = %q, want %q", result.Status, "failed")
	}
}

func TestRun_VerifyOutputCaptured(t *testing.T) {
	root := setupEvalRoot(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-verify-output",
		Name:       "Verify Output Test",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		Verify:     VerifySpec{Command: "echo 'FAIL: TestAdd expected 4 got 5' && echo 'FAIL: TestSub expected 0 got 1' >&2 && exit 1"},
	}

	result, err := Run(context.Background(), root, spec)
	if err == nil {
		t.Fatal("expected error for failing verify command")
	}
	if result.VerifyOutput == "" {
		t.Fatal("VerifyOutput should contain the command output")
	}
	if !strings.Contains(result.VerifyOutput, "FAIL: TestAdd") {
		t.Fatalf("VerifyOutput should contain stdout; got %q", result.VerifyOutput)
	}
	if !strings.Contains(result.VerifyOutput, "FAIL: TestSub") {
		t.Fatalf("VerifyOutput should contain stderr; got %q", result.VerifyOutput)
	}

	// Verify eval.json was saved even on verify failure.
	evalPath := filepath.Join(root, "runs", result.RunID, "eval.json")
	if _, err := os.Stat(evalPath); err != nil {
		t.Fatalf("eval.json should be saved on verify failure: %v", err)
	}
}

func TestRun_VerifyOutputCaptured_Passing(t *testing.T) {
	root := setupEvalRoot(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-verify-pass-output",
		Name:       "Verify Pass Output Test",
		WorkflowID: "echo",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		Verify:     VerifySpec{Command: "echo 'ok 5 passed'"},
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("Status = %q, want %q", result.Status, "passed")
	}
	if !strings.Contains(result.VerifyOutput, "ok 5 passed") {
		t.Fatalf("VerifyOutput should contain output even on pass; got %q", result.VerifyOutput)
	}
}

func TestRun_BadWorkflowID(t *testing.T) {
	root := setupEvalRoot(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-4",
		Name:       "Bad Workflow",
		WorkflowID: "nonexistent",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
	}

	result, err := Run(context.Background(), root, spec)
	if err == nil {
		t.Fatal("expected error for bad workflow_id")
	}
	if result == nil {
		t.Fatal("result should not be nil even on error")
	}
	if result.Status != "failed" {
		t.Fatalf("Status = %q, want %q", result.Status, "failed")
	}
}

func TestPrepareEvalEnv_NilInputs(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		ProjectDir: filepath.Join(root, "project"),
		Inputs:     nil,
	}

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}
	if spec.Inputs == nil {
		t.Fatal("Inputs should be initialized to empty map")
	}
	if _, ok := spec.Inputs["project_dir"]; !ok {
		t.Fatal("project_dir should be set in inputs")
	}
}

// setupEvalRootWithGate creates a toil root with a shell runner and a workflow
// that has a system gate node requiring approval after the initial shell step.
func setupEvalRootWithGate(t *testing.T) string {
	t.Helper()
	root := setupEvalRoot(t)

	workflowsDir := filepath.Join(root, "definitions", "workflows")
	if err := os.WriteFile(filepath.Join(workflowsDir, "gated.yaml"), []byte(`
id: gated
name: Gated
version: 1
nodes:
  - id: work
    kind: role
    runner: shell
    prompt: "mkdir -p $PROJECT_DIR && echo done"
  - id: gate
    kind: system
    gate: required
    decisions:
      - id: approved
      - id: rejected
edges:
  - from: work
    to: gate
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRun_AutoApprove(t *testing.T) {
	root := setupEvalRootWithGate(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:          "eval-auto",
		Name:        "Auto Approve Test",
		WorkflowID:  "gated",
		ProjectDir:  projectDir,
		AutoApprove: true,
		Inputs:      map[string]any{},
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("Status = %q, want %q", result.Status, "passed")
	}
}

func TestRun_ApprovalsMap(t *testing.T) {
	root := setupEvalRootWithGate(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-approvals",
		Name:       "Approvals Map Test",
		WorkflowID: "gated",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		Approvals: map[string]ApprovalSpec{
			"gate": {
				Decision: "approved",
				Message:  "test approval",
				Comment:  "test comment",
			},
		},
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("Status = %q, want %q", result.Status, "passed")
	}
}

func TestRun_PausedWithoutAutoApprove(t *testing.T) {
	root := setupEvalRootWithGate(t)
	projectDir := t.TempDir()

	spec := &Spec{
		ID:         "eval-paused",
		Name:       "Paused Gate Test",
		WorkflowID: "gated",
		ProjectDir: projectDir,
		Inputs:     map[string]any{},
		// No AutoApprove, no Approvals — gate should cause "paused" status.
	}

	result, err := Run(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "paused" {
		t.Fatalf("Status = %q, want %q", result.Status, "paused")
	}
	if result.RunID == "" {
		t.Fatal("RunID should not be empty for paused run")
	}
}
