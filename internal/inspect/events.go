package inspect

import (
	"encoding/json"
	"strings"

	"primeradiant.com/toil/internal/state"
)

const kindSteeringInjected = "STEERING_INJECTED"

// ParseRunnerEvent attempts to extract structured data from a node_output event.
// Returns false if the event is not a node_output or the text is not parseable.
func ParseRunnerEvent(event state.Event) (InnerEvent, bool) {
	if event.Type != eventNodeOutput {
		return InnerEvent{}, false
	}
	text := strings.TrimSpace(event.Text)
	if text == "" || text[0] != '{' {
		return InnerEvent{}, false
	}

	var raw struct {
		Kind      string          `json:"kind"`
		Data      json.RawMessage `json:"data"`
		Type      string          `json:"type"`
		SessionID string          `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return InnerEvent{}, false
	}

	inner := InnerEvent{NodeID: event.NodeID}
	switch {
	case raw.Kind != "":
		inner.Kind = raw.Kind
	case raw.Type != "":
		inner.Kind = raw.Type
	default:
		return InnerEvent{}, false
	}

	switch inner.Kind {
	case kindRoundTimings:
		var rt RoundTimings
		if err := json.Unmarshal(raw.Data, &rt); err == nil {
			inner.RoundTimings = &rt
		}
	case kindSessionStart:
		var ss SessionStart
		if len(raw.Data) > 0 {
			_ = json.Unmarshal(raw.Data, &ss)
		}
		if raw.SessionID != "" {
			ss.SessionID = raw.SessionID
		}
		inner.SessionStart = &ss
	case kindToolCallStart:
		var tc ToolCall
		if err := json.Unmarshal(raw.Data, &tc); err == nil {
			inner.ToolCall = &tc
			if tc.Name == toolNameCommunicate {
				inner.Communicate = parseCommunicate(tc.ArgumentsJSON)
			}
		}
	case kindToolCallOutputDelta:
		var delta struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(raw.Data, &delta); err == nil {
			if strings.HasPrefix(delta.Delta, "tool args schema validation failed:") {
				inner.SchemaError = delta.Delta
			}
		}
	case kindAssistantTextEnd:
		var ate struct {
			Usage struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				CacheReadTokens    int `json:"cache_read_tokens"`
				CacheWriteTokens   int `json:"cache_write_tokens"`
				CacheWrite1hTokens int `json:"cache_write_1h_tokens"`
				ReasoningTokens    int `json:"reasoning_tokens"`
			} `json:"usage"`
			Model string `json:"model"`
		}
		if err := json.Unmarshal(raw.Data, &ate); err == nil {
			inner.Usage = &Usage{
				InputTokens:        ate.Usage.InputTokens,
				OutputTokens:       ate.Usage.OutputTokens,
				CacheReadTokens:    ate.Usage.CacheReadTokens,
				CacheWriteTokens:   ate.Usage.CacheWriteTokens,
				CacheWrite1hTokens: ate.Usage.CacheWrite1hTokens,
				ReasoningTokens:    ate.Usage.ReasoningTokens,
				Model:              ate.Model,
			}
		}
	case kindSteeringInjected:
		var s struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw.Data, &s); err == nil {
			inner.SteeringText = s.Text
		}
	}

	return inner, true
}

func parseCommunicate(argsJSON string) *CommunicateOutput {
	var args struct {
		Output struct {
			Decision string         `json:"decision"`
			Message  string         `json:"message"`
			Data     map[string]any `json:"data"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil
	}
	if args.Output.Decision == "" {
		return nil
	}
	return &CommunicateOutput{
		Decision: args.Output.Decision,
		Message:  args.Output.Message,
		Data:     args.Output.Data,
	}
}

// ChildRun extracts the child_run ID from a NodeState's untyped Data map.
func ChildRun(node *state.NodeState) string {
	if node == nil || node.Data == nil {
		return ""
	}
	if cr, ok := node.Data["child_run"].(string); ok {
		return cr
	}
	return ""
}

// DetectAttemptBoundaries returns the event indices where each attempt
// begins for a given node. Each node_started event marks a new attempt.
func DetectAttemptBoundaries(events []state.Event, nodeID string) []int {
	var boundaries []int
	for i, e := range events {
		if e.Type == eventNodeStarted && e.NodeID == nodeID {
			boundaries = append(boundaries, i)
		}
	}
	return boundaries
}
