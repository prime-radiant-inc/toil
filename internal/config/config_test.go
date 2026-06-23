package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunsDirDefaultsToXDGDataHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	// Set XDG_DATA_HOME to a known location so the test is deterministic.
	dataHome := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", dataHome)
	got := RunsDir(root)
	want := filepath.Join(dataHome, "toil", "runs")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestRunsDirDefaultsToHomeDotLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	// Clear XDG_DATA_HOME so it falls back to ~/.local/share.
	t.Setenv("XDG_DATA_HOME", "")
	got := RunsDir(root)
	// Should end with .local/share/toil/runs, not be under root.
	if got == filepath.Join(root, "runs") {
		t.Fatalf("should not default to repo-local runs/, got %q", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}

func TestRunsDirUsesAbsoluteOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	override := filepath.Join(t.TempDir(), "custom-runs")
	t.Setenv("TOIL_RUNS_DIR", override)
	got := RunsDir(root)
	if got != override {
		t.Fatalf("expected %q, got %q", override, got)
	}
}

func TestRunsDirResolvesRelativeOverrideAgainstRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	t.Setenv("TOIL_RUNS_DIR", filepath.Join("var", "runs"))
	got := RunsDir(root)
	want := filepath.Join(root, "var", "runs")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestRestoreEnabledDefaultsToTrue(t *testing.T) {
	if !RestoreEnabled() {
		t.Fatal("expected restore to be enabled by default")
	}
}

func TestRestoreEnabledRespectsDisableEnv(t *testing.T) {
	t.Setenv("TOIL_DISABLE_RESTORE", "1")
	if RestoreEnabled() {
		t.Fatal("expected restore to be disabled when TOIL_DISABLE_RESTORE=1")
	}
}

func TestToilRootDefaultsToRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	got := ToilRoot(root)
	if got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}
}

func TestToilRootUsesAbsoluteOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	override := filepath.Join(t.TempDir(), "custom-root")
	t.Setenv("TOIL_ROOT", override)
	got := ToilRoot(root)
	if got != override {
		t.Fatalf("expected %q, got %q", override, got)
	}
}

func TestToilRootResolvesRelativeOverrideAgainstRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	t.Setenv("TOIL_ROOT", filepath.Join("var", "toil"))
	got := ToilRoot(root)
	want := filepath.Join(root, "var", "toil")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestIsCreatePaused_NoMarker(t *testing.T) {
	dir := t.TempDir()
	if IsCreatePaused(dir) {
		t.Fatal("expected not paused when marker is absent")
	}
}

func TestIsCreatePaused_WithMarker(t *testing.T) {
	dir := t.TempDir()
	markerPath := PausedMarkerPath(dir)
	f, err := os.Create(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if !IsCreatePaused(dir) {
		t.Fatal("expected paused when marker is present")
	}
}

func TestIsCreatePaused_AfterRemoval(t *testing.T) {
	dir := t.TempDir()
	markerPath := PausedMarkerPath(dir)
	f, err := os.Create(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if !IsCreatePaused(dir) {
		t.Fatal("expected paused with marker")
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if IsCreatePaused(dir) {
		t.Fatal("expected not paused after marker removal")
	}
}

func TestPausedMarkerPath(t *testing.T) {
	dir := "/tmp/runs"
	got := PausedMarkerPath(dir)
	want := "/tmp/runs/.paused"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
