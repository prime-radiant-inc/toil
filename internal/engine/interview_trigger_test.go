package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

func TestCollectInterviewableNodes_FiltersToSessionIDOnly(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "agent-a", Kind: "role"},
			{ID: "shell-b", Kind: "role"},
			{ID: "system-c", Kind: "system"},
		},
	}

	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("agent-a", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "sess-aaa"
		n.Attempts = 1
	})
	runState.WithNode("shell-b", func(n *state.NodeState) {
		n.Status = statusCompleted
		// No SessionID — shell runner
		n.Attempts = 1
	})
	runState.WithNode("system-c", func(n *state.NodeState) {
		n.Status = statusCompleted
		// No SessionID — system node
	})

	nodes := collectInterviewableNodes(runState, workflow)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 interviewable node, got %d", len(nodes))
	}
	if nodes[0].NodeID != "agent-a" {
		t.Fatalf("expected node 'agent-a', got %q", nodes[0].NodeID)
	}
	if nodes[0].RoleID != "" {
		t.Fatalf("expected empty RoleID, got %q", nodes[0].RoleID)
	}
	if nodes[0].SessionID != "sess-aaa" {
		t.Fatalf("expected session 'sess-aaa', got %q", nodes[0].SessionID)
	}
}

func TestCollectInterviewableNodes_SortsPriority(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "succeeded", Kind: "role"},
			{ID: "failed", Kind: "role"},
			{ID: "retried", Kind: "role"},
		},
	}

	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("succeeded", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = testSessID1
		n.Attempts = 1
	})
	runState.WithNode("failed", func(n *state.NodeState) {
		n.Status = "failed"
		n.SessionID = "sess-2"
		n.Attempts = 1
	})
	runState.WithNode("retried", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "sess-3"
		n.Attempts = 3
	})

	nodes := collectInterviewableNodes(runState, workflow)

	if len(nodes) != 3 {
		t.Fatalf("expected 3 interviewable nodes, got %d", len(nodes))
	}

	// Priority: failed > retried (attempts > 1) > succeeded
	if nodes[0].NodeID != statusFailed {
		t.Fatalf("expected first node to be 'failed', got %q", nodes[0].NodeID)
	}
	if nodes[0].Outcome != statusFailed {
		t.Fatalf("expected outcome 'failed', got %q", nodes[0].Outcome)
	}

	if nodes[1].NodeID != "retried" {
		t.Fatalf("expected second node to be 'retried', got %q", nodes[1].NodeID)
	}
	if nodes[1].Outcome != "retried" {
		t.Fatalf("expected outcome 'retried', got %q", nodes[1].Outcome)
	}

	if nodes[2].NodeID != "succeeded" {
		t.Fatalf("expected third node to be 'succeeded', got %q", nodes[2].NodeID)
	}
	if nodes[2].Outcome != "succeeded" {
		t.Fatalf("expected outcome 'succeeded', got %q", nodes[2].Outcome)
	}
}

func TestCollectInterviewableNodes_EmptyWhenNoSessions(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
		},
	}

	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("a", func(n *state.NodeState) {
		n.Status = statusCompleted
	})
	runState.WithNode("b", func(n *state.NodeState) {
		n.Status = statusCompleted
	})

	nodes := collectInterviewableNodes(runState, workflow)

	if len(nodes) != 0 {
		t.Fatalf("expected 0 interviewable nodes, got %d", len(nodes))
	}
}

func TestCollectInterviewableNodes_SkipsNodesNotInState(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "ran", Kind: "role"},
			{ID: "never-ran", Kind: "role"},
		},
	}

	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("ran", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = testSessID1
		n.Attempts = 1
	})
	// "never-ran" is not in state at all

	nodes := collectInterviewableNodes(runState, workflow)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 interviewable node, got %d", len(nodes))
	}
	if nodes[0].NodeID != "ran" {
		t.Fatalf("expected node 'ran', got %q", nodes[0].NodeID)
	}
}

func TestCollectInterviewableNodes_IncludesPendingNodesWithSession(t *testing.T) {
	// A node that is still pending but somehow has a session ID
	// (unlikely but should still be included since it has a session)
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a", Kind: "role"},
		},
	}

	runState := state.NewRunState(testRunID1, "wf", nil)
	runState.WithNode("a", func(n *state.NodeState) {
		n.Status = testStatusPending
		n.SessionID = testSessID1
		n.Attempts = 0
	})

	nodes := collectInterviewableNodes(runState, workflow)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 interviewable node, got %d", len(nodes))
	}
}

func TestInterviewCandidatesEvent_NotEmittedOnCleanCompletion(t *testing.T) {
	// on_issue should NOT emit when run completes cleanly with no retries.
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-interview",
		Name:      "Interview Workflow",
		Version:   1,
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-abc"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-int", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-int")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-int", "events.jsonl"))
	event := findEvent(events, "interview_candidates")
	if event != nil {
		t.Fatal("expected no interview_candidates event on clean completion with on_issue mode")
	}
}

