package tgwm

import (
	"fmt"
	"os"
)

// MergeOpts configures behavior of Merge and MergeWithResult.
type MergeOpts struct {
	Target         string // branch to merge into (default: main)
	KeepOnConflict bool   // if true, leave the temp worktree on conflict instead of cleaning up
}

// MergeResult holds output from MergeWithResult.
type MergeResult struct {
	WorktreePath string // non-empty only when KeepOnConflict=true and a conflict occurred
}

// Merge merges the named worktree's branch into the target branch in the bare repo.
// Uses a temporary worktree for the merge (bare repos can't merge directly).
// The temp worktree is always cleaned up, even on error. Use MergeWithResult with
// KeepOnConflict=true to retain the worktree on conflict.
func Merge(bareRepoPath, name string, opts MergeOpts) error {
	_, err := MergeWithResult(bareRepoPath, name, opts)
	return err
}

// MergeWithResult merges the named worktree's branch into the target branch and
// returns a MergeResult. When KeepOnConflict is true and a merge conflict occurs,
// the temp worktree is left on disk and its path is returned in MergeResult.WorktreePath.
//
// Acquires a per-bare-repo flock for the duration of the operation so concurrent
// tgwm calls against the same bare repo serialize on the final ref update. Callers
// don't need to coordinate externally.
//
// If the target branch is already checked out in an existing worktree (e.g., a
// parent component worktree during per-task merges in build_component), the merge
// happens in that worktree — git forbids `git worktree add` on a branch that's
// already checked out elsewhere. In that path, conflicts are aborted immediately
// (the shared worktree would otherwise be left with conflict markers, breaking
// subsequent merges) and KeepOnConflict is ignored.
func MergeWithResult(bareRepoPath, name string, opts MergeOpts) (MergeResult, error) {
	target := opts.Target
	if target == "" {
		target = defaultBranchFor(bareRepoPath)
	}

	release, err := acquireBareRepoLock(bareRepoPath)
	if err != nil {
		return MergeResult{}, fmt.Errorf("Merge: %w", err)
	}
	defer release()

	existing, err := findWorktreeForBranch(bareRepoPath, target)
	if err != nil {
		return MergeResult{}, fmt.Errorf("Merge: lookup worktree for %q: %w", target, err)
	}
	if existing != "" {
		return mergeInExistingWorktree(existing, name)
	}

	return mergeViaTempWorktree(bareRepoPath, name, target, opts)
}

func mergeInExistingWorktree(worktreePath, name string) (MergeResult, error) {
	branch := "run/" + name
	msg := "Merge " + name
	if err := runGit(worktreePath, "merge", "--no-ff", branch, "-m", msg); err != nil {
		// Abort to clear conflict markers so the shared worktree remains
		// usable for subsequent merges. runGit failure here is itself
		// non-fatal — if the abort fails the caller still sees the
		// original merge error.
		_ = runGit(worktreePath, "merge", "--abort")
		return MergeResult{}, fmt.Errorf("Merge %q: merge conflict or error: %w", name, err)
	}
	return MergeResult{}, nil
}

func mergeViaTempWorktree(bareRepoPath, name, target string, opts MergeOpts) (MergeResult, error) {
	tmpParent, err := os.MkdirTemp("", "tgwm-merge-*")
	if err != nil {
		return MergeResult{}, fmt.Errorf("Merge: mktemp: %w", err)
	}
	tmpWorktree := tmpParent + "/wt"

	cleanup := func() {
		runGit(bareRepoPath, "worktree", "remove", "--force", tmpWorktree) //nolint:errcheck
		_ = os.RemoveAll(tmpParent)
	}

	if err := runGit(bareRepoPath, "worktree", "add", tmpWorktree, target); err != nil {
		cleanup()
		return MergeResult{}, fmt.Errorf("Merge: worktree add: %w", err)
	}

	branch := "run/" + name
	msg := "Merge " + name
	if err := runGit(tmpWorktree, "merge", "--no-ff", branch, "-m", msg); err != nil {
		if opts.KeepOnConflict {
			return MergeResult{WorktreePath: tmpWorktree}, fmt.Errorf("Merge %q: merge conflict or error: %w", name, err)
		}
		cleanup()
		return MergeResult{}, fmt.Errorf("Merge %q: merge conflict or error: %w", name, err)
	}

	cleanup()
	return MergeResult{}, nil
}

// findWorktreeForBranch returns the path of the worktree that has `branch`
// checked out, or "" if no worktree has it. `branch` is the short name (e.g.
// "run/foo"), not a refs/heads/ path.
func findWorktreeForBranch(bareRepoPath, branch string) (string, error) {
	worktrees, err := listWorktrees(bareRepoPath)
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	return "", nil
}
