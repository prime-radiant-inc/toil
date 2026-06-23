package definitions

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDecisionList_UnmarshalYAML_StringShorthand(t *testing.T) {
	input := `[pass, fail]`
	var dl DecisionList
	if err := yaml.Unmarshal([]byte(input), &dl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dl) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(dl))
	}
	if dl[0].ID != "pass" || dl[0].Description != "" {
		t.Errorf("decision 0: got %+v", dl[0])
	}
	if dl[1].ID != "fail" || dl[1].Description != "" {
		t.Errorf("decision 1: got %+v", dl[1])
	}
}

func TestDecisionList_UnmarshalYAML_ObjectForm(t *testing.T) {
	input := `
- id: pass
  description: All tests pass with zero failures
- id: fail
  description: One or more tests fail
`
	var dl DecisionList
	if err := yaml.Unmarshal([]byte(input), &dl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dl) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(dl))
	}
	if dl[0].ID != "pass" || dl[0].Description != "All tests pass with zero failures" {
		t.Errorf("decision 0: got %+v", dl[0])
	}
	if dl[1].ID != "fail" || dl[1].Description != "One or more tests fail" {
		t.Errorf("decision 1: got %+v", dl[1])
	}
}

func TestDecisionList_UnmarshalYAML_Mixed(t *testing.T) {
	input := `
- pass
- id: fail
  description: Something went wrong
`
	var dl DecisionList
	if err := yaml.Unmarshal([]byte(input), &dl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dl) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(dl))
	}
	if dl[0].ID != "pass" {
		t.Errorf("decision 0: got %+v", dl[0])
	}
	if dl[1].ID != "fail" || dl[1].Description != "Something went wrong" {
		t.Errorf("decision 1: got %+v", dl[1])
	}
}

func TestDecisionList_IDs(t *testing.T) {
	dl := DecisionList{
		{ID: "pass", Description: "everything works"},
		{ID: "fail"},
	}
	ids := dl.IDs()
	if len(ids) != 2 || ids[0] != "pass" || ids[1] != "fail" {
		t.Errorf("IDs: got %v", ids)
	}
}

func TestDecisionList_HasDescriptions(t *testing.T) {
	plain := DecisionList{{ID: "pass"}, {ID: "fail"}}
	if plain.HasDescriptions() {
		t.Error("plain list should not have descriptions")
	}

	described := DecisionList{{ID: "pass", Description: "it works"}, {ID: "fail"}}
	if !described.HasDescriptions() {
		t.Error("described list should have descriptions")
	}
}

func TestDecisionList_InNodeUnmarshal(t *testing.T) {
	input := `
id: verifier
kind: role
decisions:
  - id: pass
    description: All tests pass
  - id: fail
    description: Tests fail
`
	var node Node
	if err := yaml.Unmarshal([]byte(input), &node); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	if len(node.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(node.Decisions))
	}
	if node.Decisions[0].Description != "All tests pass" {
		t.Errorf("decision 0 description: got %q", node.Decisions[0].Description)
	}
}

func TestDecisionList_InNodeUnmarshal_StringShorthand(t *testing.T) {
	input := `
id: verifier
kind: role
decisions: [pass, fail]
`
	var node Node
	if err := yaml.Unmarshal([]byte(input), &node); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	if len(node.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(node.Decisions))
	}
	if node.Decisions[0].ID != "pass" {
		t.Errorf("decision 0: got %+v", node.Decisions[0])
	}
}
