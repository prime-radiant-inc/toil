package definitions

import (
	"path/filepath"
	"testing"
)

// TestRealWorkflowsValidateClean loads every checked-in workflow definition
// through the full load-time validation path (which includes
// ValidateExpressions). It guards against the node.X.<field> surface check —
// and any other load-time rule — producing a false positive on a real,
// in-use workflow. If this fails, either a new validation rule is too strict
// or a workflow has a latent bug; both warrant a look before shipping.
func TestRealWorkflowsValidateClean(t *testing.T) {
	matches, err := filepath.Glob("../../definitions/workflows/*.yaml")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no workflow definitions found — wrong working directory?")
	}
	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := LoadWorkflowFile(path); err != nil {
				t.Errorf("%s failed validation: %v", filepath.Base(path), err)
			}
		})
	}
}
