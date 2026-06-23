package runners

import (
	"context"
	"sync"
)

// AcquireSession blocks until exclusive use of sessionID is granted, then
// returns a release function that must be called to free the lock.
//
// Empty sessionID is a no-op: returns immediately with a no-op release.
// Cancellable via ctx — if ctx.Done() fires while waiting, returns ctx.Err()
// and a nil release function.
//
// The lock is process-wide and keyed by SessionID. Two callers targeting the
// same SessionID serialize; callers with different SessionIDs proceed
// concurrently. This is the only thing preventing concurrent
// "<runner> --resume <same-id>" subprocesses from corrupting a shared
// transcript file, since none of serf/claude/codex hold filesystem locks
// on their own session state.
//
// The registry refcounts entries: when the last caller releases (or cancels
// while waiting), the entry is deleted. Memory is bounded to the set of
// sessions actively under lock at any moment, not the cumulative history.
func AcquireSession(ctx context.Context, sessionID string) (release func(), err error) {
	if sessionID == "" {
		return func() {}, nil
	}

	sessionLocksMu.Lock()
	entry, ok := sessionEntries[sessionID]
	if !ok {
		entry = &sessionEntry{ch: make(chan struct{}, 1)}
		entry.ch <- struct{}{}
		sessionEntries[sessionID] = entry
	}
	entry.refcount++
	sessionLocksMu.Unlock()

	decref := func() {
		sessionLocksMu.Lock()
		entry.refcount--
		if entry.refcount == 0 {
			delete(sessionEntries, sessionID)
		}
		sessionLocksMu.Unlock()
	}

	select {
	case <-entry.ch:
		var (
			released   bool
			releasedMu sync.Mutex
		)
		return func() {
			releasedMu.Lock()
			if released {
				releasedMu.Unlock()
				return
			}
			released = true
			releasedMu.Unlock()
			// Hand the token back FIRST so the next waiter can proceed
			// before we touch the refcount. Refcount stays > 0 while
			// any waiter is blocked on entry.ch, so the entry cannot
			// be evicted out from under them.
			select {
			case entry.ch <- struct{}{}:
			default:
			}
			decref()
		}, nil
	case <-ctx.Done():
		decref()
		return nil, ctx.Err()
	}
}

type sessionEntry struct {
	ch       chan struct{}
	refcount int
}

var (
	sessionLocksMu sync.Mutex
	sessionEntries = make(map[string]*sessionEntry)
)

// sessionLockExists reports whether sessionID currently has a registered
// lock entry. Test-only — exported for assertions about eviction.
func sessionLockExists(sessionID string) bool {
	sessionLocksMu.Lock()
	defer sessionLocksMu.Unlock()
	_, ok := sessionEntries[sessionID]
	return ok
}
