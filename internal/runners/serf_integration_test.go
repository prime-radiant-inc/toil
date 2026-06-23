package runners

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSerfRunner_Run(t *testing.T) {
	// Serf emits events to stderr (--verbose mode). Session ID comes from stderr.
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo '{"kind":"SESSION_START","session_id":"01HABCDE"}' >&2
echo 'stdout output line'
`)

	runner := NewSerfRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.SessionID != "01HABCDE" {
		t.Fatalf("SessionID = %q, want %q", result.SessionID, "01HABCDE")
	}
	if !strings.Contains(result.Output, "stdout output") {
		t.Fatalf("Output = %q, want to contain 'stdout output'", result.Output)
	}
	if !strings.Contains(result.Stderr, "SESSION_START") {
		t.Fatalf("Stderr = %q, want to contain stderr events", result.Stderr)
	}
}

func TestSerfRunner_Run_StructuredOutput(t *testing.T) {
	// When communicate tool call with result is in stderr, structured output
	// takes priority over raw stdout.
	args := map[string]any{
		"action":  "result",
		"message": `{"decision":"done","message":"ok","data":{},"artifacts":[]}`,
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	evt := map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"arguments_json": string(argsJSON),
		},
	}
	evtJSON, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo 'raw stdout'
echo '`+string(evtJSON)+`' >&2
`)

	runner := NewSerfRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Structured output should take priority.
	if !strings.Contains(result.Output, `"decision":"done"`) {
		t.Fatalf("Output = %q, want structured communicate output", result.Output)
	}
}

func TestSerfRunner_Run_ForwardsToHandler(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo 'stdout data'
`)

	runner := NewSerfRunner(Config{Command: script})

	var lines []Line
	handler := func(line Line) { lines = append(lines, line) }

	_, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(lines) == 0 {
		t.Fatal("expected handler to receive lines")
	}

	hasStdout := false
	for _, l := range lines {
		if l.Stream == streamStdout {
			hasStdout = true
		}
	}
	if !hasStdout {
		t.Fatal("expected stdout lines in handler")
	}
}

func TestSerfRunner_Run_NonZeroExit(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
exit 3
`)

	runner := NewSerfRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode)
	}
}

func TestSerfRunner_Run_NilHandler(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo 'output'
`)

	runner := NewSerfRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Output, "output") {
		t.Fatalf("Output = %q, want to contain 'output'", result.Output)
	}
}

func TestSerfRunner_BuildCommand_DefaultCommand(t *testing.T) {
	runner := NewSerfRunner(Config{})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cmd.Args[0] != "serf" {
		t.Fatalf("expected default command 'serf', got %q", cmd.Args[0])
	}
}

func TestSerfRunner_BuildCommand_EmptyPrompt(t *testing.T) {
	runner := NewSerfRunner(Config{Command: "echo"})

	_, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if prompt != "\n" {
		t.Fatalf("prompt = %q, want newline for empty prompt", prompt)
	}
}