func TestInterviewCandidatesEvent_EmittedOnFailure(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-fail-int",
		Name:      "Fail Interview Workflow",
		Version:   1,
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "agent-ok", Kind: "role", Runner: "test-runner"},
			{ID: "agent-fail", Kind: "role", Runner: "test-runner"},
		},
		Edges: []definitions.Edge{
			{From: "agent-ok", To: "agent-fail"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-ok"},
		},
		errs: []error{nil, fmt.Errorf("simulated failure")},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-fail-int", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-fail-int")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-fail-int", "events.jsonl"))
	event := findEvent(events, "interview_candidates")
	if event == nil {
		t.Fatal("expected interview_candidates event on failure")
	}
	// The first node completed with a session, so it should appear
	nodesRaw := event.Data["nodes"]
	nodesList, ok := nodesRaw.([]any)
	if !ok {
		t.Fatalf("expected nodes to be a list, got %T", nodesRaw)
	}
	if len(nodesList) < 1 {
		t.Fatal("expected at least 1 interview candidate from the successful node")
	}
}

func TestInterviewCandidatesEvent_NotEmittedWhenDisabled(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-no-interview",
		Name:      "No Interview Workflow",
		Version:   1,
		Interview: definitions.InterviewNever,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-xyz"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-no-int", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-no-int")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-no-int", "events.jsonl"))
	event := findEvent(events, "interview_candidates")
	if event != nil {
		t.Fatal("expected no interview_candidates event when interview is disabled")
	}
}

func TestInterviewCandidatesEvent_NotEmittedWhenNoCandidates(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-system-only",
		Name:    "System Only Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	setupRunForResume(t, runsDir, "run-sys", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-sys")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-sys", "events.jsonl"))
	event := findEvent(events, "interview_candidates")
	if event != nil {
		t.Fatal("expected no interview_candidates event when no candidates exist")
	}
}

func TestInterviewCandidatesEvent_SkippedWhenFailureCausedBySubworkflow(t *testing.T) {
	// When a parent workflow fails because a subworkflow node failed,
	// the interview should NOT trigger on the parent — only on the child.
	runState := state.NewRunState("run-parent", "wf-parent", nil)
	runState.Status = statusFailed

	// The subworkflow node failed and has child_run data
	runState.WithNode("sub-node", func(n *state.NodeState) {
		n.Status = statusFailed
		n.SessionID = "" // subworkflow nodes don't have sessions
		n.Data = map[string]any{"child_run": "child-run-123"}
	})
	// A role node that completed before the subworkflow failed
	runState.WithNode("role-node", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "sess-role"
		n.Attempts = 1
	})

	workflow := &definitions.Workflow{
		ID:        "wf-parent",
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "role-node", Kind: "role"},
			{ID: "sub-node", Kind: "subworkflow", Workflow: "child-wf"},
		},
		Edges: []definitions.Edge{
			{From: "role-node", To: "sub-node"},
		},
	}

	if !failureCausedBySubworkflow(runState, workflow) {
		t.Fatal("expected failureCausedBySubworkflow to return true")
	}
}

func TestFailureCausedBySubworkflow_FalseForDirectFailure(t *testing.T) {
	// When a role node itself fails (not a subworkflow), interviews should trigger.
	runState := state.NewRunState("run-direct", "wf-direct", nil)
	runState.Status = statusFailed

	runState.WithNode("agent", func(n *state.NodeState) {
		n.Status = statusFailed
		n.SessionID = "sess-agent"
		n.Attempts = 1
	})

	workflow := &definitions.Workflow{
		ID:        "wf-direct",
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role"},
		},
	}

	if failureCausedBySubworkflow(runState, workflow) {
		t.Fatal("expected failureCausedBySubworkflow to return false for direct role failure")
	}
}

func TestFailureCausedBySubworkflow_FalseWhenMixed(t *testing.T) {
	// If both a subworkflow node and a role node failed, interviews should trigger
	// (the role node failure is a direct failure worth interviewing about).
	runState := state.NewRunState("run-mixed", "wf-mixed", nil)
	runState.Status = statusFailed

	runState.WithNode("sub-node", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Data = map[string]any{"child_run": "child-run-456"}
	})
	runState.WithNode("role-node", func(n *state.NodeState) {
		n.Status = statusFailed
		n.SessionID = "sess-role"
		n.Attempts = 1
	})

	workflow := &definitions.Workflow{
		ID:        "wf-mixed",
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "role-node", Kind: "role"},
			{ID: "sub-node", Kind: "subworkflow", Workflow: "child-wf"},
		},
	}

	if failureCausedBySubworkflow(runState, workflow) {
		t.Fatal("expected failureCausedBySubworkflow to return false when mixed failures exist")
	}
}

