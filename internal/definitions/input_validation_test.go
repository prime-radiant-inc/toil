package definitions

import "testing"

func TestMissingRequiredInputs(t *testing.T) {
	workflow := &Workflow{
		Inputs: map[string]string{
			"project_dir": "string",
			"idea":        "string",
		},
		InputSchema: map[string]InputSpec{
			"idea": {Optional: true},
		},
	}
	missing := MissingRequiredInputs(workflow, map[string]any{
		"idea": "ship it",
	})
	if len(missing) != 1 || missing[0] != "project_dir" {
		t.Fatalf("expected project_dir missing, got %v", missing)
	}
}

func TestMissingRequiredInputs_OptionalListOmitted(t *testing.T) {
	workflow := &Workflow{
		Inputs: map[string]string{
			"spec":        "string",
			"stories":     "list",
			"project_dir": "string",
		},
		InputSchema: map[string]InputSpec{
			"stories": {Optional: true},
		},
	}
	// Omit stories entirely — should pass validation.
	missing := MissingRequiredInputs(workflow, map[string]any{
		"spec":        "Build a CLI app",
		"project_dir": "/tmp/project",
	})
	if len(missing) != 0 {
		t.Fatalf("expected no missing inputs when optional list omitted, got %v", missing)
	}
}

func TestMissingRequiredInputs_OptionalListNil(t *testing.T) {
	workflow := &Workflow{
		Inputs: map[string]string{
			"spec":        "string",
			"stories":     "list",
			"project_dir": "string",
		},
		InputSchema: map[string]InputSpec{
			"stories": {Optional: true},
		},
	}
	// Explicitly pass nil — should pass validation.
	missing := MissingRequiredInputs(workflow, map[string]any{
		"spec":        "Build a CLI app",
		"stories":     nil,
		"project_dir": "/tmp/project",
	})
	if len(missing) != 0 {
		t.Fatalf("expected no missing inputs when optional list is nil, got %v", missing)
	}
}

func TestMissingRequiredInputsEmptyString(t *testing.T) {
	workflow := &Workflow{
		Inputs: map[string]string{
			"project_dir": "string",
		},
	}
	missing := MissingRequiredInputs(workflow, map[string]any{
		"project_dir": "  ",
	})
	if len(missing) != 1 || missing[0] != "project_dir" {
		t.Fatalf("expected project_dir missing for empty string, got %v", missing)
	}
}
