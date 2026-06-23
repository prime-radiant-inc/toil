package runners

import (
	"context"
	"testing"
)

func TestCodexRunner_Run(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-xyz"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"codex says hello"}}'
`)

	runner := NewCodexRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test prompt",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.SessionID != "thread-xyz" {
		t.Fatalf("SessionID = %q, want %q", result.SessionID, "thread-xyz")
	}
	if result.Output != "codex says hello" {
		t.Fatalf("Output = %q, want %q", result.Output, "codex says hello")
	}
}

func TestCodexRunner_Run_ForwardsToHandler(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo '{"type":"item.completed","item":{"type":"agent_message","text":"msg"}}'
`)

	runner := NewCodexRunner(Config{Command: script})

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
	if lines[0].Stream != streamStdout {
		t.Fatalf("first line stream = %q, want stdout", lines[0].Stream)
	}
}

func TestCodexRunner_Run_NonZeroExit(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
exit 2
`)

	runner := NewCodexRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
}

func TestCodexRunner_BuildCommand_EmptyPrompt(t *testing.T) {
	runner := NewCodexRunner(Config{Command: "echo"})

	_, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: t.TempDir(),
	}, "")
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if prompt != "\n" {
		t.Fatalf("prompt = %q, want newline for empty prompt", prompt)
	}
}

func TestCodexRunner_BuildCommand_ResumeWithoutSessionID(t *testing.T) {
	runner := NewCodexRunner(Config{Command: "echo"})

	_, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
		Resume:    true,
	}, "")
	if err == nil {
		t.Fatal("expected error for resume without session ID")
	}
}

func TestCodexRunner_BuildCommand_DefaultCommand(t *testing.T) {
	runner := NewCodexRunner(Config{})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, "")
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	// When Command is empty, defaults to "codex".
	if cmd.Args[0] != "codex" {
		t.Fatalf("expected default command 'codex', got %q", cmd.Args[0])
	}
}
