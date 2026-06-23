package runners

import (
	"context"
	"strings"
	"testing"
)

func TestShellRunner_EchoOutput(t *testing.T) {
	runner := NewShellRunner(Config{})

	result, err := runner.Run(context.Background(), Request{
		Prompt: "echo hello",
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("Output = %q, want to contain 'hello'", result.Output)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestShellRunner_NonZeroExit(t *testing.T) {
	runner := NewShellRunner(Config{})

	result, err := runner.Run(context.Background(), Request{
		Prompt: "exit 42",
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 42 {
		t.Fatalf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestShellRunner_CapturesStdoutLines(t *testing.T) {
	runner := NewShellRunner(Config{})

	var lines []Line
	handler := func(line Line) { lines = append(lines, line) }

	result, err := runner.Run(context.Background(), Request{
		Prompt: "echo line1; echo line2; echo line3",
	}, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify handler received lines.
	stdoutCount := 0
	for _, l := range lines {
		if l.Stream == streamStdout {
			stdoutCount++
		}
	}
	if stdoutCount != 3 {
		t.Fatalf("stdout lines = %d, want 3", stdoutCount)
	}

	// Output should contain all lines.
	if !strings.Contains(result.Output, "line1") || !strings.Contains(result.Output, "line3") {
		t.Fatalf("Output = %q, missing expected content", result.Output)
	}
}

func TestShellRunner_CapturesStderr(t *testing.T) {
	runner := NewShellRunner(Config{})

	var stderrLines []string
	handler := func(line Line) {
		if line.Stream == "stderr" {
			stderrLines = append(stderrLines, line.Text)
		}
	}

	result, err := runner.Run(context.Background(), Request{
		Prompt: "echo error_msg >&2",
	}, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(stderrLines) == 0 {
		t.Fatal("expected stderr output via handler")
	}
	if !strings.Contains(stderrLines[0], "error_msg") {
		t.Fatalf("stderr handler = %q, want to contain 'error_msg'", stderrLines[0])
	}
	if !strings.Contains(result.Stderr, "error_msg") {
		t.Fatalf("result.Stderr = %q, want to contain 'error_msg'", result.Stderr)
	}
}

func TestShellRunner_CustomCommand(t *testing.T) {
	runner := NewShellRunner(Config{
		Command: "echo",
		Args:    []string{"-n"},
	})

	result, err := runner.Run(context.Background(), Request{
		Prompt: "custom_arg",
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Custom command: echo -n custom_arg → output should contain custom_arg.
	if !strings.Contains(result.Output, "custom_arg") {
		t.Fatalf("Output = %q, want to contain 'custom_arg'", result.Output)
	}
}

func TestShellRunner_WorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	runner := NewShellRunner(Config{})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "pwd",
		Workspace: dir,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, dir) {
		t.Fatalf("Output = %q, want to contain workspace %q", result.Output, dir)
	}
}

func TestShellRunner_EnvMerging(t *testing.T) {
	runner := NewShellRunner(Config{
		Env: map[string]string{"CONFIG_VAR": "from_config"},
	})

	result, err := runner.Run(context.Background(), Request{
		Prompt: "echo $CONFIG_VAR $REQUEST_VAR",
		Env:    map[string]string{"REQUEST_VAR": "from_request"},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, "from_config") {
		t.Fatalf("Output = %q, missing CONFIG_VAR", result.Output)
	}
	if !strings.Contains(result.Output, "from_request") {
		t.Fatalf("Output = %q, missing REQUEST_VAR", result.Output)
	}
}

func TestShellRunner_RequestEnvOverridesConfig(t *testing.T) {
	runner := NewShellRunner(Config{
		Env: map[string]string{"SHARED": "config_value"},
	})

	result, err := runner.Run(context.Background(), Request{
		Prompt: "echo $SHARED",
		Env:    map[string]string{"SHARED": "request_value"},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, "request_value") {
		t.Fatalf("Output = %q, request env should override config", result.Output)
	}
}

func TestShellRunner_ContextCancellation(t *testing.T) {
	runner := NewShellRunner(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := runner.Run(ctx, Request{
		Prompt: "sleep 30",
	}, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestNewShellRunner(t *testing.T) {
	cfg := Config{Command: "bash", TimeoutSec: 30}
	runner := NewShellRunner(cfg)

	if runner.config.Command != "bash" {
		t.Fatalf("Command = %q, want bash", runner.config.Command)
	}
	if runner.config.TimeoutSec != 30 {
		t.Fatalf("TimeoutSec = %d, want 30", runner.config.TimeoutSec)
	}
}
