package definitions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorkflowPreservesWorkspacePath(t *testing.T) {
	// Env interpolation now happens at dispatch time (via RunContext), not at
	// load time. The raw expression must be preserved verbatim after loading.
	t.Setenv("PROJECT_DIR", "/tmp/project")
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")

	content := []byte(`id: test_workflow
name: Test Workflow
version: 1
description: Test
workspace_defaults:
  mode: project
  path: "${env.PROJECT_DIR}"
nodes: []
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	workflow, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if workflow.WorkspaceDefaults == nil {
		t.Fatal("expected workspace defaults")
	}
	// The raw expression is preserved — dispatch-time resolution handles it.
	if workflow.WorkspaceDefaults.Path != "${env.PROJECT_DIR}" {
		t.Fatalf("unexpected workspace path: %s", workflow.WorkspaceDefaults.Path)
	}
}

func TestLoadWorkflowMissingEnvKeepsLiteral(t *testing.T) {
	_ = os.Unsetenv("PROJECT_DIR")
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")

	content := []byte(`id: test_workflow
name: Test Workflow
version: 1
workspace_defaults:
  mode: project
  path: ${PROJECT_DIR}
nodes: []
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Workspace paths use optional expansion — unset vars stay as literals
	// for the engine to resolve at runtime.
	workflow, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if workflow.WorkspaceDefaults.Path != "${PROJECT_DIR}" {
		t.Fatalf("expected literal ${PROJECT_DIR}, got %s", workflow.WorkspaceDefaults.Path)
	}
}

func TestWorkflowInterviewField(t *testing.T) {
	// Test explicit interview: never
	yamlContent := `
id: test-interview
name: Test Interview
version: 1
interview: never
nodes:
  - id: start
    kind: system
    prompt: "hello"
edges: []
`
	tmpFile := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	wf, err := LoadWorkflowFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.InterviewMode() != InterviewNever {
		t.Errorf("expected InterviewMode()=%q, got %q", InterviewNever, wf.InterviewMode())
	}

	// Test default (never) when field is omitted
	yamlDefault := `
id: test-default
name: Test Default
version: 1
nodes:
  - id: start
    kind: system
    prompt: "hello"
edges: []
`
	tmpFile2 := filepath.Join(t.TempDir(), "default.yaml")
	if err := os.WriteFile(tmpFile2, []byte(yamlDefault), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	wf2, err := LoadWorkflowFile(tmpFile2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf2.InterviewMode() != InterviewNever {
		t.Errorf("expected InterviewMode()=%q (default), got %q", InterviewNever, wf2.InterviewMode())
	}

	// Test on_issue
	yamlOnIssue := `
id: test-on-issue
name: Test On Issue
version: 1
interview: on_issue
nodes:
  - id: start
    kind: system
    prompt: "hello"
edges: []
`
	tmpFile3 := filepath.Join(t.TempDir(), "on_issue.yaml")
	if err := os.WriteFile(tmpFile3, []byte(yamlOnIssue), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	wf3, err := LoadWorkflowFile(tmpFile3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf3.InterviewMode() != InterviewOnIssue {
		t.Errorf("expected InterviewMode()=%q, got %q", InterviewOnIssue, wf3.InterviewMode())
	}
}

func TestNodeSessionIDField(t *testing.T) {
	yamlContent := `
id: test-session
name: Test Session
version: 1
nodes:
  - id: interviewee
    kind: role
    role: test_role
    session_id: "${input.original_session}"
    context: full
    prompt: "answer questions"
edges: []
`
	tmpFile := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	wf, err := LoadWorkflowFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Nodes[0].SessionID != "${input.original_session}" {
		t.Errorf("expected SessionID=${input.original_session}, got %q", wf.Nodes[0].SessionID)
	}
}

func TestLoadWorkflowSnapshotAllowsMissingEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")

	content := []byte(`id: test_workflow
name: Test Workflow
version: 1
workspace_defaults:
  mode: project
  path: ${PROJECT_DIR}
nodes: []
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	workflow, err := LoadWorkflowSnapshot(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if workflow.WorkspaceDefaults == nil {
		t.Fatal("expected workspace defaults")
	}
	if workflow.WorkspaceDefaults.Path != "${PROJECT_DIR}" {
		t.Fatalf("unexpected workspace path: %s", workflow.WorkspaceDefaults.Path)
	}
}

func TestLoadWorkflowWarnsOnUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")

	content := []byte(`id: test_unknown
name: Test
version: 1
nodes:
  - id: step1
    kind: role
    context_mode: fresh
    prompt: do something
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	warnings, err := LoadWorkflowFileWithWarnings(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings for unknown key 'context_mode'")
	}
	found := false
	for _, w := range warnings {
		if contains(w, "context_mode") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning mentioning 'context_mode', got: %v", warnings)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLoadWorkflow_RejectsLegacyOutputsField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.yaml")
	content := []byte(`id: legacy_workflow
name: Legacy
version: 1
nodes:
  - id: producer
    kind: role
    runner: serf
    decisions: [done]
    outputs:
      - plan
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := LoadWorkflowFile(path)
	if err == nil {
		t.Fatal("expected loading to fail for workflow with legacy outputs: field")
	}
	if !containsSubstring(err.Error(), "outputs") || !containsSubstring(err.Error(), "outputs_schema") {
		t.Fatalf("expected error to mention both outputs and outputs_schema, got: %v", err)
	}
	if !containsSubstring(err.Error(), "producer") {
		t.Fatalf("expected offending node id in error, got: %v", err)
	}
}

func TestEdgePassesDecodes(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: Tiny Workflow
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: ok
    passes:
      a_msg: "${node.a.message}"
      threshold: 3
      enabled: true
    prompt: "do the thing"
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "tw.yaml")
	if err := os.WriteFile(path, yamlSrc, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	wf, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
	if len(wf.Edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(wf.Edges))
	}
	e := wf.Edges[0]
	if e.Passes == nil {
		t.Fatalf("Passes is nil; want decoded map")
	}
	if got := e.Passes["a_msg"]; got != "${node.a.message}" {
		t.Errorf("Passes[a_msg]=%v want %q", got, "${node.a.message}")
	}
	if got := e.Passes["threshold"]; got != 3 {
		t.Errorf("Passes[threshold]=%v (%T) want 3 (int)", got, got)
	}
	if got := e.Passes["enabled"]; got != true {
		t.Errorf("Passes[enabled]=%v want true", got)
	}
}

func TestEmitNodeOutputDecodes(t *testing.T) {
	yamlSrc := []byte(`
id: ew
name: Emit Workflow
version: 1
nodes:
  - id: aggregator
    kind: emit
    output:
      decision: escalate
      message: "summary"
      data:
        last: "${input.msg}"
    decisions: [escalate]
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "ew.yaml")
	if err := os.WriteFile(path, yamlSrc, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	wf, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(wf.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(wf.Nodes))
	}
	n := wf.Nodes[0]
	if n.Output == nil {
		t.Fatalf("Output is nil; want decoded EmitOutput")
	}
	if n.Output.Decision != "escalate" {
		t.Errorf("Output.Decision=%q want %q", n.Output.Decision, "escalate")
	}
	if n.Output.Message != "summary" {
		t.Errorf("Output.Message=%q want %q", n.Output.Message, "summary")
	}
	if got := n.Output.Data["last"]; got != "${input.msg}" {
		t.Errorf("Output.Data[last]=%v want %q", got, "${input.msg}")
	}
}

func TestLoadWorkflow_AcceptsOutputsSchemaField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	content := []byte(`id: schema_workflow
name: Schema
version: 1
nodes:
  - id: producer
    kind: role
    runner: serf
    decisions: [done]
    outputs_schema:
      type: object
      required:
        - plan
      properties:
        plan:
          type: string
edges: []
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	wf, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("unexpected error loading workflow with outputs_schema: %v", err)
	}
	node := wf.Nodes[0]
	if node.OutputsSchema == nil {
		t.Fatal("expected outputs_schema to be populated")
	}
	if node.OutputsSchema["type"] != "object" {
		t.Fatalf("expected outputs_schema.type=object, got %v", node.OutputsSchema["type"])
	}
}

func TestFindEnvKeysScansEnvNamespace(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: Tiny Workflow
version: 1
workspace_defaults:
  mode: project
  path: "${env.PROJECT_DIR}"
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      api_key: "${env.OPENAI_API_KEY}"
    prompt: |
      Using model with PROJECT=${env.PROJECT_DIR}
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	wf, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
	keys := FindEnvKeys(wf)
	want := map[string]bool{"PROJECT_DIR": true, "OPENAI_API_KEY": true}
	if len(keys) != len(want) {
		t.Errorf("got %v want keys for %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

func TestNodeInputsStringFormDecodes(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: Tiny Workflow
version: 1
inputs:
  task: string
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      task:       "${workflow_input.task!}"
      prior:      "${node.x.data}"
      choice:     merge
      threshold:  3
      enabled:    true
    decisions: [ok]
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "tw.yaml")
	if err := os.WriteFile(path, yamlSrc, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	wf, err := LoadWorkflowFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	n := wf.Nodes[0]
	if got, want := n.Inputs["task"], "${workflow_input.task!}"; got != want {
		t.Errorf("Inputs[task]=%v want %q", got, want)
	}
	if got, want := n.Inputs["prior"], "${node.x.data}"; got != want {
		t.Errorf("Inputs[prior]=%v want %q", got, want)
	}
	if got, want := n.Inputs["choice"], "merge"; got != want {
		t.Errorf("Inputs[choice]=%v want %q", got, want)
	}
	if got, want := n.Inputs["threshold"], 3; got != want {
		t.Errorf("Inputs[threshold]=%v (%T) want 3 (int)", got, got)
	}
	if got, want := n.Inputs["enabled"], true; got != want {
		t.Errorf("Inputs[enabled]=%v want true", got)
	}
}
