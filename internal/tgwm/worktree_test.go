package tgwm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupBareRepo creates a source repo and initializes a bare repo from it.
// Returns workDir and bareRepoPath.
func setupBareRepo(t *testing.T) (workDir, bareRepoPath string) {
	t.Helper()
	source := makeRepoWithCommit(t)
	root := t.TempDir()
	workDir = t.TempDir()
	_, bareRepoPath, err := Init(root, workDir, InitOpts{Source: source, Slug: "test-proj"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return workDir, bareRepoPath
}

func TestWorktreeCreate(t *testing.T) {
	t.Run("creates worktree at expected path on run branch", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		wantPath := filepath.Join(workDir, "worktrees", "comp1")
		if wtPath != wantPath {
			t.Errorf("returned path = %q, want %q", wtPath, wantPath)
		}

		// Directory should exist
		if _, err := os.Stat(wtPath); err != nil {
			t.Errorf("worktree directory does not exist: %v", err)
		}

		// Should be on run/comp1 branch
		branch := strings.TrimSpace(localGitOutput(t, wtPath, "rev-parse", "--abbrev-ref", "HEAD"))
		if branch != "run/comp1" {
			t.Errorf("branch = %q, want %q", branch, "run/comp1")
		}

		// Should contain source files
		readmePath := filepath.Join(wtPath, "README.md")
		if _, err := os.Stat(readmePath); err != nil {
			t.Errorf("README.md not present in worktree: %v", err)
		}
	})

	t.Run("duplicate name returns error", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("first WorktreeCreate: %v", err)
		}

		_, err = WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err == nil {
			t.Error("expected error for duplicate worktree name, got nil")
		}
	})

	t.Run("custom base branch", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()
		workDir := t.TempDir()

		_, bareRepoPath, err := Init(root, workDir, InitOpts{Source: source, Slug: "base-test"})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}

		// Create a feature branch in the source repo with an extra commit
		localGitOutput(t, source, "checkout", "-b", "feature/x")
		addCommit(t, source, "extra.txt", "extra", "extra commit on feature/x")
		featureSHA := strings.TrimSpace(localGitOutput(t, source, "rev-parse", "HEAD"))

		// Fetch from source so bare repo has feature/x
		_, _, err = Init(root, workDir, InitOpts{Source: source, Slug: "base-test"})
		if err != nil {
			t.Fatalf("second Init: %v", err)
		}

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "wt-feature", WorktreeOpts{Base: "feature/x"})
		if err != nil {
			t.Fatalf("WorktreeCreate with base: %v", err)
		}

		// The worktree HEAD should point to featureSHA
		gotSHA := strings.TrimSpace(localGitOutput(t, wtPath, "rev-parse", "HEAD"))
		if gotSHA != featureSHA {
			t.Errorf("HEAD = %s, want %s (feature/x tip)", gotSHA, featureSHA)
		}

		// extra.txt should be present
		extraPath := filepath.Join(wtPath, "extra.txt")
		if _, err := os.Stat(extraPath); err != nil {
			t.Errorf("extra.txt not present in worktree based on feature/x: %v", err)
		}
	})
}

