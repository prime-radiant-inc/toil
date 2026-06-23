package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFromBytes(t *testing.T, yamlBytes []byte) (*Workflow, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tw.yaml")
	if err := os.WriteFile(path, yamlBytes, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return LoadWorkflowFile(path)
}

func TestValidateExpressionsRejectsInputInInputs(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: Tiny Workflow
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      task: "${input.task}"
    decisions: [ok]
`)
	_, err := loadFromBytes(t, yamlSrc)
	if err == nil {
		t.Fatalf("expected load-time error for ${input.X} inside inputs: block")
	}
	if !strings.Contains(err.Error(), "input.") || !strings.Contains(err.Error(), "workflow_input") {
		t.Errorf("error message should suggest workflow_input: %v", err)
	}
}

func TestValidateExpressionsRejectsInputInPasses(t *testing.T) {
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
      task: "${input.task}"
`)
	_, err := loadFromBytes(t, yamlSrc)
	if err == nil {
		t.Fatalf("expected load-time error for ${input.X} inside passes: block")
	}
}

func TestValidateExpressionsAcceptsWorkflowInputInInputs(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: Tiny Workflow
version: 1
inputs:
  task: object
input_schema:
  task: {optional: false}
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      task: "${workflow_input.task!}"
    decisions: [ok]
`)
	_, err := loadFromBytes(t, yamlSrc)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
}

// --- Forgotten-${} shape detector tests (Task 30) ---

func TestForgottenExpressionShape(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      task: input.task
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: input.task without ${...} should be flagged")
	}
}

func TestProseValuesNotFlagged(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      note: "input.X is a parameter"
      desc: "node.create_worktree produces the path"
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

// --- node.X.<field> surface validation tests (PRI-2103) ---

// TestValidateExpressionsRejectsUnknownNodeField verifies that a typo'd or
// unsupported node field (e.g. "mesage") is caught at load time rather than
// surfacing only at runtime in the resolver. The error must list the
// supported fields so the author can self-correct.
func TestValidateExpressionsRejectsUnknownNodeField(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    inputs:
      val: "${node.a.mesage}"
edges:
  - from: a
    to: b
    when: ok
`)
	_, err := loadFromBytes(t, yamlSrc)
	if err == nil {
		t.Fatalf("expected load-time error for unsupported node field ${node.a.mesage}")
	}
	if !strings.Contains(err.Error(), "mesage") {
		t.Errorf("error should name the offending field: %v", err)
	}
	if !strings.Contains(err.Error(), "message") {
		t.Errorf("error should list supported fields (e.g. message): %v", err)
	}
}

// TestValidateExpressionsAcceptsKnownNodeFields verifies that every supported
// node field — plus the bare node.X reference and deep data.* paths — passes
// load-time validation.
func TestValidateExpressionsAcceptsKnownNodeFields(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    inputs:
      whole: "${node.a}"
      d: "${node.a.decision}"
      m: "${node.a.message}"
      arts: "${node.a.artifacts}"
      dat: "${node.a.data}"
      deep: "${node.a.data.nested.value}"
      sid: "${node.a.session_id}"
      tg: "${node.a.tags}"
      st: "${node.a.status}"
      at: "${node.a.attempts}"
      lrd: "${node.a.last_routing_decision}"
      li: "${node.a.loop_iterations}"
edges:
  - from: a
    to: b
    when: ok
`)
	if _, err := loadFromBytes(t, yamlSrc); err != nil {
		t.Fatalf("LoadWorkflowFile: all supported node fields should be accepted: %v", err)
	}
}

// --- Required-reference satisfiability tests (Task 30b) ---

func TestWorkflowInputRequiredOnOptionalIsLoadTimeError(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
inputs:
  maybe: string
input_schema:
  maybe: {optional: true}
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      val: "${workflow_input.maybe!}"
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: maybe is optional, cannot be referenced via !")
	}
}

func TestInputRequiredInPromptUnsatisfiable(t *testing.T) {
	// B's prompt reads ${input.from_edge!} but from_edge is only on
	// the edge passes, not B's inputs, not a workflow input.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    prompt: "got=${input.from_edge!}"
edges:
  - from: a
    to: b
    when: ok
    passes:
      from_edge: "literal"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: ${input.from_edge!} in b's prompt unsatisfiable on retrigger")
	}
}

func TestWorkflowInputRequiredOnNonOptionalAccepted(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
inputs:
  must_have: string
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      val: "${workflow_input.must_have!}"
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

func TestInputRequiredOnNodeInputsAccepted(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    inputs:
      base: "value"
    prompt: "got=${input.base!}"
    decisions: [ok]
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

// --- kata 5r6b: approval gate + required-reference tests ---

// TestApprovalGate_EdgePassesUnsatisfiesRequired verifies that a gate:required
// node whose prompt references ${input.X!} — where X is only provided via
// edge passes (not on the node's own inputs: or as a workflow input) — is
// rejected at load time. RetriggerNode re-queues approval gates without edge
// context, so edge passes cannot satisfy required-ness.
func TestApprovalGate_EdgePassesUnsatisfiesRequired(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: write_code
    kind: role
    runner: shell
    decisions: [done]
  - id: human_approval
    kind: human
    gate: required
    prompt: "Please review: ${input.summary!}"
    decisions: [approved, rejected]
edges:
  - from: write_code
    to: human_approval
    when: done
    passes:
      summary: "${node.write_code.message}"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected load-time error: ${input.summary!} in approval gate prompt unsatisfiable on retrigger (edge passes bypass retrigger)")
	}
	if !strings.Contains(err.Error(), "unsatisfiable") {
		t.Errorf("expected 'unsatisfiable' in error message, got: %v", err)
	}
}

// TestApprovalGate_NodeInputsSatisfiesRequired verifies that a gate:required
// node whose prompt references ${input.X!} — where X is declared on the
// node's own inputs: block — is accepted at load time.
func TestApprovalGate_NodeInputsSatisfiesRequired(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: write_code
    kind: role
    runner: shell
    decisions: [done]
  - id: human_approval
    kind: human
    gate: required
    inputs:
      summary: "${node.write_code.message}"
    prompt: "Please review: ${input.summary!}"
    decisions: [approved, rejected]
edges:
  - from: write_code
    to: human_approval
    when: done
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}
