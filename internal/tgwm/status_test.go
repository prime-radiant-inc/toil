package tgwm

import (
	"path/filepath"
	"strings"
	"testing"
)

// realPath resolves symlinks for cross-platform path comparison in tests.
func realPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p // fall back to original if resolution fails
	}
	return resolved
}

func TestStatus(t *testing.T) {
	t.Run("returns project ID derived from bare repo name", func(t *testing.T) {
		_, bareRepoPath := setupBareRepo(t)

		info, err := Status(bareRepoPath)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		// setupBareRepo uses slug "test-proj", so bare repo is test-proj.git
		if info.ProjectID != "test-proj" {
			t.Errorf("ProjectID = %q, want %q", info.ProjectID, "test-proj")
		}
		if info.BareRepo != bareRepoPath {
			t.Errorf("BareRepo = %q, want %q", info.BareRepo, bareRepoPath)
		}
	})

	t.Run("merged worktree shows Merged=true, unmerged shows Merged=false", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Create two worktrees
		_, err := WorktreeCreate(workDir, bareRepoPath, "wt-merged", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate wt-merged: %v", err)
		}
		addCommit(t, filepath.Join(workDir, "worktrees", "wt-merged"), "merged.txt", "done", "add merged.txt")
		if err := Merge(bareRepoPath, "wt-merged", MergeOpts{}); err != nil {
			t.Fatalf("Merge wt-merged: %v", err)
		}

		_, err = WorktreeCreate(workDir, bareRepoPath, "wt-unmerged", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate wt-unmerged: %v", err)
		}
		addCommit(t, filepath.Join(workDir, "worktrees", "wt-unmerged"), "wip.txt", "wip", "add wip.txt")
		// Do NOT merge wt-unmerged

		info, err := Status(bareRepoPath)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if len(info.Worktrees) != 2 {
			t.Fatalf("expected 2 worktrees, got %d: %+v", len(info.Worktrees), info.Worktrees)
		}

		byName := make(map[string]WorktreeInfo)
		for _, wt := range info.Worktrees {
			byName[wt.Name] = wt
		}

		merged, ok := byName["wt-merged"]
		if !ok {
			t.Fatalf("wt-merged not found in status; got: %+v", info.Worktrees)
		}
		if !merged.Merged {
			t.Errorf("wt-merged.Merged = false, want true")
		}
		if merged.Branch != "run/wt-merged" {
			t.Errorf("wt-merged.Branch = %q, want %q", merged.Branch, "run/wt-merged")
		}

		unmerged, ok := byName["wt-unmerged"]
		if !ok {
			t.Fatalf("wt-unmerged not found in status; got: %+v", info.Worktrees)
		}
		if unmerged.Merged {
			t.Errorf("wt-unmerged.Merged = true, want false")
		}
	})

	t.Run("worktree path is present in status", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "wt-path", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		info, err := Status(bareRepoPath)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if len(info.Worktrees) != 1 {
			t.Fatalf("expected 1 worktree, got %d", len(info.Worktrees))
		}

		wt := info.Worktrees[0]
		expectedPath := realPath(t, filepath.Join(workDir, "worktrees", "wt-path"))
		gotPath := realPath(t, wt.Path)
		if gotPath != expectedPath {
			t.Errorf("worktree Path = %q, want %q", gotPath, expectedPath)
		}
	})

	t.Run("no worktrees returns empty list", func(t *testing.T) {
		_, bareRepoPath := setupBareRepo(t)

		info, err := Status(bareRepoPath)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if len(info.Worktrees) != 0 {
			t.Errorf("expected 0 worktrees, got %d: %+v", len(info.Worktrees), info.Worktrees)
		}
	})

	t.Run("worktree name extracted from path", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "my-component", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		info, err := Status(bareRepoPath)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if len(info.Worktrees) != 1 {
			t.Fatalf("expected 1 worktree, got %d", len(info.Worktrees))
		}

		if info.Worktrees[0].Name != "my-component" {
			t.Errorf("Name = %q, want %q", info.Worktrees[0].Name, "my-component")
		}
		if !strings.HasSuffix(info.Worktrees[0].Path, "my-component") {
			t.Errorf("Path %q should end with 'my-component'", info.Worktrees[0].Path)
		}
	})
}
