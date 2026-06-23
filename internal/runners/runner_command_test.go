package runners

import (
	"context"
	"strings"
	"testing"
)

const testWorkspaceDir = "/tmp/work"

func TestCodexBuildCommandIncludesConfiguredArgs(t *testing.T) {
	runner := NewCodexRunner(Config{
		Command: "codex",
		Args:    []string{"-m", "gpt-5-codex"},
	})

	cmd, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	}, "")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	expected := []string{
		"codex",
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-m",
		"gpt-5-codex",
		"-",
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("unexpected args: %#v", cmd.Args)
	}
	if cmd.Dir != testWorkspaceDir {
		t.Fatalf("unexpected dir: %q", cmd.Dir)
	}
	if prompt != "hello" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}

func TestCodexBuildCommandResumeIncludesSessionID(t *testing.T) {
	runner := NewCodexRunner(Config{
		Command: "codex",
		Args:    []string{"-m", "gpt-5-codex"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
		Resume:    true,
		SessionID: "session-123",
	}, "")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	expected := []string{
		"codex",
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-m",
		"gpt-5-codex",
		"resume",
		"session-123",
		"-",
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("unexpected args: %#v", cmd.Args)
	}
}

func TestClaudeBuildCommandIncludesConfiguredArgs(t *testing.T) {
	runner := NewClaudeRunner(Config{
		Command: "claude",
		Args:    []string{"--model", "claude-sonnet"},
	})

	cmd, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	expected := []string{
		"claude",
		"-p",
		"--verbose",
		"--output-format",
		"stream-json",
		"--input-format",
		"stream-json",
		"--replay-user-messages",
		"--dangerously-skip-permissions",
		"--model",
		"claude-sonnet",
		"-",
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("unexpected args: %#v", cmd.Args)
	}
	if cmd.Dir != testWorkspaceDir {
		t.Fatalf("unexpected dir: %q", cmd.Dir)
	}
	if !strings.Contains(prompt, `"type":"user"`) || !strings.Contains(prompt, `"text":"hello"`) {
		t.Fatalf("unexpected prompt payload: %q", prompt)
	}
}

func TestClaudeBuildCommandUsesContext(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "echo"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd, _, err := runner.buildCommand(ctx, Request{Prompt: "test", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path == "" {
		t.Fatal("expected command path to be set")
	}
}

func TestCodexBuildCommandNeverIncludesMaxTurns(t *testing.T) {
	runner := NewCodexRunner(Config{Command: "codex"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
		MaxTurns:  8,
	}, "")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "max-turns") {
		t.Fatalf("codex does not support --max-turns, should not be in args: %v", cmd.Args)
	}
}

func TestClaudeBuildCommandIncludesMaxTurns(t *testing.T) {
	runner := NewClaudeRunner(Config{
		Command: "claude",
		Args:    []string{"--model", "claude-sonnet"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
		MaxTurns:  12,
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--max-turns 12") {
		t.Fatalf("expected --max-turns 12 in args: %v", cmd.Args)
	}
}

func TestClaudeBuildCommandOmitsMaxTurnsWhenZero(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "claude"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
		MaxTurns:  0,
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "max-turns") {
		t.Fatalf("should not include --max-turns when zero: %v", cmd.Args)
	}
}

func TestCodexBuildCommandIncludesOutputSchema(t *testing.T) {
	runner := NewCodexRunner(Config{
		Command: "codex",
		Args:    []string{"-m", "gpt-5-codex"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	}, "/tmp/schema.json")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	expected := []string{
		"codex",
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-m",
		"gpt-5-codex",
		"--output-schema",
		"/tmp/schema.json",
		"-",
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("unexpected args: %#v", cmd.Args)
	}
}

func TestCodexBuildCommandOmitsOutputSchemaWhenEmpty(t *testing.T) {
	runner := NewCodexRunner(Config{
		Command: "codex",
		Args:    []string{"-m", "gpt-5-codex"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	}, "")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--output-schema") {
		t.Fatalf("should not include --output-schema when empty: %v", cmd.Args)
	}
}

func TestCodexBuildCommandOutputSchemaWithResume(t *testing.T) {
	runner := NewCodexRunner(Config{
		Command: "codex",
		Args:    []string{"-m", "gpt-5-codex"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
		Resume:    true,
		SessionID: "session-abc",
	}, "/tmp/schema.json")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	expected := []string{
		"codex",
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-m",
		"gpt-5-codex",
		"--output-schema",
		"/tmp/schema.json",
		"resume",
		"session-abc",
		"-",
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("unexpected args: %#v", cmd.Args)
	}
}

func TestCodexBuildCommandUsesContext(t *testing.T) {
	runner := NewCodexRunner(Config{Command: "echo"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd, _, err := runner.buildCommand(ctx, Request{Prompt: "test", Workspace: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path == "" {
		t.Fatal("expected command path to be set")
	}
}

func TestClaudeBuildCommandIncludesJSONSchema(t *testing.T) {
	runner := NewClaudeRunner(Config{
		Command: "claude",
	})

	schemaJSON := []byte(`{"type":"object"}`)
	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace:        testWorkspaceDir,
		Prompt:           "hello",
		OutputSchemaJSON: schemaJSON,
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, "\x00")
	if !strings.Contains(args, "--json-schema\x00"+string(schemaJSON)) {
		t.Fatalf("expected --json-schema with inline JSON, got: %v", cmd.Args)
	}
}

func TestClaudeBuildCommandOmitsJSONSchemaWhenEmpty(t *testing.T) {
	runner := NewClaudeRunner(Config{Command: "claude"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "--json-schema") {
		t.Fatalf("expected no --json-schema flag: %v", cmd.Args)
	}
}

func TestSerfBuildCommandIncludesOutputSchema(t *testing.T) {
	runner := NewSerfRunner(Config{
		Command: "serf",
	})

	schemaJSON := []byte(`{"type":"object"}`)
	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace:        testWorkspaceDir,
		Prompt:           "hello",
		OutputSchemaJSON: schemaJSON,
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, "\x00")
	if !strings.Contains(args, "--output-schema\x00"+string(schemaJSON)) {
		t.Fatalf("expected --output-schema with inline JSON, got: %v", cmd.Args)
	}
}

func TestSerfBuildCommandOmitsOutputSchemaWhenEmpty(t *testing.T) {
	runner := NewSerfRunner(Config{Command: "serf"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: testWorkspaceDir,
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "--output-schema") {
		t.Fatalf("expected no --output-schema flag: %v", cmd.Args)
	}
}

func TestExpandArgsWithEnvUsesProvidedMap(t *testing.T) {
	env := map[string]string{"MY_MODEL": "gpt-5.4-mini"}
	args := []string{"--model", "$MY_MODEL"}
	expanded := ExpandArgsWithEnv(args, env)
	if expanded[1] != "gpt-5.4-mini" {
		t.Fatalf("expected gpt-5.4-mini, got %q", expanded[1])
	}
}

func TestExpandArgsWithEnvFallsBackToDefault(t *testing.T) {
	env := map[string]string{}
	args := []string{"--effort", "${REASONING_EFFORT:-medium}"}
	expanded := ExpandArgsWithEnv(args, env)
	if expanded[1] != "medium" {
		t.Fatalf("expected medium, got %q", expanded[1])
	}
}

func TestExpandArgsWithEnvOverridesDefault(t *testing.T) {
	env := map[string]string{"REASONING_EFFORT": "low"}
	args := []string{"--effort", "${REASONING_EFFORT:-medium}"}
	expanded := ExpandArgsWithEnv(args, env)
	if expanded[1] != "low" {
		t.Fatalf("expected low, got %q", expanded[1])
	}
}
