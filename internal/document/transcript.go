package document

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"primeradiant.com/toil/internal/state"
)

var (
	markdownRenderer = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.TaskList,
			extension.Strikethrough,
			extension.Table,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
		),
	)
	markdownSanitizer = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")
		return p
	}()
)

// renderMarkdownToHTML converts markdown text to sanitized HTML.
// Returns an empty string for empty input or on render error (the frontend
// falls back to text rendering in those cases).
func renderMarkdownToHTML(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := markdownRenderer.Convert([]byte(text), &buf); err != nil {
		return ""
	}
	return markdownSanitizer.Sanitize(buf.String())
}

// serfEventProbe probes the "kind" field of a serf NDJSON line.
type serfEventProbe struct {
	Kind string `json:"kind"`
}

// serfToolCallStart is a TOOL_CALL_START event emitted by serf.
type serfToolCallStart struct {
	Data struct {
		ToolName      string `json:"tool_name"`
		CallID        string `json:"call_id"`
		ArgumentsJSON string `json:"arguments_json"`
	} `json:"data"`
}

// serfToolCallEnd is a TOOL_CALL_END event emitted by serf.
type serfToolCallEnd struct {
	Data struct {
		CallID  string `json:"call_id"`
		Output  string `json:"output"`
		IsError bool   `json:"is_error"`
	} `json:"data"`
}

