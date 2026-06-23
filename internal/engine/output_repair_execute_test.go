package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

type sequentialRunner struct {
	results  []runners.Result
	errs     []error
	requests []runners.Request
}

func (runner *sequentialRunner) Run(_ context.Context, req runners.Request, handler runners.LineHandler) (runners.Result, error) {
	index := len(runner.requests)
	runner.requests = append(runner.requests, req)

	var result runners.Result
	if index < len(runner.results) {
		result = runner.results[index]
	}

	var runErr error
	if index < len(runner.errs) {
		runErr = runner.errs[index]
	}

	if handler != nil && result.Output != "" {
		handler(runners.Line{Stream: "stdout", Text: result.Output})
	}

	return result, runErr
}

func TestRoleNodeRepairsWhenStructuredOutputMissing(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-role-repair",
		Name:    "Role Repair",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
	}
	setupRunForResume(t, runsDir, "run-role-repair", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: "", SessionID: testSessID1},
			{Output: `{"decision":"done","message":"completed","data":{},"artifacts":[]}`, SessionID: testSessID1},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), "run-role-repair")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDone {
		t.Fatalf("expected repaired decision 'done', got %q", output.Decision)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner calls (initial + repair), got %d", len(runner.requests))
	}
	// Empty output → soft incomplete-work prompt (NOT the harsh "no tool calls" prompt).
	if !strings.Contains(runner.requests[1].Prompt, "Your previous turn ended without producing") {
		t.Fatalf("expected soft incomplete-work repair prompt for empty output, got: %q", runner.requests[1].Prompt)
	}
	if strings.Contains(runner.requests[1].Prompt, "Do NOT call any tools") {
		t.Fatalf("soft repair prompt should NOT forbid tool calls, got: %q", runner.requests[1].Prompt)
	}
	if runner.requests[1].SessionID != testSessID1 {
		t.Fatalf("expected repair call to reuse session, got %q", runner.requests[1].SessionID)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-role-repair", "events.jsonl"))
	if findEvent(events, "node_failed") != nil {
		t.Fatal("did not expect node_failed event")
	}
}

func TestRoleNodeRepairFallsBackToFreshSessionOnResumeFailure(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-role-repair-fresh-fallback",
		Name:    "Role Repair Fresh Fallback",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
	}
	setupRunForResume(t, runsDir, "run-role-repair-fresh-fallback", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: "", SessionID: testSessID1},
			{},
			{Output: `{"decision":"done","message":"recovered in fresh session","data":{},"artifacts":[]}`, SessionID: "sess-2"},
		},
		errs: []error{
			nil,
			fmt.Errorf("anthropic invalid_request_error: tool_use ids without tool_result"),
			nil,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), "run-role-repair-fresh-fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDone {
		t.Fatalf("expected repaired decision 'done', got %q", output.Decision)
	}
	if len(runner.requests) != 3 {
		t.Fatalf("expected three runner calls (initial + resume repair + fresh repair), got %d", len(runner.requests))
	}
	if !runner.requests[1].Resume || runner.requests[1].SessionID != testSessID1 {
		t.Fatalf("expected second call to resume existing session, got resume=%v session=%q", runner.requests[1].Resume, runner.requests[1].SessionID)
	}
	if runner.requests[2].Resume || runner.requests[2].SessionID != "" {
		t.Fatalf("expected third call to use fresh session, got resume=%v session=%q", runner.requests[2].Resume, runner.requests[2].SessionID)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-role-repair-fresh-fallback", "events.jsonl"))
	if findEvent(events, "node_failed") != nil {
		t.Fatal("did not expect node_failed event")
	}
}

