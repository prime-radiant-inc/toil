package runners

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildClaudeStreamInput(t *testing.T) {
	line, err := buildClaudeStreamInput("Hello Jesse.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected newline suffix")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["type"] != "user" {
		t.Fatalf("unexpected type: %v", payload["type"])
	}
	message, ok := payload["message"].(map[string]any)
	if !ok {
		t.Fatalf("expected message object")
	}
	if message["role"] != "user" {
		t.Fatalf("unexpected message role: %v", message["role"])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content")
	}
	entry, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content entry")
	}
	if entry["type"] != "text" {
		t.Fatalf("unexpected content type: %v", entry["type"])
	}
	if entry["text"] != "Hello Jesse." {
		t.Fatalf("unexpected content text: %v", entry["text"])
	}
}
