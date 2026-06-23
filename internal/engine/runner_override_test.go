package engine

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestResolveRunnerID_NoRunnerConfigured_ReturnsError(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role"}
	workflow := &definitions.Workflow{}

	_, err := ResolveRunnerID(node, workflow)
	if err == nil {
		t.Fatal("expected error when no runner configured, got nil")
	}
}

func TestResolveRunnerID_MatchingTagOverride(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Tags: []string{"gpu"}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
	}

	got, err := ResolveRunnerID(node, workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "gpu-runner" {
		t.Fatalf("expected gpu-runner, got %q", got)
	}
}

func TestResolveRunnerID_NodeRunnerTakesPrecedence(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Runner: "node-runner", Tags: []string{"gpu"}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
	}

	got, err := ResolveRunnerID(node, workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "node-runner" {
		t.Fatalf("expected node-runner, got %q", got)
	}
}

func TestResolveRunnerID_MultipleTagsFirstMatchWins(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Tags: []string{"fast", "gpu", "large"}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{
			"gpu":   "gpu-runner",
			"large": "large-runner",
			"fast":  "fast-runner",
		},
	}

	got, err := ResolveRunnerID(node, workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fast-runner" {
		t.Fatalf("expected fast-runner (first tag match), got %q", got)
	}
}

func TestResolveRunnerID_TagOverrideBeatExplicitRunner(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Runner: "explicit", Tags: []string{"gpu"}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
	}

	got, err := ResolveRunnerID(node, workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicit runner takes precedence over tag override
	if got != "explicit" {
		t.Fatalf("expected explicit runner to win, got %q", got)
	}
}

func TestResolveRunnerID_NoMatchingTag_ReturnsError(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Tags: []string{"cpu"}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
	}

	_, err := ResolveRunnerID(node, workflow)
	if err == nil {
		t.Fatal("expected error when no tag matches and no runner set, got nil")
	}
}

func TestResolveRunnerID_TagsButNoOverridesMap_ReturnsError(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Tags: []string{"gpu"}}
	workflow := &definitions.Workflow{} // no RunnerOverrides

	_, err := ResolveRunnerID(node, workflow)
	if err == nil {
		t.Fatal("expected error when no overrides map and no runner set, got nil")
	}
}

func TestResolveRunnerID_EmptyTagsList_ReturnsError(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Tags: []string{}}
	workflow := &definitions.Workflow{
		RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
	}

	_, err := ResolveRunnerID(node, workflow)
	if err == nil {
		t.Fatal("expected error when tags list is empty and no runner set, got nil")
	}
}

func TestResolveRunnerID_ExplicitRunner_Succeeds(t *testing.T) {
	node := &definitions.Node{ID: "n1", Kind: "role", Runner: "my-runner"}
	workflow := &definitions.Workflow{}

	got, err := ResolveRunnerID(node, workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-runner" {
		t.Fatalf("expected my-runner, got %q", got)
	}
}

func TestLastStderrLine(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{"single line", "error: something broke", "error: something broke"},
		{"multiple lines", "info: starting\nerror: no API key\n", "error: no API key"},
		{"trailing newlines", "fatal error\n\n\n", "fatal error"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastStderrLine(tt.stderr)
			if got != tt.want {
				t.Fatalf("lastStderrLine(%q) = %q, want %q", tt.stderr, got, tt.want)
			}
		})
	}
}
