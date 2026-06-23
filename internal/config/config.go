package config

import (
	"os"
	"path/filepath"
	"strconv"
)

const (
	RunsDirEnv        = "TOIL_RUNS_DIR"
	ToilRootEnv       = "TOIL_ROOT"
	DisableRestoreEnv = "TOIL_DISABLE_RESTORE"
)

func ToilRoot(root string) string {
	override := os.Getenv(ToilRootEnv)
	if override == "" {
		return root
	}
	if filepath.IsAbs(override) {
		return override
	}
	return filepath.Join(root, override)
}

func RunsDir(root string) string {
	override := os.Getenv(RunsDirEnv)
	if override != "" {
		if filepath.IsAbs(override) {
			return override
		}
		return filepath.Join(root, override)
	}
	// Default: XDG-style data directory, outside the repo tree.
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Fallback to repo-local if home dir unavailable.
			return filepath.Join(root, "runs")
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "toil", "runs")
}

func RestoreEnabled() bool {
	value := os.Getenv(DisableRestoreEnv)
	if value == "" {
		return true
	}
	disabled, err := strconv.ParseBool(value)
	if err != nil {
		return true
	}
	return !disabled
}

// PausedMarkerPath returns the path of the file whose presence signals
// that the daemon should reject new run creation.
func PausedMarkerPath(runsDir string) string {
	return filepath.Join(runsDir, ".paused")
}

// IsCreatePaused reports whether a .paused marker file exists in runsDir.
// The check is performed on every call — there is no caching — so
// removal of the marker takes effect immediately on the next createRun
// request.
func IsCreatePaused(runsDir string) bool {
	_, err := os.Stat(PausedMarkerPath(runsDir))
	return err == nil
}
