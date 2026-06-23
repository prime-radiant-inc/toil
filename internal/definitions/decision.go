package definitions

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Decision represents a single allowed decision for a node.
//
// Tags attach cross-cutting semantic labels to a decision, read by
// downstream infrastructure:
//
//   - The engine materializes them onto NodeState.Tags at emit time
//     and includes them in node_completed event data.
//   - `tree.tagged.<name>` expressions query matching nodes across
//     a run tree.
//   - The dashboard and topology renderers use tags to surface
//     decisions that carry special meaning (e.g. "override" for
//     review-escalation waivers).
//
// Tags are workflow-authored conventions — the engine treats any
// string as a valid tag. Document tag conventions in workflow
// comments or a project-wide reference so different workflows stay
// consistent with shared vocabulary.
type Decision struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
}

// DecisionList is a list of decisions that can be unmarshaled from either
// a list of strings (["pass", "fail"]) or a list of objects
// ([{id: "pass", description: "..."}]), or a mix of both.
type DecisionList []Decision

func (dl *DecisionList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("decisions must be a sequence, got %v", value.Kind)
	}
	result := make(DecisionList, 0, len(value.Content))
	for _, item := range value.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			result = append(result, Decision{ID: item.Value})
		case yaml.MappingNode:
			var d Decision
			if err := item.Decode(&d); err != nil {
				return fmt.Errorf("decode decision: %w", err)
			}
			result = append(result, d)
		default:
			return fmt.Errorf("decision item must be a string or object, got %v", item.Kind)
		}
	}
	*dl = result
	return nil
}

// IDs returns just the decision ID strings.
func (dl DecisionList) IDs() []string {
	ids := make([]string, len(dl))
	for i, d := range dl {
		ids[i] = d.ID
	}
	return ids
}

// StringDecisions creates a DecisionList from plain string IDs.
func StringDecisions(ids ...string) DecisionList {
	dl := make(DecisionList, len(ids))
	for i, id := range ids {
		dl[i] = Decision{ID: id}
	}
	return dl
}

// HasDescriptions returns true if any decision has a description.
func (dl DecisionList) HasDescriptions() bool {
	for _, d := range dl {
		if d.Description != "" {
			return true
		}
	}
	return false
}

// Find returns the decision matching id. Returns (Decision{}, false)
// if no decision matches — callers should treat this as "the runner
// returned an unknown decision," which is normally caught earlier by
// node-output validation. This lookup exists so the engine can
// resolve a matched decision back to its full definition
// (specifically its Tags) at completion time.
func (dl DecisionList) Find(id string) (Decision, bool) {
	for _, d := range dl {
		if d.ID == id {
			return d, true
		}
	}
	return Decision{}, false
}
