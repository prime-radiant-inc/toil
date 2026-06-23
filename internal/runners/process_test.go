package runners

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// --- outputCollector tests ---

func TestOutputCollector_Empty(t *testing.T) {
	var c outputCollector
	if got := c.String(); got != "" {
		t.Fatalf("empty collector should return empty string, got %q", got)
	}
}

func TestOutputCollector_SingleAppend(t *testing.T) {
	var c outputCollector
	c.append("hello")

	if got := c.String(); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestOutputCollector_JoinsWithNewline(t *testing.T) {
	var c outputCollector
	c.append("line1")
	c.append("line2")

	if got := c.String(); got != "line1\nline2" {
		t.Fatalf("got %q, want %q", got, "line1\nline2")
	}
}

func TestOutputCollector_NoDoubleNewline(t *testing.T) {
	var c outputCollector
	c.append("line1\n")
	c.append("line2")

	// line1 already ends with \n, should not add another.
	if got := c.String(); got != "line1\nline2" {
		t.Fatalf("got %q, want %q", got, "line1\nline2")
	}
}

func TestOutputCollector_SkipsEmptyStrings(t *testing.T) {
	var c outputCollector
	c.append("hello")
	c.append("")
	c.append("world")

	if got := c.String(); got != "hello\nworld" {
		t.Fatalf("got %q, want %q", got, "hello\nworld")
	}
}

func TestOutputCollector_TextStartingWithNewline(t *testing.T) {
	var c outputCollector
	c.append("first")
	c.append("\nsecond")

	// second starts with \n, no extra \n needed.
	if got := c.String(); got != "first\nsecond" {
		t.Fatalf("got %q, want %q", got, "first\nsecond")
	}
}

func TestOutputCollector_MultipleNewlines(t *testing.T) {
	var c outputCollector
	c.append("a\n\n")
	c.append("b")

	// a ends with \n, so no separator added.
	if got := c.String(); got != "a\n\nb" {
		t.Fatalf("got %q, want %q", got, "a\n\nb")
	}
}

// --- newStreamLineHandler tests ---

func TestNewStreamLineHandler_ForwardsToHandler(t *testing.T) {
	var forwarded []Line
	handler := func(line Line) {
		forwarded = append(forwarded, line)
	}
	parse := func(line string) (string, error) {
		return line, nil
	}
	var collector outputCollector

	h := newStreamLineHandler(handler, parse, &collector)

	h(Line{Stream: streamStdout, Text: "hello"})
	h(Line{Stream: "stderr", Text: "err"})

	if len(forwarded) != 2 {
		t.Fatalf("expected 2 forwarded lines, got %d", len(forwarded))
	}
}

func TestNewStreamLineHandler_OnlyParsesStdout(t *testing.T) {
	parse := func(line string) (string, error) {
		return "[" + line + "]", nil
	}
	var collector outputCollector

	h := newStreamLineHandler(nil, parse, &collector)

	h(Line{Stream: streamStdout, Text: "out"})
	h(Line{Stream: "stderr", Text: "err"})

	if got := collector.String(); got != "[out]" {
		t.Fatalf("got %q, want %q — stderr should not be parsed", got, "[out]")
	}
}

func TestNewStreamLineHandler_NilHandler(t *testing.T) {
	parse := func(line string) (string, error) {
		return line, nil
	}
	var collector outputCollector

	// Should not panic with nil handler.
	h := newStreamLineHandler(nil, parse, &collector)
	h(Line{Stream: streamStdout, Text: "test"})

	if got := collector.String(); got != "test" {
		t.Fatalf("got %q, want %q", got, "test")
	}
}

func TestNewStreamLineHandler_ParseError(t *testing.T) {
	parse := func(line string) (string, error) {
		return "", &parseErr{}
	}
	var collector outputCollector

	h := newStreamLineHandler(nil, parse, &collector)
	h(Line{Stream: streamStdout, Text: "bad"})

	if got := collector.String(); got != "" {
		t.Fatalf("parse error should skip output, got %q", got)
	}
}

type parseErr struct{}

func (e *parseErr) Error() string { return "parse failed" }

// --- streamLines tests ---

func TestStreamLines_BasicReading(t *testing.T) {
	reader := strings.NewReader("line1\nline2\nline3\n")

	var lines []Line
	handler := func(line Line) {
		lines = append(lines, line)
	}

	err := streamLines(reader, streamStdout, handler)
	if err != nil {
		t.Fatalf("streamLines: %v", err)
	}

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0].Text != "line1" || lines[0].Stream != streamStdout {
		t.Fatalf("line[0] = %+v", lines[0])
	}
	if lines[2].Text != "line3" {
		t.Fatalf("line[2].Text = %q", lines[2].Text)
	}
}

