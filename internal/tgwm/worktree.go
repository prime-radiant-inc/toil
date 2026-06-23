package tgwm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fallbackDefaultBranch is the branch tgwm assumes when it can't ask the
// bare repo what HEAD points at. Used only by Init's empty-bare path
// (which creates the first commit before HEAD is meaningful) and as a
// last-resort fallback if `git symbolic-ref HEAD` fails.
const fallbackDefaultBranch = "main"

// defaultBranchFor returns the branch HEAD points at on the given bare
// repo. Reads `git symbolic-ref --short HEAD` rather than hardcoding
// "main" so projects that ship master, develop, trunk, or a renamed
// default branch work correctly. Falls back to "main" only when the
// bare repo isn't queryable.
func defaultBranchFor(bareRepoPath string) string {
	if bareRepoPath == "" {
		return fallbackDefaultBranch
	}
	out, err := gitOutput(bareRepoPath, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return fallbackDefaultBranch
	}
	if branch := strings.TrimSpace(out); branch != "" {
		return branch
	}
	return fallbackDefaultBranch
}

// WorktreeOpts configures behavior of WorktreeCreate.
type WorktreeOpts struct {
	Base string // branch to base off (default: main)
	// Force destroys any existing worktree+branch with the same name
	// before creating, instead of failing on the collision. Required
	// when plan_tasks re-plans with a task ID that previously failed —
	// the leftover preserved branch would otherwise block creation.
	Force bool
}

// WorktreeCreate creates a worktree under workDir/worktrees/<name>.
// Branch name: run/<name>. Returns the worktree path.
// bareRepoPath: path to the project's bare repo (from Init).
//
// Acquires a per-bare-repo flock so concurrent creates don't race on
// the bare repo's .git/config.lock during `git worktree add -b`.
func WorktreeCreate(workDir, bareRepoPath, name string, opts WorktreeOpts) (string, error) {
	worktreePath := filepath.Join(workDir, "worktrees", name)

	if err := os.MkdirAll(filepath.Join(workDir, "worktrees"), 0o755); err != nil {
		return "", fmt.Errorf("WorktreeCreate: mkdir worktrees: %w", err)
	}

	release, err := acquireBareRepoLock(bareRepoPath)
	if err != nil {
		return "", fmt.Errorf("WorktreeCreate: %w", err)
	}
	defer release()

	branch := "run/" + name

	if opts.Force {
		// Destroy any existing worktree+branch with this name so the
		// upcoming `git worktree add -b` succeeds. Best-effort: ignore
		// errors (e.g., nothing to destroy). The lock is already held,
		// so we use git directly here instead of recursing into
		// WorktreeDestroy (which would try to re-acquire the lock).
		if _, statErr := os.Stat(worktreePath); statErr == nil {
			_ = runGit(bareRepoPath, "worktree", "remove", "--force", worktreePath)
		}
		_ = runGit(bareRepoPath, "branch", "-D", branch)
	}

	base := opts.Base
	if base == "" {
		base = defaultBranchFor(bareRepoPath)
	}

	args := []string{"worktree", "add", worktreePath, "-b", branch, base}
	if err := runGit(bareRepoPath, args...); err != nil {
		return "", fmt.Errorf("WorktreeCreate %q: %w", name, err)
	}

	return worktreePath, nil
}

// CleanupOpts configures behavior of Cleanup.
type CleanupOpts struct {
	KeepOnFailure bool // only remove if branch is merged into main
}

// Cleanup removes a worktree. Branch and commits survive in the bare repo.
// If opts.KeepOnFailure is true, the worktree is only removed if run/<name> is
// reachable from main (i.e., merged). If the branch is not merged, an error is
// returned and the worktree is left intact.
func Cleanup(workDir, bareRepoPath, name string, opts CleanupOpts) error {
	worktreePath := filepath.Join(workDir, "worktrees", name)

	if _, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("Cleanup: worktree %q not found: %w", name, err)
	}

	release, err := acquireBareRepoLock(bareRepoPath)
	if err != nil {
		return fmt.Errorf("Cleanup: %w", err)
	}
	defer release()

	if opts.KeepOnFailure {
		branch := "run/" + name
		target := defaultBranchFor(bareRepoPath)
		if err := runGit(bareRepoPath, "merge-base", "--is-ancestor", branch, target); err != nil {
			return fmt.Errorf("Cleanup: branch %q is not merged into %q; keeping worktree", branch, target)
		}
	}

	if err := runGit(bareRepoPath, "worktree", "remove", "--force", worktreePath); err != nil {
		return fmt.Errorf("Cleanup %q: %w", name, err)
	}

	return nil
}

