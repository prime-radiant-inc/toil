package state

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendWritesToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	logger, err := NewLoggerWithStdout(logPath, io.Discard)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	if err := logger.Append(Event{Type: "run_started", RunID: "run-abc"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content := readTestFile(t, logPath)
	if !strings.Contains(content, "run_started") {
		t.Fatalf("expected file to contain run_started, got: %s", content)
	}
	if !strings.Contains(content, "run-abc") {
		t.Fatalf("expected file to contain run-abc, got: %s", content)
	}
}

func TestAppendWritesToStdout(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	var buf bytes.Buffer
	logger, err := NewLoggerWithStdout(logPath, &buf)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	if err := logger.Append(Event{
		Type:   "node_completed",
		RunID:  "run-xyz",
		NodeID: "my-node",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected stdout output, got empty string")
	}

	// Must be valid JSON terminated by a newline.
	line := strings.TrimRight(out, "\n")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("stdout output is not valid JSON: %v\noutput: %s", err, out)
	}

	// Must be slog-compatible.
	if parsed["level"] != "INFO" {
		t.Fatalf("expected level INFO, got %v", parsed["level"])
	}
	if parsed["msg"] != "toil.event" {
		t.Fatalf("expected msg toil.event, got %v", parsed["msg"])
	}
	if _, ok := parsed["time"]; !ok {
		t.Fatal("expected time field in stdout JSON")
	}

	// Event-specific fields must be present.
	if parsed["type"] != "node_completed" {
		t.Fatalf("expected type node_completed, got %v", parsed["type"])
	}
	if parsed["run_id"] != "run-xyz" {
		t.Fatalf("expected run_id run-xyz, got %v", parsed["run_id"])
	}
	if parsed["node_id"] != "my-node" {
		t.Fatalf("expected node_id my-node, got %v", parsed["node_id"])
	}
}

func TestAppendStdoutTimestampIsRFC3339(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	var buf bytes.Buffer
	logger, err := NewLoggerWithStdout(logPath, &buf)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	// RFC3339 truncates to seconds, so record before/after at that resolution.
	before := time.Now().UTC().Truncate(time.Second)
	if err := logger.Append(Event{Type: "run_started", RunID: "r1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	after := time.Now().UTC().Truncate(time.Second)

	line := strings.TrimRight(buf.String(), "\n")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	rawTime, ok := parsed["time"].(string)
	if !ok {
		t.Fatalf("expected time to be a string, got %T", parsed["time"])
	}
	ts, err := time.Parse(time.RFC3339, rawTime)
	if err != nil {
		t.Fatalf("time is not RFC3339: %v (value: %s)", err, rawTime)
	}
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("timestamp %v outside expected range [%v, %v]", ts, before, after)
	}
}

func TestAppendTruncatesLargeEvents(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	var buf bytes.Buffer
	logger, err := NewLoggerWithStdout(logPath, &buf)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	// Build an event whose JSON will exceed 8192 bytes.
	event := Event{
		Type:   "node_output",
		RunID:  "run-big",
		NodeID: "big-node",
		Text:   strings.Repeat("x", 9000),
	}
	if err := logger.Append(event); err != nil {
		t.Fatalf("Append: %v", err)
	}

	line := strings.TrimRight(buf.String(), "\n")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("stdout output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	if parsed["truncated"] != true {
		t.Fatalf("expected truncated=true for large event, got %v", parsed["truncated"])
	}
	if text, ok := parsed["text"]; ok && text != "" {
		t.Fatalf("expected text to be cleared on truncation, got %q", text)
	}
	// Identity fields must survive truncation.
	if parsed["type"] != "node_output" {
		t.Fatalf("expected type to survive truncation, got %v", parsed["type"])
	}
	if parsed["run_id"] != "run-big" {
		t.Fatalf("expected run_id to survive truncation, got %v", parsed["run_id"])
	}
}

func TestAppendRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	var stdout bytes.Buffer
	logger, err := NewLoggerWithStdout(logPath, &stdout)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	logger.SetSecrets(map[string]string{
		"GITHUB_TOKEN": "ghp_superSecret123",
	})

	if err := logger.Append(Event{
		Type:  "node_output",
		RunID: "run-1",
		Text:  "Cloning with token ghp_superSecret123 done",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Check file output
	fileContent := readTestFile(t, logPath)
	if strings.Contains(fileContent, "ghp_superSecret123") {
		t.Fatal("secret value found in file output")
	}
	if !strings.Contains(fileContent, "[REDACTED]") {
		t.Fatal("expected [REDACTED] in file output")
	}

	// Check stdout output
	stdoutContent := stdout.String()
	if strings.Contains(stdoutContent, "ghp_superSecret123") {
		t.Fatal("secret value found in stdout output")
	}
	if !strings.Contains(stdoutContent, "[REDACTED]") {
		t.Fatal("expected [REDACTED] in stdout output")
	}
}

func TestAppendRedactsSecretsInData(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	var stdout bytes.Buffer
	logger, err := NewLoggerWithStdout(logPath, &stdout)
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	logger.SetSecrets(map[string]string{
		"API_KEY": "sk-secret-api-key-999",
	})

	if err := logger.Append(Event{
		Type:  "node_output",
		RunID: "run-2",
		Data: map[string]any{
			"command": "curl -H 'Authorization: sk-secret-api-key-999'",
			"count":   42,
		},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Check file output
	fileContent := readTestFile(t, logPath)
	if strings.Contains(fileContent, "sk-secret-api-key-999") {
		t.Fatal("secret value found in file output Data field")
	}
	if !strings.Contains(fileContent, "[REDACTED]") {
		t.Fatal("expected [REDACTED] in file output Data field")
	}

	// Check stdout output
	stdoutContent := stdout.String()
	if strings.Contains(stdoutContent, "sk-secret-api-key-999") {
		t.Fatal("secret value found in stdout output Data field")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readTestFile %s: %v", path, err)
	}
	return string(data)
}

func TestReadEventsWithOffset_PartialTrailingLineNotConsumed(t *testing.T) {
	// Regression guard: a writer that has flushed a partial line
	// (bytes without the trailing \n) must not have those bytes
	// counted in the returned offset. Otherwise follow-mode tailing
	// starts past the partial and misses the event when the newline
	// eventually arrives.
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	complete := `{"type":"run_started","run_id":"r1"}` + "\n" +
		`{"type":"node_started","run_id":"r1","node_id":"a"}` + "\n"
	partial := `{"type":"node_co` // no newline
	if err := os.WriteFile(path, []byte(complete+partial), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	events, offset, err := ReadEventsWithOffset(path)
	if err != nil {
		t.Fatalf("ReadEventsWithOffset: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 complete events, got %d", len(events))
	}
	if offset != int64(len(complete)) {
		t.Fatalf("offset = %d, want %d (end of complete portion, not including partial)",
			offset, len(complete))
	}
}
