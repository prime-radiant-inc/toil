package runners

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewHumanRunner(t *testing.T) {
	cfg := Config{Command: "/usr/bin/human", TimeoutSec: 60}
	runner := NewHumanRunner(cfg)

	if runner.config.Command != "/usr/bin/human" {
		t.Fatalf("Command = %q, want /usr/bin/human", runner.config.Command)
	}
}

func TestHumanRunner_CustomCommand(t *testing.T) {
	// Use "cat" as the command — it reads stdin and writes to stdout,
	// letting us verify the JSON request payload flows through.
	runner := NewHumanRunner(Config{
		Command: "cat",
		Args:    nil,
	})

	req := Request{
		Prompt:    "Please review this",
		Workspace: t.TempDir(),
	}

	result, err := runner.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}

	// cat echoes the JSON payload to stdout, so output should be valid JSON.
	output := strings.TrimSpace(result.Output)
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %q", err, output)
	}

	if payload["Prompt"] != "Please review this" {
		t.Fatalf("Prompt = %v, want 'Please review this'", payload["Prompt"])
	}
}

func TestHumanRunner_CapturesStdoutViaHandler(t *testing.T) {
	runner := NewHumanRunner(Config{
		Command: "echo",
		Args:    []string{"human output"},
	})

	var lines []Line
	handler := func(line Line) { lines = append(lines, line) }

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "ignored by echo",
		Workspace: t.TempDir(),
	}, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, "human output") {
		t.Fatalf("Output = %q, want to contain 'human output'", result.Output)
	}

	stdoutCount := 0
	for _, l := range lines {
		if l.Stream == streamStdout {
			stdoutCount++
		}
	}
	if stdoutCount == 0 {
		t.Fatal("expected handler to receive stdout lines")
	}
}

func TestHumanRunner_NonZeroExit(t *testing.T) {
	runner := NewHumanRunner(Config{
		Command: "bash",
		Args:    []string{"-c", "exit 7"},
	})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "unused",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}
}

func TestHumanRunner_EnvMerging(t *testing.T) {
	runner := NewHumanRunner(Config{
		Command: "bash",
		Args:    []string{"-c", "echo $HUMAN_TEST_VAR"},
		Env:     map[string]string{"HUMAN_TEST_VAR": "from_config"},
	})

	result, err := runner.Run(context.Background(), Request{
		Prompt:    "unused",
		Workspace: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Output, "from_config") {
		t.Fatalf("Output = %q, want to contain 'from_config'", result.Output)
	}
}

func TestHumanRunner_RequestPayloadIsValidJSON(t *testing.T) {
	runner := NewHumanRunner(Config{Command: "cat"})

	req := Request{
		Prompt:    "Review changes",
		Workspace: t.TempDir(),
		Decisions: []string{"approve", "reject"},
		Env:       map[string]string{"KEY": "val"},
	}

	result, err := runner.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := strings.TrimSpace(result.Output)
	var payload Request
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("output is not valid Request JSON: %v\noutput: %q", err, output)
	}

	if payload.Prompt != "Review changes" {
		t.Fatalf("Prompt = %q, want 'Review changes'", payload.Prompt)
	}
	if len(payload.Decisions) != 2 || payload.Decisions[0] != "approve" {
		t.Fatalf("Decisions = %v, want [approve, reject]", payload.Decisions)
	}
}
