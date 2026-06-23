package tgwm

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// StatusInfo holds project state.
type StatusInfo struct {
	ProjectID string
	BareRepo  string
	Worktrees []WorktreeInfo
}

// WorktreeInfo holds state for a single worktree.
type WorktreeInfo struct {
	Name   string
	Path   string
	Branch string
	Merged bool // true if branch is ancestor of main
}

// Status returns project state: bare repo path, active worktrees, and merge status.
func Status(bareRepoPath string) (*StatusInfo, error) {
	projectID := strings.TrimSuffix(filepath.Base(bareRepoPath), ".git")

	worktrees, err := listWorktrees(bareRepoPath)
	if err != nil {
		return nil, fmt.Errorf("Status: %w", err)
	}

	return &StatusInfo{
		ProjectID: projectID,
		BareRepo:  bareRepoPath,
		Worktrees: worktrees,
	}, nil
}

// listWorktrees parses `git worktree list --porcelain` and returns worktree info.
// The bare repo itself is skipped.
func listWorktrees(bareRepoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "-C", bareRepoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	var result []WorktreeInfo

	// Output is blocks separated by blank lines. Each block has:
	//   worktree <path>
	//   HEAD <sha>
	//   branch refs/heads/<branch>   (or "detached")
	//   bare                         (only for bare repos)
	blocks := strings.Split(strings.TrimSpace(string(out)), "\n\n")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 {
			continue
		}

		var wtPath, branch string
		isBare := false

		for _, line := range lines {
			switch {
			case strings.HasPrefix(line, "worktree "):
				wtPath = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch refs/heads/"):
				branch = strings.TrimPrefix(line, "branch refs/heads/")
			case line == "bare":
				isBare = true
			}
		}

		// Skip the bare repo itself
		if isBare || branch == "" {
			continue
		}

		merged := isMergedIntoDefault(bareRepoPath, branch)
		name := filepath.Base(wtPath)

		result = append(result, WorktreeInfo{
			Name:   name,
			Path:   wtPath,
			Branch: branch,
			Merged: merged,
		})
	}

	return result, nil
}

// isMergedIntoDefault returns true if branch is an ancestor of the
// bare repo's default branch (whatever HEAD points at).
func isMergedIntoDefault(bareRepoPath, branch string) bool {
	target := defaultBranchFor(bareRepoPath)
	err := runGit(bareRepoPath, "merge-base", "--is-ancestor", branch, target)
	return err == nil
}
