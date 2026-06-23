package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

const streamStderr = "stderr"

type SerfRunner struct {
	config Config
}

func NewSerfRunner(config Config) *SerfRunner {
	return &SerfRunner{config: config}
}

func (runner *SerfRunner) Run(ctx context.Context, request Request, handler LineHandler) (Result, error) {
	ctx, cancel := withTimeout(ctx, runner.config.TimeoutSec)
	defer cancel()

	var outputBuilder strings.Builder
	var stderrBuilder strings.Builder
	sessionID := ""
	structuredOutput := ""
	toolCalls := 0

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
			if parsed := parseSerfSessionID(line.Text); parsed != "" {
				sessionID = parsed
			}
			if parsed := parseSerfCommunicateResultOutput(line.Text); parsed != "" {
				structuredOutput = parsed
			}
			if isSerfToolCallStart(line.Text) {
				toolCalls++
			}
		}
	}

	cmd, prompt, err := runner.buildCommand(ctx, request)
	if err != nil {
		return Result{}, err
	}

	exitCode, err := runCommand(ctx, cmd, prompt, lineHandler)
	if err != nil {
		return Result{}, err
	}

	output := outputBuilder.String()
	if strings.TrimSpace(structuredOutput) != "" {
		output = strings.TrimSpace(structuredOutput) + "\n"
	}

	return Result{
		Output:    output,
		Stderr:    stderrBuilder.String(),
		SessionID: sessionID,
		ExitCode:  exitCode,
		ToolCalls: toolCalls,
	}, nil
}

func (runner *SerfRunner) buildCommand(ctx context.Context, request Request) (*exec.Cmd, string, error) {
	command := runner.config.Command
	if command == "" {
		command = commandSerf
	}

	env := resolveRunnerEnv(runner.config, request.Env)

	args := make([]string, 0, len(env.Args)+4)
	args = append(args, env.Args...)
	if !slices.Contains(args, flagVerbose) {
		args = append(args, flagVerbose)
	}
	if request.Resume {
		if request.SessionID == "" {
			return nil, "", fmt.Errorf("session id required for resume")
		}
		args = append(args, "--resume", request.SessionID)
		if request.Fork {
			args = append(args, "--fork")
		}
	}
	if len(request.OutputSchemaJSON) > 0 {
		args = append(args, "--output-schema", string(request.OutputSchemaJSON))
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = request.Workspace
	cmd.Env = env.Slice

	prompt := request.Prompt
	if prompt == "" {
		prompt = "\n"
	}

	return cmd, prompt, nil
}

func parseSerfSessionID(line string) string {
	var event struct {
		Kind      string `json:"kind"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}
	if event.SessionID == "" {
		return ""
	}
	kind := strings.TrimSpace(event.Kind)
	if kind == "" {
		return ""
	}
	return event.SessionID
}

func isSerfToolCallStart(line string) bool {
	var event struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return false
	}
	return event.Kind == kindToolCallStart
}

func parseSerfCommunicateResultOutput(line string) string {
	// Serf emits NDJSON events to stderr when --verbose is set. The final output
	// is delivered via the `communicate` tool call arguments, not stdout.
	var event struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}
	if strings.TrimSpace(event.Kind) != kindToolCallStart {
		return ""
	}

	var toolCall struct {
		ToolName      string `json:"tool_name"`
		ArgumentsJSON string `json:"arguments_json"`
	}
	if err := json.Unmarshal(event.Data, &toolCall); err != nil {
		return ""
	}
	if strings.TrimSpace(toolCall.ToolName) != toolCommunicate {
		return ""
	}
	if strings.TrimSpace(toolCall.ArgumentsJSON) == "" {
		return ""
	}

	var args struct {
		Message string          `json:"message"`
		Output  json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal([]byte(toolCall.ArgumentsJSON), &args); err != nil {
		return ""
	}

	// Prefer a full NodeOutput object embedded as JSON in the `message` field.
	// This is a common failure mode when the model puts structured output in message
	// instead of output.data.* fields.
	msg := strings.TrimSpace(args.Message)
	if msg != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(msg), &parsed); err == nil {
			if _, ok := parsed["decision"]; ok {
				if _, ok := parsed[keyMessage]; ok {
					if _, ok := parsed["data"]; ok {
						if _, ok := parsed["artifacts"]; ok {
							return msg
						}
					}
				}
			}
		}
	}

	// Fall back to the `output` object if present.
	out := strings.TrimSpace(string(args.Output))
	if out == "" || out == "null" {
		return ""
	}

	// When output is a structured object with an empty message, prefer the
	// top-level message. This happens during interrogation: the agent puts
	// the diagnostic answer in the communicate message field and leaves
	// output.message empty.
	if msg != "" {
		var outMap map[string]any
		if err := json.Unmarshal(args.Output, &outMap); err == nil {
			outMsg, _ := outMap[keyMessage].(string)
			if strings.TrimSpace(outMsg) == "" {
				return msg
			}
		}
	}

	return out
}
