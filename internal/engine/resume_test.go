package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestResumeReadyNodes_ReplaysLoopWhenSourceNewer(t *testing.T) {
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "architect"},
			{ID: "verify_code_meets_acceptance_criteria"},
		},
		Edges: []definitions.Edge{
			{From: "architect", To: "verify_code_meets_acceptance_criteria", When: "spec_ready"},
			{From: "verify_code_meets_acceptance_criteria", To: "architect", When: testDecisionApproved},
		},
	}

	runState := state.NewRunState(testRunID1, "workflow", nil)
	t1 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Minute)

	runState.WithNode("architect", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = "spec_ready"
		node.EndedAt = &t1
	})
	runState.WithNode("verify_code_meets_acceptance_criteria", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = testDecisionApproved
		node.EndedAt = &t2
	})

	runContext := &RunContext{
		Outputs: map[string]NodeOutput{
			"architect":                             {Decision: "spec_ready"},
			"verify_code_meets_acceptance_criteria": {Decision: testDecisionApproved},
		},
		Inputs: map[string]any{},
	}

	ready := resumeReadyNodes(workflow, runState, runContext)
	if len(ready) != 1 || ready[0].ID != "architect" {
		t.Fatalf("expected architect to be ready, got %+v", ready)
	}
}

func TestResumeReadyNodes_SkipsCompletedTargetWhenNewer(t *testing.T) {
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "source"},
			{ID: "target"},
		},
		Edges: []definitions.Edge{
			{From: "source", To: "target", When: "go"},
		},
	}

	runState := state.NewRunState("run-2", "workflow", nil)
	t1 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Minute)

	runState.WithNode("source", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = "go"
		node.EndedAt = &t1
	})
	runState.WithNode("target", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = testDecisionDone
		node.EndedAt = &t2
	})

	runContext := &RunContext{
		Outputs: map[string]NodeOutput{
			"source": {Decision: "go"},
			"target": {Decision: testDecisionDone},
		},
		Inputs: map[string]any{},
	}

	ready := resumeReadyNodes(workflow, runState, runContext)
	if len(ready) != 0 {
		t.Fatalf("expected no ready nodes, got %+v", ready)
	}
}

func TestResumeReadyNodes_SkipsCompletedTargetWhenSourceEndedAtMissing(t *testing.T) {
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "source"},
			{ID: "target"},
		},
		Edges: []definitions.Edge{
			{From: "source", To: "target", When: "go"},
		},
	}

	runState := state.NewRunState("run-3", "workflow", nil)
	t2 := time.Date(2026, 2, 4, 12, 1, 0, 0, time.UTC)

	runState.WithNode("source", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = "go"
		node.EndedAt = nil
	})
	runState.WithNode("target", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = testDecisionDone
		node.EndedAt = &t2
	})

	runContext := &RunContext{
		Outputs: map[string]NodeOutput{
			"source": {Decision: "go"},
			"target": {Decision: testDecisionDone},
		},
		Inputs: map[string]any{},
	}

	ready := resumeReadyNodes(workflow, runState, runContext)
	if len(ready) != 0 {
		t.Fatalf("expected no ready nodes, got %+v", ready)
	}
}

func TestResumeReadyNodes_HonorsLastRoutingDecision(t *testing.T) {
	// Workflow: node "x" has two outgoing edges:
	//   x → debugger     when: fix_failed       (the real decision)
	//   x → declare_stuck when: _loop_exhausted  (the meta-decision)
	workflow := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "x"},
			{ID: "debugger"},
			{ID: "declare_stuck"},
		},
		Edges: []definitions.Edge{
			{From: "x", To: "debugger", When: "fix_failed"},
			{From: "x", To: "declare_stuck", When: MetaDecisionLoopExhausted},
		},
	}

	runState := state.NewRunState("run-lrd", "workflow", nil)
	t1 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	runState.WithNode("x", func(node *state.NodeState) {
		node.Status = statusCompleted
		node.Decision = "fix_failed"
		node.LastRoutingDecision = MetaDecisionLoopExhausted
		node.EndedAt = &t1
	})

	// RunContext.Outputs mirrors what RunContextFromState would produce,
	// including LastRoutingDecision copied from NodeState.
	runContext := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {
				Decision:            "fix_failed",
				LastRoutingDecision: MetaDecisionLoopExhausted,
			},
		},
		Inputs: map[string]any{},
	}

	ready := resumeReadyNodes(workflow, runState, runContext)
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready node, got %d: %+v", len(ready), ready)
	}
	if ready[0].ID != "declare_stuck" {
		t.Errorf("expected declare_stuck to be ready (via LastRoutingDecision), got %q", ready[0].ID)
	}
}

func TestRunContextFromState_PreservesLoopIterations(t *testing.T) {
	rs := state.NewRunState("r1", "tw", map[string]any{})
	rs.Nodes["x"] = &state.NodeState{
		ID:                  "x",
		Status:              "completed",
		Decision:            "fix_failed",
		Message:             "boom",
		LoopIterations:      5,
		LastRoutingDecision: "_loop_exhausted",
	}
	wf := &definitions.Workflow{}
	ctx := RunContextFromState(rs, wf)
	if got := ctx.Outputs["x"].LoopIterations; got != 5 {
		t.Errorf("LoopIterations after resume = %d want 5", got)
	}
	if got := ctx.Outputs["x"].LastRoutingDecision; got != "_loop_exhausted" {
		t.Errorf("LastRoutingDecision after resume = %q want %q", got, "_loop_exhausted")
	}
}

func TestResumeRunReturnsErrorForCancelledRun(t *testing.T) {
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "runs")
	runDir := filepath.Join(runsDir, testRunID1)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("failed to create run dir: %v", err)
	}

	rs := state.NewRunState(testRunID1, "wf", nil)
	rs.Status = "cancelled"
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	eng := &Engine{RunsDir: runsDir}
	_, err := eng.ResumeRun(context.Background(), testRunID1)
	if err == nil {
		t.Fatal("expected error for cancelled run")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected error about cancellation, got: %v", err)
	}
}
