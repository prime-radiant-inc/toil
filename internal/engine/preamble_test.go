package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// ============================================================
// resolveContextMode tests
// ============================================================

func TestResolveContextMode_NodeContextTakesPrecedence(t *testing.T) {
	node := &definitions.Node{ID: "n1", Context: "fresh"}
	workflow := &definitions.Workflow{ContextDefault: "compact"}
	mode := resolveContextMode(node, workflow)
	if mode != "fresh" {
		t.Fatalf("expected node context 'fresh' to take precedence, got %q", mode)
	}
}

func TestResolveContextMode_FallsBackToWorkflowDefault(t *testing.T) {
	node := &definitions.Node{ID: "n1"}
	workflow := &definitions.Workflow{ContextDefault: "compact"}
	mode := resolveContextMode(node, workflow)
	if mode != "compact" {
		t.Fatalf("expected workflow context_default 'compact', got %q", mode)
	}
}

func TestResolveContextMode_DefaultsToFull(t *testing.T) {
	node := &definitions.Node{ID: "n1"}
	workflow := &definitions.Workflow{}
	mode := resolveContextMode(node, workflow)
	if mode != "full" {
		t.Fatalf("expected default 'full' when both are empty, got %q", mode)
	}
}

func TestResolveContextMode_NodeCompactOverridesWorkflowFull(t *testing.T) {
	node := &definitions.Node{ID: "n1", Context: "compact"}
	workflow := &definitions.Workflow{ContextDefault: "full"}
	mode := resolveContextMode(node, workflow)
	if mode != "compact" {
		t.Fatalf("expected 'compact', got %q", mode)
	}
}

func TestResolveContextMode_NodeSummaryOverridesWorkflowFresh(t *testing.T) {
	node := &definitions.Node{ID: "n1", Context: "summary"}
	workflow := &definitions.Workflow{ContextDefault: "fresh"}
	mode := resolveContextMode(node, workflow)
	if mode != "summary" {
		t.Fatalf("expected 'summary', got %q", mode)
	}
}

// ============================================================
// buildContextPreamble tests
// ============================================================

func TestBuildContextPreamble_CompactMode(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
			{ID: "n2", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", map[string]any{"repo": "github.com/example"})
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionApproved
		n.Message = "The code looks good and passes all tests."
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionDone
		n.Message = "All changes have been committed."
	})

	preamble := buildContextPreamble("compact", workflow, rs)

	if !strings.Contains(preamble, "Test Workflow") {
		t.Fatal("preamble should contain workflow name")
	}
	if !strings.Contains(preamble, "repo") {
		t.Fatal("preamble should contain input keys")
	}
	if !strings.Contains(preamble, "n1") {
		t.Fatal("preamble should contain node n1")
	}
	if !strings.Contains(preamble, testDecisionApproved) {
		t.Fatal("preamble should contain n1's decision")
	}
	if !strings.Contains(preamble, "n2") {
		t.Fatal("preamble should contain node n2")
	}
	if !strings.Contains(preamble, testDecisionDone) {
		t.Fatal("preamble should contain n2's decision")
	}
}

func TestBuildContextPreamble_SummaryMode_LastNodeFullMessage(t *testing.T) {
	longMessage := strings.Repeat("x", 500)
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
			{ID: "n2", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = longMessage
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionDone
		n.Message = longMessage
		n.Data = map[string]any{"key1": "val1", "key2": "val2"}
	})

	preamble := buildContextPreamble("summary", workflow, rs)

	// n1 (non-last) should be truncated: its 500-char message should appear only
	// as truncated (200 chars + "..."). n2 (last) should have the full message.
	truncatedN1 := longMessage[:200] + "..."
	if !strings.Contains(preamble, truncatedN1) {
		t.Fatal("summary mode should truncate non-last node messages")
	}

	// n2 is the last completed node; its full message should be present
	if !strings.Contains(preamble, longMessage) {
		t.Fatal("summary mode should include last node's full message")
	}

	// n2's data keys should be listed
	if !strings.Contains(preamble, "key1") {
		t.Fatal("summary mode should list data keys for last node")
	}
	if !strings.Contains(preamble, "key2") {
		t.Fatal("summary mode should list data keys for last node")
	}
}

