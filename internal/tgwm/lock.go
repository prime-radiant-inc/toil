package tgwm

import (
	"fmt"
	"syscall"
)

// acquireBareRepoLock acquires a POSIX flock on a sibling lock file
// next to the bare repo (`<bareRepoPath>.toil.lock`). Concurrent tgwm
// operations against the same bare repo serialize on this lock; ops
// against different bare repos don't contend.
//
// Sibling rather than nested-inside-the-bare-repo so the lock file
// doesn't sit inside git's directory layout. Sibling rather than
// `$TOIL_ROOT/locks/<slug>.lock` so the scheme works for any bare
// repo path a caller passes via --repo, not just ones under
// `$TOIL_ROOT/repos/`.
//
// Returns a release function that callers must defer to release
// the lock and close the file descriptor.
func acquireBareRepoLock(bareRepoPath string) (func(), error) {
	lockPath := bareRepoPath + ".toil.lock"
	fd, err := syscall.Open(lockPath, syscall.O_RDWR|syscall.O_CREAT, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %q: %w", lockPath, err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("flock %q: %w", lockPath, err)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = syscall.Close(fd)
	}, nil
}
