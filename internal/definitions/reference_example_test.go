package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferenceExampleParses extracts the main YAML workflow from the
// reference example documentation and verifies it parses and passes
// graph validation. This keeps the docs honest — if the code changes
// and the example becomes invalid, this test fails.
func TestReferenceExampleParses(t *testing.T) {
	docPath := filepath.Join("..", "..", "docs", "specs", "workflow-reference-example.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read reference example: %v", err)
	}

	yaml := extractFirstYAMLBlock(string(data))
	if yaml == "" {
		t.Fatal("no ```yaml block found in reference example")
	}

	// Write to temp file and load as workflow snapshot (skips env expansion)
	dir := t.TempDir()
	path := filepath.Join(dir, "reference_example.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	workflow, err := LoadWorkflowSnapshot(path)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot failed: %v", err)
	}

	if workflow.ID != "reference_example" {
		t.Fatalf("expected id 'reference_example', got %q", workflow.ID)
	}

	// Run graph validation — should produce no errors
	result := ValidateGraph(workflow)
	if result.HasErrors() {
		t.Fatalf("reference example has validation errors: %s", result.Error())
	}
}

// extractFirstYAMLBlock finds the first ```yaml ... ``` block in markdown.
func extractFirstYAMLBlock(content string) string {
	const startMarker = "```yaml"
	const endMarker = "```"

	startIdx := strings.Index(content, startMarker)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startMarker)
	// Skip to next newline
	nlIdx := strings.Index(content[startIdx:], "\n")
	if nlIdx == -1 {
		return ""
	}
	startIdx += nlIdx + 1

	rest := content[startIdx:]
	endIdx := strings.Index(rest, endMarker)
	if endIdx == -1 {
		return ""
	}

	return rest[:endIdx]
}
