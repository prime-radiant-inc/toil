package engine

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// captureRunner records the Request passed to it and returns a configurable Result.
type captureRunner struct {
	lastRequest runners.Request
	result      runners.Result
	err         error
}

func (r *captureRunner) Run(_ context.Context, req runners.Request, handler runners.LineHandler) (runners.Result, error) {
	r.lastRequest = req
	if handler != nil {
		handler(runners.Line{Stream: "stdout", Text: r.result.Output})
	}
	return r.result, r.err
}

// TestShellRoleSkipsFormatInjection verifies that when a role node uses a shell
// runner, the prompt sent to the runner is just the node prompt (with input refs
// expanded) — no structured output format injection, no "Inputs:" block, no
// "Allowed decisions:".
func TestShellRoleSkipsFormatInjection(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-shell", workflow, nil)

	capture := &captureRunner{
		result: runners.Result{Output: "some output\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-shell")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The prompt should NOT contain format injection
	prompt := capture.lastRequest.Prompt
	if contains(prompt, "REQUIRED OUTPUT FORMAT") {
		t.Error("shell runner prompt should not contain REQUIRED OUTPUT FORMAT")
	}
	if contains(prompt, "Inputs:") {
		t.Error("shell runner prompt should not contain Inputs: block")
	}
	if contains(prompt, "Allowed decisions:") {
		t.Error("shell runner prompt should not contain Allowed decisions:")
	}
}

// TestShellRolePassesPromptRaw verifies that the shell prompt is passed through
// as-is — no ${input.xxx} expansion. Shell scripts use $ENV_VAR syntax instead.
func TestShellRolePassesPromptRaw(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "run_cmd", Kind: "role", Runner: "shell",
				Prompt: "echo $GREETING",
			},
		},
		Edges: []definitions.Edge{},
	}

	inputs := map[string]any{"greeting": testInputHello}
	setupRunForResume(t, runsDir, "run-raw", workflow, inputs)

	capture := &captureRunner{
		result: runners.Result{Output: "hello\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Prompt should be the raw command — bash resolves $GREETING from env.
	if capture.lastRequest.Prompt != "echo $GREETING" {
		t.Errorf("expected raw prompt 'echo $GREETING', got %q", capture.lastRequest.Prompt)
	}
	// Inputs available as env vars for bash to resolve.
	if capture.lastRequest.Env["GREETING"] != testInputHello {
		t.Errorf("expected GREETING=hello in env, got %q", capture.lastRequest.Env["GREETING"])
	}
}

// TestShellRolePassesInputsAsEnv verifies that workflow inputs are passed as
// environment variables to the shell runner.
func TestShellRolePassesInputsAsEnv(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "run_cmd", Kind: "role", Runner: "shell",
				Prompt: "echo $PROJECT_DIR",
			},
		},
		Edges: []definitions.Edge{},
	}

	inputs := map[string]any{"project_dir": "/tmp/test"}
	setupRunForResume(t, runsDir, "run-env", workflow, inputs)

	capture := &captureRunner{
		result: runners.Result{Output: "/tmp/test\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := capture.lastRequest.Env
	if env == nil {
		t.Fatal("expected Env to be set")
	}
	if env["PROJECT_DIR"] != "/tmp/test" {
		t.Errorf("expected PROJECT_DIR=/tmp/test, got %q", env["PROJECT_DIR"])
	}
}

// TestShellRoleEnvJsonEncodesComplexTypes verifies that list and map inputs
// are JSON-encoded in environment variables (not Go fmt.Sprint format).
func TestShellRoleEnvJsonEncodesComplexTypes(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell", Prompt: "echo test"},
		},
		Edges: []definitions.Edge{},
	}

	inputs := map[string]any{
		"items":   []any{"auth", "api", "db"},
		"name":    "test-project",
		"count":   42,
		"details": map[string]any{"key": "value"},
	}
	setupRunForResume(t, runsDir, "run-json", workflow, inputs)

	capture := &captureRunner{
		result: runners.Result{Output: "ok\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := capture.lastRequest.Env
	// String values pass through directly
	if env["NAME"] != "test-project" {
		t.Errorf("expected NAME=test-project, got %q", env["NAME"])
	}
	// Lists are JSON-encoded
	if env["ITEMS"] != `["auth","api","db"]` {
		t.Errorf("expected ITEMS as JSON array, got %q", env["ITEMS"])
	}
	// Maps are JSON-encoded
	if env["DETAILS"] != `{"key":"value"}` {
		t.Errorf("expected DETAILS as JSON object, got %q", env["DETAILS"])
	}
	// Numbers are JSON-encoded
	if env["COUNT"] != "42" {
		t.Errorf("expected COUNT=42, got %q", env["COUNT"])
	}
}

// TestShellRoleOutputDefaultDecision verifies that shell runner output produces
// decision=testDecisionDefault with stdout as the message.
func TestShellRoleOutputDefaultDecision(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-decision", workflow, nil)

	capture := &captureRunner{
		result: runners.Result{Output: "all good\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := eng.ResumeRun(context.Background(), "run-decision")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output.Decision != testDecisionDefault {
		t.Errorf("expected decision 'default', got %q", output.Decision)
	}
	if output.Message != "all good" {
		t.Errorf("expected message 'all good', got %q", output.Message)
	}
}

// TestShellRoleNonZeroExitFails verifies that a non-zero exit code from a shell
// runner results in a node failure.
func TestShellRoleNonZeroExitFails(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-fail", workflow, nil)

	capture := &captureRunner{
		result: runners.Result{Output: "error: not found\n", ExitCode: 1},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-fail")
	if err == nil {
		t.Fatal("expected error from non-zero exit code")
	}

	events := parseEvents(t, filepath.Join(runsDir, "run-fail", "events.jsonl"))
	failed := findEvent(events, "node_failed")
	if failed == nil {
		t.Fatal("expected node_failed event")
	}
}

// TestShellRoleFatalErrorIncludesNodeID verifies that when a shell node fails
// fatally (no failure edges), the run-level error includes the node ID.
func TestShellRoleFatalErrorIncludesNodeID(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "check_config", Kind: "role", Runner: "shell", Prompt: "echo test",
				Decisions: definitions.StringDecisions("go", "skip"),
			},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-fatal", workflow, nil)

	// Output valid JSON but with a decision not in the declared enum.
	capture := &captureRunner{
		result: runners.Result{
			Output:   `{"decision":"bogus","message":"proceeding","data":{},"artifacts":[]}`,
			ExitCode: 0,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-fatal")
	if err == nil {
		t.Fatal("expected error from validation failure")
	}

	// The run-level error should include the node ID so operators know WHERE the failure occurred
	if !strings.Contains(err.Error(), "check_config") {
		t.Errorf("expected run error to include node ID 'check_config', got: %v", err)
	}
}

// TestShellRoleSetsWorkflowScriptDir verifies that TOIL_WORKFLOW_SCRIPT_DIR is
// injected into the env for shell nodes, pointing to a directory named after
// the workflow ID alongside the workflow's source YAML.
func TestShellRoleSetsWorkflowScriptDir(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:         "implement_spec",
		Name:       "Implement Spec",
		Version:    1,
		SourcePath: "/defs/workflows/implement_spec.yaml",
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell", Prompt: "echo $TOIL_WORKFLOW_SCRIPT_DIR"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-scriptdir", workflow, nil)

	capture := &captureRunner{
		result: runners.Result{Output: "ok\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), "run-scriptdir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := capture.lastRequest.Env
	want := "/defs/workflows/implement_spec"
	if env["TOIL_WORKFLOW_SCRIPT_DIR"] != want {
		t.Errorf("expected TOIL_WORKFLOW_SCRIPT_DIR=%q, got %q", want, env["TOIL_WORKFLOW_SCRIPT_DIR"])
	}
}

// TestShellRoleSetsRunID verifies that TOIL_RUN_ID from the run state env
// is passed through to shell node execution. This is critical for parallel
// runs that need unique resource names (e.g., git worktree branches).
func TestShellRoleSetsRunID(t *testing.T) {
	runsDir := t.TempDir()
	runID := "run-with-id"
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell", Prompt: "echo $TOIL_RUN_ID"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, runID, workflow, nil)

	// Patch the saved state to include TOIL_RUN_ID (normally set by StartRun).
	statePath := filepath.Join(runsDir, runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if rs.Env == nil {
		rs.Env = map[string]string{}
	}
	rs.Env["TOIL_RUN_ID"] = runID
	if err := state.SaveState(statePath, rs); err != nil {
		t.Fatalf("save state: %v", err)
	}

	capture := &captureRunner{
		result: runners.Result{Output: "ok\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err = eng.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := capture.lastRequest.Env
	if env["TOIL_RUN_ID"] != runID {
		t.Errorf("expected TOIL_RUN_ID=%q, got %q", runID, env["TOIL_RUN_ID"])
	}
}

// TestShellRoleNoRole verifies that a shell node with no role (empty node.Role)
// works correctly — the role is optional for shell runners.
func TestShellRoleNoRole(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "run_cmd", Kind: "role", Runner: "shell", Prompt: "echo hi"},
		},
		Edges: []definitions.Edge{},
	}

	setupRunForResume(t, runsDir, "run-norole", workflow, nil)

	capture := &captureRunner{
		result: runners.Result{Output: "hi\n", ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("shell", capture)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"shell": {ID: "shell", Type: "shell"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	output, err := eng.ResumeRun(context.Background(), "run-norole")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Decision != testDecisionDefault {
		t.Errorf("expected decision 'default', got %q", output.Decision)
	}
}

// stateCheckRunner reads state.json during execution and records the node status.
type stateCheckRunner struct {
	statePath       string
	nodeID          string
	statusDuringRun string
	result          runners.Result
}

func (r *stateCheckRunner) Run(_ context.Context, req runners.Request, handler runners.LineHandler) (runners.Result, error) {
	rs, err := state.LoadState(r.statePath)
	if err != nil {
		return runners.Result{}, err
	}
	rs.WithNode(r.nodeID, func(n *state.NodeState) {
		r.statusDuringRun = n.Status
	})
	if handler != nil {
		handler(runners.Line{Stream: "stdout", Text: r.result.Output})
	}
	return r.result, nil
}

func TestStateSavedBeforeNodeExecution(t *testing.T) {
	runsDir := t.TempDir()
	runID := "run-presave"
	workflow := &definitions.Workflow{
		ID:      "test-wf",
		Name:    "Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "step", Kind: "role", Runner: "checker"},
		},
		Edges: []definitions.Edge{},
	}
	setupRunForResume(t, runsDir, runID, workflow, nil)

	checker := &stateCheckRunner{
		statePath: filepath.Join(runsDir, runID, "state.json"),
		nodeID:    "step",
		result:    runners.Result{Output: `{"decision":"default","message":"ok"}`, ExitCode: 0},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("checker", checker)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"checker": {ID: "checker", Type: "serf"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	_, err := eng.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if checker.statusDuringRun != statusRunning {
		t.Errorf("expected node status %q on disk during execution, got %q", statusRunning, checker.statusDuringRun)
	}
}
