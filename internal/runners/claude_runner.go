package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type ClaudeRunner struct {
	config Config
}

func NewClaudeRunner(config Config) *ClaudeRunner {
	return &ClaudeRunner{config: config}
}

func (runner *ClaudeRunner) Run(ctx context.Context, request Request, handler LineHandler) (Result, error) {
	ctx, cancel := withTimeout(ctx, runner.config.TimeoutSec)
	defer cancel()

	stream := NewClaudeStream()
	collector := &outputCollector{}
	lineHandler := newStreamLineHandler(handler, stream.Handle, collector)

	cmd, prompt, err := runner.buildCommand(ctx, request)
	if err != nil {
		return Result{}, err
	}

	exitCode, err := runCommand(ctx, cmd, prompt, lineHandler)
	if err != nil {
		return Result{}, err
	}

	output := collector.String()
	if stream.HasResult && strings.TrimSpace(stream.Result) != "" {
		output = stream.Result
	}

	return Result{
		Output:    output,
		SessionID: stream.SessionID,
		ExitCode:  exitCode,
	}, nil
}

func (runner *ClaudeRunner) buildCommand(ctx context.Context, request Request) (*exec.Cmd, string, error) {
	command := runner.config.Command
	if command == "" {
		command = commandClaude
	}

	env := resolveRunnerEnv(runner.config, request.Env)

	args := []string{
		"-p",
		flagVerbose,
		"--output-format", flagStreamJSON,
		"--input-format", flagStreamJSON,
		"--replay-user-messages",
		"--dangerously-skip-permissions",
	}
	if len(env.Args) > 0 {
		args = append(args, env.Args...)
	}
	if request.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(request.MaxTurns))
	}
	if request.Resume {
		if request.SessionID == "" {
			return nil, "", fmt.Errorf("session id required for resume")
		}
		args = append(args, "--resume", request.SessionID)
	} else if request.SessionID != "" {
		args = append(args, "--session-id", request.SessionID)
	}
	if len(request.OutputSchemaJSON) > 0 {
		args = append(args, "--json-schema", string(request.OutputSchemaJSON))
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = request.Workspace
	cmd.Env = env.Slice

	prompt := request.Prompt
	if prompt == "" {
		prompt = "\n"
	}
	streamInput, err := buildClaudeStreamInput(prompt)
	if err != nil {
		return nil, "", err
	}

	return cmd, streamInput, nil
}

func buildClaudeStreamInput(prompt string) (string, error) {
	if prompt == "" {
		prompt = "\n"
	}
	payload := map[string]any{
		"type": typeUser,
		keyMessage: map[string]any{
			"role": typeUser,
			"content": []map[string]string{
				{
					"type":   typeText,
					typeText: prompt,
				},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
