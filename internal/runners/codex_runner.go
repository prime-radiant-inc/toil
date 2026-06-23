package runners

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

type CodexRunner struct {
	config Config
}

func NewCodexRunner(config Config) *CodexRunner {
	return &CodexRunner{config: config}
}

func (runner *CodexRunner) Run(ctx context.Context, request Request, handler LineHandler) (Result, error) {
	ctx, cancel := withTimeout(ctx, runner.config.TimeoutSec)
	defer cancel()

	var schemaPath string
	if len(request.OutputSchemaJSON) > 0 {
		file, err := os.CreateTemp(request.Workspace, "output_schema-*.json")
		if err != nil {
			return Result{}, err
		}
		if _, err := file.Write(request.OutputSchemaJSON); err != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
			return Result{}, err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(file.Name())
			return Result{}, err
		}
		schemaPath = file.Name()
		defer func() { _ = os.Remove(schemaPath) }()
	}

	stream := NewCodexStream()
	collector := &outputCollector{}
	lineHandler := newStreamLineHandler(handler, stream.Handle, collector)

	cmd, prompt, err := runner.buildCommand(ctx, request, schemaPath)
	if err != nil {
		return Result{}, err
	}

	exitCode, err := runCommand(ctx, cmd, prompt, lineHandler)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Output:    collector.String(),
		SessionID: stream.SessionID,
		ExitCode:  exitCode,
	}, nil
}

func (runner *CodexRunner) buildCommand(ctx context.Context, request Request, schemaPath string) (*exec.Cmd, string, error) {
	command := runner.config.Command
	if command == "" {
		command = commandCodex
	}

	env := resolveRunnerEnv(runner.config, request.Env)

	args := []string{subcommandExec, flagJSON, flagBypassSandbox}
	if len(env.Args) > 0 {
		args = append(args, env.Args...)
	}
	if schemaPath != "" {
		args = append(args, "--output-schema", schemaPath)
	}
	if request.Resume {
		if request.SessionID == "" {
			return nil, "", fmt.Errorf("session id required for resume")
		}
		args = append(args, "resume", request.SessionID)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = request.Workspace
	cmd.Env = env.Slice

	prompt := request.Prompt
	if prompt == "" {
		prompt = "\n"
	}

	return cmd, prompt, nil
}