// BuildTranscript walks the event slice for a node and produces a
// chronological transcript: attempts → messages. Events for other nodes
// are ignored.
//
// Attempts are demarcated by node_attempt_started events. If no
// node_attempt_started events are present (older runs predating that
// feature), node_started events are used as attempt markers instead.
//
// Tool calls are parsed inline from node_output events whose Text field
// contains serf NDJSON (TOOL_CALL_START / TOOL_CALL_END). Other serf
// event kinds (SESSION_START, TOOL_CALL_OUTPUT_DELTA, etc.) are ignored.
// Structured-output JSON blobs (agent's final {decision,message,...}
// payload) are suppressed. Non-JSON node_output lines are treated as
// plain assistant text.
//
// node_prompt is split into system_prompt (role boilerplate) and
// user_prompt (the LOCAL section) using ExtractLocalPrompt.
func BuildTranscript(nodeID string, events []state.Event) Transcript {
	tr := Transcript{}
	var current *Attempt
	pendingCalls := map[string]int{} // call_id → message index within current.Messages

	ensureAttempt := func(ordinal int) {
		if current != nil && current.Ordinal == ordinal {
			return
		}
		tr.Attempts = append(tr.Attempts, Attempt{Ordinal: ordinal})
		current = &tr.Attempts[len(tr.Attempts)-1]
		pendingCalls = map[string]int{}
	}

	appendAssistantText := func(text string) {
		if current == nil {
			ensureAttempt(1)
		}
		// Coalesce consecutive assistant text messages.
		if n := len(current.Messages); n > 0 && current.Messages[n-1].Kind == kindAssistant {
			current.Messages[n-1].Text += text
			return
		}
		current.Messages = append(current.Messages, Message{Kind: kindAssistant, Text: text})
	}

	// Bug 1: detect whether attempt-level events are present; if not, fall
	// back to using node_started as attempt markers.
	hasAttemptEvents := false
	for _, ev := range events {
		if ev.NodeID == nodeID && ev.Type == eventNodeAttemptStarted {
			hasAttemptEvents = true
			break
		}
	}
	nodeStartedCount := 0 // used only in fallback mode

	for _, ev := range events {
		if ev.NodeID != nodeID {
			continue
		}
		switch ev.Type {
		case eventNodeStarted:
			// Bug 1 fallback: use node_started as attempt markers when no
			// node_attempt_started events exist in the stream.
			if !hasAttemptEvents {
				nodeStartedCount++
				ensureAttempt(nodeStartedCount)
			}
		case eventNodePrompt:
			// Bug 3: split into system_prompt (boilerplate) + user_prompt (LOCAL).
			//
			// In fallback mode (no node_attempt_started), node_prompt precedes
			// node_started for each attempt. When the previous attempt has already
			// been closed (Outcome != ""), pre-open the next attempt slot so the
			// prompt lands in the right attempt rather than appending to the
			// finished one. The subsequent node_started will be a no-op for this
			// ordinal since it's already current.
			if !hasAttemptEvents && current != nil && current.Outcome != "" {
				ensureAttempt(nodeStartedCount + 1)
			} else {
				ensureAttempt(currentOrdinalOr1(current))
			}
			local, boilerplate := ExtractLocalPrompt(ev.Text)
			if boilerplate != "" {
				current.Messages = append(current.Messages, Message{
					Kind: "system_prompt",
					Text: boilerplate,
				})
			}
			if local != "" {
				current.Messages = append(current.Messages, Message{
					Kind: kindUserPrompt,
					Text: local,
				})
			} else if boilerplate == "" {
				// No LOCAL markers — fall back to whole text as user_prompt.
				current.Messages = append(current.Messages, Message{
					Kind: kindUserPrompt,
					Text: ev.Text,
				})
			}
		case eventNodeAttemptStarted:
			ord := intField(ev.Data, "attempt", 1)
			ensureAttempt(ord)
		case eventNodeOutput:
			if ev.Text == "" {
				continue
			}
			// Try to parse as serf NDJSON.
			var probe serfEventProbe
			if err := json.Unmarshal([]byte(ev.Text), &probe); err == nil && probe.Kind != "" {
				switch probe.Kind {
				case "TOOL_CALL_START":
					var start serfToolCallStart
					if err := json.Unmarshal([]byte(ev.Text), &start); err != nil {
						continue
					}
					ensureAttempt(currentOrdinalOr1(current))
					args := map[string]any{}
					if start.Data.ArgumentsJSON != "" {
						_ = json.Unmarshal([]byte(start.Data.ArgumentsJSON), &args)
					}
					current.Messages = append(current.Messages, Message{
						Kind: kindToolCall,
						ToolCall: &MessageTool{
							ToolID:   start.Data.CallID,
							ToolName: start.Data.ToolName,
							Args:     args,
						},
					})
					pendingCalls[start.Data.CallID] = len(current.Messages) - 1
				case "TOOL_CALL_END":
					if current == nil {
						continue
					}
					var end serfToolCallEnd
					if err := json.Unmarshal([]byte(ev.Text), &end); err != nil {
						continue
					}
					idx, ok := pendingCalls[end.Data.CallID]
					if !ok {
						continue
					}
					contentRaw, _ := json.Marshal(end.Data.Output)
					current.Messages[idx].ToolCall.Result = &MessageToolResult{
						IsError: end.Data.IsError,
						Content: contentRaw,
					}
					delete(pendingCalls, end.Data.CallID)
				default:
					// Other serf event kinds (SESSION_START, TOOL_CALL_OUTPUT_DELTA, ...) — ignore.
				}
				continue
			}
			// Bug 2: suppress the agent's final structured-output dump
			// ({decision, message, data, artifacts}). This payload is not
			// a serf NDJSON event (no "kind" field), so it would otherwise
			// fall through to appendAssistantText as a wall of raw JSON.
			var probe2 map[string]any
			if err := json.Unmarshal([]byte(ev.Text), &probe2); err == nil {
				if _, hasDecision := probe2[kindDecision]; hasDecision {
					if _, hasMessage := probe2["message"]; hasMessage {
						// Structured-output dump — drop it.
						continue
					}
				}
			}
			// Not serf JSON and not structured-output: plain assistant text.
			appendAssistantText(ev.Text)
		case "node_attempt_failed":
			ord := intField(ev.Data, "attempt", currentOrdinalOr1(current))
			ensureAttempt(ord)
			current.Outcome = outcomeFailed
			if r, ok := ev.Data["reason"].(string); ok {
				current.FailureReason = r
			}
		case eventNodeCompleted:
			ord := intField(ev.Data, "attempt", currentOrdinalOr1(current))
			ensureAttempt(ord)
			current.Outcome = outcomeSucceeded
			if dec, ok := ev.Data[kindDecision].(string); ok && dec != "" {
				current.Messages = append(current.Messages, Message{
					Kind:     kindDecision,
					Decision: &MessageDecision{ID: dec},
				})
			}
		}
	}

	// Render markdown to HTML for message kinds that display rich text.
	// system_prompt is shown verbatim (pre block) — no HTML rendering.
	for ai := range tr.Attempts {
		for mi := range tr.Attempts[ai].Messages {
			m := &tr.Attempts[ai].Messages[mi]
			if m.Kind == kindAssistant || m.Kind == kindUserPrompt {
				m.HTML = renderMarkdownToHTML(m.Text)
			}
		}
	}

	return tr
}

func currentOrdinalOr1(a *Attempt) int {
	if a == nil {
		return 1
	}
	return a.Ordinal
}

func intField(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}
