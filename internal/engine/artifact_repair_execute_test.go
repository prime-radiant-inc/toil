package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

func TestRoleNodeArtifactRepairUsesLatestSessionID(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-artifact-repair-session",
		Name:    "Artifact Repair Session",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
	}
	runID := "run-artifact-repair-session"
	setupRunForResume(t, runsDir, runID, workflow, nil)

	runDir := filepath.Join(runsDir, runID)
	workspace := filepath.Join(runDir, "workspaces", "nodes", "agent")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "present.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write artifact fixture: %v", err)
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"first","data":{},"artifacts":["missing.txt"]}`, SessionID: testSessID1},
			{Output: `{"decision":"done","message":"repaired","data":{},"artifacts":["present.txt"]}`, SessionID: testSessID1},
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
		t.Fatalf("expected decision done, got %q", output.Decision)
	}
	if len(output.Artifacts) != 1 {
		t.Fatalf("expected one collected artifact, got %v", output.Artifacts)
	}
	if !strings.HasSuffix(output.Artifacts[0], filepath.Join("artifacts", "agent", "present.txt")) {
		t.Fatalf("unexpected artifact path: %q", output.Artifacts[0])
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner calls (initial + artifact repair), got %d", len(runner.requests))
	}
	if !runner.requests[1].Resume || runner.requests[1].SessionID != testSessID1 {
		t.Fatalf("expected artifact repair to resume latest session, got resume=%v session=%q", runner.requests[1].Resume, runner.requests[1].SessionID)
	}
	if !strings.Contains(runner.requests[1].Prompt, "required JSON shape") {
		t.Fatalf("expected JSON artifact repair prompt, got: %q", runner.requests[1].Prompt)
	}
}

func TestRoleNodeArtifactRepairFallsBackToFreshSession(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-artifact-repair-fresh-fallback",
		Name:    "Artifact Repair Fresh Fallback",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "role", Runner: "test-runner", Decisions: definitions.StringDecisions(testDecisionDone)},
		},
	}
	runID := "run-artifact-repair-fresh-fallback"
	setupRunForResume(t, runsDir, runID, workflow, nil)

	runDir := filepath.Join(runsDir, runID)
	workspace := filepath.Join(runDir, "workspaces", "nodes", "agent")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "present.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write artifact fixture: %v", err)
	}

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"first","data":{},"artifacts":["missing.txt"]}`, SessionID: testSessID1},
			{},
			{Output: `{"decision":"done","message":"recovered","data":{},"artifacts":["present.txt"]}`, SessionID: "sess-2"},
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

	output, err := engine.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDone {
		t.Fatalf("expected decision done, got %q", output.Decision)
	}
	if len(output.Artifacts) != 1 {
		t.Fatalf("expected one collected artifact, got %v", output.Artifacts)
	}
	if len(runner.requests) != 3 {
		t.Fatalf("expected three runner calls (initial + resume repair + fresh repair), got %d", len(runner.requests))
	}
	if !runner.requests[1].Resume || runner.requests[1].SessionID != testSessID1 {
		t.Fatalf("expected second call to resume existing session, got resume=%v session=%q", runner.requests[1].Resume, runner.requests[1].SessionID)
	}
	if runner.requests[2].Resume || runner.requests[2].SessionID != "" {
		t.Fatalf("expected third call to use a fresh session, got resume=%v session=%q", runner.requests[2].Resume, runner.requests[2].SessionID)
	}
}