func TestWorktreeCleanup(t *testing.T) {
	t.Run("removes worktree directory, branch survives in bare repo", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		if err := Cleanup(workDir, bareRepoPath, "comp1", CleanupOpts{}); err != nil {
			t.Fatalf("Cleanup: %v", err)
		}

		// Worktree directory should be gone
		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("worktree directory still exists after Cleanup")
		}

		// Branch should still exist in bare repo
		if !bareHasRef(t, bareRepoPath, "refs/heads/run/comp1") {
			t.Error("branch run/comp1 was deleted from bare repo; expected it to survive")
		}
	})

	t.Run("KeepOnFailure=true, branch merged into main → removes worktree", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp2", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		// Commit a file in the worktree so the branch has content
		addCommit(t, wtPath, "work.txt", "done", "work commit")

		// Fast-forward main to run/comp2 in the bare repo (simulates a merge)
		branchSHA := strings.TrimSpace(localGitOutput(t, bareRepoPath, "rev-parse", "run/comp2"))
		localGitOutput(t, bareRepoPath, "update-ref", "refs/heads/main", branchSHA)

		if err := Cleanup(workDir, bareRepoPath, "comp2", CleanupOpts{KeepOnFailure: true}); err != nil {
			t.Fatalf("Cleanup with KeepOnFailure after merge: %v", err)
		}

		// Worktree directory should be gone
		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("worktree directory still exists after Cleanup")
		}
	})

	t.Run("KeepOnFailure=true, branch not merged → returns error, worktree preserved", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp3", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		// Commit something in the branch so it diverges from main
		addCommit(t, wtPath, "work.txt", "wip", "work in progress")

		err = Cleanup(workDir, bareRepoPath, "comp3", CleanupOpts{KeepOnFailure: true})
		if err == nil {
			t.Error("expected error when branch not merged, got nil")
		}

		// Worktree directory should still exist
		if _, err := os.Stat(wtPath); err != nil {
			t.Errorf("worktree directory was removed despite unmerged branch: %v", err)
		}
	})

	t.Run("nonexistent worktree returns error", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		err := Cleanup(workDir, bareRepoPath, "nonexistent", CleanupOpts{})
		if err == nil {
			t.Error("expected error for nonexistent worktree, got nil")
		}
	})
}

// TestWorktreeCreateForce pins the safety-net behavior plan_tasks
// re-plans depend on: when a task ID collides with a previous
// attempt's preserved branch, --force destroys the old
// worktree+branch and recreates fresh.
func TestWorktreeCreateForce(t *testing.T) {
	t.Run("force recreates over existing worktree+branch", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Initial create.
		first, err := WorktreeCreate(workDir, bareRepoPath, "task-2", WorktreeOpts{})
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		// Add a commit so the branch has divergent state — make sure
		// force can blow it away regardless.
		addCommit(t, first, "wip.txt", "wip", "previous attempt's wip")

		// Second create without --force should fail (branch already
		// exists). This is the case --force is supposed to handle.
		_, err = WorktreeCreate(workDir, bareRepoPath, "task-2", WorktreeOpts{})
		if err == nil {
			t.Error("expected create without --force to fail on existing branch")
		}

		// With --force, succeeds.
		second, err := WorktreeCreate(workDir, bareRepoPath, "task-2", WorktreeOpts{Force: true})
		if err != nil {
			t.Fatalf("force create: %v", err)
		}
		if second != first {
			t.Errorf("expected same path, got %q vs %q", second, first)
		}

		// New worktree's branch should be at the base, not at the
		// previous attempt's commit.
		out := localGitOutput(t, second, "log", "--oneline")
		if strings.Contains(out, "wip") {
			t.Errorf("force create did not discard previous attempt's commits:\n%s", out)
		}
	})

	t.Run("force on fresh name works like a normal create", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		path, err := WorktreeCreate(workDir, bareRepoPath, "fresh", WorktreeOpts{Force: true})
		if err != nil {
			t.Fatalf("force create: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("worktree directory not created: %v", err)
		}
	})
}