func TestFailureCausedBySubworkflow_FalseWhenNoFailedNodes(t *testing.T) {
	runState := state.NewRunState("run-ok", "wf-ok", nil)
	runState.Status = statusCompleted

	runState.WithNode("agent", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "sess-ok"
		n.Attempts = 1
	})

	workflow := &definitions.Workflow{
		ID: "wf-ok",
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role"},
		},
	}

	if failureCausedBySubworkflow(runState, workflow) {
		t.Fatal("expected failureCausedBySubworkflow to return false when no failures")
	}
}

func TestInterviewMode_DefaultsToNever(t *testing.T) {
	w := &definitions.Workflow{}
	if w.InterviewMode() != definitions.InterviewNever {
		t.Fatalf("expected InterviewMode()=%q, got %q", definitions.InterviewNever, w.InterviewMode())
	}
}

func TestInterviewMode_ExplicitNever(t *testing.T) {
	w := &definitions.Workflow{Interview: definitions.InterviewNever}
	if w.InterviewMode() != definitions.InterviewNever {
		t.Fatalf("expected InterviewMode()=%q, got %q", definitions.InterviewNever, w.InterviewMode())
	}
}

func TestInterviewMode_OnFailure(t *testing.T) {
	w := &definitions.Workflow{Interview: definitions.InterviewOnFailure}
	if w.InterviewMode() != definitions.InterviewOnFailure {
		t.Fatalf("expected InterviewMode()=%q, got %q", definitions.InterviewOnFailure, w.InterviewMode())
	}
}

func TestInterviewMode_OnIssue(t *testing.T) {
	w := &definitions.Workflow{Interview: definitions.InterviewOnIssue}
	if w.InterviewMode() != definitions.InterviewOnIssue {
		t.Fatalf("expected InterviewMode()=%q, got %q", definitions.InterviewOnIssue, w.InterviewMode())
	}
}

func TestOnInterviewCandidates_CalledOnFailure(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-trigger",
		Name:      "Trigger Workflow",
		Version:   1,
		Interview: definitions.InterviewOnIssue,
		Nodes: []definitions.Node{
			{ID: "agent-ok", Kind: "role", Runner: "test-runner"},
			{ID: "agent-fail", Kind: "role", Runner: "test-runner"},
		},
		Edges: []definitions.Edge{
			{From: "agent-ok", To: "agent-fail"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-trigger"},
		},
		errs: []error{nil, fmt.Errorf("simulated failure")},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-trigger", workflow, nil)

	var callbackRunID string
	var callbackRunDir string
	var callbackNodes []InterviewableNode
	done := make(chan struct{})

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnInterviewCandidates: func(runID string, runDir string, nodes []InterviewableNode) {
			callbackRunID = runID
			callbackRunDir = runDir
			callbackNodes = nodes
			close(done)
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-trigger")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	<-done

	if callbackRunID != "run-trigger" {
		t.Fatalf("expected callback runID 'run-trigger', got %q", callbackRunID)
	}
	if callbackRunDir != filepath.Join(runsDir, "run-trigger") {
		t.Fatalf("expected callback runDir %q, got %q", filepath.Join(runsDir, "run-trigger"), callbackRunDir)
	}
	if len(callbackNodes) < 1 {
		t.Fatal("expected at least 1 callback node")
	}
	if callbackNodes[0].SessionID != "sess-trigger" {
		t.Fatalf("expected session 'sess-trigger', got %q", callbackNodes[0].SessionID)
	}
}

func TestOnInterviewCandidates_NotCalledWhenDisabled(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-no-trigger",
		Name:      "No Trigger Workflow",
		Version:   1,
		Interview: definitions.InterviewNever,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-xyz"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-no-trigger", workflow, nil)

	called := make(chan struct{}, 1)
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnInterviewCandidates: func(runID string, runDir string, nodes []InterviewableNode) {
			called <- struct{}{}
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-no-trigger")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The callback should not have been invoked since the goroutine was never
	// spawned (interviews are disabled). A brief runtime.Gosched ensures any
	// previously-scheduled goroutine would have had a chance to run.
	runtime.Gosched()
	select {
	case <-called:
		t.Fatal("expected callback not to be called when interviews are disabled")
	default:
	}
}

func TestOnInterviewCandidates_NotCalledWhenNoCandidates(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-no-cand",
		Name:    "No Candidates Workflow",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "a", Kind: "system"},
		},
	}

	setupRunForResume(t, runsDir, "run-no-cand", workflow, nil)

	called := make(chan struct{}, 1)
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: runners.NewRegistry(),
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnInterviewCandidates: func(runID string, runDir string, nodes []InterviewableNode) {
			called <- struct{}{}
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-no-cand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	runtime.Gosched()
	select {
	case <-called:
		t.Fatal("expected callback not to be called when no candidates exist")
	default:
	}
}

