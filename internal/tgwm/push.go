package tgwm

import "fmt"

// Push pushes a branch from the bare repo to a remote.
// Defaults to pushing the bare repo's default branch (HEAD) if branch is empty.
func Push(bareRepoPath, remote, branch string) error {
	if branch == "" {
		branch = defaultBranchFor(bareRepoPath)
	}
	if err := runGit(bareRepoPath, "push", remote, branch); err != nil {
		return fmt.Errorf("Push: %w", err)
	}
	return nil
}
