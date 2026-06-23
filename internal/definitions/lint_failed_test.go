package definitions

import (
	"strings"
	"testing"
)

func TestLint_FailedRequiredOnMetaDecisionEdge(t *testing.T) {
	yamlStr := []byte(`
id: test_wf
name: Test Workflow
version: 1
nodes:
  - id: looper
    kind: role
    runner: shell
    decisions:
      - id: again
    prompt: "echo hi"
  - id: done
    kind: emit
    decisions: [stop]
    output:
      decision: stop
      message: "done"
edges:
  - from: looper
    to: looper
    when: again
  - from: looper
    to: done
    when: _loop_exhausted
limits:
  max_loop_iterations: 3
`)
	_, err := loadFromBytes(t, yamlStr)
	if err == nil {
		t.Fatalf("expected load error for missing failed: on meta-decision edge, got nil")
	}
	if !strings.Contains(err.Error(), "failed:") || !strings.Contains(err.Error(), "_loop_exhausted") {
		t.Fatalf("expected error to mention 'failed:' and '_loop_exhausted'; got: %v", err)
	}
}

func TestLint_FailedAllowedOnMetaDecisionEdge(t *testing.T) {
	yamlStr := []byte(`
id: test_wf_ok
name: Test Workflow OK
version: 1
nodes:
  - id: looper
    kind: role
    runner: shell
    decisions:
      - id: again
    prompt: "echo hi"
  - id: done
    kind: emit
    decisions: [stop]
    output:
      decision: stop
      message: "done"
edges:
  - from: looper
    to: looper
    when: again
  - from: looper
    to: done
    when: _loop_exhausted
    failed: true
limits:
  max_loop_iterations: 3
`)
	_, err := loadFromBytes(t, yamlStr)
	if err != nil {
		t.Fatalf("expected clean load for valid failed: declaration, got: %v", err)
	}
}

func TestLint_FailedForbiddenOnNonMetaEdge(t *testing.T) {
	yamlStr := []byte(`
id: test_wf_bad
name: Test Workflow Bad
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [next]
    prompt: "echo"
  - id: b
    kind: emit
    decisions: [done]
    output:
      decision: done
      message: "done"
edges:
  - from: a
    to: b
    when: next
    failed: true
`)
	_, err := loadFromBytes(t, yamlStr)
	if err == nil {
		t.Fatalf("expected load error for failed: on non-meta-decision edge, got nil")
	}
	if !strings.Contains(err.Error(), "failed:") || !strings.Contains(err.Error(), "meta-decision") {
		t.Fatalf("expected error mentioning 'failed:' and 'meta-decision'; got: %v", err)
	}
}

func TestLint_FailedForbiddenOnEdgeWithDefaultWhen(t *testing.T) {
	// "when: default failed: true" must fail validation even though
	// default is an early-continue case in checkEdgeWhenValues.
	yamlStr := []byte(`
id: test_wf_default_failed
name: Test Workflow Default Failed
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [next]
    prompt: "echo"
  - id: b
    kind: emit
    decisions: [done]
    output:
      decision: done
      message: "done"
edges:
  - from: a
    to: b
    when: default
    failed: true
`)
	_, err := loadFromBytes(t, yamlStr)
	if err == nil {
		t.Fatalf("expected load error for failed: on default when edge, got nil")
	}
	if !strings.Contains(err.Error(), "failed:") || !strings.Contains(err.Error(), "meta-decision") {
		t.Fatalf("expected error mentioning 'failed:' and 'meta-decision'; got: %v", err)
	}
}

func TestLint_FailedForbiddenOnEdgeWithEmptyWhen(t *testing.T) {
	// An edge with no when: field at all must also reject failed:.
	yamlStr := []byte(`
id: test_wf_empty_failed
name: Test Workflow Empty Failed
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [next]
    prompt: "echo"
  - id: b
    kind: emit
    decisions: [done]
    output:
      decision: done
      message: "done"
edges:
  - from: a
    to: b
    failed: true
`)
	_, err := loadFromBytes(t, yamlStr)
	if err == nil {
		t.Fatalf("expected load error for failed: on empty-when edge, got nil")
	}
	if !strings.Contains(err.Error(), "failed:") || !strings.Contains(err.Error(), "meta-decision") {
		t.Fatalf("expected error mentioning 'failed:' and 'meta-decision'; got: %v", err)
	}
}

func TestLint_FailedForbiddenOnEdgeWithExpressionWhen(t *testing.T) {
	// An edge with an expression when: (e.g. "status == 'failed'") must also
	// reject failed: — expression edges are an early-continue case.
	yamlStr := []byte(`
id: test_wf_expr_failed
name: Test Workflow Expr Failed
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [next]
    prompt: "echo"
  - id: b
    kind: emit
    decisions: [done]
    output:
      decision: done
      message: "done"
edges:
  - from: a
    to: b
    when: "${node.a.decision == 'foo'}"
    failed: true
`)
	_, err := loadFromBytes(t, yamlStr)
	if err == nil {
		t.Fatalf("expected load error for failed: on expression-when edge, got nil")
	}
	if !strings.Contains(err.Error(), "failed:") || !strings.Contains(err.Error(), "meta-decision") {
		t.Fatalf("expected error mentioning 'failed:' and 'meta-decision'; got: %v", err)
	}
}

func TestLint_AtMostOneMetaDecisionEdgePerSource(t *testing.T) {
	yamlStr := `
id: test_wf_dup
name: Test Workflow Dup
version: 1
nodes:
  - id: looper
    kind: role
    runner: shell
    decisions: [again]
    prompt: "echo"
  - id: a
    kind: emit
    decisions: [stop]
    output:
      decision: stop
      message: "a"
  - id: b
    kind: emit
    decisions: [stop]
    output:
      decision: stop
      message: "b"
edges:
  - from: looper
    to: looper
    when: again
  - from: looper
    to: a
    when: _loop_exhausted
    failed: true
  - from: looper
    to: b
    when: _loop_exhausted
    failed: false
limits:
  max_loop_iterations: 3
`
	_, err := loadFromBytes(t, []byte(yamlStr))
	if err == nil {
		t.Fatalf("expected load error for two _loop_exhausted edges from same source, got nil")
	}
	if !strings.Contains(err.Error(), "_loop_exhausted") {
		t.Fatalf("expected error mentioning '_loop_exhausted'; got: %v", err)
	}
}
