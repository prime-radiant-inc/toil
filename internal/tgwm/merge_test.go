package tgwm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	t.Run("basic merge: main contains file committed in worktree", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "comp1", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		wtPath := filepath.Join(workDir, "worktrees", "comp1")
		addCommit(t, wtPath, "new.txt", "hello", "add new.txt")

		if err := Merge(bareRepoPath, "comp1", MergeOpts{}); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		// Verify main has the file
		out, err := gitOutput(bareRepoPath, "show", "main:new.txt")
		if err != nil {
			t.Fatalf("git show main:new.txt: %v", err)
		}
		if strings.TrimSpace(out) != "hello" {
			t.Errorf("main:new.txt = %q, want %q", strings.TrimSpace(out), "hello")
		}
	})

	t.Run("merge preserves history with --no-ff merge commit", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "comp2", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		wtPath := filepath.Join(workDir, "worktrees", "comp2")
		addCommit(t, wtPath, "work.txt", "work", "add work.txt")

		if err := Merge(bareRepoPath, "comp2", MergeOpts{}); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		// git log --oneline on main should show a merge commit
		log := localGitOutput(t, bareRepoPath, "log", "--oneline", "main")
		if !strings.Contains(log, "Merge comp2") {
			t.Errorf("expected merge commit in log, got:\n%s", log)
		}
	})

	t.Run("sequential merges: second component sees first's changes", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// First worktree: add file-a.txt
		_, err := WorktreeCreate(workDir, bareRepoPath, "comp-a", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate comp-a: %v", err)
		}
		wtA := filepath.Join(workDir, "worktrees", "comp-a")
		addCommit(t, wtA, "file-a.txt", "from A", "add file-a.txt")

		if err := Merge(bareRepoPath, "comp-a", MergeOpts{}); err != nil {
			t.Fatalf("Merge comp-a: %v", err)
		}

		// Second worktree based on main (after first merge)
		_, err = WorktreeCreate(workDir, bareRepoPath, "comp-b", WorktreeOpts{Base: "main"})
		if err != nil {
			t.Fatalf("WorktreeCreate comp-b: %v", err)
		}
		wtB := filepath.Join(workDir, "worktrees", "comp-b")
		addCommit(t, wtB, "file-b.txt", "from B", "add file-b.txt")

		if err := Merge(bareRepoPath, "comp-b", MergeOpts{}); err != nil {
			t.Fatalf("Merge comp-b: %v", err)
		}

		// main should have both files
		outA, err := gitOutput(bareRepoPath, "show", "main:file-a.txt")
		if err != nil {
			t.Fatalf("git show main:file-a.txt: %v", err)
		}
		if strings.TrimSpace(outA) != "from A" {
			t.Errorf("main:file-a.txt = %q, want %q", strings.TrimSpace(outA), "from A")
		}

		outB, err := gitOutput(bareRepoPath, "show", "main:file-b.txt")
		if err != nil {
			t.Fatalf("git show main:file-b.txt: %v", err)
		}
		if strings.TrimSpace(outB) != "from B" {
			t.Errorf("main:file-b.txt = %q, want %q", strings.TrimSpace(outB), "from B")
		}
	})

	t.Run("merge into custom target branch", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Create a target branch off main.
		_, err := WorktreeCreate(workDir, bareRepoPath, "setup-target", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate setup-target: %v", err)
		}
		setupPath := filepath.Join(workDir, "worktrees", "setup-target")
		addCommit(t, setupPath, "base.txt", "base", "add base.txt")
		// Merge setup into main so we have a non-empty main, then create
		// a custom branch from that state.
		if err := Merge(bareRepoPath, "setup-target", MergeOpts{}); err != nil {
			t.Fatalf("Merge setup-target: %v", err)
		}
		// Create the custom target branch at the current main.
		if err := runGit(bareRepoPath, "branch", "integration", "main"); err != nil {
			t.Fatalf("create integration branch: %v", err)
		}

		// Create a worktree based on the integration branch.
		_, err = WorktreeCreate(workDir, bareRepoPath, "feat", WorktreeOpts{Base: "integration"})
		if err != nil {
			t.Fatalf("WorktreeCreate feat: %v", err)
		}
		wtPath := filepath.Join(workDir, "worktrees", "feat")
		addCommit(t, wtPath, "feature.txt", "new feature", "add feature.txt")

		// Merge into integration branch, NOT main.
		if err := Merge(bareRepoPath, "feat", MergeOpts{Target: "integration"}); err != nil {
			t.Fatalf("Merge feat into integration: %v", err)
		}

		// integration branch should have the file.
		out, err := gitOutput(bareRepoPath, "show", "integration:feature.txt")
		if err != nil {
			t.Fatalf("git show integration:feature.txt: %v", err)
		}
		if strings.TrimSpace(out) != "new feature" {
			t.Errorf("integration:feature.txt = %q, want %q", strings.TrimSpace(out), "new feature")
		}

		// main should NOT have the file (merge went to integration, not main).
		_, err = gitOutput(bareRepoPath, "show", "main:feature.txt")
		if err == nil {
			t.Error("expected main:feature.txt to not exist, but it does")
		}
	})

	t.Run("merge with empty target defaults to main", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "default-target", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}
		wtPath := filepath.Join(workDir, "worktrees", "default-target")
		addCommit(t, wtPath, "dt.txt", "default", "add dt.txt")

		// Empty target → should merge into main.
		if err := Merge(bareRepoPath, "default-target", MergeOpts{}); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		out, err := gitOutput(bareRepoPath, "show", "main:dt.txt")
		if err != nil {
			t.Fatalf("git show main:dt.txt: %v", err)
		}
		if strings.TrimSpace(out) != "default" {
			t.Errorf("main:dt.txt = %q, want %q", strings.TrimSpace(out), "default")
		}
	})

	t.Run("merge conflict returns error with useful info", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		// Two worktrees that both modify the same file with conflicting content
		_, err := WorktreeCreate(workDir, bareRepoPath, "conflict-a", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate conflict-a: %v", err)
		}
		wtA := filepath.Join(workDir, "worktrees", "conflict-a")
		addCommit(t, wtA, "shared.txt", "version A\n", "add shared.txt from A")

		_, err = WorktreeCreate(workDir, bareRepoPath, "conflict-b", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate conflict-b: %v", err)
		}
		wtB := filepath.Join(workDir, "worktrees", "conflict-b")
		addCommit(t, wtB, "shared.txt", "version B\n", "add shared.txt from B")

		// Merge first branch succeeds
		if err := Merge(bareRepoPath, "conflict-a", MergeOpts{}); err != nil {
			t.Fatalf("Merge conflict-a: %v", err)
		}

		// Merge second branch should fail with conflict
		err = Merge(bareRepoPath, "conflict-b", MergeOpts{})
		if err == nil {
			t.Fatal("expected merge conflict error, got nil")
		}
		if !strings.Contains(err.Error(), "conflict") && !strings.Contains(err.Error(), "CONFLICT") &&
			!strings.Contains(err.Error(), "Merge") {
			t.Errorf("error message not informative: %v", err)
		}
	})

	t.Run("KeepOnConflict leaves worktree on conflict", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "keep-a", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate keep-a: %v", err)
		}
		wtA := filepath.Join(workDir, "worktrees", "keep-a")
		addCommit(t, wtA, "shared.txt", "version A\n", "add shared.txt from A")

		_, err = WorktreeCreate(workDir, bareRepoPath, "keep-b", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate keep-b: %v", err)
		}
		wtB := filepath.Join(workDir, "worktrees", "keep-b")
		addCommit(t, wtB, "shared.txt", "version B\n", "add shared.txt from B")

		// Merge first branch succeeds
		if err := Merge(bareRepoPath, "keep-a", MergeOpts{}); err != nil {
			t.Fatalf("Merge keep-a: %v", err)
		}

		// Merge second branch with KeepOnConflict should fail but leave the worktree
		result, err := MergeWithResult(bareRepoPath, "keep-b", MergeOpts{KeepOnConflict: true})
		if err == nil {
			t.Fatal("expected merge conflict error, got nil")
		}
		if result.WorktreePath == "" {
			t.Fatal("expected non-empty WorktreePath on conflict with KeepOnConflict=true")
		}

		// Worktree directory must still exist
		if _, statErr := os.Stat(result.WorktreePath); os.IsNotExist(statErr) {
			t.Errorf("worktree directory was cleaned up, expected it to exist at %s", result.WorktreePath)
		}

		// Conflict markers must be present in the file
		content, readErr := os.ReadFile(filepath.Join(result.WorktreePath, "shared.txt"))
		if readErr != nil {
			t.Fatalf("ReadFile shared.txt: %v", readErr)
		}
		if !strings.Contains(string(content), "<<<<<<<") {
			t.Errorf("expected conflict markers in shared.txt, got:\n%s", string(content))
		}

		// Clean up manually
		runGit(bareRepoPath, "worktree", "remove", "--force", result.WorktreePath) //nolint:errcheck
		_ = os.RemoveAll(filepath.Dir(result.WorktreePath))
	})

	t.Run("KeepOnConflict=false still cleans up on conflict", func(t *testing.T) {
		workDir, bareRepoPath := setupBareRepo(t)

		_, err := WorktreeCreate(workDir, bareRepoPath, "noclean-a", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate noclean-a: %v", err)
		}
		wtA := filepath.Join(workDir, "worktrees", "noclean-a")
		addCommit(t, wtA, "shared.txt", "version A\n", "add shared.txt from A")

		_, err = WorktreeCreate(workDir, bareRepoPath, "noclean-b", WorktreeOpts{})
		if err != nil {
			t.Fatalf("WorktreeCreate noclean-b: %v", err)
		}
		wtB := filepath.Join(workDir, "worktrees", "noclean-b")
		addCommit(t, wtB, "shared.txt", "version B\n", "add shared.txt from B")

		// Merge first branch succeeds
		if err := Merge(bareRepoPath, "noclean-a", MergeOpts{}); err != nil {
			t.Fatalf("Merge noclean-a: %v", err)
		}

		// Merge second branch without KeepOnConflict should fail and clean up
		result, err := MergeWithResult(bareRepoPath, "noclean-b", MergeOpts{KeepOnConflict: false})
		if err == nil {
			t.Fatal("expected merge conflict error, got nil")
		}
		if result.WorktreePath != "" {
			t.Errorf("expected empty WorktreePath on conflict with KeepOnConflict=false, got %s", result.WorktreePath)
		}
	})
}
