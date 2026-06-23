package tgwm

import (
	"strings"
	"testing"
)

func TestPush(t *testing.T) {
	t.Run("pushes main to remote bare repo", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_ = workDir

		// Create a second bare repo as the "remote"
		remoteBare := t.TempDir()
		localGitOutput(t, remoteBare, "init", "--bare")

		// setupBareRepo may have set origin; overwrite or add as needed
		localGitOutput(t, bareRepoPath, "remote", "set-url", "origin", remoteBare)

		// Push main to remote
		if err := Push(bareRepoPath, "origin", "main"); err != nil {
			t.Fatalf("Push: %v", err)
		}

		// Remote should now have main branch with our commits
		out := localGitOutput(t, remoteBare, "log", "--oneline", "main")
		if !strings.Contains(out, "initial commit") {
			t.Errorf("remote main does not have expected commit; got:\n%s", out)
		}
	})

	t.Run("defaults to main when branch is empty", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_ = workDir

		remoteBare := t.TempDir()
		localGitOutput(t, remoteBare, "init", "--bare")
		localGitOutput(t, bareRepoPath, "remote", "set-url", "origin", remoteBare)

		// Push with empty branch - should default to main
		if err := Push(bareRepoPath, "origin", ""); err != nil {
			t.Fatalf("Push with empty branch: %v", err)
		}

		// Remote should have main
		out := localGitOutput(t, remoteBare, "log", "--oneline", "main")
		if !strings.Contains(out, "initial commit") {
			t.Errorf("remote main does not have expected commit; got:\n%s", out)
		}
	})

	t.Run("pushes branch with commits after merges", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Add a worktree, commit, and merge
		_, err := WorktreeCreate(workDir, bareRepoPath, "feat1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}
		wtPath, _ := WorktreePath(workDir, "feat1")
		addCommit(t, wtPath, "feature.txt", "feature", "add feature.txt")
		if err := Merge(bareRepoPath, "feat1", MergeOpts{}); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		remoteBare := t.TempDir()
		localGitOutput(t, remoteBare, "init", "--bare")
		localGitOutput(t, bareRepoPath, "remote", "set-url", "origin", remoteBare)

		if err := Push(bareRepoPath, "origin", "main"); err != nil {
			t.Fatalf("Push: %v", err)
		}

		// Remote should have the feature.txt file on main
		out := localGitOutput(t, remoteBare, "show", "main:feature.txt")
		if strings.TrimSpace(out) != "feature" {
			t.Errorf("remote main:feature.txt = %q, want %q", strings.TrimSpace(out), "feature")
		}
	})

	t.Run("returns error for invalid remote", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_ = workDir

		err := Push(bareRepoPath, "nonexistent-remote", "main")
		if err == nil {
			t.Error("expected error pushing to nonexistent remote, got nil")
		}
	})
}
