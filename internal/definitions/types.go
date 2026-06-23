package definitions

type Runner struct {
	ID         string            `yaml:"id"`
	Type       string            `yaml:"type"`
	Command    string            `yaml:"command,omitempty"`
	Args       []string          `yaml:"args,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	TimeoutSec int               `yaml:"timeout_sec,omitempty"`
	Resume     bool              `yaml:"resume,omitempty"`
}

type InputSpec struct {
	Type        string `yaml:"type,omitempty"`
	Optional    bool   `yaml:"optional,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type Workflow struct {
	ID                string               `yaml:"id"`
	Name              string               `yaml:"name"`
	Version           int                  `yaml:"version"`
	Description       string               `yaml:"description,omitempty"`
	PromptInputsMode  string               `yaml:"prompt_inputs_mode,omitempty"`
	Inputs            map[string]string    `yaml:"inputs,omitempty"`
	InputSchema       map[string]InputSpec `yaml:"input_schema,omitempty"`
	Outputs           map[string]string    `yaml:"outputs,omitempty"`
	WorkspaceDefaults *Workspace           `yaml:"workspace_defaults,omitempty"`
	Nodes             []Node               `yaml:"nodes"`
	Edges             []Edge               `yaml:"edges"`
	Limits            map[string]int       `yaml:"limits,omitempty"`
	Tags              []string             `yaml:"tags,omitempty"`
	RunnerOverrides   map[string]string    `yaml:"runner_overrides,omitempty"` // tag -> runner ID
	RetryTarget       string               `yaml:"retry_target,omitempty"`
	ContextDefault    string               `yaml:"context_default,omitempty"`
	Interview         string               `yaml:"interview,omitempty"`
	SourcePath        string               `yaml:"-"`
}

const (
	InterviewNever     = "never"
	InterviewOnFailure = "on_failure"
	InterviewOnIssue   = "on_issue"
)

// InterviewMode returns the interview trigger mode for this workflow.
// Defaults to "never" when the field is not set.
func (w *Workflow) InterviewMode() string {
	switch w.Interview {
	case InterviewOnFailure, InterviewOnIssue:
		return w.Interview
	default:
		return InterviewNever
	}
}

type Workspace struct {
	Mode   string `yaml:"mode"`
	Group  string `yaml:"group,omitempty"`
	Path   string `yaml:"path,omitempty"`
	Access string `yaml:"access,omitempty"`
}

type Node struct {
	ID               string            `yaml:"id"`
	Kind             string            `yaml:"kind"`
	Role             string            `yaml:"role,omitempty"`
	Runner           string            `yaml:"runner,omitempty"`
	RunnerEnv        map[string]string `yaml:"runner_env,omitempty"`
	PromptInputsMode string            `yaml:"prompt_inputs_mode,omitempty"`
	Tags             []string          `yaml:"tags,omitempty"`
	Workflow         string            `yaml:"workflow,omitempty"`
	Prompt           string            `yaml:"prompt,omitempty"`
	Output           *EmitOutput       `yaml:"output,omitempty"`
	Context          string            `yaml:"context,omitempty"`
	SessionID        string            `yaml:"session_id,omitempty"`
	Retry            *RetryPolicy      `yaml:"retry,omitempty"`
	Inputs           map[string]any    `yaml:"inputs,omitempty"`
	OutputsSchema    map[string]any    `yaml:"outputs_schema,omitempty"`
	Decisions        DecisionList      `yaml:"decisions,omitempty"`
	Gate             string            `yaml:"gate,omitempty"`
	GoalGate         bool              `yaml:"goal_gate,omitempty"`
	PromptOnResume   bool              `yaml:"prompt_on_resume,omitempty"`
	RetryTarget      string            `yaml:"retry_target,omitempty"`
	TimeoutSec       int               `yaml:"timeout_sec,omitempty"`
	Loop             *Loop             `yaml:"loop,omitempty"`
	// LoopExhaustionPolicy is the author's explicit opt-out from the
	// "looping node should have a _loop_exhausted edge" lint. Empty
	// (default) means the lint applies: if the node can loop and the
	// workflow declares max_loop_iterations, the loader emits a warning
	// when the node has no outgoing when: _loop_exhausted edge. Set to
	// "fatal" to explicitly accept the legacy fatal-exhaustion behavior
	// and silence the warning. Allowed values: "" (default), "fatal".
	LoopExhaustionPolicy string     `yaml:"loop_exhaustion,omitempty"`
	ForEach              *ForEach   `yaml:"for_each,omitempty"`
	Join                 string     `yaml:"join,omitempty"`
	Workspace            *Workspace `yaml:"workspace,omitempty"`
	MaxTurns             int        `yaml:"max_turns,omitempty"`
}

// EmitOutput is the declarative envelope for kind:emit nodes. Decision
// must be a literal string in the node's Decisions list (no ${...}
// interpolation). Message is a string template (may interpolate
// ${input.X} etc. — evaluated in Phase 5). Data is a recursive map
// whose leaf values may be ${...} expression strings or literals; type
// preservation applies to leaves that are a single ${...} expression.
type EmitOutput struct {
	Decision string         `yaml:"decision"`
	Message  string         `yaml:"message"`
	Data     map[string]any `yaml:"data,omitempty"`
}

type Loop struct {
	On     string `yaml:"on"`
	BackTo string `yaml:"back_to"`
}

type ForEach struct {
	List      string `yaml:"list"`
	Item      string `yaml:"item"`
	DependsOn string `yaml:"depends_on,omitempty"`
	Body      string `yaml:"body,omitempty"`
}

type RetryPolicy struct {
	Max          int    `yaml:"max"`
	Backoff      string `yaml:"backoff,omitempty"`
	InitialDelay string `yaml:"initial_delay,omitempty"`
	MaxDelay     string `yaml:"max_delay,omitempty"`
	Jitter       bool   `yaml:"jitter,omitempty"`
}

type Edge struct {
	From   string         `yaml:"from"`
	To     string         `yaml:"to"`
	When   string         `yaml:"when,omitempty"`
	Prompt string         `yaml:"prompt,omitempty"`
	Passes map[string]any `yaml:"passes,omitempty"`
	Failed *bool          `yaml:"failed,omitempty"`
}
