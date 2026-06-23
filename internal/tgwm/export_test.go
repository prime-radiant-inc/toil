package tgwm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExport(t *testing.T) {
	t.Run("exports main branch files to destination directory", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Create a worktree, add a file, merge it into main
		_, err := WorktreeCreate(workDir, bareRepoPath, "wt1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}
		wtPath := filepath.Join(workDir, "worktrees", "wt1")
		addCommit(t, wtPath, "feature.txt", "feature content", "add feature.txt")

		if err := Merge(bareRepoPath, "wt1", MergeOpts{}); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		// Export main to a destination
		dest := t.TempDir()
		if err := Export(bareRepoPath, dest); err != nil {
			t.Fatalf("Export: %v", err)
		}

		// Destination should have README.md (from initial commit) and feature.txt (from merge)
		readmePath := filepath.Join(dest, "README.md")
		if _, err := os.Stat(readmePath); err != nil {
			t.Errorf("README.md not found in export destination: %v", err)
		}

		featurePath := filepath.Join(dest, "feature.txt")
		data, err := os.ReadFile(featurePath)
		if err != nil {
			t.Errorf("feature.txt not found in export destination: %v", err)
		} else if string(data) != "feature content" {
			t.Errorf("feature.txt content = %q, want %q", string(data), "feature content")
		}
	})

	t.Run("export to nonexistent destination path creates it", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)
		_ = workDir

		// Dest path that does not yet exist
		base := t.TempDir()
		dest := filepath.Join(base, "new-dest")

		if err := Export(bareRepoPath, dest); err != nil {
			t.Fatalf("Export: %v", err)
		}

		if _, err := os.Stat(dest); err != nil {
			t.Errorf("expected dest to be created: %v", err)
		}

		readmePath := filepath.Join(dest, "README.md")
		if _, err := os.Stat(readmePath); err != nil {
			t.Errorf("README.md not found in export destination: %v", err)
		}
	})

	t.Run("export to non-empty destination fails", func(t *testing.T) {
		_, bareRepoPath := setupBareRepo(t)

		dest := t.TempDir()
		if err := os.WriteFile(filepath.Join(dest, "blocker.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("create blocker file: %v", err)
		}
		if err := Export(bareRepoPath, dest); err == nil {
			t.Fatal("expected error when exporting to non-empty directory")
		}
	})

	t.Run("multiple merges: destination has all files", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// First worktree
		_, err := WorktreeCreate(workDir, bareRepoPath, "wt-a", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate wt-a: %v", err)
		}
		addCommit(t, filepath.Join(workDir, "worktrees", "wt-a"), "file-a.txt", "from A", "add file-a.txt")
		if err := Merge(bareRepoPath, "wt-a", MergeOpts{}); err != nil {
			t.Fatalf("Merge wt-a: %v", err)
		}

		// Second worktree based on main after first merge
		_, err = WorktreeCreate(workDir, bareRepoPath, "wt-b", WorktreeOpts{Base: "main"})
		if err != nil {
			t.Fatalf("WorktreeCreate wt-b: %v", err)
		}
		addCommit(t, filepath.Join(workDir, "worktrees", "wt-b"), "file-b.txt", "from B", "add file-b.txt")
		if err := Merge(bareRepoPath, "wt-b", MergeOpts{}); err != nil {
			t.Fatalf("Merge wt-b: %v", err)
		}

		dest := t.TempDir()
		if err := Export(bareRepoPath, dest); err != nil {
			t.Fatalf("Export: %v", err)
		}

		for _, f := range []string{"README.md", "file-a.txt", "file-b.txt"} {
			if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
				t.Errorf("%s not found in export destination: %v", f, err)
			}
		}
	})
}
