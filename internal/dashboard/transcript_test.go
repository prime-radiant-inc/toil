package dashboard

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestExtractTranscriptItems_AnthropicAssistantText(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Kind != transcriptKindMessage {
		t.Errorf("expected kind=message, got %q", item.Kind)
	}
	if item.Role != transcriptRoleAssistant {
		t.Errorf("expected role=assistant, got %q", item.Role)
	}
	if item.Text != "Hello world" {
		t.Errorf("expected text='Hello world', got %q", item.Text)
	}
}

func TestExtractTranscriptItems_AnthropicToolUse(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu_123","name":"read_file","input":{"path":"/tmp/foo"}}]}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Kind != transcriptKindTool {
		t.Errorf("expected kind=tool, got %q", item.Kind)
	}
	if item.ToolName != "read_file" {
		t.Errorf("expected tool_name=read_file, got %q", item.ToolName)
	}
	if item.ToolUseID != "tu_123" {
		t.Errorf("expected tool_use_id=tu_123, got %q", item.ToolUseID)
	}
	if item.Input["path"] != "/tmp/foo" {
		t.Errorf("expected input.path=/tmp/foo, got %v", item.Input["path"])
	}
}

func TestExtractTranscriptItems_AnthropicUserText(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"user","message":{"content":[{"type":"text","text":"Fix the bug"}]}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Role != transcriptRoleUser {
		t.Errorf("expected role=user, got %q", items[0].Role)
	}
	if items[0].Text != "Fix the bug" {
		t.Errorf("expected text='Fix the bug', got %q", items[0].Text)
	}
}

func TestExtractTranscriptItems_AnthropicToolResult(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_123","content":"file contents here"}]}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != transcriptKindTool {
		t.Errorf("expected kind=tool, got %q", items[0].Kind)
	}
	if items[0].ToolUseID != "tu_123" {
		t.Errorf("expected tool_use_id=tu_123, got %q", items[0].ToolUseID)
	}
	if items[0].Output != "file contents here" {
		t.Errorf("expected output='file contents here', got %q", items[0].Output)
	}
}

func TestExtractTranscriptItems_OpenAIItemCompleted(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"item.completed","item":{"type":"agent_message","text":"Done with task"}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Role != transcriptRoleAssistant {
		t.Errorf("expected role=assistant, got %q", items[0].Role)
	}
	if items[0].Text != "Done with task" {
		t.Errorf("expected text='Done with task', got %q", items[0].Text)
	}
}

func TestExtractTranscriptItems_OpenAIToolCall(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"item.completed","item":{"type":"tool_call","name":"bash","id":"call_1","input":{"command":"ls"}}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ToolName != "bash" {
		t.Errorf("expected tool_name=bash, got %q", items[0].ToolName)
	}
}

func TestExtractTranscriptItems_RawToolCall(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"tool_call","name":"write_file","id":"tc_1","input":{"path":"/tmp/x","content":"hello"}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != transcriptKindTool {
		t.Errorf("expected kind=tool, got %q", items[0].Kind)
	}
	if items[0].ToolName != "write_file" {
		t.Errorf("expected tool_name=write_file, got %q", items[0].ToolName)
	}
}

func TestExtractTranscriptItems_RawToolResult(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"type":"tool_result","tool_use_id":"tc_1","content":"success"}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Output != "success" {
		t.Errorf("expected output='success', got %q", items[0].Output)
	}
}

func TestExtractTranscriptItems_IgnoredTypes(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, typ := range []string{"system", "result", "thread.started", "session.started"} {
		input := `{"type":"` + typ + `","data":"ignored"}`
		items := ExtractTranscriptItems(input, ts)
		if len(items) != 0 {
			t.Errorf("expected 0 items for type=%q, got %d", typ, len(items))
		}
	}
}

