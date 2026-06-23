package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// findEventWithNodeID returns the first event matching type and nodeID.
func findEventWithNodeID(events []state.Event, eventType, nodeID string) *state.Event {
	for i := range events {
		if events[i].Type == eventType && events[i].NodeID == nodeID {
			return &events[i]
		}
	}
	return nil
}

func TestComputeUnresolvedFailure_DirectFailedTrue(t *testing.T) {
	failedTrue := true
	wf := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "looper", Kind: "role"},
			{ID: "done", Kind: "emit"},
		},
		Edges: []definitions.Edge{
			{From: "looper", To: "done", When: "_loop_exhausted", Failed: &failedTrue},
		},
	}
	rs := &state.RunState{
		ID: "test",
		Nodes: map[string]*state.NodeState{
			"looper": {ID: "looper", Status: "completed", LastRoutingDecision: "_loop_exhausted"},
			"done":   {ID: "done", Status: "completed"},
		},
	}
	result := ComputeUnresolvedFailure(rs, wf, t.TempDir())
	if !result {
		t.Fatalf("expected true; got false")
	}
	if !rs.HasUnresolvedFailure {
		t.Fatalf("expected HasUnresolvedFailure=true on runState; got false")
	}
}

func TestComputeUnresolvedFailure_DirectFailedFalse(t *testing.T) {
	failedFalse := false
	wf := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "looper", Kind: "role"},
			{ID: "done", Kind: "emit"},
		},
		Edges: []definitions.Edge{
			{From: "looper", To: "done", When: "_loop_exhausted", Failed: &failedFalse},
		},
	}
	rs := &state.RunState{
		ID: "test",
		Nodes: map[string]*state.NodeState{
			"looper": {ID: "looper", Status: "completed", LastRoutingDecision: "_loop_exhausted"},
		},
	}
	if ComputeUnresolvedFailure(rs, wf, t.TempDir()) {
		t.Fatalf("expected false; got true")
	}
}

func TestComputeUnresolvedFailure_NoMetaDecisionFired(t *testing.T) {
	failedTrue := true
	wf := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "looper", Kind: "role"},
		},
		Edges: []definitions.Edge{
			{From: "looper", To: "done", When: "_loop_exhausted", Failed: &failedTrue},
		},
	}
	rs := &state.RunState{
		ID: "test",
		Nodes: map[string]*state.NodeState{
			"looper": {ID: "looper", Status: "completed", LastRoutingDecision: "done"},
		},
	}
	if ComputeUnresolvedFailure(rs, wf, t.TempDir()) {
		t.Fatalf("expected false (no meta-decision fired); got true")
	}
}

func TestComputeUnresolvedFailure_TransitiveViaChild(t *testing.T) {
	wf := &definitions.Workflow{
		ID: "parent",
		Nodes: []definitions.Node{
			{ID: "subw", Kind: "subworkflow"},
		},
	}
	// Set up child run dir under the same parent runs/ directory.
	// runDir = runsDir/parent-run; childDir = runsDir/child-run-id.
	runsDir := t.TempDir()
	parentRunDir := filepath.Join(runsDir, "parent-run")
	childID := "child-run-id"
	childDir := filepath.Join(runsDir, childID)
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child dir: %v", err)
	}
	childState := &state.RunState{
		ID:                   childID,
		Status:               "completed",
		HasUnresolvedFailure: true,
		Inputs:               map[string]any{},
		Nodes:                map[string]*state.NodeState{},
	}
	if err := state.SaveState(filepath.Join(childDir, "state.json"), childState); err != nil {
		t.Fatalf("save child state: %v", err)
	}

	rs := &state.RunState{
		ID: "parent-run",
		Nodes: map[string]*state.NodeState{
			"subw": {
				ID:     "subw",
				Status: "completed",
				Data:   map[string]any{"child_run": childID},
			},
		},
	}
	if !ComputeUnresolvedFailure(rs, wf, parentRunDir) {
		t.Fatalf("expected true (child has flag); got false")
	}
}

func TestComputeUnresolvedFailure_ResetClearsFlag(t *testing.T) {
	// Even if RunState.HasUnresolvedFailure was true from a prior computation,
	// the helper should recompute and clear it if no indicator is present.
	wf := &definitions.Workflow{
		ID:    "wf",
		Nodes: []definitions.Node{{ID: "looper", Kind: "role"}},
		Edges: []definitions.Edge{},
	}
	rs := &state.RunState{
		ID:                   "test",
		Nodes:                map[string]*state.NodeState{},
		HasUnresolvedFailure: true, // stale value from prior run
	}
	if ComputeUnresolvedFailure(rs, wf, t.TempDir()) {
		t.Fatalf("expected false (no nodes); got true")
	}
	if rs.HasUnresolvedFailure {
		t.Fatalf("stale flag not cleared")
	}
}

