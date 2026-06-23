package dashboard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunNodeTemplate_RendersCompactSection(t *testing.T) {
	// Synthetic tree: a root run with one expanded sub-run child and one
	// compact sub-run child. Verify both branches render correctly.
	rootJSON := `{
		"run_id": "root",
		"workflow_id": "implement_spec",
		"compact": false,
		"summary": "ensure_repo",
		"children": [
			{
				"kind": "row",
				"run_id": "root",
				"node_id": "ensure_repo",
				"role": "ensure_repo",
				"attempt_ordinal": 1,
				"decision": "ready",
				"decision_family": "ok"
			},
			{
				"kind": "subrun",
				"run": {
					"run_id": "child",
					"workflow_id": "impl_task",
					"compact": true,
					"summary": "write_tests · write_code",
					"duration_ms": 12345,
					"decision": "tests_pass",
					"decision_family": "ok",
					"children": []
				}
			}
		]
	}`
	var rootMap map[string]any
	if err := json.Unmarshal([]byte(rootJSON), &rootMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "run_node", rootMap); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()

	// Root section renders WITHOUT the compact class.
	if !strings.Contains(html, `<section class="run-node"`) {
		t.Errorf("root not rendered as expanded run-node: %s", html)
	}
	if !strings.Contains(html, `data-run-id="root"`) {
		t.Errorf("missing root data-run-id")
	}

	// Child sub-run renders WITH the compact class.
	if !strings.Contains(html, `<section class="run-node compact"`) {
		t.Errorf("child not rendered as compact section: %s", html)
	}
	if !strings.Contains(html, `data-run-id="child"`) {
		t.Errorf("missing child data-run-id")
	}

	// The row child renders inside the root's children container.
	if !strings.Contains(html, `data-node-id="ensure_repo"`) {
		t.Errorf("missing ensure_repo row")
	}
	if !strings.Contains(html, `class="run-node-children"`) {
		t.Errorf("missing run-node-children wrapper")
	}
}

// TestRoleColor_InlineStyleNotSanitized guards against a regression where
// html/template's CSS sanitizer strips oklch(...) from inline style
// attributes (replacing the value with the literal "ZgotmplZ"). RoleColor
// returns template.CSS so the value is embedded verbatim.
func TestRoleColor_InlineStyleNotSanitized(t *testing.T) {
	rootJSON := `{
		"run_id": "root",
		"workflow_id": "implement_spec",
		"compact": false,
		"summary": "ensure_repo",
		"children": [
			{
				"kind": "row",
				"run_id": "root",
				"node_id": "ensure_repo",
				"role": "ensure_repo",
				"attempt_ordinal": 1,
				"decision": "ready",
				"decision_family": "ok"
			}
		]
	}`
	var rootMap map[string]any
	if err := json.Unmarshal([]byte(rootJSON), &rootMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "run_node", rootMap); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rendered := buf.String()

	if strings.Contains(rendered, "ZgotmplZ") {
		t.Fatalf("template stripped oklch() to ZgotmplZ:\n%s", rendered)
	}
	if !strings.Contains(rendered, "oklch(") {
		t.Fatalf("rendered output missing oklch():\n%s", rendered)
	}
}
