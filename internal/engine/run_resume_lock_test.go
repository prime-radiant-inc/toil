package engine

import (
	"context"
	"testing"
	"time"

	"primeradiant.com/toil/internal/runners"
)

// fakeRunner signals when each Run call enters and exits, so a test can
// assert serialization.
type fakeRunner struct {
	entered chan string // sessionID when Run starts
	release chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, req runners.Request, _ runners.LineHandler) (runners.Result, error) {
	select {
	case f.entered <- req.SessionID:
	case <-ctx.Done():
		return runners.Result{}, ctx.Err()
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return runners.Result{}, ctx.Err()
	}
	return runners.Result{SessionID: req.SessionID, Output: "ok"}, nil
}

func TestRunWithResumeFallback_SerializesSameSessionID(t *testing.T) {
	engine := &Engine{}
	runner := &fakeRunner{
		entered: make(chan string, 2),
		release: make(chan struct{}, 2),
	}

	ctx := context.Background()

	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _ = engine.runWithResumeFallback(ctx, "run-x", "node-x", nil, runner,
				runners.Request{SessionID: "sess-shared", Resume: true}, false, nil, nil)
			done <- struct{}{}
		}()
	}

	// First call enters; second is blocked at the lock.
	sid := <-runner.entered
	if sid != "sess-shared" {
		t.Fatalf("expected first entered SessionID=sess-shared, got %q", sid)
	}

	// Brief pause to confirm second is blocked.
	select {
	case <-runner.entered:
		t.Fatal("second runner.Run entered before first released the session lock")
	case <-time.After(80 * time.Millisecond):
	}

	// Release first; second should now enter.
	runner.release <- struct{}{}
	<-done

	sid = <-runner.entered
	if sid != "sess-shared" {
		t.Fatalf("expected second entered SessionID=sess-shared, got %q", sid)
	}
	runner.release <- struct{}{}
	<-done
}

func TestRunWithResumeFallback_DifferentSessionsDoNotBlock(t *testing.T) {
	engine := &Engine{}
	runner := &fakeRunner{
		entered: make(chan string, 2),
		release: make(chan struct{}, 2),
	}

	ctx := context.Background()
	done := make(chan struct{}, 2)

	go func() {
		_, _ = engine.runWithResumeFallback(ctx, "run-x", "node-a", nil, runner,
			runners.Request{SessionID: "sess-a", Resume: true}, false, nil, nil)
		done <- struct{}{}
	}()
	go func() {
		_, _ = engine.runWithResumeFallback(ctx, "run-x", "node-b", nil, runner,
			runners.Request{SessionID: "sess-b", Resume: true}, false, nil, nil)
		done <- struct{}{}
	}()

	// Both should enter concurrently.
	for i := 0; i < 2; i++ {
		select {
		case <-runner.entered:
		case <-time.After(time.Second):
			t.Fatalf("only %d of 2 runner calls entered concurrently", i)
		}
	}

	// Release both.
	runner.release <- struct{}{}
	runner.release <- struct{}{}
	<-done
	<-done
}

// Compile-time check that fakeRunner satisfies runners.Runner.
var _ runners.Runner = (*fakeRunner)(nil)