func TestHumanNodeRepairsWhenStructuredOutputMissing(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-human-repair",
		Name:    "Human Repair",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "human", Runner: "test-runner", Prompt: "summarize"},
		},
	}
	setupRunForResume(t, runsDir, "run-human-repair", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: ""},
			{Output: `{"decision":"default","message":"completed","data":{},"artifacts":[]}`},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), "run-human-repair")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDefault {
		t.Fatalf("expected repaired decision 'default', got %q", output.Decision)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner calls (initial + repair), got %d", len(runner.requests))
	}
	// Empty output → soft incomplete-work prompt (NOT the harsh "no tool calls" prompt).
	if !strings.Contains(runner.requests[1].Prompt, "Your previous turn ended without producing") {
		t.Fatalf("expected soft incomplete-work repair prompt for empty output, got: %q", runner.requests[1].Prompt)
	}
	if strings.Contains(runner.requests[1].Prompt, "Do NOT call any tools") {
		t.Fatalf("soft repair prompt should NOT forbid tool calls, got: %q", runner.requests[1].Prompt)
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-human-repair", "events.jsonl"))
	if findEvent(events, "node_failed") != nil {
		t.Fatal("did not expect node_failed event")
	}
}

func TestRoleNodeRetriesFreshSessionWhenResumeRejected(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-role-resume-fallback",
		Name:    "Role Resume Fallback",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
	}
	runID := "run-role-resume-fallback"
	setupRunForResume(t, runsDir, runID, workflow, nil)

	statePath := filepath.Join(runsDir, runID, "state.json")
	runState, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	runState.WithNode("agent", func(nodeState *state.NodeState) {
		nodeState.SessionID = testSessIDStale
	})
	if err := state.SaveState(statePath, runState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{},
			{Output: `{"decision":"done","message":"recovered","data":{},"artifacts":[]}`, SessionID: "sess-new"},
		},
		errs: []error{
			fmt.Errorf("anthropic invalid_request_error: tool_use ids without tool_result"),
			nil,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDone {
		t.Fatalf("expected decision 'done', got %q", output.Decision)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner calls (resume + fresh fallback), got %d", len(runner.requests))
	}
	if !runner.requests[0].Resume || runner.requests[0].SessionID != testSessIDStale {
		t.Fatalf("expected first call to resume stale session, got resume=%v session=%q", runner.requests[0].Resume, runner.requests[0].SessionID)
	}
	if runner.requests[1].Resume || runner.requests[1].SessionID != "" {
		t.Fatalf("expected second call to use fresh session, got resume=%v session=%q", runner.requests[1].Resume, runner.requests[1].SessionID)
	}
}

func TestHumanNodeRetriesFreshSessionWhenResumeRejected(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-human-resume-fallback",
		Name:    "Human Resume Fallback",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "human", Runner: "test-runner", Prompt: "summarize"},
		},
	}
	runID := "run-human-resume-fallback"
	setupRunForResume(t, runsDir, runID, workflow, nil)

	statePath := filepath.Join(runsDir, runID, "state.json")
	runState, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	runState.WithNode("agent", func(nodeState *state.NodeState) {
		nodeState.SessionID = testSessIDStale
	})
	if err := state.SaveState(statePath, runState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{},
			{Output: `{"decision":"default","message":"recovered","data":{},"artifacts":[]}`, SessionID: "sess-new"},
		},
		errs: []error{
			fmt.Errorf("anthropic invalid_request_error: tool_use ids without tool_result"),
			nil,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := engine.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDefault {
		t.Fatalf("expected decision 'default', got %q", output.Decision)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner calls (resume + fresh fallback), got %d", len(runner.requests))
	}
	if !runner.requests[0].Resume || runner.requests[0].SessionID != testSessIDStale {
		t.Fatalf("expected first call to resume stale session, got resume=%v session=%q", runner.requests[0].Resume, runner.requests[0].SessionID)
	}
	if runner.requests[1].Resume || runner.requests[1].SessionID != "" {
		t.Fatalf("expected second call to use fresh session, got resume=%v session=%q", runner.requests[1].Resume, runner.requests[1].SessionID)
	}
}
