package definitions

import (
	"fmt"
	"regexp"
	"strings"
)

// forgottenExprPattern matches bare scalars that look like expression
// references (e.g. "input.task", "node.foo.message") but are missing the
// ${...} wrapper. Prose values with spaces or other non-identifier characters
// will not match.
var forgottenExprPattern = regexp.MustCompile(`^(input|workflow_input|node|env|run|tree)\.[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// ValidateExpressions walks all node Inputs, edge Passes, and emit
// Output blocks and applies the phase-aware namespace position rules.
// Returns the first error encountered (load fails fast).
//
// Rules enforced:
//   - ${input.X} is forbidden inside node Inputs and edge Passes block
//     values (the merged map doesn't exist until Phase 4; authors must
//     use ${workflow_input.X} to read run start inputs).
//   - Namespace prefix must be in KnownNamespaces.
//   - Bare scalars matching the expression-like pattern (e.g. "input.task")
//     are flagged as forgotten ${...} wraps.
//   - ${workflow_input.X!} requires X declared and NOT optional in input_schema.
//   - ${input.X!} in Phase 5 positions requires X on the destination node's
//     own inputs: block OR as a non-optional workflow input (edge passes don't
//     satisfy required-ness because retrigger bypasses edges).
func ValidateExpressions(w *Workflow) error {
	for _, n := range w.Nodes {
		node := n // capture for pointer-safety
		for key, raw := range n.Inputs {
			s, ok := raw.(string)
			if !ok {
				continue // literal scalar
			}
			label := fmt.Sprintf("node %q inputs.%s", n.ID, key)
			if err := validateBlockExpression(s, label, false, w, &node); err != nil {
				return err
			}
		}
		// Node prompt is a Phase 5 position: ${input.X} is allowed here.
		if n.Prompt != "" {
			label := fmt.Sprintf("node %q prompt", n.ID)
			if err := validateBlockExpression(n.Prompt, label, true, w, &node); err != nil {
				return err
			}
		}
		if n.Output != nil {
			// Emit output.message and output.data ARE Phase 5 positions,
			// so ${input.X} IS allowed here. Only check namespace
			// prefixes are known.
			label := fmt.Sprintf("node %q output.message", n.ID)
			if err := validateBlockExpression(n.Output.Message, label, true, w, &node); err != nil {
				return err
			}
			if err := validateDataExpressions(n.Output.Data, fmt.Sprintf("node %q output.data", n.ID), true, w, &node); err != nil {
				return err
			}
		}
	}
	for i, e := range w.Edges {
		for key, raw := range e.Passes {
			s, ok := raw.(string)
			if !ok {
				continue
			}
			label := fmt.Sprintf("edge %d (%s -> %s) passes.%s", i, e.From, e.To, key)
			// No owning node for edge passes — pass nil.
			if err := validateBlockExpression(s, label, false, w, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateBlockExpression checks one string value. allowInput indicates
// whether ${input.X} is permitted at this position (true for Phase 5
// positions: prompts, emit output). w and owningNode are used for
// required-reference satisfiability checks (Task 30b).
func validateBlockExpression(s, label string, allowInput bool, w *Workflow, owningNode *Node) error {
	// Task 30: detect bare scalars that look like forgotten ${...} expressions.
	trimmed := strings.TrimSpace(s)
	if !strings.Contains(trimmed, "${") && forgottenExprPattern.MatchString(trimmed) {
		return fmt.Errorf("%s: value %q looks like a forgotten expression — wrap in ${...} or quote a non-expression literal", label, trimmed)
	}

	refs := FindExpressionRefs(s)
	for _, ref := range refs {
		if !IsKnownNamespace(ref.Namespace) {
			return fmt.Errorf("%s: unknown namespace %q in %s (supported: %s)",
				label, ref.Namespace, ref.Raw, strings.Join(KnownNamespaces, ", "))
		}
		if ref.Namespace == nsInput && !allowInput {
			return fmt.Errorf("%s: ${input.X} is not allowed in inputs:/passes: blocks "+
				"(use ${workflow_input.X} to read run start inputs; ${input.X} only valid "+
				"in prompts and emit output:); found %s", label, ref.Raw)
		}

		// node.<id>.<field> must reference a field the resolver actually
		// serves. Catching unsupported fields here turns a runtime
		// "unknown node field" failure into a load-time error. The bare
		// node.<id> form (no field segment) resolves to the full output
		// map and needs no field check.
		if ref.Namespace == nsNode {
			segments := strings.Split(ref.Path, ".")
			if len(segments) >= 2 && segments[1] != "" && !IsSupportedNodeField(segments[1]) {
				return fmt.Errorf("%s: unsupported node field %q in %s (supported: %s)",
					label, segments[1], ref.Raw, strings.Join(SupportedNodeFields, ", "))
			}
		}

		// Task 30b: ${workflow_input.X!} satisfiability check.
		if ref.Namespace == nsWorkflowInput && ref.Required {
			key := ref.Path
			if dot := strings.Index(key, "."); dot >= 0 {
				key = key[:dot]
			}
			spec, hasSchema := w.InputSchema[key]
			if hasSchema && spec.Optional {
				return fmt.Errorf("%s: ${workflow_input.%s!} references workflow input declared optional", label, key)
			}
			if !hasSchema {
				if _, declared := w.Inputs[key]; !declared {
					return fmt.Errorf("%s: ${workflow_input.%s!} references undeclared workflow input", label, key)
				}
			}
		}

		// Task 30b / kata 5r6b: ${input.X!} in Phase 5 positions satisfiability check.
		// Edge passes don't satisfy required-ness because retrigger bypasses edges:
		// RetriggerNode marks the target as statusRetrying, and resumeReadyNodes
		// re-queues it with Passes=nil (no edge context). This applies equally to
		// gate:required approval nodes — they can be retriggered just like regular
		// nodes. Authors who need ${input.X!} in an approval gate prompt must declare
		// X on the node's own inputs: block OR as a non-optional workflow input.
		if ref.Namespace == nsInput && ref.Required && allowInput {
			key := ref.Path
			if dot := strings.Index(key, "."); dot >= 0 {
				key = key[:dot]
			}
			// Satisfied via node's own inputs: block?
			onOwnInputs := false
			if owningNode != nil {
				_, onOwnInputs = owningNode.Inputs[key]
			}
			if onOwnInputs {
				continue
			}
			// Satisfied via non-optional workflow input?
			if _, declared := w.Inputs[key]; declared {
				spec, hasSchema := w.InputSchema[key]
				if !hasSchema || !spec.Optional {
					continue
				}
			}
			return fmt.Errorf("%s: ${input.%s!} unsatisfiable on retrigger — declare on node's inputs: block or as a non-optional workflow input", label, key)
		}
	}
	return nil
}

func validateDataExpressions(data map[string]any, label string, allowInput bool, w *Workflow, owningNode *Node) error {
	for k, v := range data {
		nested := fmt.Sprintf("%s.%s", label, k)
		switch tv := v.(type) {
		case string:
			if err := validateBlockExpression(tv, nested, allowInput, w, owningNode); err != nil {
				return err
			}
		case map[string]any:
			if err := validateDataExpressions(tv, nested, allowInput, w, owningNode); err != nil {
				return err
			}
		}
	}
	return nil
}
