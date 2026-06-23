package inspect

import "primeradiant.com/toil/internal/state"

const (
	eventNodeStarted    = "node_started"
	eventNodeCompleted  = "node_completed"
	eventNodeFailed     = "node_failed"
	eventNodeOutput     = "node_output"
	eventNodePrompt     = "node_prompt"
	eventNodeEdgePrompt = "node_edge_prompt"

	kindRoundTimings        = "ROUND_TIMINGS"
	kindSessionStart        = "SESSION_START"
	kindToolCallStart       = "TOOL_CALL_START"
	kindToolCallOutputDelta = "TOOL_CALL_OUTPUT_DELTA"
	kindAssistantTextEnd    = "ASSISTANT_TEXT_END"

	flowTypeCompleted    = "completed"
	flowTypeDecision     = "decision"
	flowTypeSteering     = "steering"
	flowTypeLoopDetected = "loop_detected"

	errorTypeSchemaValidation = "schema_validation"

	toolNameCommunicate = "communicate"

	keyError = "error"
)

// Processor is the core interface for all inspect aspects.
type Processor interface {
	ProcessEvent(event state.Event)
	Changed() bool
	Result() any
}

// RunLoader provides access to state and events for other runs.
type RunLoader interface {
	LoadState(runID string) (*state.RunState, error)
	LoadEvents(runID string) ([]state.Event, error)
}

// InnerEvent holds parsed data from a runner's structured output
// embedded inside a node_output event.
type InnerEvent struct {
	NodeID       string
	Kind         string
	RoundTimings *RoundTimings
	SessionStart *SessionStart
	ToolCall     *ToolCall
	Communicate  *CommunicateOutput
	Usage        *Usage
	SteeringText string
	SchemaError  string
}

// Usage holds per-turn token usage from ASSISTANT_TEXT_END events.
// Field semantics match serf's llm.Usage invariant: InputTokens is new
// uncached input only; CacheReadTokens is subset of "total processed";
// CacheWriteTokens is cache-creation tokens (Anthropic cache_creation_input_tokens).
// ReasoningTokens is metadata only — already inside OutputTokens for billing.
type Usage struct {
	InputTokens        int    `json:"input_tokens"`
	OutputTokens       int    `json:"output_tokens"`
	CacheReadTokens    int    `json:"cache_read_tokens"`
	CacheWriteTokens   int    `json:"cache_write_tokens"`
	CacheWrite1hTokens int    `json:"cache_write_1h_tokens"`
	ReasoningTokens    int    `json:"reasoning_tokens"`
	Model              string `json:"model"`
}

type RoundTimings struct {
	Round           int   `json:"round"`
	TotalRoundNs    int64 `json:"total_round_ns"`
	InputTokens     int   `json:"input_tokens"`
	OutputTokens    int   `json:"output_tokens"`
	CacheReadTokens int   `json:"cache_read_tokens"`
	ReasoningTokens int   `json:"reasoning_tokens"`
}

type SessionStart struct {
	Profile   string `json:"profile"`
	Model     string `json:"model"`
	SessionID string `json:"session_id"`
}

type ToolCall struct {
	Name          string `json:"tool_name"`
	CallID        string `json:"call_id"`
	ArgumentsJSON string `json:"arguments_json"`
}

type CommunicateOutput struct {
	Decision string         `json:"decision"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data"`
}
