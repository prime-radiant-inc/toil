package dashboard

import (
	"encoding/json"
	"strings"
	"time"
)

// Transcript item kind values.
const (
	transcriptKindMessage = "message"
	transcriptKindTool    = "tool"
	transcriptKindDivider = "divider"
)

// Anthropic/OpenAI block type values used in parsing.
const (
	blockTypeToolResult = "tool_result"
)

// CSS badge classes shared across the dashboard.
const (
	badgeClassMuted = "bg-edge-light text-muted"
)

// Transcript item role values.
const (
	transcriptRoleAssistant = "assistant"
	transcriptRoleUser      = "user"
	transcriptRoleTool      = "tool"
	transcriptRoleSystem    = "system"
)

// TranscriptItem represents a single rendered item in a node's transcript.
type TranscriptItem struct {
	Kind      string         `json:"kind"`        // "message", "tool", "prompt", "divider"
	Role      string         `json:"role"`        // "assistant", "user", "system", "tool"
	Text      string         `json:"text"`        // message content (markdown)
	ToolName  string         `json:"tool_name"`   // for tool items
	ToolUseID string         `json:"tool_use_id"` // for deduplication/merging
	Input     map[string]any `json:"input"`       // tool input parameters
	Output    string         `json:"output"`      // tool output text
	IsError   bool           `json:"is_error"`    // tool result was an error
	Timestamp time.Time      `json:"timestamp"`   // event time

	// ToolState is the optional `tool_state` snapshot carried on a serf
	// TOOL_CALL_END event — the authoritative post-mutation state a tool
	// exposes for richer rendering (e.g. task_list emits the full task list).
	// nil for tools that don't produce one.
	ToolState any `json:"tool_state,omitempty"`

	// Divider fields (kind == "divider")
	Attempt    int    `json:"attempt,omitempty"`    // 1-based execution number
	SessionID  string `json:"session_id,omitempty"` // runner session ID
	Decision   string `json:"decision,omitempty"`   // node decision (on end divider)
	DurationMs int64  `json:"duration_ms,omitempty"`
	IsEnd      bool   `json:"is_end,omitempty"`   // true for session-end dividers
	IsCycle    bool   `json:"is_cycle,omitempty"` // true when this is a graph cycle, not a retry
}

// ExtractTranscriptItems parses an event text blob into transcript items.
// Handles Anthropic, OpenAI, and raw tool_call/tool_result formats.
func ExtractTranscriptItems(text string, timestamp time.Time) []TranscriptItem {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		// Not JSON — treat as plain text message
		return []TranscriptItem{{Kind: transcriptKindMessage, Role: transcriptRoleAssistant, Text: text, Timestamp: timestamp}}
	}

	// Serf verbose NDJSON events use "kind" instead of "type".
	if kind, _ := parsed["kind"].(string); kind != "" {
		return parseSerfEvent(parsed, kind, timestamp)
	}

	eventType, _ := parsed["type"].(string)

	// Anthropic assistant message
	if eventType == transcriptRoleAssistant {
		return parseAnthropicMessage(parsed, transcriptRoleAssistant, timestamp)
	}

	// Anthropic user message (may contain tool_result blocks)
	if eventType == "user" {
		return parseAnthropicMessage(parsed, transcriptRoleUser, timestamp)
	}

	// Ignored event types
	ignored := map[string]bool{
		transcriptRoleSystem: true, fieldResult: true,
		"thread.started": true, "thread.updated": true,
		"session.started": true, "session.updated": true,
	}
	if ignored[eventType] {
		return nil
	}

	// OpenAI item.completed
	if eventType == "item.completed" {
		return parseOpenAIItem(parsed, timestamp)
	}

	// Raw tool_call / tool_result
	if eventType == "tool_call" {
		return []TranscriptItem{parseToolUse(parsed, timestamp)}
	}
	if eventType == blockTypeToolResult {
		return []TranscriptItem{parseToolResult(parsed, timestamp)}
	}

	return nil
}

