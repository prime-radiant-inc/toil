package runners

import "testing"

func TestCodexStreamExtractsSessionAndText(t *testing.T) {
	stream := NewCodexStream()

	line1 := `{"type":"thread.started","thread_id":"thread-123"}`
	text, err := stream.Handle(line1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
	if stream.SessionID != "thread-123" {
		t.Fatalf("unexpected session id: %s", stream.SessionID)
	}

	line2 := `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Hello"}}`
	text, err = stream.Handle(line2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello" {
		t.Fatalf("unexpected text: %s", text)
	}
}

func TestCodexStream_InvalidJSON(t *testing.T) {
	stream := NewCodexStream()
	_, err := stream.Handle("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCodexStream_ItemCompletedWithoutItem(t *testing.T) {
	stream := NewCodexStream()
	text, err := stream.Handle(`{"type":"item.completed"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

func TestCodexStream_ItemCompletedNonAgentMessage(t *testing.T) {
	stream := NewCodexStream()
	text, err := stream.Handle(`{"type":"item.completed","item":{"type":"tool_output","text":"result"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text for non-agent_message item, got %q", text)
	}
}

func TestCodexStream_UnknownEventType(t *testing.T) {
	stream := NewCodexStream()
	text, err := stream.Handle(`{"type":"thread.completed","data":{}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text for unknown event, got %q", text)
	}
}

func TestCodexStream_NoTypeField(t *testing.T) {
	stream := NewCodexStream()
	text, err := stream.Handle(`{"data":"something"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}
