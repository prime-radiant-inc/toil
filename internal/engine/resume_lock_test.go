package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// slowRunner sleeps for a configurable duration and tracks peak concurrency.
type slowRunner struct {
	delay      time.Duration
	concurrent int64
	peak       int64
}

func (r *slowRunner) Run(_ context.Context, _ runners.Request, handler runners.LineHandler) (runners.Result, error) {
	cur := atomic.AddInt64(&r.concurrent, 1)
	defer atomic.AddInt64(&r.concurrent, -1)
	for {
		old := atomic.LoadInt64(&r.peak)
		if cur <= old || atomic.CompareAndSwapInt64(&r.peak, old, cur) {
			break
		}
	}
	time.Sleep(r.delay)
	output := `{"decision":"done","message":"ok","data":{}}`
	if handler != nil {
		handler(runners.Line{Stream: "stdout", Text: output})
	}
	return runners.Result{Output: output}, nil
}

// TestResumeRunLockPreventsParallelExecution verifies that concurrent
// ResumeRun calls on the same run ID are serialized — the per-run mutex
// prevents the duplicate child-run explosion that caused the corewars
// 400-run incident.
func TestResumeRunLockPreventsParallelExecution(t *testing.T) {
	runsDir := t.TempDir()

	runner := &slowRunner{delay: 50 * time.Millisecond}
	registry := runners.NewRegistry()
	_ = registry.Register("slow", runner)

	workflow := &definitions.Workflow{
		ID:      "lock-test",
		Name:    "Lock Test",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "step", Kind: "role", Runner: "slow"},
		},
	}

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{"lock-test": workflow},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	setupRunForResume(t, runsDir, "lock-run", workflow, nil)

	// Fire 5 concurrent ResumeRun calls on the same runID.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = eng.ResumeRun(context.Background(), "lock-run")
		}()
	}
	wg.Wait()

	if p := atomic.LoadInt64(&runner.peak); p > 1 {
		t.Fatalf("per-run lock failed: peak concurrency was %d, expected 1", p)
	}

	// Verify the run completed.
	rs, err := state.LoadState(filepath.Join(runsDir, "lock-run", "state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if rs.Status != statusCompleted {
		t.Fatalf("expected status completed, got %s", rs.Status)
	}

	// Only one run dir should exist (no duplicate child runs).
	entries, _ := os.ReadDir(runsDir)
	runCount := 0
	for _, e := range entries {
		if e.IsDir() {
			runCount++
		}
	}
	if runCount != 1 {
		t.Fatalf("expected exactly 1 run dir, got %d", runCount)
	}
}