func TestBuildContextPreamble_NoCompletedNodes(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", map[string]any{"input1": "value1"})
	// n1 is pending, not completed

	preamble := buildContextPreamble("compact", workflow, rs)

	if !strings.Contains(preamble, "Test Workflow") {
		t.Fatal("preamble should contain workflow name even with no completed nodes")
	}
	if !strings.Contains(preamble, "input1") {
		t.Fatal("preamble should contain inputs even with no completed nodes")
	}
	// Should NOT have a "Completed Nodes" section with entries
	if strings.Contains(preamble, "decision=") {
		t.Fatal("preamble should not contain completed node entries when none are completed")
	}
}

func TestBuildContextPreamble_TruncatesLongMessages(t *testing.T) {
	longMessage := strings.Repeat("a", 500)
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = longMessage
	})

	preamble := buildContextPreamble("compact", workflow, rs)

	// In compact mode, messages should be truncated to 200 chars
	if strings.Contains(preamble, longMessage) {
		t.Fatal("compact mode should truncate long messages, but found full 500-char message")
	}
	// Should contain the truncated version (200 chars + "...")
	truncated := longMessage[:200] + "..."
	if !strings.Contains(preamble, truncated) {
		t.Fatalf("compact mode should contain truncated message with '...', preamble:\n%s", preamble)
	}
}

func TestBuildContextPreamble_TruncatesLongInputValues(t *testing.T) {
	longValue := strings.Repeat("b", 500)
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", map[string]any{"big_input": longValue})

	preamble := buildContextPreamble("compact", workflow, rs)

	if strings.Contains(preamble, longValue) {
		t.Fatal("preamble should truncate long input values")
	}
	truncated := longValue[:200] + "..."
	if !strings.Contains(preamble, truncated) {
		t.Fatalf("preamble should contain truncated input value, preamble:\n%s", preamble)
	}
}

func TestBuildContextPreamble_SummaryDataKeysForLastNode(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
			{ID: "n2", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = "first done"
		n.Data = map[string]any{"should_not_appear": true}
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionDone
		n.Message = "second done"
		n.Data = map[string]any{"result_file": "output.txt", "line_count": 42}
	})

	preamble := buildContextPreamble("summary", workflow, rs)

	// n1's data keys should NOT be listed (only last node's)
	if strings.Contains(preamble, "should_not_appear") {
		t.Fatal("summary mode should only list data keys for the last completed node")
	}
	// n2's data keys should be listed
	if !strings.Contains(preamble, "result_file") {
		t.Fatal("expected data key 'result_file' for last node")
	}
	if !strings.Contains(preamble, "line_count") {
		t.Fatal("expected data key 'line_count' for last node")
	}
}

func TestBuildContextPreamble_SkipsNonCompletedNodes(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
			{ID: "n2", Kind: "role"},
			{ID: "n3", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = "first done"
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.Status = "running" // Not completed
		n.Decision = ""
		n.Message = ""
	})
	rs.WithNode("n3", func(n *state.NodeState) {
		n.Status = testStatusPending // Not completed
	})

	preamble := buildContextPreamble("compact", workflow, rs)

	if !strings.Contains(preamble, "n1") {
		t.Fatal("preamble should include completed node n1")
	}
	// n2 and n3 should not appear as completed nodes
	lines := strings.Split(preamble, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- n2:") {
			t.Fatal("preamble should not include non-completed node n2")
		}
		if strings.HasPrefix(strings.TrimSpace(line), "- n3:") {
			t.Fatal("preamble should not include non-completed node n3")
		}
	}
}

