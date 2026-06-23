package state

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const maxStdoutEventBytes = 8192

// ReadEvents reads a JSONL events file and returns the parsed events.
func ReadEvents(path string) ([]Event, error) {
	events, _, err := ReadEventsWithOffset(path)
	return events, err
}

// ReadEventsWithOffset reads the current contents of the event log and
// returns the parsed events plus the byte offset AFTER the last
// newline-terminated line. Callers that want to tail events after this
// snapshot must use the returned offset, not a fresh os.Stat call — a
// writer may append between ReadFile and Stat, and using Stat would
// cause the tail to start AFTER those appends and miss them.
//
// If the file ends mid-line (writer flushed partial bytes without the
// newline), the offset stops at the end of the last complete line so
// tail polling re-reads the partial bytes together with the newline
// the writer will eventually add.
func ReadEventsWithOffset(path string) ([]Event, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var events []Event
	var consumed int64
	for {
		idx := bytes.IndexByte(data[consumed:], '\n')
		if idx < 0 {
			// No more newline — any remaining bytes are an incomplete
			// trailing line and must not be consumed into the offset.
			break
		}
		lineEnd := consumed + int64(idx) + 1 // include the '\n'
		line := strings.TrimRight(string(data[consumed:lineEnd]), "\r\n")
		consumed = lineEnd
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, consumed, nil
}

type Event struct {
	Timestamp  time.Time      `json:"timestamp"`
	Type       string         `json:"type"`
	RunID      string         `json:"run_id"`
	NodeID     string         `json:"node_id,omitempty"`
	Stream     string         `json:"stream,omitempty"`
	Text       string         `json:"text,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	DurationMs *int64         `json:"duration_ms,omitempty"`
}

type Logger struct {
	file    *os.File
	stdout  io.Writer
	secrets map[string]string
	mu      sync.Mutex
}

func NewLogger(path string) (*Logger, error) {
	return NewLoggerWithStdout(path, os.Stdout)
}

func NewLoggerWithStdout(path string, stdout io.Writer) (*Logger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: file, stdout: stdout}, nil
}

func (logger *Logger) Close() error {
	return logger.file.Close()
}

// SetSecrets configures secret values that will be redacted from all
// subsequent log output (both file and stdout).
func (logger *Logger) SetSecrets(secrets map[string]string) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.secrets = secrets
}

// redactEvent replaces secret values with [REDACTED] in ev.Text and
// string values in ev.Data. Must be called with logger.mu held.
func (logger *Logger) redactEvent(ev *Event) {
	if len(logger.secrets) == 0 {
		return
	}
	for _, secret := range logger.secrets {
		if secret == "" {
			continue
		}
		ev.Text = strings.ReplaceAll(ev.Text, secret, "[REDACTED]")
		for k, v := range ev.Data {
			if s, ok := v.(string); ok {
				ev.Data[k] = strings.ReplaceAll(s, secret, "[REDACTED]")
			}
		}
	}
}

func (logger *Logger) Append(event Event) error {
	logger.mu.Lock()
	defer logger.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	logger.redactEvent(&event)

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = logger.file.Write(payload)
	if err != nil {
		return err
	}

	logger.writeStdout(event)
	return nil
}

// writeStdout emits a slog-compatible JSON line for the event. It is
// best-effort: any write error is silently ignored. Must be called with
// logger.mu held.
func (logger *Logger) writeStdout(event Event) {
	record := map[string]any{
		"time":   event.Timestamp.Format(time.RFC3339),
		"level":  "INFO",
		"msg":    "toil.event",
		"type":   event.Type,
		"run_id": event.RunID,
	}
	if event.NodeID != "" {
		record["node_id"] = event.NodeID
	}
	if event.Stream != "" {
		record["stream"] = event.Stream
	}
	if event.Text != "" {
		record["text"] = event.Text
	}
	if len(event.Data) > 0 {
		record["data"] = event.Data
	}
	if event.DurationMs != nil {
		record["duration_ms"] = *event.DurationMs
	}

	line, err := json.Marshal(record)
	if err != nil {
		return
	}

	if len(line) > maxStdoutEventBytes {
		delete(record, "text")
		delete(record, "data")
		record["truncated"] = true
		line, err = json.Marshal(record)
		if err != nil {
			return
		}
	}

	line = append(line, '\n')
	_, _ = logger.stdout.Write(line)
}
