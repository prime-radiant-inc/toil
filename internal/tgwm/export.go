package tgwm

import "fmt"

// Export checks out the bare repo's default branch (HEAD) to a directory.
func Export(bareRepoPath, destPath string) error {
	branch := defaultBranchFor(bareRepoPath)
	if err := runGit("", "clone", "--branch", branch, bareRepoPath, destPath); err != nil {
		return fmt.Errorf("Export: %w", err)
	}
	return nil
}
