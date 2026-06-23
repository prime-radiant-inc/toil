package document

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestWorkflowRegistry_FindDecision(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"review": {
				ID: "review",
				Nodes: []definitions.Node{
					{
						ID: "reviewer",
						Decisions: definitions.DecisionList{
							{ID: "pass", Description: "Meets quality bar."},
							{ID: "fail", Description: "Needs rework."},
						},
					},
				},
			},
		},
	}
	reg := NewWorkflowRegistry(bundle, nil)

	t.Run("found with description", func(t *testing.T) {
		d := reg.FindDecision("review", "pass")
		if d == nil {
			t.Fatal("expected decision, got nil")
		}
		if d.ID != "pass" {
			t.Errorf("id: got %q, want %q", d.ID, "pass")
		}
		if d.Description != "Meets quality bar." {
			t.Errorf("description: got %q, want %q", d.Description, "Meets quality bar.")
		}
	})

	t.Run("found second decision", func(t *testing.T) {
		d := reg.FindDecision("review", "fail")
		if d == nil {
			t.Fatal("expected decision, got nil")
		}
		if d.ID != "fail" {
			t.Errorf("id: got %q, want %q", d.ID, "fail")
		}
	})

	t.Run("unknown decision returns nil", func(t *testing.T) {
		d := reg.FindDecision("review", "unknown")
		if d != nil {
			t.Errorf("expected nil, got %+v", d)
		}
	})

	t.Run("unknown workflow returns nil", func(t *testing.T) {
		d := reg.FindDecision("no_such_workflow", "pass")
		if d != nil {
			t.Errorf("expected nil, got %+v", d)
		}
	})

	t.Run("nil bundle returns nil", func(t *testing.T) {
		nilReg := NewWorkflowRegistry(nil, nil)
		d := nilReg.FindDecision("review", "pass")
		if d != nil {
			t.Errorf("expected nil for nil bundle, got %+v", d)
		}
	})
}
