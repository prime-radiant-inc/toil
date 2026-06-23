package tgwm

import (
	"testing"
)

func TestToilRoot(t *testing.T) {
	t.Run("returns error when TOIL_ROOT is not set", func(t *testing.T) {
		t.Setenv("TOIL_ROOT", "")
		_, err := ToilRoot()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("returns value when TOIL_ROOT is set", func(t *testing.T) {
		t.Setenv("TOIL_ROOT", "/some/path")
		got, err := ToilRoot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/some/path" {
			t.Errorf("got %q, want %q", got, "/some/path")
		}
	})
}

func TestWorkflowDir(t *testing.T) {
	t.Run("returns error when TOIL_CURRENT_WORKFLOW_DIR is not set", func(t *testing.T) {
		t.Setenv("TOIL_CURRENT_WORKFLOW_DIR", "")
		_, err := WorkflowDir()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("returns value when TOIL_CURRENT_WORKFLOW_DIR is set", func(t *testing.T) {
		t.Setenv("TOIL_CURRENT_WORKFLOW_DIR", "/workflow/dir")
		got, err := WorkflowDir()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/workflow/dir" {
			t.Errorf("got %q, want %q", got, "/workflow/dir")
		}
	})
}