func TestBuildContextPreamble_CompactShortMessageNotTruncated(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = "short message"
	})

	preamble := buildContextPreamble("compact", workflow, rs)

	if !strings.Contains(preamble, "short message") {
		t.Fatal("short messages should not be truncated")
	}
	if strings.Contains(preamble, "...") {
		t.Fatal("short messages should not have ellipsis")
	}
}

func TestBuildContextPreamble_SummaryModeNonLastNodeTruncated(t *testing.T) {
	longMessage := strings.Repeat("z", 500)
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
			{ID: "n2", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "ok"
		n.Message = longMessage
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionDone
		n.Message = "last node message"
	})

	preamble := buildContextPreamble("summary", workflow, rs)

	// n1's long message should be truncated
	truncated := longMessage[:200] + "..."
	if !strings.Contains(preamble, truncated) {
		t.Fatal("summary mode should truncate non-last node messages")
	}
	// n2's short message should be in full
	if !strings.Contains(preamble, "last node message") {
		t.Fatal("summary mode should include last node's full message")
	}
}

func TestBuildContextPreamble_NoInputs(t *testing.T) {
	workflow := &definitions.Workflow{
		Name: "Test Workflow",
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role"},
		},
	}
	rs := state.NewRunState(testRunID1, "wf", nil) // no inputs

	preamble := buildContextPreamble("compact", workflow, rs)

	if !strings.Contains(preamble, "Test Workflow") {
		t.Fatal("preamble should contain workflow name")
	}
	// Should not crash or have empty "Inputs:" section with entries
}

// ============================================================
// Integration: preamble injection through executeRole
// ============================================================

// capturingRunner records the prompts it receives and returns a valid decision.
type capturingRunner struct {
	prompts []string
}

func (r *capturingRunner) Run(_ context.Context, req runners.Request, _ runners.LineHandler) (runners.Result, error) {
	r.prompts = append(r.prompts, req.Prompt)
	decision := testDecisionDone
	if len(req.Decisions) > 0 {
		decision = req.Decisions[0]
	}
	return runners.Result{
		Output: fmt.Sprintf(`{"decision":"%s","message":"test output","data":{},"artifacts":[]}`, decision),
	}, nil
}

func TestPreambleInjection_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:   "preamble-wf",
		Name: "Preamble Test Workflow",
		Nodes: []definitions.Node{
			{ID: "node_a", Kind: "system"},
			{ID: "node_b", Kind: "role", Runner: "test-runner", Context: "compact", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
		Edges: []definitions.Edge{
			{From: "node_a", To: "node_b"},
		},
	}

	capture := &capturingRunner{}
	registry := runners.NewRegistry()
	if err := registry.Register("test-runner", capture); err != nil {
		t.Fatalf("failed to register runner: %v", err)
	}

	eng := &Engine{
		Definitions:    &definitions.Bundle{},
		RunnerRegistry: registry,
	}

	runState := state.NewRunState(testRunID1, "preamble-wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// The capturing runner should have been called exactly once (for node_b).
	if len(capture.prompts) != 1 {
		t.Fatalf("expected 1 captured prompt, got %d", len(capture.prompts))
	}

	prompt := capture.prompts[0]

	// The prompt should start with the preamble header.
	if !strings.HasPrefix(prompt, "## Prior Context") {
		t.Fatalf("expected prompt to start with '## Prior Context', got:\n%s", prompt)
	}

	// The preamble should contain the workflow name.
	if !strings.Contains(prompt, "Preamble Test Workflow") {
		t.Fatalf("expected prompt to contain workflow name, got:\n%s", prompt)
	}

	// The preamble should contain node_a's ID and its decision from executeSystem.
	if !strings.Contains(prompt, "node_a") {
		t.Fatalf("expected prompt to contain node_a, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, testDecisionDefault) {
		t.Fatalf("expected prompt to contain node_a's decision 'default', got:\n%s", prompt)
	}
}
