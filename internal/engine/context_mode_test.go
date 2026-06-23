package engine

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestFreshContextClearsSessionID(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = "old-session-123"
		n.Attempts = 1
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "fresh"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID for fresh context, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for fresh context")
	}
	// Verify state was also cleared
	sn := rs.Node("n1")
	if sn.SessionID != "" {
		t.Fatalf("expected state SessionID cleared, got %q", sn.SessionID)
	}
}

func TestFullContextPreservesSessionID(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = testSessExisting
		n.Attempts = 1
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "full"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != testSessExisting {
		t.Fatalf("expected preserved sessionID, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true for full context with existing session")
	}
}

func TestDefaultContextPreservesSessionID(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = testSessExisting
		n.Attempts = 1
	})
	// Empty string context means default (full)
	node := &definitions.Node{ID: "n1", Kind: "role"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != testSessExisting {
		t.Fatalf("expected preserved sessionID for default context, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true for default context with existing session")
	}
}

func TestFreshContextWithNoExistingSession(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "fresh"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false")
	}
}

func TestContextValidation_RejectsInvalid(t *testing.T) {
	w := &definitions.Workflow{
		ID:      "wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "n1", Kind: "role", Context: "bogus"},
		},
		Edges: []definitions.Edge{},
	}
	result := definitions.ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected validation error for invalid context value")
	}
}

func TestContextValidation_AcceptsValid(t *testing.T) {
	for _, ctx := range []string{"", "full", "fresh", "compact", "summary"} {
		w := &definitions.Workflow{
			ID:      "wf",
			Name:    "Test",
			Version: 1,
			Nodes: []definitions.Node{
				{ID: "n1", Kind: "role", Context: ctx},
			},
			Edges: []definitions.Edge{},
		}
		result := definitions.ValidateGraph(w)
		if result.HasErrors() {
			t.Fatalf("context %q should be valid, got: %v", ctx, result)
		}
	}
}

// ============================================================
// Additional edge-case tests for context fidelity
// ============================================================

func TestResolveSession_FullContextWithEmptySessionID(t *testing.T) {
	// "full" context with no prior session should return empty sessionID and resume=false
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "full"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID for full context with no prior session, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false when no prior session exists")
	}
}

func TestResolveSession_FreshContextRepeatedCalls(t *testing.T) {
	// Fresh context should clear session on every call, even if state is modified between calls
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "fresh"}

	// First call
	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)
	if sessionID != "" || resume {
		t.Fatal("first call: expected empty session, no resume")
	}

	// Simulate a runner setting a session after execution
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = "new-session-abc"
	})

	// Second call with fresh should clear it again
	sessionID, resume = resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)
	if sessionID != "" {
		t.Fatalf("second call: expected empty sessionID after fresh clears, got %q", sessionID)
	}
	if resume {
		t.Fatal("second call: expected resume=false for fresh context")
	}

	// Verify state was cleared
	sn := rs.Node("n1")
	if sn.SessionID != "" {
		t.Fatalf("expected state SessionID cleared after second fresh resolve, got %q", sn.SessionID)
	}
}

func TestResolveSession_DefaultContextWithEmptyString(t *testing.T) {
	// Context="" is the default and should behave like "full"
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = "session-xyz"
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: ""}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "session-xyz" {
		t.Fatalf("expected preserved session for default context, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true for default context with existing session")
	}
}

func TestResolveSession_CompactContextClearsSession(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = testSessExisting
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "compact"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID for compact context, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for compact context")
	}
}

func TestResolveSession_SummaryContextClearsSession(t *testing.T) {
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = testSessExisting
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "summary"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID for summary context, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for summary context")
	}
}

func TestResolveSession_DifferentNodesIndependent(t *testing.T) {
	// Two different nodes should have independent sessions
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = "session-n1"
	})
	rs.WithNode("n2", func(n *state.NodeState) {
		n.SessionID = "session-n2"
	})

	// n1 with fresh should clear only n1
	node1 := &definitions.Node{ID: "n1", Kind: "role", Context: "fresh"}
	sessionID, _ := resolveSession(rs, node1, &definitions.Workflow{}, node1.ID, node1.SessionID)
	if sessionID != "" {
		t.Fatalf("n1 fresh should be empty, got %q", sessionID)
	}

	// n2 with full should still have its session
	node2 := &definitions.Node{ID: "n2", Kind: "role", Context: "full"}
	sessionID, resume := resolveSession(rs, node2, &definitions.Workflow{}, node2.ID, node2.SessionID)
	if sessionID != "session-n2" {
		t.Fatalf("n2 full should preserve session, got %q", sessionID)
	}
	if !resume {
		t.Fatal("n2 should resume=true")
	}
}

func TestContextValidation_RejectsMultipleInvalid(t *testing.T) {
	invalidValues := []string{"partial", "none", "FRESH", "FULL", "Fresh", "Full", "COMPACT", "SUMMARY"}
	for _, ctx := range invalidValues {
		w := &definitions.Workflow{
			ID:      "wf",
			Name:    "Test",
			Version: 1,
			Nodes: []definitions.Node{
				{ID: "n1", Kind: "role", Context: ctx},
			},
			Edges: []definitions.Edge{},
		}
		result := definitions.ValidateGraph(w)
		if !result.HasErrors() {
			t.Fatalf("context %q should be invalid but passed validation", ctx)
		}
	}
}

