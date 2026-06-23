package runners

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeRunner_Run(t *testing.T) {
	// Create a script that outputs claude stream-json format, ignoring all args.
	script := writeScript(t, `#!/bin/bash
cat >/dev/null  # consume stdin
echo '{"type":"assistant","session_id":"sess-abc","message":{"content":[{"type":"text","text":"Hello from claude"}]}}'
echo '{"type":"result","result":"final answer"}'
`)

	runner := NewClaudeRunner(Config{Command: script})

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
	if result.SessionID != "sess-abc" {
		t.Fatalf("SessionID = %q, want %q", result.SessionID, "sess-abc")
	}
	// When HasResult is true, Result field takes priority over collected output.
	if result.Output != "final answer" {
		t.Fatalf("Output = %q, want %q", result.Output, "final answer")
	}
}

func TestClaudeRunner_Run_CollectsText(t *testing.T) {
	// No "result" event — output comes from assistant text content.
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"streamed text"}]}}'
`)

	runner := NewClaudeRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "streamed text" {
		t.Fatalf("Output = %q, want %q", result.Output, "streamed text")
	}
}

func TestClaudeRunner_Run_ForwardsToHandler(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}'
`)

	runner := NewClaudeRunner(Config{Command: script})

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
}

func TestClaudeRunner_Run_NonZeroExit(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
cat >/dev/null
exit 1
`)

	runner := NewClaudeRunner(Config{Command: script})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
}

func TestClaudeRunner_BuildCommand_ResumeWithoutSessionID(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "echo"})

	_, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
		Resume:    true,
	})
	if err == nil {
		t.Fatal("expected error for resume without session ID")
	}
}

func TestClaudeRunner_BuildCommand_SessionIDWithoutResume(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "echo"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
		SessionID: "sess-123",
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	found := false
	for i, arg := range cmd.Args {
		if arg == "--session-id" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "sess-123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --session-id sess-123 in args: %v", cmd.Args)
	}
}

func TestClaudeRunner_BuildCommand_EmptyPrompt(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "echo"})

	_, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	// Empty prompt should be replaced with "\n" in the stream-json payload.
	if prompt == "" {
		t.Fatal("prompt should not be empty")
	}
}

func TestClaudeRunner_BuildCommand_DefaultCommand(t *testing.T) {
	runner := NewClaudeRunner(Config{})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cmd.Args[0] != "claude" {
		t.Fatalf("expected default command 'claude', got %q", cmd.Args[0])
	}
}

func TestClaudeRunner_BuildCommand_ResumeWithSessionID(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "echo"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Prompt:    "test",
		Workspace: t.TempDir(),
		Resume:    true,
		SessionID: "sess-abc",
	})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	found := false
	for i, arg := range cmd.Args {
		if arg == "--resume" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "sess-abc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --resume sess-abc in args: %v", cmd.Args)
	}
}

func TestBuildClaudeStreamInput_EmptyPrompt(t *testing.T) {
	input, err := buildClaudeStreamInput("")
	if err != nil {
		t.Fatalf("buildClaudeStreamInput: %v", err)
	}
	// Empty prompt should be replaced with "\n".
	if !strings.Contains(input, `\n`) {
		t.Fatalf("expected \\n in input for empty prompt, got %q", input)
	}
}

// writeScript creates a temporary executable script and returns its path.
func writeScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
