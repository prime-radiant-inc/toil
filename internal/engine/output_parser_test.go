package engine

import "testing"

func TestParseNodeOutputPlainJSON(t *testing.T) {
	output := `{"decision":"approved","message":"ok","data":{"tasks":["task-1"]},"artifacts":[]}`
	parsed, err := ParseNodeOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Decision != testDecisionApproved {
		t.Fatalf("unexpected decision: %s", parsed.Decision)
	}
	if parsed.Message != "ok" {
		t.Fatalf("unexpected message: %s", parsed.Message)
	}
	if parsed.Data == nil {
		t.Fatal("expected data")
	}
}

func TestParseNodeOutputFencedJSON(t *testing.T) {
	output := "Notes\n```json\n{\"decision\":\"ready\",\"message\":\"ok\",\"data\":{},\"artifacts\":[]}\n```\n"
	parsed, err := ParseNodeOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Decision != "ready" {
		t.Fatalf("unexpected decision: %s", parsed.Decision)
	}
	if parsed.Message != "ok" {
		t.Fatalf("unexpected message: %s", parsed.Message)
	}
}

func TestParseNodeOutputPrefersFencedJSONOverOuterText(t *testing.T) {
	output := "agent chatter\n```json\n{\"decision\":\"ready\",\"message\":\"ok\",\"data\":{},\"artifacts\":[]}\n```\n"
	parsed, err := ParseNodeOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Decision != "ready" {
		t.Fatalf("unexpected decision: %s", parsed.Decision)
	}
}

func TestParseNodeOutputArtifactsObject(t *testing.T) {
	output := `{"decision":"ready","message":"ok","data":{},"artifacts":[{"path":"docs/spec.md","type":"spec"}]}`
	parsed, err := ParseNodeOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Artifacts) != 1 || parsed.Artifacts[0] != "docs/spec.md" {
		t.Fatalf("unexpected artifacts: %v", parsed.Artifacts)
	}
}

func TestParseNodeOutputArtifactsInlineContentIgnored(t *testing.T) {
	output := `{"decision":"ready","message":"ok","data":{},"artifacts":["line1\nline2"]}`
	parsed, err := ParseNodeOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Artifacts) != 0 {
		t.Fatalf("expected inline artifact content to be ignored, got: %v", parsed.Artifacts)
	}
}

func TestParseNodeOutputRejectsNonJSON(t *testing.T) {
	if _, err := ParseNodeOutput("plain response without json"); err == nil {
		t.Fatal("expected parse error for plain text output")
	}
}

func TestParseNodeOutputRejectsJSONArray(t *testing.T) {
	if _, err := ParseNodeOutput(`[{"decision":"ok"}]`); err == nil {
		t.Fatal("expected parse error for non-object JSON output")
	}
}

func TestParseNodeOutputRejectsMultipleJSONValues(t *testing.T) {
	if _, err := ParseNodeOutput(`{"decision":"ok"} {"message":"extra"}`); err == nil {
		t.Fatal("expected parse error for multiple JSON values")
	}
}
