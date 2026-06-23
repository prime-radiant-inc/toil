package runners

import "context"

const streamStdout = "stdout"

const (
	commandClaude = "claude"
	commandCodex  = "codex"
	commandSerf   = "serf"
	commandBash   = "bash"

	subcommandExec = "exec"

	flagVerbose       = "--verbose"
	flagJSON          = "--json"
	flagStreamJSON    = "stream-json"
	flagBypassSandbox = "--dangerously-bypass-approvals-and-sandbox"

	typeText   = "text"
	typeUser   = "user"
	typeResult = "result"
	keyMessage = "message"

	kindToolCallStart = "TOOL_CALL_START"
	toolCommunicate   = "communicate"
)

type Line struct {
	Stream string
	Text   string
}

type LineHandler func(Line)

type Request struct {
	Prompt           string
	Workspace        string
	Decisions        []string
	SessionID        string
	Resume           bool
	Fork             bool
	Env              map[string]string
	MaxTurns         int
	OutputSchemaJSON []byte
}

type Result struct {
	Output    string
	Stderr    string
	SessionID string
	ExitCode  int
	ToolCalls int
}

type Runner interface {
	Run(ctx context.Context, request Request, handler LineHandler) (Result, error)
}

type Config struct {
	Command    string
	Args       []string
	Env        map[string]string
	TimeoutSec int
	Resume     bool
}