func TestWorktreeDestroy(t *testing.T) {
	t.Run("removes worktree directory and deletes branch", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		if err := WorktreeDestroy(workDir, bareRepoPath, "comp1", DestroyOpts{}); err != nil {
			t.Fatalf("WorktreeDestroy: %v", err)
		}

		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("worktree directory still exists after destroy")
		}
		if bareHasRef(t, bareRepoPath, "refs/heads/run/comp1") {
			t.Error("branch run/comp1 still exists after destroy; expected it to be deleted")
		}
	})

	t.Run("KeepBranch=true keeps the branch", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp2", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		if err := WorktreeDestroy(workDir, bareRepoPath, "comp2", DestroyOpts{KeepBranch: true}); err != nil {
			t.Fatalf("WorktreeDestroy: %v", err)
		}

		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("worktree directory still exists after destroy")
		}
		if !bareHasRef(t, bareRepoPath, "refs/heads/run/comp2") {
			t.Error("branch run/comp2 was deleted despite KeepBranch=true")
		}
	})

	t.Run("destroys unmerged branch (force semantics)", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		wtPath, err := WorktreeCreate(workDir, bareRepoPath, "comp3", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}
		// Add a commit so the branch diverges from main — this would
		// fail with `git branch -d` (lowercase) but should succeed
		// with destroy's force semantics.
		addCommit(t, wtPath, "wip.txt", "wip", "work in progress")

		if err := WorktreeDestroy(workDir, bareRepoPath, "comp3", DestroyOpts{}); err != nil {
			t.Fatalf("WorktreeDestroy on unmerged branch: %v", err)
		}

		if bareHasRef(t, bareRepoPath, "refs/heads/run/comp3") {
			t.Error("unmerged branch run/comp3 still exists; destroy should have force-deleted it")
		}
	})
}

func TestPrune(t *testing.T) {
	t.Run("destroys matching worktrees and leaves others alone", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		names := []string{"comp-a-parent-1", "comp-a-parent-1-task-0", "comp-a-parent-1-task-1", "comp-b-parent-1"}
		for _, n := range names {
			if _, err := WorktreeCreate(workDir, bareRepoPath, n, WorktreeOpts{}); err != nil {
				t.Fatalf("create %s: %v", n, err)
			}
		}

		// Prune all task-* worktrees of comp-a-parent-1.
		result, err := Prune(workDir, bareRepoPath, "comp-a-parent-1-task-*")
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}

		gotMap := map[string]bool{}
		for _, n := range result.Destroyed {
			gotMap[n] = true
		}
		want := []string{"comp-a-parent-1-task-0", "comp-a-parent-1-task-1"}
		for _, n := range want {
			if !gotMap[n] {
				t.Errorf("expected %s in destroyed list, got %v", n, result.Destroyed)
			}
		}
		if len(result.Destroyed) != len(want) {
			t.Errorf("destroyed %d worktrees, want %d (got %v)", len(result.Destroyed), len(want), result.Destroyed)
		}

		// Non-matching worktrees survive.
		for _, n := range []string{"comp-a-parent-1", "comp-b-parent-1"} {
			if _, err := os.Stat(filepath.Join(workDir, "worktrees", n)); err != nil {
				t.Errorf("worktree %q was unexpectedly removed: %v", n, err)
			}
			if !bareHasRef(t, bareRepoPath, "refs/heads/run/"+n) {
				t.Errorf("branch run/%s was unexpectedly deleted", n)
			}
		}

		// Pruned branches are gone.
		for _, n := range want {
			if bareHasRef(t, bareRepoPath, "refs/heads/run/"+n) {
				t.Errorf("branch run/%s should have been deleted", n)
			}
		}
	})

	t.Run("empty match", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_, _ = WorktreeCreate(workDir, bareRepoPath, "lone", WorktreeOpts{})

		result, err := Prune(workDir, bareRepoPath, "nomatch-*")
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(result.Destroyed) != 0 {
			t.Errorf("expected 0 destroyed, got %v", result.Destroyed)
		}
	})

	t.Run("invalid pattern errors", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_, err := Prune(workDir, bareRepoPath, "[bad")
		if err == nil {
			t.Error("expected error for malformed pattern")
		}
	})
}

func TestWorktreePath(t *testing.T) {
	t.Run("returns correct path for existing worktree", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		got, err := WorktreePath(workDir, "comp1")
		if err != nil {
			t.Fatalf("WorktreePath: %v", err)
		}

		want := filepath.Join(workDir, "worktrees", "comp1")
		if got != want {
			t.Errorf("WorktreePath = %q, want %q", got, want)
		}
	})

	t.Run("returns error for missing worktree", func(t *testing.T) {
		workDir := t.TempDir()

		_, err := WorktreePath(workDir, "nonexistent")
		if err == nil {
			t.Error("expected error for missing worktree, got nil")
		}
	})
}
