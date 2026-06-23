package tgwm

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentMergesToSameTargetAutoSerialize pins the safety property
// per-task worktree merges depend on: N concurrent merges to the same
// target branch produce a linearized commit history with every task's
// work present. tgwm.Merge takes a per-bare-repo flock internally — no
// caller coordination required. If the lock weren't there, ref updates
// would race and merge commits would be lost.
func TestConcurrentMergesToSameTargetAutoSerialize(t *testing.T) {
	workDir, bareRepoPath := setupBareRepo(t)

	componentName := "comp-auto-serialize"
	componentPath, err := WorktreeCreate(workDir, bareRepoPath, componentName, WorktreeOpts{})
	if err != nil {
		t.Fatalf("create component worktree: %v", err)
	}
	addCommit(t, componentPath, "component.txt", "component base", "component base")
	componentBranch := "run/" + componentName

	const N = 6
	taskNames := make([]string, N)
	taskFiles := make([]string, N)
	for i := 0; i < N; i++ {
		name := fmt.Sprintf("%s-task-%d", componentName, i)
		taskNames[i] = name
		taskFiles[i] = fmt.Sprintf("task-%d.txt", i)
		if _, err := WorktreeCreate(workDir, bareRepoPath, name, WorktreeOpts{Base: componentBranch}); err != nil {
			t.Fatalf("create task worktree %s: %v", name, err)
		}
		taskPath := filepath.Join(workDir, "worktrees", name)
		addCommit(t, taskPath, taskFiles[i], fmt.Sprintf("content from %s", name), fmt.Sprintf("add %s", taskFiles[i]))
	}

	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := Merge(bareRepoPath, taskNames[idx], MergeOpts{Target: componentBranch}); err != nil {
				errs[idx] = fmt.Errorf("merge %s: %w", taskNames[idx], err)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("task %d: %v", i, err)
		}
	}
	if t.Failed() {
		return
	}

	for i, f := range taskFiles {
		out, err := gitOutput(bareRepoPath, "show", componentBranch+":"+f)
		if err != nil {
			t.Errorf("git show %s:%s failed: %v", componentBranch, f, err)
			continue
		}
		wantPrefix := fmt.Sprintf("content from %s", taskNames[i])
		if !strings.Contains(out, wantPrefix) {
			t.Errorf("%s:%s content = %q, want to contain %q", componentBranch, f, out, wantPrefix)
		}
	}

	log := localGitOutput(t, bareRepoPath, "log", "--oneline", componentBranch)
	mergeCount := strings.Count(log, "Merge "+componentName+"-task-")
	if mergeCount != N {
		t.Errorf("expected %d merge commits on %s, got %d\nlog:\n%s", N, componentBranch, mergeCount, log)
	}
}

// TestConcurrentWorktreeCreatesAutoSerialize covers the symmetric
// property: concurrent WorktreeCreate calls don't race on the bare
// repo's .git/config.lock during `git worktree add -b`. Without
// serialization, every-other create would fail with "could not lock
// config file."
func TestConcurrentWorktreeCreatesAutoSerialize(t *testing.T) {
	workDir, bareRepoPath := setupBareRepo(t)

	const N = 6
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("concurrent-%d", idx)
			if _, err := WorktreeCreate(workDir, bareRepoPath, name, WorktreeOpts{}); err != nil {
				errs[idx] = fmt.Errorf("create %s: %w", name, err)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worktree %d: %v", i, err)
		}
	}
}
