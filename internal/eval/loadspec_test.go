package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSpec_Valid(t *testing.T) {
	dir := t.TempDir()
	specYAML := `
id: test-eval
name: Test Eval
workflow_id: build
project_dir: /tmp/project
inputs:
  idea: "Build a CLI"
  ledger_path: "ledger.tsv"
verify:
  command: "go test ./..."
auto_approve: true
`
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	if spec.ID != "test-eval" {
		t.Fatalf("ID = %q, want %q", spec.ID, "test-eval")
	}
	if spec.WorkflowID != "build" {
		t.Fatalf("WorkflowID = %q, want %q", spec.WorkflowID, "build")
	}
	if spec.ProjectDir != "/tmp/project" {
		t.Fatalf("ProjectDir = %q, want %q", spec.ProjectDir, "/tmp/project")
	}
	if spec.Verify.Command != "go test ./..." {
		t.Fatalf("Verify.Command = %q, want %q", spec.Verify.Command, "go test ./...")
	}
	if !spec.AutoApprove {
		t.Fatal("AutoApprove should be true")
	}
	if spec.Inputs["idea"] != "Build a CLI" {
		t.Fatalf("Inputs[idea] = %q, want %q", spec.Inputs["idea"], "Build a CLI")
	}
}

func TestLoadSpec_MissingID(t *testing.T) {
	dir := t.TempDir()
	specYAML := `
workflow_id: build
project_dir: /tmp/project
`
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestLoadSpec_MissingWorkflowID(t *testing.T) {
	dir := t.TempDir()
	specYAML := `
id: test
project_dir: /tmp/project
`
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for missing workflow_id")
	}
}

func TestLoadSpec_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadSpec_FileNotFound(t *testing.T) {
	_, err := LoadSpec("/nonexistent/spec.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSpec_NilInputsInitialized(t *testing.T) {
	dir := t.TempDir()
	specYAML := `
id: test
workflow_id: build
project_dir: /tmp
`
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec.Inputs == nil {
		t.Fatal("Inputs should be initialized to empty map, got nil")
	}
}

func TestLoadSpec_WithApprovals(t *testing.T) {
	dir := t.TempDir()
	specYAML := `
id: test
workflow_id: build
project_dir: /tmp
approvals:
  review_node:
    decision: approved
    message: "Auto approved"
    comment: "eval"
`
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	a, ok := spec.Approvals["review_node"]
	if !ok {
		t.Fatal("expected approval for review_node")
	}
	if a.Decision != "approved" {
		t.Fatalf("Decision = %q, want %q", a.Decision, "approved")
	}
}
