package runners

import "testing"

func TestClaudeStreamExtractsSessionAndText(t *testing.T) {
	stream := NewClaudeStream()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hi Jesse."}]},"session_id":"session-abc"}`
	text, err := stream.Handle(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hi Jesse." {
		t.Fatalf("unexpected text: %s", text)
	}
	if stream.SessionID != "session-abc" {
		t.Fatalf("unexpected session id: %s", stream.SessionID)
	}
}

func TestClaudeStreamExtractsResult(t *testing.T) {
	stream := NewClaudeStream()
	line := "{\"type\":\"result\",\"result\":\"```json\\n{\\\"decision\\\":\\\"ok\\\",\\\"message\\\":\\\"done\\\",\\\"data\\\":{},\\\"artifacts\\\":[]}\\n```\\n\",\"session_id\":\"session-xyz\"}"
	text, err := stream.Handle(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("unexpected text: %s", text)
	}
	if !stream.HasResult {
		t.Fatal("expected result flag")
	}
	if stream.Result == "" {
		t.Fatal("expected result output")
	}
	if stream.SessionID != "session-xyz" {
		t.Fatalf("unexpected session id: %s", stream.SessionID)
	}
}

func TestClaudeStream_InvalidJSON(t *testing.T) {
	stream := NewClaudeStream()
	_, err := stream.Handle("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestClaudeStream_AssistantWithoutMessage(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"assistant"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestClaudeStream_AssistantWithoutContent(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"assistant","message":{}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestClaudeStream_AssistantWithNonTextContent(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"bash"}]}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text for non-text content, got %q", text)
	}
}

func TestClaudeStream_AssistantWithMixedContent(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"assistant","message":{"content":[{"type":"tool_use"},{"type":"text","text":"answer"}]}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "answer" {
		t.Fatalf("text = %q, want 'answer'", text)
	}
}

func TestClaudeStream_UnknownEventType(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"system","message":"info"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text for unknown event, got %q", text)
	}
}

func TestClaudeStream_ContentItemNotMap(t *testing.T) {
	stream := NewClaudeStream()
	text, err := stream.Handle(`{"type":"assistant","message":{"content":["a string item"]}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}
