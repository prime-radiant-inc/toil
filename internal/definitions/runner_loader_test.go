package definitions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRunnerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")

	content := []byte("id: test_runner\ntype: codex\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	runner, err := LoadRunnerFile(path)
	if err != nil {
		t.Fatalf("load runner: %v", err)
	}
	if runner.ID != "test_runner" {
		t.Fatalf("unexpected runner id: %s", runner.ID)
	}
	if runner.Type != "codex" {
		t.Fatalf("unexpected runner type: %s", runner.Type)
	}
}