func TestResolveSession_HumanNodeSameAsRole(t *testing.T) {
	// Context fidelity should work the same for human nodes
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("h1", func(n *state.NodeState) {
		n.SessionID = "human-session"
	})

	// Fresh on human node
	node := &definitions.Node{ID: "h1", Kind: "human", Context: "fresh"}
	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)
	if sessionID != "" {
		t.Fatalf("expected empty sessionID for fresh human node, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for fresh human node")
	}
}

func TestResolveSession_NodeWithNoStateYet(t *testing.T) {
	// Node that has never been executed (no state entry)
	rs := state.NewRunState(testRunID1, "wf", nil)
	// Don't create any node state for "new_node"

	node := &definitions.Node{ID: "new_node", Kind: "role", Context: "full"}
	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	// Should return empty session and no resume since there's no prior state
	if sessionID != "" {
		t.Fatalf("expected empty sessionID for new node, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for new node with no state")
	}
}

// ============================================================
// Explicit SessionID tests (for interview/cross-run resume)
// ============================================================

func TestResolveSession_ExplicitSessionIDOverridesState(t *testing.T) {
	// When node has an explicit SessionID and context is "full", the explicit
	// session ID should be used instead of whatever is in state.
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("n1", func(n *state.NodeState) {
		n.SessionID = "state-session"
	})
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "full", SessionID: "explicit-session-from-other-run"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "explicit-session-from-other-run" {
		t.Fatalf("expected explicit session ID, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true when explicit session ID provided")
	}
}

func TestResolveSession_ExplicitSessionIDWithFreshContext(t *testing.T) {
	// Fresh context should still clear the session, even with explicit SessionID.
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "fresh", SessionID: "explicit-session"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "" {
		t.Fatalf("expected empty sessionID for fresh context even with explicit, got %q", sessionID)
	}
	if resume {
		t.Fatal("expected resume=false for fresh context")
	}
}

func TestResolveSession_ExplicitSessionIDWithDefaultContext(t *testing.T) {
	// Default context (empty string) with explicit SessionID should use the explicit one.
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", SessionID: "cross-run-session"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "cross-run-session" {
		t.Fatalf("expected explicit session ID with default context, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true")
	}
}

func TestFreshContext_SecondAttemptIncludesNodePrompt(t *testing.T) {
	// Regression: fresh context on second attempt (Attempts > 0) must still
	// include the node prompt. Previously, selectPrompts received firstRun
	// (attempt count) rather than !resume (session state), so fresh-context
	// nodes lost their prompt on re-dispatch.
	//
	// The fix: executeRole passes !resume to selectPrompts. resolveSession
	// returns resume=false for fresh context, so !resume=true → prompt included.
	node := &definitions.Node{
		ID:      "reviewer",
		Kind:    "role",
		Context: "fresh",
		Prompt:  "You are the plan reviewer. Review the plan.",
	}
	workflow := &definitions.Workflow{}

	// resolveSession returns resume=false for fresh context
	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("reviewer", func(n *state.NodeState) {
		n.SessionID = "old-session"
		n.Attempts = 1 // second attempt
	})
	_, resume := resolveSession(rs, node, workflow, node.ID, "")

	rolePrompt, nodePrompt := selectPrompts(!resume, "", node.Prompt, "", node.PromptOnResume)

	if nodePrompt != node.Prompt {
		t.Fatalf("fresh context second attempt: expected node prompt %q, got %q", node.Prompt, nodePrompt)
	}
	if rolePrompt != "" {
		t.Fatalf("expected empty role prompt, got %q", rolePrompt)
	}
}

func TestFullContext_SecondAttemptOmitsNodePrompt(t *testing.T) {
	// For full context, second attempt resumes the session which already
	// contains the role prompt. selectPrompts should NOT re-send it.
	node := &definitions.Node{
		ID:      "engineer",
		Kind:    "role",
		Context: "full",
		Prompt:  "You are the code engineer.",
	}
	workflow := &definitions.Workflow{}

	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.WithNode("engineer", func(n *state.NodeState) {
		n.SessionID = "existing-session"
		n.Attempts = 1
	})
	_, resume := resolveSession(rs, node, workflow, node.ID, "")

	// resume=true for full context → !resume=false → no prompt
	rolePrompt, nodePrompt := selectPrompts(!resume, "", node.Prompt, "", node.PromptOnResume)

	if nodePrompt != "" {
		t.Fatalf("full context resume: expected empty node prompt, got %q", nodePrompt)
	}
	if rolePrompt != "" {
		t.Fatalf("expected empty role prompt, got %q", rolePrompt)
	}
}

func TestResolveSession_ExplicitSessionIDWithNoStateSession(t *testing.T) {
	// Node has no prior state session but has explicit SessionID.
	rs := state.NewRunState(testRunID1, "wf", nil)
	node := &definitions.Node{ID: "n1", Kind: "role", Context: "full", SessionID: "external-session"}

	sessionID, resume := resolveSession(rs, node, &definitions.Workflow{}, node.ID, node.SessionID)

	if sessionID != "external-session" {
		t.Fatalf("expected explicit session ID, got %q", sessionID)
	}
	if !resume {
		t.Fatal("expected resume=true for explicit session ID")
	}
}
