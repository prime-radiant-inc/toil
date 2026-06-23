package runners

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSerfRunner_FlagsMatchHelp guards against silent drift between the
// flags toil passes to serf in definitions/runners/serf.yaml and the
// flag surface that serf --help advertises. When serf renames or
// removes a flag, this test fails loudly instead of letting toil keep
// invoking serf with an obsolete argument (which serf currently swallows
// by printing help and exiting cleanly).
//
// Skipped when serf isn't on PATH so hermetic environments don't fail —
// matches the convention in internal/metrics/parity_test.go for node(1).
func TestSerfRunner_FlagsMatchHelp(t *testing.T) {
	if _, err := exec.LookPath("serf"); err != nil {
		t.Skip("serf not on PATH; skipping flag contract check")
	}

	yamlPath, err := filepath.Abs("../../definitions/runners/serf.yaml")
	if err != nil {
		t.Fatalf("resolve serf.yaml path: %v", err)
	}

	flags, err := flagTokensFromRunnerYAML(yamlPath)
	if err != nil {
		t.Fatalf("parse %s: %v", yamlPath, err)
	}
	if len(flags) == 0 {
		t.Fatalf("no --flag tokens found in %s; test would be vacuous", yamlPath)
	}

	help, err := exec.Command("serf", "--help").CombinedOutput()
	if err != nil {
		// serf --help may legitimately exit non-zero on some versions;
		// only bail if we got no output to inspect.
		if len(help) == 0 {
			t.Fatalf("serf --help: %v (no output)", err)
		}
	}
	helpText := string(help)

	var missing []string
	for _, flag := range flags {
		if !strings.Contains(helpText, flag) {
			missing = append(missing, flag)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("flags in %s not found in `serf --help` output: %v\n"+
			"serf likely renamed or removed these. Update the YAML to match the current serf surface.\n"+
			"--- serf --help ---\n%s",
			filepath.Base(yamlPath), missing, helpText)
	}
}

// flagTokensFromRunnerYAML returns every distinct token in args: that
// starts with "--". Values, positional args, and ${VAR} expansions are
// ignored.
func flagTokensFromRunnerYAML(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc struct {
		Args []string `yaml:"args"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	for _, token := range doc.Args {
		if strings.HasPrefix(token, "--") {
			seen[token] = struct{}{}
		}
	}

	flags := make([]string, 0, len(seen))
	for flag := range seen {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return flags, nil
}