func TestExtractTranscriptItems_PlainText(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := ExtractTranscriptItems("just some text", ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Text != "just some text" {
		t.Errorf("expected text='just some text', got %q", items[0].Text)
	}
}

func TestExtractTranscriptItems_EmptyInput(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if items := ExtractTranscriptItems("", ts); len(items) != 0 {
		t.Errorf("expected 0 items for empty, got %d", len(items))
	}
	if items := ExtractTranscriptItems("  ", ts); len(items) != 0 {
		t.Errorf("expected 0 items for whitespace, got %d", len(items))
	}
}

func TestMergeTranscriptItems(t *testing.T) {
	items := []TranscriptItem{
		{Kind: "tool", ToolUseID: "tu_1", ToolName: "bash", Input: map[string]any{"cmd": "ls"}},
		{Kind: "message", Role: "assistant", Text: "Running command"},
		{Kind: "tool", ToolUseID: "tu_1", Output: "file1.go\nfile2.go"},
	}
	merged := MergeTranscriptItems(items)
	if len(merged) != 2 {
		t.Fatalf("expected 2 items after merge, got %d", len(merged))
	}
	tool := merged[0]
	if tool.ToolName != "bash" {
		t.Errorf("expected tool_name=bash, got %q", tool.ToolName)
	}
	if tool.Output != "file1.go\nfile2.go" {
		t.Errorf("expected output='file1.go\\nfile2.go', got %q", tool.Output)
	}
	if tool.Input["cmd"] != "ls" {
		t.Errorf("expected input.cmd=ls, got %v", tool.Input)
	}
}

func TestRoleLabel(t *testing.T) {
	tests := []struct{ input, expected string }{
		{"assistant", "Assistant"},
		{"user", "User"},
		{"system", "System"},
		{"tool", "Tool"},
		{"", "Message"},
	}
	for _, tt := range tests {
		if got := RoleLabel(tt.input); got != tt.expected {
			t.Errorf("RoleLabel(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEventBadgeClass(t *testing.T) {
	if got := EventBadgeClass("node_completed"); got != "bg-green-100 text-green-700" {
		t.Errorf("EventBadgeClass(node_completed) = %q", got)
	}
	if got := EventBadgeClass("unknown_event"); got == "" {
		t.Error("EventBadgeClass should return a default for unknown events")
	}
}

func TestExtractTranscriptItems_SerfAssistantText(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"ASSISTANT_TEXT_END","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{"text":"I'll read the file now.","usage":{"input_tokens":100,"output_tokens":20}}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != transcriptKindMessage {
		t.Errorf("expected kind=message, got %q", items[0].Kind)
	}
	if items[0].Role != transcriptRoleAssistant {
		t.Errorf("expected role=assistant, got %q", items[0].Role)
	}
	if items[0].Text != "I'll read the file now." {
		t.Errorf("expected text, got %q", items[0].Text)
	}
}

func TestExtractTranscriptItems_SerfToolCallStart(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"TOOL_CALL_START","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{"tool_name":"read_file","call_id":"call_abc","arguments_json":"{\"path\":\"/tmp/foo\"}"}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != transcriptKindTool {
		t.Errorf("expected kind=tool, got %q", items[0].Kind)
	}
	if items[0].ToolName != "read_file" {
		t.Errorf("expected tool_name=read_file, got %q", items[0].ToolName)
	}
	if items[0].ToolUseID != "call_abc" {
		t.Errorf("expected tool_use_id=call_abc, got %q", items[0].ToolUseID)
	}
	if items[0].Input["path"] != "/tmp/foo" {
		t.Errorf("expected input.path=/tmp/foo, got %v", items[0].Input["path"])
	}
}

func TestExtractTranscriptItems_SerfToolCallEnd(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"TOOL_CALL_END","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{"tool_name":"read_file","call_id":"call_abc","output":"file contents here"}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != transcriptKindTool {
		t.Errorf("expected kind=tool, got %q", items[0].Kind)
	}
	if items[0].ToolUseID != "call_abc" {
		t.Errorf("expected tool_use_id=call_abc, got %q", items[0].ToolUseID)
	}
	if items[0].Output != "file contents here" {
		t.Errorf("expected output, got %q", items[0].Output)
	}
}

func TestExtractTranscriptItems_SerfUserInput(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"USER_INPUT","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{"text":"implement the feature"}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Role != transcriptRoleUser {
		t.Errorf("expected role=user, got %q", items[0].Role)
	}
	if items[0].Text != "implement the feature" {
		t.Errorf("expected text, got %q", items[0].Text)
	}
}

func TestExtractTranscriptItems_SerfSteeringInjected(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"STEERING_INJECTED","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{"text":"You must use the communicate tool."}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Role != "system" {
		t.Errorf("expected role=system, got %q", items[0].Role)
	}
}

func TestExtractTranscriptItems_SerfIgnoredKinds(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{"SESSION_START", "PROMPT_LOADED", "ASSISTANT_TEXT_START", "TOOL_CALL_OUTPUT_DELTA"} {
		input := `{"kind":"` + kind + `","timestamp":"2026-01-01T00:00:00Z","session_id":"sess1","data":{}}`
		items := ExtractTranscriptItems(input, ts)
		if len(items) != 0 {
			t.Errorf("expected 0 items for %s, got %d", kind, len(items))
		}
	}
}

func TestRenderPartials(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("node-status-badge", func(t *testing.T) {
		data := struct{ ID, Status string }{"step_1", "completed"}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "node-status-badge", data); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "Completed") {
			t.Errorf("expected 'Completed' in output, got %q", out)
		}
		if !strings.Contains(out, "bg-green") {
			t.Errorf("expected green status class in output, got %q", out)
		}
	})

	t.Run("transcript-item-message", func(t *testing.T) {
		item := TranscriptItem{Kind: "message", Role: "assistant", Text: "Hello world", Timestamp: time.Now()}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "transcript-item", item); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "Hello world") {
			t.Errorf("expected 'Hello world' in output, got %q", out)
		}
		if !strings.Contains(out, "text-accent") {
			t.Errorf("expected assistant role class in output, got %q", out)
		}
	})

	t.Run("transcript-item-tool", func(t *testing.T) {
		item := TranscriptItem{
			Kind:      "tool",
			Role:      "tool",
			ToolName:  "read_file",
			ToolUseID: "tu_123",
			Input:     map[string]any{"path": "/tmp/foo"},
			Output:    "file contents",
			Timestamp: time.Now(),
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "transcript-item", item); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "read_file") {
			t.Errorf("expected 'read_file' in output, got %q", out)
		}
		if !strings.Contains(out, `data-tool-use-id="tu_123"`) {
			t.Errorf("expected data-tool-use-id in output, got %q", out)
		}
	})
}
