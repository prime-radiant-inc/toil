package inspect

import (
	"fmt"
	"strings"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("errors", func(rs *state.RunState) Processor { return NewErrorsProcessor(rs) })
}

// ErrorsResult holds all detected errors across the run.
type ErrorsResult struct {
	Errors []ErrorEntry `json:"errors"`
}

// ErrorEntry describes a single detected error event.
type ErrorEntry struct {
	Type    string `json:"type"` // schema_validation, steering, silent_exit, tool_error
	Node    string `json:"node"`
	Attempt int    `json:"attempt"`
	Message string `json:"message"`
	Ts      string `json:"ts"` // ISO 8601 UTC
}

type errorsProcessor struct {
	rs       *state.RunState
	errors   []ErrorEntry
	attempts map[string]int // nodeID -> current attempt number
	changed  bool
}

func NewErrorsProcessor(rs *state.RunState) *errorsProcessor {
	return &errorsProcessor{
		rs:       rs,
		attempts: make(map[string]int),
	}
}

func (p *errorsProcessor) ProcessEvent(event state.Event) {
	if event.Type == eventNodeStarted {
		p.attempts[event.NodeID]++
		return
	}

	if event.Type == eventNodeFailed {
		p.processSilentExit(event)
		return
	}

	inner, ok := ParseRunnerEvent(event)
	if !ok {
		return
	}

	if inner.SchemaError != "" {
		p.addError(ErrorEntry{
			Type:    errorTypeSchemaValidation,
			Node:    inner.NodeID,
			Attempt: p.currentAttempt(inner.NodeID),
			Message: inner.SchemaError,
			Ts:      formatTS(event.Timestamp),
		})
		return
	}

	if inner.SteeringText != "" {
		p.addError(ErrorEntry{
			Type:    flowTypeSteering,
			Node:    inner.NodeID,
			Attempt: p.currentAttempt(inner.NodeID),
			Message: inner.SteeringText,
			Ts:      formatTS(event.Timestamp),
		})
		return
	}

	// Note: tool_error detection removed. Generic string matching on
	// TOOL_CALL_OUTPUT_DELTA produces false positives (e.g., file content
	// containing the word "error"). Schema validation errors are already
	// caught by the SchemaError check above.
}

// processSilentExit detects node_failed events with no meaningful text/output.
func (p *errorsProcessor) processSilentExit(event state.Event) {
	if strings.TrimSpace(event.Text) != "" {
		return
	}
	if event.Data == nil {
		return
	}
	if _, hasExitCode := event.Data["exit_code"]; !hasExitCode {
		return
	}

	p.addError(ErrorEntry{
		Type:    "silent_exit",
		Node:    event.NodeID,
		Attempt: p.currentAttempt(event.NodeID),
		Message: exitCodeMessage(event.Data),
		Ts:      formatTS(event.Timestamp),
	})
}

func (p *errorsProcessor) addError(e ErrorEntry) {
	p.errors = append(p.errors, e)
	p.changed = true
}

func (p *errorsProcessor) currentAttempt(nodeID string) int {
	attempt := p.attempts[nodeID]
	if attempt == 0 {
		return 1
	}
	return attempt
}

func (p *errorsProcessor) Changed() bool {
	return p.changed
}

func (p *errorsProcessor) Result() any {
	p.changed = false
	errors := p.errors
	if errors == nil {
		errors = []ErrorEntry{}
	}
	return ErrorsResult{Errors: errors}
}

// exitCodeMessage builds a human-readable message from Data containing exit_code/error.
func exitCodeMessage(data map[string]any) string {
	if errMsg, ok := data[keyError].(string); ok && errMsg != "" {
		return errMsg
	}
	if code, ok := data["exit_code"]; ok {
		return fmt.Sprintf("exit code: %v", code)
	}
	return "silent exit"
}