func parseAnthropicMessage(parsed map[string]any, role string, ts time.Time) []TranscriptItem {
	message, _ := parsed[transcriptKindMessage].(map[string]any)
	if message == nil {
		text, _ := parsed[fieldText].(string)
		if text != "" {
			return []TranscriptItem{{Kind: transcriptKindMessage, Role: role, Text: text, Timestamp: ts}}
		}
		return nil
	}
	content, _ := message[fieldContent].([]any)
	if content == nil {
		return nil
	}
	var items []TranscriptItem
	for _, entry := range content {
		block, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case fieldText:
			text, _ := block[fieldText].(string)
			if strings.TrimSpace(text) != "" {
				items = append(items, TranscriptItem{Kind: transcriptKindMessage, Role: role, Text: text, Timestamp: ts})
			}
		case "tool_use":
			items = append(items, parseToolUse(block, ts))
		case blockTypeToolResult:
			items = append(items, parseToolResult(block, ts))
		}
	}
	return items
}

func parseToolUse(block map[string]any, ts time.Time) TranscriptItem {
	name := firstString(block, "name", "tool_name", "toolName", "tool")
	if name == "" {
		name = transcriptRoleTool
	}
	id := firstString(block, "id", "tool_use_id", "toolUseId")
	input, _ := block["input"].(map[string]any)
	if input == nil {
		if args, ok := block["arguments"].(map[string]any); ok {
			input = args
		}
	}
	return TranscriptItem{
		Kind:      transcriptKindTool,
		Role:      transcriptRoleTool,
		ToolName:  name,
		ToolUseID: id,
		Input:     input,
		Timestamp: ts,
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func parseToolResult(block map[string]any, ts time.Time) TranscriptItem {
	id := firstString(block, "tool_use_id", "toolUseId", "id")
	output := extractOutput(block)
	isError := boolField(block, "is_error") || boolField(block, "isError")
	return TranscriptItem{
		Kind:      transcriptKindTool,
		Role:      transcriptRoleTool,
		ToolUseID: id,
		Output:    output,
		IsError:   isError,
		Timestamp: ts,
	}
}

func extractOutput(block map[string]any) string {
	for _, key := range []string{fieldContent, fieldOutput, fieldResult} {
		if v, ok := block[key].(string); ok {
			return v
		}
		if v, ok := block[key]; ok && v != nil {
			data, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				return string(data)
			}
		}
	}
	return ""
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// MergeTranscriptItems merges tool_result items into matching tool_use items
// by ToolUseID. Items without a ToolUseID or without a matching use are kept as-is.
func MergeTranscriptItems(items []TranscriptItem) []TranscriptItem {
	useIndex := map[string]int{} // toolUseId -> index in result
	var result []TranscriptItem

	for _, item := range items {
		if item.Kind == transcriptKindTool && item.ToolUseID != "" {
			if idx, ok := useIndex[item.ToolUseID]; ok {
				if item.Output != "" {
					result[idx].Output = item.Output
				}
				if item.IsError {
					result[idx].IsError = true
				}
				if item.ToolState != nil {
					result[idx].ToolState = item.ToolState
				}
				continue
			}
			useIndex[item.ToolUseID] = len(result)
		}
		result = append(result, item)
	}
	return result
}

// RoleLabel returns a display label for a transcript role.
func RoleLabel(role string) string {
	switch role {
	case transcriptRoleAssistant:
		return "Assistant"
	case transcriptRoleUser:
		return "User"
	case transcriptRoleSystem:
		return "System"
	case transcriptRoleTool:
		return "Tool"
	default:
		return labelMessage
	}
}

// EventBadgeClass returns Tailwind CSS classes for an event type badge.
func EventBadgeClass(eventType string) string {
	classes := map[string]string{
		eventNodeOutput:        "bg-edge-light text-ink-light",
		"node_prompt":          "bg-indigo-100 text-indigo-700",
		eventNodeStarted:       decisionPillBlue,
		eventNodeCompleted:     decisionPillGreen,
		eventNodeFailed:        decisionPillRed,
		eventNodeFailedHandled: decisionPillAmber,
		"node_resume_degraded": decisionPillAmber,
		eventNodeSkipped:       badgeClassMuted,
		eventApprovalRequested: decisionPillAmber,
		eventApprovalResolved:  "bg-emerald-100 text-emerald-700",
		eventRunStarted:        decisionPillBlue,
		eventRunPaused:         decisionPillAmber,
		eventRunResumed:        decisionPillBlue,
		eventRunCompleted:      decisionPillGreen,
		eventRunFailed:         decisionPillRed,
		eventRunCancelled:      decisionPillGray,
	}
	if c, ok := classes[eventType]; ok {
		return c
	}
	return badgeClassMuted
}

func parseSerfEvent(parsed map[string]any, kind string, ts time.Time) []TranscriptItem {
	data, _ := parsed[fieldData].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}

	switch kind {
	case "ASSISTANT_TEXT_END":
		text, _ := data[fieldText].(string)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []TranscriptItem{{Kind: transcriptKindMessage, Role: transcriptRoleAssistant, Text: text, Timestamp: ts}}

	case "TOOL_CALL_START":
		name, _ := data["tool_name"].(string)
		callID, _ := data["call_id"].(string)
		var input map[string]any
		if argsJSON, ok := data["arguments_json"].(string); ok && argsJSON != "" {
			_ = json.Unmarshal([]byte(argsJSON), &input)
		}
		return []TranscriptItem{{
			Kind:      transcriptKindTool,
			Role:      transcriptRoleTool,
			ToolName:  name,
			ToolUseID: callID,
			Input:     input,
			Timestamp: ts,
		}}

	case "TOOL_CALL_END":
		callID, _ := data["call_id"].(string)
		output := firstString(data, fieldOutput, fieldResult)
		item := TranscriptItem{
			Kind:      transcriptKindTool,
			Role:      transcriptRoleTool,
			ToolUseID: callID,
			Output:    output,
			Timestamp: ts,
		}
		if state, ok := data["tool_state"]; ok && state != nil {
			item.ToolState = state
		}
		return []TranscriptItem{item}

	case "USER_INPUT":
		text, _ := data[fieldText].(string)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []TranscriptItem{{Kind: transcriptKindMessage, Role: transcriptRoleUser, Text: text, Timestamp: ts}}

	case "STEERING_INJECTED":
		text, _ := data[fieldText].(string)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []TranscriptItem{{Kind: transcriptKindMessage, Role: transcriptRoleSystem, Text: text, Timestamp: ts}}
	}

	// Ignore noise: SESSION_START, PROMPT_LOADED, ASSISTANT_TEXT_START,
	// TOOL_CALL_OUTPUT_DELTA, etc.
	return nil
}

func parseOpenAIItem(parsed map[string]any, ts time.Time) []TranscriptItem {
	item, _ := parsed["item"].(map[string]any)
	if item == nil {
		return nil
	}
	itemType, _ := item["type"].(string)

	switch {
	case itemType == "agent_message" || itemType == "assistant_message":
		text := firstString(item, fieldText, fieldContent)
		if text == "" {
			return nil
		}
		return []TranscriptItem{{Kind: transcriptKindMessage, Role: transcriptRoleAssistant, Text: text, Timestamp: ts}}

	case itemType == "tool_call":
		return []TranscriptItem{parseToolUse(item, ts)}

	case itemType == "tool_result":
		return []TranscriptItem{parseToolResult(item, ts)}

	case strings.Contains(itemType, "tool"):
		return []TranscriptItem{parseToolUse(item, ts)}
	}

	return nil
}