// DestroyOpts configures behavior of WorktreeDestroy.
type DestroyOpts struct {
	KeepBranch bool // if true, keep the branch in the bare repo after removing the worktree
}

// WorktreeDestroy removes the worktree directory AND deletes the branch
// in one atomic (lock-held) operation. Default behavior is force —
// task branches are ephemeral by design and the caller is responsible
// for ensuring any merge happened first. Use KeepBranch to preserve
// the branch.
//
// Replaces the shell-level compose of `tgwm cleanup` + manual
// `git branch -D` that ad-hoc callers used previously.
func WorktreeDestroy(workDir, bareRepoPath, name string, opts DestroyOpts) error {
	worktreePath := filepath.Join(workDir, "worktrees", name)

	release, err := acquireBareRepoLock(bareRepoPath)
	if err != nil {
		return fmt.Errorf("WorktreeDestroy: %w", err)
	}
	defer release()

	if _, err := os.Stat(worktreePath); err == nil {
		if err := runGit(bareRepoPath, "worktree", "remove", "--force", worktreePath); err != nil {
			return fmt.Errorf("WorktreeDestroy %q: remove worktree: %w", name, err)
		}
	}

	if !opts.KeepBranch {
		branch := "run/" + name
		// `git branch -D` is force-delete (vs `-d` which is safe-delete).
		// Task branches are ephemeral; if the caller invoked destroy
		// they want the branch gone regardless of merge state.
		if err := runGit(bareRepoPath, "branch", "-D", branch); err != nil {
			return fmt.Errorf("WorktreeDestroy %q: delete branch: %w", name, err)
		}
	}

	return nil
}

// WorktreePath returns the path to an existing worktree. Errors if it doesn't exist.
func WorktreePath(workDir, name string) (string, error) {
	p := filepath.Join(workDir, "worktrees", name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("WorktreePath: worktree %q not found: %w", name, err)
	}
	return p, nil
}

// PruneResult lists the worktrees a Prune call destroyed.
type PruneResult struct {
	Destroyed []string // names of worktrees that were destroyed
}

// Prune destroys every worktree on the bare repo whose name matches the
// given glob pattern. Pattern syntax follows filepath.Match — `*` matches
// any sequence of non-separator characters, `?` matches a single char.
//
// Each match goes through WorktreeDestroy semantics (worktree + branch
// removed atomically under the per-bare-repo lock). Failed destroys are
// recorded but don't stop the prune — Prune is best-effort cleanup.
//
// Use cases: tearing down task worktrees from a canceled implement_spec run
// (`tgwm prune "<comp>-<parent>-*"`), reclaiming space after a series
// of failed builds, or any scripted cleanup that needs name-pattern
// matching rather than one-at-a-time destroy.
func Prune(workDir, bareRepoPath, pattern string) (PruneResult, error) {
	// Validate the pattern up front so a malformed pattern errors even
	// when no worktrees would match (or none exist). filepath.Match's
	// only error is ErrBadPattern, so a no-op match against an empty
	// name surfaces it cheaply.
	if _, err := filepath.Match(pattern, ""); err != nil {
		return PruneResult{}, fmt.Errorf("Prune: bad pattern %q: %w", pattern, err)
	}

	worktrees, err := listWorktrees(bareRepoPath)
	if err != nil {
		return PruneResult{}, fmt.Errorf("Prune: list worktrees: %w", err)
	}

	var destroyed []string
	var firstErr error
	for _, wt := range worktrees {
		match, err := filepath.Match(pattern, wt.Name)
		if err != nil {
			return PruneResult{}, fmt.Errorf("Prune: bad pattern %q: %w", pattern, err)
		}
		if !match {
			continue
		}
		if err := WorktreeDestroy(workDir, bareRepoPath, wt.Name, DestroyOpts{}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		destroyed = append(destroyed, wt.Name)
	}

	return PruneResult{Destroyed: destroyed}, firstErr
}
