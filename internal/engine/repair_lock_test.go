package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// signalingRunner mirrors fakeRunner in run_resume_lock_test.go but returns
// output that parses cleanly so repairMissingArtifacts can complete after
// release.
type signalingRunner struct {
	entered chan string
	release chan struct{}
}

func (r *signalingRunner) Run(ctx context.Context, req runners.Request, _ runners.LineHandler) (runners.Result, error) {
	select {
	case r.entered <- req.SessionID:
	case <-ctx.Done():
		return runners.Result{}, ctx.Err()
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return runners.Result{}, ctx.Err()
	}
	// No artifacts required, so collectArtifacts returns nil and the repair
	// loop exits successfully on the first iteration.
	return runners.Result{
		SessionID: req.SessionID,
		Output:    `{"decision":"done","message":"ok","data":{},"artifacts":[]}`,
	}, nil
}

var _ runners.Runner = (*signalingRunner)(nil)

// TestRepairMissingArtifacts_SerializesSameSessionID proves the per-session
// lock added to repairMissingArtifacts prevents two concurrent repair calls
// against the same SessionID from overlapping at runner.Run.
func TestRepairMissingArtifacts_SerializesSameSessionID(t *testing.T) {
	runsDir := t.TempDir()
	runID := "run-repair-lock"
	runDir := filepath.Join(runsDir, runID)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunsDir: runsDir,
	}

	node := &definitions.Node{
		ID:        "agent",
		Kind:      "role",
		Runner:    "test-runner",
		Decisions: definitions.StringDecisions("done"),
	}

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}
	logger, err := state.NewLogger(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState(runID, "wf", nil)
	runState.WithNode("agent", func(n *state.NodeState) { n.SessionID = "sess-shared" })

	runner := &signalingRunner{
		entered: make(chan string, 2),
		release: make(chan struct{}, 2),
	}

	ctx := context.Background()
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, _ = engine.repairMissingArtifacts(
				ctx,
				runID,
				runDir,
				"agent",
				node,
				t.TempDir(),
				runner,
				logger,
				runState,
				NodeOutput{Decision: "done"},
				"sess-shared",
				&ArtifactMissingError{Missing: []string{"missing.txt"}},
				nil,
			)
			done <- struct{}{}
		}()
	}

	// First call enters; second is blocked at the lock.
	sid := <-runner.entered
	if sid != "sess-shared" {
		t.Fatalf("expected first entered SessionID=sess-shared, got %q", sid)
	}

	select {
	case <-runner.entered:
		t.Fatal("second runner.Run entered before first released the session lock")
	case <-time.After(80 * time.Millisecond):
	}

	// Release first; second should now be admitted.
	runner.release <- struct{}{}
	<-done

	sid = <-runner.entered
	if sid != "sess-shared" {
		t.Fatalf("expected second entered SessionID=sess-shared, got %q", sid)
	}
	runner.release <- struct{}{}
	<-done
}
