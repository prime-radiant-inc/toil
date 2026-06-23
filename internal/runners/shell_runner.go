package runners

import (
	"context"
	"os/exec"
	"strings"
)

type ShellRunner struct {
	config Config
}

func NewShellRunner(config Config) *ShellRunner {
	return &ShellRunner{config: config}
}

func (runner *ShellRunner) Run(ctx context.Context, request Request, handler LineHandler) (Result, error) {
	ctx, cancel := withTimeout(ctx, runner.config.TimeoutSec)
	defer cancel()

	env := resolveRunnerEnv(runner.config, request.Env)

	command := runner.config.Command
	var args []string
	if command == "" {
		command = commandBash
		args = []string{"-lc", request.Prompt}
	} else {
		args = make([]string, 0, len(env.Args)+1)
		args = append(args, env.Args...)
		args = append(args, request.Prompt)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = request.Workspace
	cmd.Env = env.Slice

	var outputBuilder strings.Builder
	var stderrBuilder strings.Builder
	lineHandler := func(line Line) {
		if handler != nil {
			handler(line)
		}
		switch line.Stream {
		case streamStdout:
			outputBuilder.WriteString(line.Text)
			outputBuilder.WriteString("\n")
		case streamStderr:
			stderrBuilder.WriteString(line.Text)
			stderrBuilder.WriteString("\n")
		}
	}

	exitCode, err := runCommand(ctx, cmd, "", lineHandler)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Output:   outputBuilder.String(),
		Stderr:   stderrBuilder.String(),
		ExitCode: exitCode,
	}, nil
}