// TestResumeRun_EndToEnd_FailedTrueReturnsSentinel runs a minimal workflow
// end-to-end and verifies that a failed:true edge causes ResumeRun to return
// ErrUnresolvedFailure, the persisted Status remains "completed", and
// HasUnresolvedFailure is set on the saved state.
//
// Workflow:
//
//	looper (system) → self-loop (limit=1 to force exhaustion)
//	looper → done   when: _loop_exhausted, failed: true
//	done   (system) → (terminal)
func TestResumeRun_EndToEnd_FailedTrueReturnsSentinel(t *testing.T) {
	runsDir := t.TempDir()
	const runID = "run-e2e-failed-true"
	const wfID = "wf-failed-true"

	failedTrue := true
	workflow := &definitions.Workflow{
		ID:      wfID,
		Name:    "E2E FailedTrue",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "looper", Kind: "system"},
			{ID: "done", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "looper", To: "looper"},
			{From: "looper", To: "done", When: MetaDecisionLoopExhausted, Failed: &failedTrue},
		},
		Limits: map[string]int{"max_loop_iterations": 1},
	}

	setupRunForResume(t, runsDir, runID, workflow, map[string]any{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{wfID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), runID)
	if !errors.Is(err, ErrUnresolvedFailure) {
		t.Fatalf("want ErrUnresolvedFailure; got %v", err)
	}

	// Load persisted state and verify flag + status.
	rs, loadErr := state.LoadState(filepath.Join(runsDir, runID, "state.json"))
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if !rs.HasUnresolvedFailure {
		t.Fatalf("HasUnresolvedFailure not set on persisted state")
	}
	if rs.Status != statusCompleted {
		t.Fatalf("Status should be %q; got %q", statusCompleted, rs.Status)
	}
}

// TestResumeRun_FastPath_PropagatesFailureFlag calls ResumeRun a second time
// on a run that already has Status=completed and HasUnresolvedFailure=true,
// exercising the fast-path return at the top of ResumeRun that avoids
// re-executing the run loop but still propagates the sentinel.
func TestResumeRun_FastPath_PropagatesFailureFlag(t *testing.T) {
	runsDir := t.TempDir()
	const runID = "run-fast-path-flag"

	// Write a run state directly: status=completed, flag=true.
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rs := state.NewRunState(runID, "wf-stub", map[string]any{})
	rs.Status = statusCompleted
	rs.HasUnresolvedFailure = true
	// Add a terminal node so lastOutputFromState has something to return.
	rs.Nodes["done"] = &state.NodeState{
		ID:       "done",
		Status:   statusCompleted,
		Decision: "stop",
	}
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), runID)
	if !errors.Is(err, ErrUnresolvedFailure) {
		t.Fatalf("fast path: want ErrUnresolvedFailure on re-resume; got %v", err)
	}
}

// TestFailureEdgeFired_EventEmittedOnTrueEdge verifies that a "failure_edge_fired"
// event is written to events.jsonl when a failed:true edge is selected during
// routing. Uses the same workflow as TestResumeRun_EndToEnd_FailedTrueReturnsSentinel.
func TestFailureEdgeFired_EventEmittedOnTrueEdge(t *testing.T) {
	runsDir := t.TempDir()
	const runID = "run-failure-edge-fired"
	const wfID = "wf-failure-edge-fired"

	failedTrue := true
	workflow := &definitions.Workflow{
		ID:      wfID,
		Name:    "FailureEdgeFired",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "looper", Kind: "system"},
			{ID: "done", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "looper", To: "looper"},
			{From: "looper", To: "done", When: MetaDecisionLoopExhausted, Failed: &failedTrue},
		},
		Limits: map[string]int{"max_loop_iterations": 1},
	}

	setupRunForResume(t, runsDir, runID, workflow, map[string]any{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{wfID: workflow},
		},
		RunsDir:     runsDir,
		EventStdout: io.Discard,
	}

	_, _ = eng.ResumeRun(context.Background(), runID)

	events := parseEvents(t, filepath.Join(runsDir, runID, "events.jsonl"))
	ev := findEventWithNodeID(events, "failure_edge_fired", "looper")
	if ev == nil {
		t.Fatalf("no failure_edge_fired event with node_id=looper found in events.jsonl")
	}
	if ev.Data["to"] != "done" {
		t.Errorf("failure_edge_fired data.to=%v; want done", ev.Data["to"])
	}
	if ev.Data["when"] != MetaDecisionLoopExhausted {
		t.Errorf("failure_edge_fired data.when=%v; want %s", ev.Data["when"], MetaDecisionLoopExhausted)
	}
}
