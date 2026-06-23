package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildComponent_CreateWorktreeDelegatesToTgwm verifies that the
// create_worktree.sh script uses tgwm to set up the worktree. Python venv
// bootstrap is now tgwm's responsibility (internal/tgwm), not the shell script's.
func TestBuildComponent_CreateWorktreeDelegatesToTgwm(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "definitions", "workflows", "build_component", "create_worktree.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := string(data)

	if !strings.Contains(script, "tgwm init") {
		t.Fatalf("expected create_worktree.sh to call tgwm init")
	}
	if !strings.Contains(script, "tgwm worktree create") {
		t.Fatalf("expected create_worktree.sh to call tgwm worktree create")
	}
}
