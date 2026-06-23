package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareEvalEnv_ExpandsProjectDir(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_EVAL_EXPAND", projectDir)
	spec := &Spec{
		ProjectDir: "${TEST_EVAL_EXPAND}",
		Inputs:     map[string]any{},
	}

	got, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}
	if got != projectDir {
		t.Fatalf("expected projectDir=%q, got %q", projectDir, got)
	}
	if os.Getenv("PROJECT_DIR") != projectDir {
		t.Fatalf("PROJECT_DIR not set: %q", os.Getenv("PROJECT_DIR"))
	}
}

func TestPrepareEvalEnv_RelativeProjectDir(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		ProjectDir: "subdir",
		Inputs:     map[string]any{},
	}

	got, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}
	expected := filepath.Join(root, "subdir")
	if got != expected {
		t.Fatalf("expected projectDir=%q, got %q", expected, got)
	}
}

func TestPrepareEvalEnv_PrependsBinToPath(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	spec := &Spec{
		ProjectDir: root,
		Inputs:     map[string]any{},
	}

	origPath := os.Getenv("PATH")
	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	newPath := os.Getenv("PATH")
	prefix := binDir + string(os.PathListSeparator)
	if !strings.HasPrefix(newPath, prefix) {
		t.Fatalf("PATH should start with %q, got %q", prefix, newPath)
	}

	// Verify the original PATH is still present after the prefix.
	rest := strings.TrimPrefix(newPath, prefix)
	if rest != origPath {
		t.Fatalf("original PATH not preserved: got %q, want %q", rest, origPath)
	}
}

func TestPrepareEvalEnv_NoBinDir(t *testing.T) {
	root := t.TempDir()
	// No bin/ directory created.
	spec := &Spec{
		ProjectDir: root,
		Inputs:     map[string]any{},
	}

	origPath := os.Getenv("PATH")
	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	// PATH should be unchanged.
	if os.Getenv("PATH") != origPath {
		t.Fatalf("PATH changed when bin/ doesn't exist: %q", os.Getenv("PATH"))
	}
}

func TestPrepareEvalEnv_WiresLedgerPath_Relative(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	spec := &Spec{
		ProjectDir: projectDir,
		Inputs: map[string]any{
			"ledger_path": "semantic_port/ledger.tsv",
		},
	}

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	expected := filepath.Join(projectDir, "semantic_port/ledger.tsv")
	if os.Getenv("LEDGER_PATH") != expected {
		t.Fatalf("LEDGER_PATH=%q, want %q", os.Getenv("LEDGER_PATH"), expected)
	}
}

func TestPrepareEvalEnv_WiresLedgerPath_Absolute(t *testing.T) {
	root := t.TempDir()
	absLedger := filepath.Join(root, "absolute", "ledger.tsv")

	spec := &Spec{
		ProjectDir: root,
		Inputs: map[string]any{
			"ledger_path": absLedger,
		},
	}

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	// Absolute paths should be used as-is.
	if os.Getenv("LEDGER_PATH") != absLedger {
		t.Fatalf("LEDGER_PATH=%q, want %q", os.Getenv("LEDGER_PATH"), absLedger)
	}
}

func TestPrepareEvalEnv_NoLedgerPathInput(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		ProjectDir: root,
		Inputs:     map[string]any{},
	}

	// Set LEDGER_PATH to something so we can verify it's not changed.
	t.Setenv("LEDGER_PATH", "should-not-change")

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	if os.Getenv("LEDGER_PATH") != "should-not-change" {
		t.Fatalf("LEDGER_PATH changed when no input: %q", os.Getenv("LEDGER_PATH"))
	}
}

func TestPrepareEvalEnv_EmptyLedgerPath(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		ProjectDir: root,
		Inputs: map[string]any{
			"ledger_path": "",
		},
	}

	t.Setenv("LEDGER_PATH", "should-not-change")

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	// Empty ledger_path should not set LEDGER_PATH.
	if os.Getenv("LEDGER_PATH") != "should-not-change" {
		t.Fatalf("LEDGER_PATH changed for empty input: %q", os.Getenv("LEDGER_PATH"))
	}
}

func TestPrepareEvalEnv_SetsProjectDirInput(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		ProjectDir: root,
		Inputs:     map[string]any{},
	}

	got, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	// project_dir input should be set to the resolved path.
	if v, ok := spec.Inputs["project_dir"]; !ok {
		t.Fatal("project_dir not set in inputs")
	} else if v != got {
		t.Fatalf("project_dir input=%q, want %q", v, got)
	}
}

func TestCleanEvalArtifacts_RemovesProjectDir(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "eval-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanEvalArtifacts(root, projectDir)

	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Fatalf("projectDir should have been removed, got err=%v", err)
	}
}

func TestCleanEvalArtifacts_RemovesBareRepos(t *testing.T) {
	root := t.TempDir()
	reposDir := filepath.Join(root, "repos")
	if err := os.MkdirAll(filepath.Join(reposDir, "test-proj.git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cleanEvalArtifacts(root, "")

	entries, err := os.ReadDir(reposDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("repos dir should be empty, got %d entries", len(entries))
	}
}

func TestCleanEvalArtifacts_NoReposDir(t *testing.T) {
	root := t.TempDir()
	// No repos/ directory — should not panic.
	cleanEvalArtifacts(root, "")
}

func TestPrepareEvalEnv_ExpandsInputEnvVars(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TEST_INPUT_VAR", "expanded-value")

	spec := &Spec{
		ProjectDir: root,
		Inputs: map[string]any{
			"some_input": "${TEST_INPUT_VAR}",
			"number":     42,
		},
	}

	_, err := prepareEvalEnv(root, spec)
	if err != nil {
		t.Fatalf("prepareEvalEnv: %v", err)
	}

	if spec.Inputs["some_input"] != "expanded-value" {
		t.Fatalf("input not expanded: %q", spec.Inputs["some_input"])
	}
	// Non-string inputs should be left alone.
	if spec.Inputs["number"] != 42 {
		t.Fatalf("non-string input modified: %v", spec.Inputs["number"])
	}
}
