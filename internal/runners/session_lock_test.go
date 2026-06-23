package runners

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireSession_EmptySessionIDIsNoOp(t *testing.T) {
	release, err := AcquireSession(context.Background(), "")
	if err != nil {
		t.Fatalf("empty session ID should not error: %v", err)
	}
	if release == nil {
		t.Fatal("release function should be non-nil")
	}
	release()
	// Second call returns immediately too.
	release2, err := AcquireSession(context.Background(), "")
	if err != nil {
		t.Fatalf("second empty acquire should not error: %v", err)
	}
	release2()
}

func TestAcquireSession_SerializesSameSessionID(t *testing.T) {
	ctx := context.Background()
	rel1, err := AcquireSession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	var secondAcquired atomic.Bool
	done := make(chan struct{})
	go func() {
		rel2, err := AcquireSession(ctx, "sess-a")
		if err != nil {
			t.Errorf("second acquire: %v", err)
			close(done)
			return
		}
		secondAcquired.Store(true)
		rel2()
		close(done)
	}()

	// Brief pause: second acquire should still be blocked.
	time.Sleep(50 * time.Millisecond)
	if secondAcquired.Load() {
		t.Fatal("second acquire should be blocked while first holds the lock")
	}

	rel1()

	select {
	case <-done:
		// expected
	case <-time.After(time.Second):
		t.Fatal("second acquire did not fire within 1s of release")
	}

	if !secondAcquired.Load() {
		t.Fatal("second acquire should have succeeded after release")
	}
}

func TestAcquireSession_DifferentSessionIDsDoNotBlock(t *testing.T) {
	ctx := context.Background()
	rel1, err := AcquireSession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer rel1()

	rel2, err := AcquireSession(ctx, "sess-b")
	if err != nil {
		t.Fatalf("acquire b should succeed (different session ID): %v", err)
	}
	rel2()
}

func TestAcquireSession_EvictsEntryWhenNotHeld(t *testing.T) {
	rel, err := AcquireSession(context.Background(), "evict-me")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !sessionLockExists("evict-me") {
		t.Fatal("entry should exist while held")
	}
	rel()
	if sessionLockExists("evict-me") {
		t.Fatal("entry should be evicted after release")
	}
}

func TestAcquireSession_EvictsOnCancellation(t *testing.T) {
	rel, err := AcquireSession(context.Background(), "cancel-evict")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err = AcquireSession(ctx, "cancel-evict")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Entry still exists because the first acquire still holds the lock.
	if !sessionLockExists("cancel-evict") {
		t.Fatal("entry should still exist while first acquire holds it")
	}
}

func TestAcquireSession_CancelWhileWaiting(t *testing.T) {
	rel1, err := AcquireSession(context.Background(), "sess-cancel")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel1()

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, err := AcquireSession(ctx, "sess-cancel")
		cancelled <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("expected context error after cancel, got nil")
		}
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireSession did not return after context cancel")
	}
}
