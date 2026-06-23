package engine

import (
	"reflect"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestResolvePromptInputsMode(t *testing.T) {
	tests := []struct {
		name     string
		workflow *definitions.Workflow
		node     *definitions.Node
		want     string
	}{
		{
			name: "node override wins",
			workflow: &definitions.Workflow{
				PromptInputsMode: "all",
			},
			node: &definitions.Node{
				PromptInputsMode: "none",
			},
			want: "none",
		},
		{
			name: "workflow default used when node unset",
			workflow: &definitions.Workflow{
				PromptInputsMode: "none",
			},
			node: &definitions.Node{},
			want: "none",
		},
		{
			name:     "global default is declared",
			workflow: &definitions.Workflow{},
			node:     &definitions.Node{},
			want:     "declared",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolvePromptInputsMode(tc.workflow, tc.node)
			if got != tc.want {
				t.Fatalf("resolvePromptInputsMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildPromptDisplayInputs(t *testing.T) {
	runInputs := map[string]any{
		"spec":    "full spec",
		"stories": []any{"story-a", "story-b"},
		"global":  "run-level",
	}
	nodeInputs := map[string]any{
		"spec":      "node narrowed spec",
		"component": "api",
	}

	t.Run("all mode includes merged run and node inputs", func(t *testing.T) {
		got := buildPromptDisplayInputs("all", runInputs, nodeInputs)
		want := map[string]any{
			"spec":      "node narrowed spec",
			"stories":   []any{"story-a", "story-b"},
			"global":    "run-level",
			"component": "api",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("all mode mismatch\ngot:  %#v\nwant: %#v", got, want)
		}
	})

	t.Run("declared mode includes only node inputs", func(t *testing.T) {
		got := buildPromptDisplayInputs("declared", runInputs, nodeInputs)
		want := map[string]any{
			"spec":      "node narrowed spec",
			"component": "api",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("declared mode mismatch\ngot:  %#v\nwant: %#v", got, want)
		}
	})

	t.Run("none mode omits inputs block", func(t *testing.T) {
		got := buildPromptDisplayInputs("none", runInputs, nodeInputs)
		if len(got) != 0 {
			t.Fatalf("none mode should return empty input map, got: %#v", got)
		}
	})
}
