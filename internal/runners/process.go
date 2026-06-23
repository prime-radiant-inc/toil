package runners

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func runCommand(ctx context.Context, cmd *exec.Cmd, prompt string, handler LineHandler) (int, error) {
	if prompt != "" {
		cmd.Stdin = strings.NewReader(prompt)
	}

	// Wire stdout/stderr to line-splitting writers rather than using
	// StdoutPipe/StderrPipe. With cmd.Stdout/Stderr set to a non-*os.File,
	// exec copies the child's output through internal goroutines that
	// cmd.Wait() waits for, so all output is delivered before Wait returns.
	// StdoutPipe instead requires draining the pipe to EOF *before* cmd.Wait()
	// (Wait closes the pipe) — and that races: under load the pipe can be
	// closed before the reader drains it, dropping or truncating output.
	stdoutW := &lineWriter{stream: streamStdout, handler: handler}
	stderrW := &lineWriter{stream: streamStderr, handler: handler}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	// Put the child in its own process group so we can kill it and all its
	// children on timeout. Without this, children inherit the output pipe and
	// keep it open after the parent is killed, causing a hang.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the entire process group, not just the leader.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	// Failsafe: if a child survives the group kill and holds the output pipe
	// open, force-close it (unblocking the copier) after the delay.
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	// cmd.Wait() returns after the process exits AND the output copiers
	// finish (bounded by WaitDelay if a surviving child holds the pipe).
	waitErr := cmd.Wait()

	// Emit any trailing line not terminated by a newline.
	stdoutW.flush()
	stderrW.flush()

	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		// A clean exit whose pipe was force-closed by WaitDelay surfaces as a
		// non-ExitError; honor the real exit code rather than reporting failure.
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return cmd.ProcessState.ExitCode(), nil
		}
		return -1, waitErr
	}
	return 0, nil
}

// lineWriter is an io.Writer that splits incoming bytes into newline-delimited
// lines and forwards each to handler, mirroring bufio.Scanner's ScanLines (a
// trailing \r is stripped). exec calls Write from a single copier goroutine per
// stream, so no locking is needed. Call flush() after cmd.Wait() to emit a
// final line that was not newline-terminated.
type lineWriter struct {
	stream  string
	handler LineHandler
	buf     []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) emit(line []byte) {
	if w.handler != nil {
		w.handler(Line{Stream: w.stream, Text: string(bytes.TrimSuffix(line, []byte("\r")))})
	}
}

func withTimeout(ctx context.Context, timeoutSec int) (context.Context, context.CancelFunc) {
	if timeoutSec <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
}

// streamHandler is used by runners that parse stdout through a stream parser.
// It wraps the caller's line handler, feeds stdout lines through the parser,
// and collects the extracted text into an output buffer.
type streamHandler func(line string) (string, error)

// outputCollector accumulates parsed output text from a stream parser,
// joining text fragments with newlines when needed.
type outputCollector struct {
	builder   strings.Builder
	hasOutput bool
	lastByte  byte
}

func (c *outputCollector) append(text string) {
	if text == "" {
		return
	}
	if c.hasOutput && c.lastByte != '\n' && !strings.HasPrefix(text, "\n") {
		c.builder.WriteString("\n")
	}
	c.builder.WriteString(text)
	c.hasOutput = true
	c.lastByte = text[len(text)-1]
}

func (c *outputCollector) String() string {
	return c.builder.String()
}

// newStreamLineHandler creates a LineHandler that forwards all lines to the
// caller's handler, parses stdout lines through the stream parser, and
// collects extracted text into the provided outputCollector.
func newStreamLineHandler(handler LineHandler, parse streamHandler, collector *outputCollector) LineHandler {
	return func(line Line) {
		if handler != nil {
			handler(line)
		}
		if line.Stream != streamStdout {
			return
		}
		text, err := parse(line.Text)
		if err != nil {
			return
		}
		collector.append(text)
	}
}

func streamLines(reader io.Reader, stream string, handler LineHandler) error {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 1024*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		if handler != nil {
			handler(Line{Stream: stream, Text: scanner.Text()})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", stream, err)
	}
	return nil
}