func TestOnInterviewCandidates_CalledOnFailureMode(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-fail-trigger",
		Name:      "Fail Trigger Workflow",
		Version:   1,
		Interview: definitions.InterviewOnFailure,
		Nodes: []definitions.Node{
			{ID: "agent-ok", Kind: "role", Runner: "test-runner"},
			{ID: "agent-fail", Kind: "role", Runner: "test-runner"},
		},
		Edges: []definitions.Edge{
			{From: "agent-ok", To: "agent-fail"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-ok"},
		},
		errs: []error{nil, fmt.Errorf("simulated failure")},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-fail-trigger", workflow, nil)

	var callbackNodes []InterviewableNode
	done := make(chan struct{})
	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
		OnInterviewCandidates: func(runID string, runDir string, nodes []InterviewableNode) {
			callbackNodes = nodes
			close(done)
		},
	}

	_, err := eng.ResumeRun(context.Background(), "run-fail-trigger")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}

	<-done

	if len(callbackNodes) < 1 {
		t.Fatal("expected at least 1 callback node from the successful node")
	}
}

func TestCollectInterviewableNodes_IncludesTemplateExpansions(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{
				ID:      "my_orch",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "my_template"},
			},
			{
				ID:   "my_template",
				Kind: "role",
				Role: "worker",
			},
		},
	}
	runState := state.NewRunState("run-1", "wf", map[string]any{})

	// Simulate that two expanded items ran and have session IDs
	runState.WithNode("my_template::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "session-0"
		n.Attempts = 1
	})
	runState.WithNode("my_template::1", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.SessionID = "session-1"
		n.Attempts = 2
	})

	nodes := collectInterviewableNodes(runState, workflow)

	// Expect at least two interview candidates — one per expanded session.
	expandedCandidates := 0
	for _, n := range nodes {
		if n.NodeID == "my_template::0" || n.NodeID == "my_template::1" {
			expandedCandidates++
		}
	}
	if expandedCandidates != 2 {
		t.Fatalf("expected 2 expanded-item interview candidates, got %d out of %+v", expandedCandidates, nodes)
	}
}

func TestCollectInterviewableNodes_SubworkflowTemplateNotScannedForExpansions(t *testing.T) {
	// subworkflow templates DO produce interview candidates, but those come from the CHILD runs,
	// not from the expanded state entries. This test verifies we don't double-count.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.x", Item: "i", Body: "tmpl"}},
			{ID: "tmpl", Kind: "subworkflow", Workflow: "child_wf"},
		},
	}
	runState := state.NewRunState("run-1", "wf", map[string]any{})
	runState.WithNode("tmpl::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]any{"child_run": "child-0"}
	})
	nodes := collectInterviewableNodes(runState, workflow)
	// Expansions of a subworkflow template shouldn't be emitted as top-level interview candidates here
	// (the child run's own nodes are where interview candidates for subworkflow work come from)
	for _, n := range nodes {
		if n.NodeID == "tmpl::0" {
			t.Fatalf("subworkflow template expansion should not be an interview candidate directly")
		}
	}
}

func TestOnInterviewCandidates_NilCallbackSafe(t *testing.T) {
	// Verify that a nil OnInterviewCandidates callback does not panic
	// when interviews would be triggered (on_failure mode + failed run).
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:        "wf-nil-cb",
		Name:      "Nil Callback Workflow",
		Version:   1,
		Interview: definitions.InterviewOnFailure,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner"},
		},
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{}}`, SessionID: "sess-nil"},
		},
		errs: []error{fmt.Errorf("simulated failure")},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	setupRunForResume(t, runsDir, "run-nil-cb", workflow, nil)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
		},
		RunnerRegistry:        registry,
		RunsDir:               runsDir,
		EventStdout:           io.Discard,
		OnInterviewCandidates: nil, // explicitly nil
	}

	_, err := eng.ResumeRun(context.Background(), "run-nil-cb")
	if err == nil {
		t.Fatal("expected error from failing runner")
	}
	// No panic means success
}

func TestCollectInterviewableNodes_FailedHandledClassifiedAsFailure(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.x", Item: "i", Body: "tmpl"}},
			{ID: "tmpl", Kind: "role", Role: "worker"},
		},
	}
	runState := state.NewRunState("run-1", "wf", map[string]any{})
	runState.WithNode("tmpl::0", func(n *state.NodeState) {
		n.Status = statusFailedHandled
		n.SessionID = "s-0"
		n.Attempts = 1
	})
	nodes := collectInterviewableNodes(runState, workflow)
	found := false
	for _, n := range nodes {
		if n.NodeID == "tmpl::0" && n.Outcome == outcomeFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tmpl::0 with outcome=failed, got %+v", nodes)
	}
}
