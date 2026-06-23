package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `# comment
TOIL_TEST_A=hello
TOIL_TEST_B="quoted value"
TOIL_TEST_C='single quoted'

TOIL_TEST_D=already_set
`
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-set one var to verify it isn't overwritten.
	t.Setenv("TOIL_TEST_D", "original")

	if err := LoadEnvFile(envFile); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{"TOIL_TEST_A", "hello"},
		{"TOIL_TEST_B", "quoted value"},
		{"TOIL_TEST_C", "single quoted"},
		{"TOIL_TEST_D", "original"},
	}
	for _, tt := range tests {
		got := os.Getenv(tt.key)
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	err := LoadEnvFile("/nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
