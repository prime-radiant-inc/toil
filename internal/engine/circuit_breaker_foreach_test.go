package engine

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestCircuitBreakerUsesStateNodeID(t *testing.T) {
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	rs := state.NewRunState(testRunID1, "test", map[string]any{})

	wf := &definitions.Workflow{}
	node := &definitions.Node{ID: "process"}

	eng := &Engine{}

	// First call with stateNodeID "process::0"
	output := NodeOutput{Decision: testDecisionDone, Message: "ok"}
	err := eng.enforceCircuitBreaker(testRunID1, wf, node, "hash-a", output, logger, rs, "process::0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify state was written to "process::0", not "process"
	status, exists := rs.NodeStatus("process::0")
	if !exists {
		t.Fatal("expected state entry for process::0")
	}
	_ = status

	// Verify the template node "process" was NOT touched
	_, templateExists := rs.NodeStatus("process")
	if templateExists {
		t.Error("circuit breaker wrote to template node ID 'process' instead of expanded 'process::0'")
	}

	// Verify a second call with "process::1" gets independent state
	err = eng.enforceCircuitBreaker(testRunID1, wf, node, "hash-b", output, logger, rs, "process::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, exists1 := rs.NodeStatus("process::1")
	if !exists1 {
		t.Fatal("expected state entry for process::1")
	}
}
