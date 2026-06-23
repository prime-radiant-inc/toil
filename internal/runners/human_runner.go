package runners

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

type HumanRunner struct {
	config Config
}

func NewHumanRunner(config Config) *HumanRunner {
	return &HumanRunner{config: config}
}

func (runner *HumanRunner) Run(ctx context.Context, request Request, handler LineHandler) (Result, error) {
	env := resolveRunnerEnv(runner.config, request.Env)

	command := runner.config.Command
	args := env.Args
	if command == "" {
		path, err := os.Executable()
		if err != nil {
			return Result{}, err
		}
		command = path
		args = []string{"human"}
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = request.Workspace
	cmd.Env = env.Slice

	payload, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}

	var outputBuilder strings.Builder
	lineHandler := func(line Line) {
		if handler != nil {
			handler(line)
		}
		if line.Stream == streamStdout {
			outputBuilder.WriteString(line.Text)
			outputBuilder.WriteString("\n")
		}
	}

	exitCode, err := runCommand(ctx, cmd, string(payload), lineHandler)
	if err != nil {
		return Result{}, err
	}

	return Result{Output: outputBuilder.String(), ExitCode: exitCode}, nil
}
