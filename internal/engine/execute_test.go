package engine

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestShouldInjectPromptEcho_SkipsSelfEchoingRunners(t *testing.T) {
	engine := &Engine{Definitions: &definitions.Bundle{
		Runners: map[string]*definitions.Runner{
			"claude-runner": {ID: "claude-runner", Type: "claude"},
			"serf-runner":   {ID: "serf-runner", Type: "serf"},
			"codex-runner":  {ID: "codex-runner", Type: "codex"},
			"shell-runner":  {ID: "shell-runner", Type: "shell"},
			"human-runner":  {ID: "human-runner", Type: "human"},
		},
	}}

	cases := []struct {
		runnerID string
		want     bool
	}{
		{"claude-runner", false},
		{"serf-runner", false},
		{"codex-runner", true},
		{"shell-runner", true},
		{"human-runner", true},
		{"unknown", true},
		{"", true},
	}
	for _, tc := range cases {
		if got := engine.shouldInjectPromptEcho(tc.runnerID); got != tc.want {
			t.Errorf("shouldInjectPromptEcho(%q) = %v, want %v", tc.runnerID, got, tc.want)
		}
	}
}
