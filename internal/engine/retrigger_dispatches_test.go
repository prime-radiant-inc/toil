package engine

import (
	"testing"

	"primeradiant.com/toil/internal/state"
)

// TestResetNodeState_PreservesDispatches is a regression guard. The
// per-dispatch inputs directory design relies on NodeState.Dispatches
// being monotonic across retriggers (so post-retrigger dispatch dirs
// don't collide with pre-retrigger ones at the same number). A
// well-meaning future contributor might add `node.Dispatches = 0` to
// resetNodeState by analogy with `node.Attempts = 0` — this test
// catches that.
func TestResetNodeState_PreservesDispatches(t *testing.T) {
	node := &state.NodeState{
		Status:     "failed",
		Attempts:   3,
		Dispatches: 5,
		SessionID:  "sess-x",
		RetryCount: 2,
	}

	resetNodeState(node)

	// Attempts and SessionID should be reset.
	if node.Attempts != 0 {
		t.Errorf("expected Attempts=0 after reset, got %d", node.Attempts)
	}
	if node.SessionID != "" {
		t.Errorf("expected SessionID=\"\" after reset, got %q", node.SessionID)
	}
	// Dispatches MUST NOT be reset.
	if node.Dispatches != 5 {
		t.Fatalf("expected Dispatches=5 (PRESERVED across retrigger), got %d", node.Dispatches)
	}
}