func TestStreamLines_EmptyReader(t *testing.T) {
	reader := strings.NewReader("")

	var lines []Line
	handler := func(line Line) {
		lines = append(lines, line)
	}

	err := streamLines(reader, "stderr", handler)
	if err != nil {
		t.Fatalf("streamLines: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
}

func TestStreamLines_NilHandler(t *testing.T) {
	reader := strings.NewReader("line1\nline2\n")

	// Should not panic with nil handler.
	err := streamLines(reader, streamStdout, nil)
	if err != nil {
		t.Fatalf("streamLines: %v", err)
	}
}

func TestStreamLines_SetsStreamField(t *testing.T) {
	reader := strings.NewReader("data\n")

	var got Line
	handler := func(line Line) { got = line }

	_ = streamLines(reader, "stderr", handler)
	if got.Stream != "stderr" {
		t.Fatalf("Stream = %q, want stderr", got.Stream)
	}
}

// --- withTimeout tests ---

func TestWithTimeout_ZeroReturnsNoOp(t *testing.T) {
	// withTimeout(ctx, 0) should return the same context (no deadline).
	ctx, cancel := withTimeout(t.Context(), 0)
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero timeout should not set deadline")
	}
}

func TestWithTimeout_NegativeReturnsNoOp(t *testing.T) {
	ctx, cancel := withTimeout(t.Context(), -5)
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("negative timeout should not set deadline")
	}
}

func TestWithTimeout_PositiveSetsDeadline(t *testing.T) {
	ctx, cancel := withTimeout(t.Context(), 60)
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("expected deadline to be set")
	}
}

// --- streamLines error path ---

// errReader is a reader that returns an error after some data.
type errReader struct {
	data string
	read bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, errors.New("injected read error")
}

func TestStreamLines_ScannerError(t *testing.T) {
	// errReader returns data without a newline, then errors on next read.
	// The scanner will have a partial line in the buffer when the error occurs.
	reader := &errReader{data: "partial"}

	err := streamLines(reader, streamStdout, nil)
	if err == nil {
		t.Fatal("expected error from scanner")
	}
	if !strings.Contains(err.Error(), "scan stdout") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- runCommand tests ---

func TestRunCommand_BinaryNotFound(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "/nonexistent/binary")
	exitCode, err := runCommand(t.Context(), cmd, "", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if exitCode != -1 {
		t.Fatalf("exit code = %d, want -1", exitCode)
	}
}

func TestRunCommand_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	// Use sleep to give us time to cancel.
	cmd := exec.CommandContext(ctx, "sleep", "30")
	done := make(chan struct{})
	var exitCode int
	var err error
	go func() {
		exitCode, err = runCommand(ctx, cmd, "", nil)
		close(done)
	}()

	// Cancel after a brief delay to ensure process has started.
	cancel()
	<-done

	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if exitCode != -1 {
		t.Fatalf("exit code = %d, want -1", exitCode)
	}
}

func TestRunCommandReturnsWhenChildHoldsPipe(t *testing.T) {
	// Spawn a parent that exits immediately while a background child
	// holds stdout open. Without the fix, runCommand blocks forever
	// waiting for pipe EOF.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c",
		// Parent writes to stdout, spawns a background sleep that
		// inherits the pipe, then exits.
		`echo parent_output; (sleep 60 &); exit 0`,
	)

	var lines []string
	handler := func(line Line) {
		lines = append(lines, line.Text)
	}

	exitCode, err := runCommand(ctx, cmd, "", handler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "parent_output") {
			found = true
		}
	}
	if !found {
		t.Error("expected to see parent_output in captured lines")
	}
}
