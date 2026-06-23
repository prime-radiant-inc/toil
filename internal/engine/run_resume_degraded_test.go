package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// degradingRunner returns a retry-eligible error on the first call (so
// runWithResumeFallback fires its fresh-session fallback) and succeeds on
// the second.
type degradingRunner struct {
	calls   int
	sawReq  []runners.Request
	failErr error
}

func (d *degradingRunner) Run(_ context.Context, req runners.Request, _ runners.LineHandler) (runners.Result, error) {
	d.calls++
	d.sawReq = append(d.sawReq, req)
	if d.calls == 1 {
		return runners.Result{}, d.failErr
	}
	return runners.Result{SessionID: req.SessionID, Output: "ok"}, nil
}

// PRI-1575: when the provider rejects the resume and we retry with a
// fresh session, emit a structured node_resume_degraded event carrying
// the original SessionID, the provider error, and an `intended` flag
// distinguishing YAML-explicit resumes from loop-iteration auto-resumes.
func TestRunWithResumeFallback_EmitsDegradedEvent_IntendedTrue(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/events.jsonl"
	logger, err := state.NewLoggerWithStdout(logPath, os.NewFile(0, os.DevNull))
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	runner := &degradingRunner{
		// shouldRetryFreshSession looks for "tool_use" + "tool_result"
		// substrings — match that shape.
		failErr: errors.New("provider rejected resume: tool_use without matching tool_result"),
	}
	eng := &Engine{}

	_, err = eng.runWithResumeFallback(context.Background(), "run-1", "judge", logger, runner,
		runners.Request{SessionID: "sess-plan-tasks", Resume: true}, true, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 runner calls (resume + fresh), got %d", runner.calls)
	}
	if runner.sawReq[1].SessionID != "" || runner.sawReq[1].Resume {
		t.Fatalf("second call should be fresh session, got SessionID=%q Resume=%v", runner.sawReq[1].SessionID, runner.sawReq[1].Resume)
	}

	_ = logger.Close()
	contents, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read events: %v", readErr)
	}
	var found *state.Event
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}
		var ev state.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "node_resume_degraded" {
			found = &ev
			break
		}
	}
	if found == nil {
		t.Fatalf("expected node_resume_degraded event, got:\n%s", contents)
	}
	if found.NodeID != "judge" {
		t.Errorf("node_id = %q, want judge", found.NodeID)
	}
	if got, _ := found.Data["original_session_id"].(string); got != "sess-plan-tasks" {
		t.Errorf("original_session_id = %q, want sess-plan-tasks", got)
	}
	if got, _ := found.Data["error"].(string); !strings.Contains(got, "tool_use") {
		t.Errorf("error should carry provider message, got %q", got)
	}
	if got, _ := found.Data["intended"].(bool); !got {
		t.Errorf("intended = %v, want true (YAML-explicit resume)", got)
	}
}

func TestRunWithResumeFallback_EmitsDegradedEvent_IntendedFalse(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/events.jsonl"
	logger, err := state.NewLoggerWithStdout(logPath, os.NewFile(0, os.DevNull))
	if err != nil {
		t.Fatalf("NewLoggerWithStdout: %v", err)
	}
	defer func() { _ = logger.Close() }()

	runner := &degradingRunner{
		failErr: errors.New("provider rejected resume: tool_use without matching tool_result"),
	}
	eng := &Engine{}

	_, err = eng.runWithResumeFallback(context.Background(), "run-1", "engineer", logger, runner,
		runners.Request{SessionID: "sess-loop", Resume: true}, false, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = logger.Close()
	contents, _ := os.ReadFile(logPath)
	if !strings.Contains(string(contents), `"intended":false`) {
		t.Fatalf("expected intended:false in event log, got:\n%s", contents)
	}
}
