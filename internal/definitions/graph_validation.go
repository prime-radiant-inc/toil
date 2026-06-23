package definitions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// looksLikeExpression returns true if the when value contains expression operators.
// This is used to skip expression edges during static validation. Mirrors the
// engine's IsExpression in `internal/engine/expr.go` — both must agree on what
// counts as an expression, otherwise the validator and runtime drift.
func looksLikeExpression(s string) bool {
	return strings.Contains(s, "==") ||
		strings.Contains(s, "!=") ||
		strings.Contains(s, ">=") ||
		strings.Contains(s, "<=") ||
		strings.Contains(s, "&&") ||
		strings.Contains(s, "||") ||
		containsSingleAngleBracket(s)
}

// containsSingleAngleBracket reports whether s has a `<` or `>` that is NOT
// part of `<=` or `>=`. Mirrors the same helper in
// `internal/engine/expr.go` — kept duplicated here to avoid a circular
// import between definitions and engine.
func containsSingleAngleBracket(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '>' && (i == 0 || s[i-1] != '=') && (i+1 >= len(s) || s[i+1] != '=') {
			return true
		}
		if ch == '<' && (i+1 >= len(s) || s[i+1] != '=') {
			return true
		}
	}
	return false
}

// edgeMatchesFailedStatus reports whether an edge's `when:` expression
// would match a failed-status node. Used by validators that need to know
// whether an edge is a "failure edge" without importing the engine's full
// expression evaluator.
//
// Recognized forms (whitespace-insensitive, both quote styles):
//   - `status == 'failed'` / `status == "failed"`
//   - any expression containing the above as a subterm
//
// Explicitly rejected:
//   - `status != 'failed'` (inverse filter)
//   - `node.X.status == 'failed'` (property of a different node, not the
//     current edge's source — this is a different variable even though the
//     string "status == 'failed'" appears)
//
// Note: this is a static approximation of the engine's EvalEdgeExpr. For
// edges with compound conditions whose failure-matching depends on
// runtime data, the validator may have false positives. Extracting
// expression evaluation into a shared package would make this exact.
func edgeMatchesFailedStatus(when string) bool {
	if when == "" {
		return false
	}
	if !looksLikeExpression(when) {
		return false
	}
	compact := strings.Join(strings.Fields(when), "")
	// Check positive match first — if any unqualified `status == 'failed'`
	// subterm appears, this expression COULD route a failed item.
	// Compound expressions like `status == 'failed' && status != 'failed'`
	// (pathological tautology) and `status != 'failed' || status == 'failed'`
	// (tautology that always matches) both contain a positive subterm and
	// are conservatively classified as failure edges.
	for _, needle := range []string{"status=='failed'", `status=="failed"`} {
		if hasUnqualifiedToken(compact, needle) {
			return true
		}
	}
	// Otherwise, an unqualified inverse `status != 'failed'` (the
	// success-only filter pattern) is explicitly NOT a failure edge.
	for _, needle := range []string{"status!='failed'", `status!="failed"`} {
		if hasUnqualifiedToken(compact, needle) {
			return false
		}
	}
	return false
}

// hasUnqualifiedToken reports whether needle occurs in compact at a position
// where it stands alone as a token — not immediately preceded by '.', '_',
// or any alphanumeric (which would mean it's the suffix of a longer
// identifier like `mystatus`, `complete_status`, or `node.X.status`).
func hasUnqualifiedToken(compact, needle string) bool {
	idx := 0
	for idx < len(compact) {
		hit := strings.Index(compact[idx:], needle)
		if hit < 0 {
			return false
		}
		pos := idx + hit
		if pos == 0 || !isIdentPrefix(compact[pos-1]) {
			return true
		}
		idx = pos + len(needle)
	}
	return false
}

// isIdentPrefix reports whether a byte preceding a token would make that
// token part of a larger qualified expression. Recognized:
//   - '.', '_', letters, digits  → continuation of a longer identifier
//     or a dotted member access (`node.X.status`, `mystatus`,
//     `complete_status`)
//   - '!' → unary negation prefix (`!status`); the engine's tokenizer
//     doesn't accept this form, but classifying it as part of a larger
//     expression keeps the validator from spuriously requiring failure
//     decisions for what's a runtime parse error.
func isIdentPrefix(b byte) bool {
	if b == '.' || b == '_' || b == '!' {
		return true
	}
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	return false
}

const (
	whenDefault     = "default"
	kindSubworkflow = "subworkflow"
	kindHuman       = "human"
)

// Meta-decision sentinel names — engine-synthesized edge `when:` values.
const (
	metaLoopExhausted  = "_loop_exhausted"
	metaTimeout        = "_timeout"
	metaRetryExhausted = "_retry_exhausted"
)

// MetaDecisions is the closed set of engine-synthesized meta-decision
// names that workflows may use as edge `when:` values. Unknown
// underscore-prefixed values are load-time errors.
var MetaDecisions = map[string]bool{
	metaLoopExhausted:  true,
	metaTimeout:        true,
	metaRetryExhausted: true,
}

type Severity int

const (
	SeverityInfo    Severity = iota
	SeverityWarning          // iota 1
	SeverityError            // iota 2
)

type Diagnostic struct {
	Severity Severity
	Message  string
	NodeID   string
	EdgeIdx  int
}

type ValidationResult struct {
	Diagnostics []Diagnostic
}

func (r *ValidationResult) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r *ValidationResult) HasWarnings() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

func (r *ValidationResult) HasInfo() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityInfo {
			return true
		}
	}
	return false
}

func (r *ValidationResult) Error() string {
	var msgs []string
	for _, d := range r.Diagnostics {
		prefix := "info"
		switch d.Severity {
		case SeverityWarning:
			prefix = "warning"
		case SeverityError:
			prefix = "error"
		}
		if d.NodeID != "" {
			msgs = append(msgs, fmt.Sprintf("%s: node %q: %s", prefix, d.NodeID, d.Message))
		} else {
			msgs = append(msgs, fmt.Sprintf("%s: %s", prefix, d.Message))
		}
	}
	return strings.Join(msgs, "; ")
}

func (r *ValidationResult) add(severity Severity, nodeID string, edgeIdx int, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: severity,
		Message:  msg,
		NodeID:   nodeID,
		EdgeIdx:  edgeIdx,
	})
}

// ValidateGraph checks a single workflow's graph structure.
func ValidateGraph(w *Workflow) *ValidationResult {
	r := &ValidationResult{}
	nodeIDs := checkDuplicateNodes(r, w)
	checkEdgeTargets(r, w, nodeIDs)
	checkReachability(r, w, nodeIDs)
	checkEdgeWhenValues(r, w, nodeIDs)
	checkNodeFields(r, w)
	checkOutputsSchemas(r, w)
	checkRetryTargets(r, w, nodeIDs)
	checkTimeoutFields(r, w)
	checkTimeoutInverseRule(r, w)
	validateJoinNodes(r, w)
	validateForEachBodies(r, w, nodeIDs)
	validateTemplateEdgeTargets(r, w)
	validateTemplateIncomingEdges(r, w)
	validateForEachOrchestratorDecisions(r, w)
	checkConvergentEdgePrompts(r, w)
	checkConvergentEdgePasses(r, w)
	checkLoopExhaustionPolicyValid(r, w)
	checkLoopExhaustionCoverage(r, w)
	return r
}

var (
	validContextValues    = map[string]bool{"": true, "full": true, "fresh": true, "compact": true, "summary": true}
	validPromptInputsMode = map[string]bool{"": true, "all": true, "declared": true, "none": true}
)

func checkNodeFields(r *ValidationResult, w *Workflow) {
	if w.ContextDefault != "" && !validContextValues[w.ContextDefault] {
		r.add(SeverityError, "", -1, fmt.Sprintf("invalid context_default value %q (must be \"full\", \"fresh\", \"compact\", or \"summary\")", w.ContextDefault))
	}
	if !validPromptInputsMode[w.PromptInputsMode] {
		r.add(SeverityError, "", -1, fmt.Sprintf("invalid prompt_inputs_mode value %q (must be \"all\", \"declared\", or \"none\")", w.PromptInputsMode))
	}
	for _, n := range w.Nodes {
		if !validContextValues[n.Context] {
			r.add(SeverityError, n.ID, -1, fmt.Sprintf("invalid context value %q (must be \"full\", \"fresh\", \"compact\", or \"summary\")", n.Context))
		}
		if !validPromptInputsMode[n.PromptInputsMode] {
			r.add(SeverityError, n.ID, -1, fmt.Sprintf("invalid prompt_inputs_mode value %q (must be \"all\", \"declared\", or \"none\")", n.PromptInputsMode))
		}
	}
}

// checkOutputsSchemas validates that outputs_schema is a valid JSON Schema
// on any node that declares it. Also rejects outputs_schema on node kinds/runners
// that can't enforce it: shell (no native schema enforcement), kind: subworkflow
// (the parent node dispatches to a child run — schemas must be declared on the
// child's leaf nodes), and kind: human (the human runner doesn't consume
// OutputSchemaJSON).
func checkOutputsSchemas(r *ValidationResult, w *Workflow) {
	for _, n := range w.Nodes {
		if n.OutputsSchema == nil {
			continue
		}
		if strings.EqualFold(n.Runner, "shell") {
			r.add(SeverityError, n.ID, -1,
				"outputs_schema is not supported on shell nodes (no native schema enforcement available)")
			continue
		}
		if n.Kind == kindSubworkflow {
			r.add(SeverityError, n.ID, -1,
				"outputs_schema is not supported on subworkflow nodes (the parent node dispatches to a child run; declare outputs_schema on the child workflow's leaf nodes instead)")
			continue
		}
		if n.Kind == kindHuman {
			r.add(SeverityError, n.ID, -1,
				"outputs_schema is not supported on human nodes (the human runner does not enforce schemas)")
			continue
		}
		raw, err := json.Marshal(n.OutputsSchema)
		if err != nil {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("outputs_schema failed to marshal: %v", err))
			continue
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("outputs_schema.json", bytes.NewReader(raw)); err != nil {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("outputs_schema is not valid JSON Schema: %v", err))
			continue
		}
		if _, err := compiler.Compile("outputs_schema.json"); err != nil {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("outputs_schema failed to compile: %v", err))
		}
	}
}

// checkRetryTargets validates retry_target references on nodes and the workflow,
// and warns when goal_gate nodes have no retry target.
func checkRetryTargets(r *ValidationResult, w *Workflow, nodeIDs map[string]bool) {
	if w.RetryTarget != "" && !nodeIDs[w.RetryTarget] {
		r.add(SeverityError, "", -1, fmt.Sprintf("workflow retry_target references nonexistent node %q", w.RetryTarget))
	}
	for _, n := range w.Nodes {
		if n.RetryTarget != "" && !nodeIDs[n.RetryTarget] {
			r.add(SeverityError, n.ID, -1, fmt.Sprintf("retry_target references nonexistent node %q", n.RetryTarget))
		}
		if n.GoalGate && n.RetryTarget == "" && w.RetryTarget == "" {
			r.add(SeverityWarning, n.ID, -1, "goal_gate node has no retry_target (node or workflow level)")
		}
	}
}

// checkTimeoutFields validates timeout_sec and timeout_default on nodes.
func checkTimeoutFields(r *ValidationResult, w *Workflow) {
	for _, n := range w.Nodes {
		if n.TimeoutSec < 0 {
			r.add(SeverityError, n.ID, -1, fmt.Sprintf("timeout_sec must be non-negative, got %d", n.TimeoutSec))
		}
		// Note: timeout_default is deprecated; Task 32 will remove the field.
		// The warning for invalid timeout_default values is intentionally removed
		// here since timeout_sec now fires the _timeout meta-decision regardless
		// of timeout_default.
	}
}

// checkTimeoutInverseRule enforces that every node with timeout_sec > 0 AND
// gate: required has an outgoing _timeout edge. Without one, a timed-out
// approval silently stays pending forever.
func checkTimeoutInverseRule(r *ValidationResult, w *Workflow) {
	for _, n := range w.Nodes {
		if n.TimeoutSec <= 0 || n.Gate != "required" {
			continue
		}
		hasTimeoutEdge := false
		for _, e := range w.Edges {
			if e.From == n.ID && e.When == metaTimeout {
				hasTimeoutEdge = true
				break
			}
		}
		if !hasTimeoutEdge {
			r.add(SeverityError, n.ID, -1, fmt.Sprintf("node %s has timeout_sec > 0 and gate: required but no outgoing _timeout edge (silent forever-pending approval forbidden)", n.ID))
		}
	}
}

// validateJoinNodes checks all 9 validation rules for join: all nodes.
func validateJoinNodes(r *ValidationResult, w *Workflow) {
	// Build lookup structures
	nodeByID := map[string]*Node{}
	for i := range w.Nodes {
		nodeByID[w.Nodes[i].ID] = &w.Nodes[i]
	}

	// Build outgoing edges per node (for Rule 1 caveat check)
	outgoingEdges := map[string][]Edge{}
	for _, e := range w.Edges {
		outgoingEdges[e.From] = append(outgoingEdges[e.From], e)
	}

	for _, node := range w.Nodes {
		if node.Join == "" {
			continue
		}

		// Validate join value
		if node.Join != "all" {
			r.add(SeverityError, node.ID, -1, fmt.Sprintf("invalid join value %q (only \"all\" is supported)", node.Join))
			continue
		}

		// Collect incoming edges to this join node
		var incomingEdges []Edge
		predecessorCount := map[string]int{} // predecessor ID -> count of edges to this join
		for _, e := range w.Edges {
			if e.To == node.ID {
				incomingEdges = append(incomingEdges, e)
				predecessorCount[e.From]++
			}
		}

		// Rule 7: Zero incoming edges
		if len(incomingEdges) == 0 {
			r.add(SeverityError, node.ID, -1, "join node has zero incoming edges")
			continue
		}

		// Rule 8: One incoming edge (warning)
		if len(incomingEdges) == 1 {
			r.add(SeverityWarning, node.ID, -1, "join node has one incoming edge — likely an authoring mistake")
		}

		// Rule 5: Self-loop
		if predecessorCount[node.ID] > 0 {
			r.add(SeverityError, node.ID, -1, "self-loop on join node")
		}

		// Rule 6: Same predecessor has multiple edges to join
		for predID, count := range predecessorCount {
			if count > 1 {
				r.add(SeverityError, node.ID, -1, fmt.Sprintf("predecessor %q has multiple edges to join node", predID))
			}
		}

		// Rule 1: Check each incoming edge for conditional/expression when clauses
		for _, e := range incomingEdges {
			if e.From == node.ID {
				continue // self-loop already caught above
			}
			when := e.When
			if when != "" && when != whenDefault {
				// Non-empty, non-default when clause (includes expressions)
				r.add(SeverityError, node.ID, -1, fmt.Sprintf("conditional edge from %q to join node (when: %q)", e.From, when))
				continue
			}
			// Rule 1 caveat: default/empty edge, but predecessor has competing specific edges
			// that target OTHER nodes (not this join). If so, the edge to the join may not fire.
			if predecessorHasCompetingEdges(outgoingEdges[e.From], node.ID) {
				r.add(SeverityError, node.ID, -1, fmt.Sprintf("conditional edge from %q to join node: predecessor has competing outgoing edges that may prevent this edge from firing", e.From))
			}
		}

		// Rule 4: ForEach has direct edge to join
		for _, e := range incomingEdges {
			pred := nodeByID[e.From]
			if pred != nil && pred.ForEach != nil {
				r.add(SeverityError, node.ID, -1, fmt.Sprintf("foreach node %q has direct edge to join node", e.From))
			}
		}

		// Rule 9: goal_gate on join node (warning)
		if node.GoalGate {
			r.add(SeverityWarning, node.ID, -1, "goal_gate on join node — retry could cause re-trigger")
		}
	}
}

// predecessorHasCompetingEdges returns true if the predecessor has outgoing edges
// with specific when values that target nodes other than the join node. This means
// a default/empty edge to the join might not fire.
func predecessorHasCompetingEdges(edges []Edge, joinNodeID string) bool {
	for _, e := range edges {
		if e.To == joinNodeID {
			continue
		}
		// If there's an edge to a different target with a specific when value,
		// the default edge to the join might not fire
		if e.When != "" && e.When != whenDefault {
			return true
		}
	}
	return false
}

// validateForEachBodies enforces that every for_each declares body and that
// body references an existing node. Also rejects the old inline form where
// a node carries both for_each and kind/runner/workflow.
func validateForEachBodies(r *ValidationResult, w *Workflow, nodeIDs map[string]bool) {
	for _, n := range w.Nodes {
		if n.ForEach == nil {
			continue
		}
		if n.ForEach.Body == "" {
			r.add(SeverityError, n.ID, -1,
				"for_each requires 'body: <template-node-id>' — see docs/superpowers/specs/2026-04-20-foreach-body-design.md")
			continue
		}
		if !nodeIDs[n.ForEach.Body] {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("for_each.body references nonexistent node %q", n.ForEach.Body))
		}
		// Reject inline form: orchestrator must not also carry kind/runner/workflow.
		if n.Kind != "" || n.Runner != "" || n.Workflow != "" {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("node %q has for_each and also %s — the orchestrator must not declare kind/runner/workflow; move those fields to the template node", n.ID, inlineFieldsList(n)))
		}
		// Reject self-reference: body pointing at its own orchestrator.
		if n.ForEach.Body == n.ID {
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("for_each.body %q points at its own orchestrator (self-reference)", n.ForEach.Body))
		}
		// Reject nested ForEach: a template that is itself a ForEach orchestrator
		// would have unclear expanded-ID semantics. Not supported.
		if nodeIDs[n.ForEach.Body] {
			template := FindNode(w, n.ForEach.Body)
			if template != nil && template.ForEach != nil {
				r.add(SeverityError, n.ID, -1,
					fmt.Sprintf("for_each.body %q is itself a ForEach orchestrator — nested ForEach is not supported", n.ForEach.Body))
			}
		}
	}
}

func inlineFieldsList(n Node) string {
	var fields []string
	if n.Kind != "" {
		fields = append(fields, "kind")
	}
	if n.Runner != "" {
		fields = append(fields, "runner")
	}
	if n.Workflow != "" {
		fields = append(fields, "workflow")
	}
	return strings.Join(fields, "/")
}

// validateTemplateEdgeTargets enforces that every edge FROM a template
// (node referenced by some for_each.body) must route TO the orchestrator
// that references it. Also rejects shared templates (two orchestrators
// referencing the same template body would collide on expanded IDs).
func validateTemplateEdgeTargets(r *ValidationResult, w *Workflow) {
	templateToOrchestrator := map[string]string{}
	sharingOrchestrators := map[string][]string{}
	for _, n := range w.Nodes {
		if n.ForEach == nil || n.ForEach.Body == "" {
			continue
		}
		if prior, exists := templateToOrchestrator[n.ForEach.Body]; exists {
			sharingOrchestrators[n.ForEach.Body] = append(
				sharingOrchestrators[n.ForEach.Body], prior, n.ID,
			)
			continue
		}
		templateToOrchestrator[n.ForEach.Body] = n.ID
	}

	// Report shared templates (use first orchestrator as the diagnostic node)
	for body, orchs := range sharingOrchestrators {
		// Dedupe
		seen := map[string]bool{}
		var unique []string
		for _, o := range orchs {
			if !seen[o] {
				seen[o] = true
				unique = append(unique, o)
			}
		}
		r.add(SeverityError, unique[0], -1,
			fmt.Sprintf("template %q is referenced by multiple orchestrators (%s); each orchestrator needs its own template", body, strings.Join(unique, ", ")))
	}

	// Existing edge-target validation
	for _, e := range w.Edges {
		orch, isTemplate := templateToOrchestrator[e.From]
		if !isTemplate {
			continue
		}
		// Skip edges from shared templates — the shared-template error
		// above already signals the misconfiguration; checking edge
		// targets against a single (arbitrary) orchestrator would be
		// misleading.
		if _, shared := sharingOrchestrators[e.From]; shared {
			continue
		}
		if e.To != orch {
			r.add(SeverityError, e.From, -1,
				fmt.Sprintf("edge from template %q must route to its orchestrator %q, not %q",
					e.From, orch, e.To))
		}
	}
}

// validateTemplateIncomingEdges rejects edges targeting a template node.
// Templates must be reached only via for_each.body.
func validateTemplateIncomingEdges(r *ValidationResult, w *Workflow) {
	templates := map[string]string{} // template id -> orchestrator id
	for _, n := range w.Nodes {
		if n.ForEach != nil && n.ForEach.Body != "" {
			templates[n.ForEach.Body] = n.ID
		}
	}
	for _, e := range w.Edges {
		if orch, ok := templates[e.To]; ok {
			r.add(SeverityError, e.To, -1,
				fmt.Sprintf("template %q has an incoming edge from %q; templates are reached only via for_each.body on orchestrator %q",
					e.To, e.From, orch))
		}
	}
}

// validateForEachOrchestratorDecisions enforces that the orchestrator's
// declared decisions list is consistent with whether the template has a
// failure edge. Without a failure edge, failures are fatal — some_failed /
// all_failed are unreachable. With a failure edge, those decisions are
// reachable and must be declared so edges referencing them are reachable.
func validateForEachOrchestratorDecisions(r *ValidationResult, w *Workflow) {
	templatesWithFailureEdge := map[string]bool{}
	for _, e := range w.Edges {
		if edgeMatchesFailedStatus(e.When) {
			templatesWithFailureEdge[e.From] = true
		}
	}
	for _, n := range w.Nodes {
		if n.ForEach == nil || n.ForEach.Body == "" {
			continue
		}
		has := templatesWithFailureEdge[n.ForEach.Body]
		declared := map[string]bool{}
		for _, d := range n.Decisions {
			declared[d.ID] = true
		}
		if has {
			if !declared["some_failed"] {
				r.add(SeverityError, n.ID, -1,
					"template has a failure edge so the orchestrator can emit 'some_failed' — add it to the decisions list")
			}
			if !declared["all_failed"] {
				r.add(SeverityError, n.ID, -1,
					"template has a failure edge so the orchestrator can emit 'all_failed' — add it to the decisions list")
			}
		} else {
			if declared["some_failed"] {
				r.add(SeverityError, n.ID, -1,
					"template has no failure edge so 'some_failed' is unreachable — remove it from the decisions list (or add a failure edge to the template)")
			}
			if declared["all_failed"] {
				r.add(SeverityError, n.ID, -1,
					"template has no failure edge so 'all_failed' is unreachable — remove it from the decisions list (or add a failure edge to the template)")
			}
		}
	}
}

// checkConvergentEdgePrompts detects when multiple edges into the same
// destination node carry textually-differing prompt: values. Only one edge
// fires at runtime, but the engine cannot know which prompt is "correct" at
// definition time, and merging would silently drop all but one. Authors must
// unify the prompts, remove all but one, or route through intermediate nodes.
func checkConvergentEdgePrompts(r *ValidationResult, w *Workflow) {
	// dest -> trimmed-prompt -> edge indexes
	promptsByDest := map[string]map[string][]int{}
	for i, e := range w.Edges {
		p := strings.TrimSpace(e.Prompt)
		if p == "" {
			continue
		}
		if promptsByDest[e.To] == nil {
			promptsByDest[e.To] = map[string][]int{}
		}
		promptsByDest[e.To][p] = append(promptsByDest[e.To][p], i)
	}
	for dest, prompts := range promptsByDest {
		if len(prompts) <= 1 {
			continue
		}
		var samples []string
		var allEdges []int
		for p, edges := range prompts {
			samples = append(samples, fmt.Sprintf("edges %v: %q", edges, truncatePromptForError(p)))
			allEdges = append(allEdges, edges...)
		}
		sort.Strings(samples)
		sort.Ints(allEdges)
		for _, idx := range allEdges {
			r.add(SeverityError, dest, idx, fmt.Sprintf(
				"convergent edges to %q declare differing prompts; merge would silently drop. "+
					"Resolve by unifying, removing, or routing through an intermediate node. Variants: %s",
				dest, strings.Join(samples, " | ")))
		}
	}
}

// checkConvergentEdgePasses emits INFO messages when convergent edges
// to the same destination declare overlapping passes: keys. The merge
// rule (edge-index ASC, highest wins) is deterministic but silent;
// the INFO lets authors see when their convergent dispatches will
// shadow keys on overlap.
func checkConvergentEdgePasses(r *ValidationResult, w *Workflow) {
	// dest -> key -> edge indexes
	keysByDest := map[string]map[string][]int{}
	for i, e := range w.Edges {
		if len(e.Passes) == 0 {
			continue
		}
		if keysByDest[e.To] == nil {
			keysByDest[e.To] = map[string][]int{}
		}
		for k := range e.Passes {
			keysByDest[e.To][k] = append(keysByDest[e.To][k], i)
		}
	}
	for dest, keys := range keysByDest {
		for key, edges := range keys {
			if len(edges) <= 1 {
				continue
			}
			// Emit INFO on the highest-index edge (the winner).
			sort.Ints(edges)
			r.add(SeverityInfo, dest, edges[len(edges)-1], fmt.Sprintf(
				"convergent edges to %q overlap on passes key %q (edges %v); highest edge-index wins on dispatch (edge %d wins)",
				dest, key, edges, edges[len(edges)-1]))
		}
	}
}

// truncatePromptForError returns a display-safe snippet of a prompt for error messages.
func truncatePromptForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// checkLoopExhaustionPolicyValid emits a SeverityError when a node sets
// loop_exhaustion to any value other than "" or "fatal".
func checkLoopExhaustionPolicyValid(r *ValidationResult, w *Workflow) {
	for _, n := range w.Nodes {
		switch n.LoopExhaustionPolicy {
		case "", "fatal":
			// ok
		default:
			r.add(SeverityError, n.ID, -1, fmt.Sprintf(
				"node %s: invalid loop_exhaustion value %q (allowed: \"\", \"fatal\")",
				n.ID, n.LoopExhaustionPolicy))
		}
	}
}

// checkLoopExhaustionCoverage warns when a node can loop (is in a
// non-trivial SCC of the decision-edges graph) AND the workflow
// declares max_loop_iterations AND the node has no outgoing
// `when: _loop_exhausted` edge AND the node has not opted out via
// LoopExhaustionPolicy: "fatal".
//
// The warning surfaces the gap at load time so authors can either
// add graceful escalation routing OR explicitly opt into the legacy
// fatal-exhaustion behavior.
func checkLoopExhaustionCoverage(r *ValidationResult, w *Workflow) {
	limit := w.Limits["max_loop_iterations"]
	if limit <= 0 {
		return
	}

	adj := buildDecisionEdgeAdjacency(w)
	sccs := tarjanSCC(adj)

	// Build inSCC map: nodeID -> SCC members (only for nodes that
	// qualify per the same rule nodeInLoopableScc uses: non-trivial
	// SCC OR size-1 SCC with self-edge).
	inSCC := make(map[string][]string)
	for _, scc := range sccs {
		if len(scc) == 0 {
			continue
		}
		if len(scc) == 1 {
			id := scc[0]
			for _, target := range adj[id] {
				if target == id {
					inSCC[id] = scc
					break
				}
			}
			continue
		}
		for _, id := range scc {
			inSCC[id] = scc
		}
	}

	// Build hasExhaustEdge set: source nodes that have an outgoing
	// _loop_exhausted edge.
	hasExhaustEdge := make(map[string]bool)
	for _, e := range w.Edges {
		if e.When == metaLoopExhausted {
			hasExhaustEdge[e.From] = true
		}
	}

	// Iterate nodes in declaration order for stable output.
	for _, n := range w.Nodes {
		members, looping := inSCC[n.ID]
		if !looping {
			continue
		}
		if hasExhaustEdge[n.ID] {
			continue
		}
		if n.LoopExhaustionPolicy == "fatal" {
			continue
		}
		r.add(SeverityWarning, n.ID, -1, fmt.Sprintf(
			"node %s can loop (SCC: %v) and workflow has max_loop_iterations=%d, but has no outgoing `when: _loop_exhausted` edge. Loop exhaustion will fail the run fatally. Add a graceful escalation edge OR set `loop_exhaustion: fatal` on the node to acknowledge the fatal path.",
			n.ID, members, limit))
	}
}

// ValidateBundle checks cross-workflow references in a bundle.
func ValidateBundle(b *Bundle) *ValidationResult {
	r := &ValidationResult{}
	checkSubworkflowReferences(r, b)
	checkSubworkflowCycles(r, b)
	checkRunnerOverrides(r, b)
	checkNodeRunners(r, b)
	checkMaxTurns(r, b)
	validateResumeIntegrity(r, b)
	return r
}

// checkNodeRunners validates that every node's `runner` field references
// an existing runner in the bundle. Nodes without an explicit runner
// (relying on defaults) are skipped, as are subworkflow nodes which
// don't have runners.
func checkNodeRunners(r *ValidationResult, b *Bundle) {
	if b.Runners == nil {
		return
	}
	var available []string
	for id := range b.Runners {
		available = append(available, id)
	}
	sort.Strings(available)
	availableList := strings.Join(available, ", ")

	for wfID, wf := range b.Workflows {
		for _, n := range wf.Nodes {
			if n.Runner == "" {
				continue
			}
			if n.Kind == kindSubworkflow {
				continue
			}
			if _, ok := b.Runners[n.Runner]; ok {
				continue
			}
			r.add(SeverityError, n.ID, -1,
				fmt.Sprintf("workflow %q: node %q references unknown runner %q (available: %s)",
					wfID, n.ID, n.Runner, availableList))
		}
	}
}

// checkDuplicateNodes returns the set of valid node IDs and reports duplicates.
// Also rejects IDs containing reserved substrings (currently "::", which is
// used internally for ForEach expansion naming). Diagnostics are emitted in
// declaration order for deterministic output.
func checkDuplicateNodes(r *ValidationResult, w *Workflow) map[string]bool {
	counts := map[string]int{}
	for _, n := range w.Nodes {
		counts[n.ID]++
	}
	nodeIDs := map[string]bool{}
	seen := map[string]bool{}
	for _, n := range w.Nodes {
		id := n.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		if counts[id] > 1 {
			r.add(SeverityError, id, -1, fmt.Sprintf("duplicate node id %q (%d occurrences)", id, counts[id]))
		}
		if strings.Contains(id, "::") {
			r.add(SeverityError, id, -1,
				fmt.Sprintf("node id %q contains reserved substring \"::\" (used for ForEach expansion)", id))
		}
		nodeIDs[id] = true
	}
	return nodeIDs
}

// checkEdgeTargets validates that all edge From/To reference existing nodes.
func checkEdgeTargets(r *ValidationResult, w *Workflow, nodeIDs map[string]bool) {
	for i, e := range w.Edges {
		if !nodeIDs[e.From] {
			r.add(SeverityError, "", i, fmt.Sprintf("edge %d: 'from' references nonexistent node %q", i, e.From))
		}
		if !nodeIDs[e.To] {
			r.add(SeverityError, "", i, fmt.Sprintf("edge %d: 'to' references nonexistent node %q", i, e.To))
		}
	}
}

// checkReachability warns about nodes that cannot be reached from any start node.
// Mirrors the engine's startNodes logic: nodes with zero incoming edges are start
// nodes; if none exist, the first node in the list is the start node.
func checkReachability(r *ValidationResult, w *Workflow, nodeIDs map[string]bool) {
	if len(w.Nodes) == 0 {
		return
	}

	// Count incoming edges per node (including self-edges, matching engine behavior)
	incoming := map[string]int{}
	for _, e := range w.Edges {
		incoming[e.To]++
	}
	for _, n := range w.Nodes {
		// for_each.body targets count as having an incoming reference from their orchestrator
		if n.ForEach != nil && n.ForEach.Body != "" {
			incoming[n.ForEach.Body]++
		}
	}

	var starts []string
	for _, n := range w.Nodes {
		if incoming[n.ID] == 0 {
			starts = append(starts, n.ID)
		}
	}
	if len(starts) == 0 {
		starts = []string{w.Nodes[0].ID}
	}

	// BFS from start nodes, following edges and for_each.body links
	reachable := map[string]bool{}
	queue := starts
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if reachable[cur] {
			continue
		}
		reachable[cur] = true
		for _, e := range w.Edges {
			if e.From == cur && !reachable[e.To] {
				queue = append(queue, e.To)
			}
		}
		// Follow for_each.body references: templates are reachable via their orchestrator
		curNode := FindNode(w, cur)
		if curNode != nil && curNode.ForEach != nil && curNode.ForEach.Body != "" && !reachable[curNode.ForEach.Body] {
			queue = append(queue, curNode.ForEach.Body)
		}
	}

	for _, n := range w.Nodes {
		if !reachable[n.ID] {
			r.add(SeverityWarning, n.ID, -1, fmt.Sprintf("node %q is unreachable from any start node", n.ID))
		}
	}
}

// checkEdgeWhenValues warns when an edge's When value doesn't match the source node's declared decisions.
func checkEdgeWhenValues(r *ValidationResult, w *Workflow, nodeIDs map[string]bool) {
	// Build a map of node ID -> declared decisions
	decisions := map[string]map[string]bool{}
	for _, n := range w.Nodes {
		if len(n.Decisions) > 0 {
			d := map[string]bool{}
			for _, dec := range n.Decisions {
				d[dec.ID] = true
			}
			decisions[n.ID] = d
		}
	}

	for i, e := range w.Edges {
		// Reject failed: on any non-meta-decision edge regardless of when: form.
		// This must come before the early-continue checks below so that empty,
		// default, and expression when-values are not exempt.
		if e.Failed != nil && !strings.HasPrefix(e.When, "_") {
			r.add(SeverityError, e.From, i, fmt.Sprintf(
				"edge %d from %q to %q with when: %q: field \"failed:\" only applies to meta-decision edges (_loop_exhausted, _timeout, _retry_exhausted)",
				i, e.From, e.To, e.When,
			))
		}

		if e.When == "" || e.When == whenDefault || looksLikeExpression(e.When) {
			continue
		}
		if strings.HasPrefix(e.When, "_") {
			if !MetaDecisions[e.When] {
				r.add(SeverityError, e.From, i, fmt.Sprintf("edge %d: unknown meta-decision %q (supported: _loop_exhausted, _timeout, _retry_exhausted)", i, e.When))
				continue
			}
			if e.Failed == nil {
				r.add(SeverityError, e.From, i, fmt.Sprintf(
					"edge %d from %q to %q with when: %q: missing required field \"failed:\" (set to true if this routing represents a run-level failure, false if it is a normal terminus)",
					i, e.From, e.To, e.When,
				))
			}
			if e.When == metaLoopExhausted {
				if !nodeInLoopableScc(w, e.From) {
					r.add(SeverityError, e.From, i, fmt.Sprintf("edge %d: node %q cannot loop; _loop_exhausted will never fire", i, e.From))
				}
				continue
			}
			if e.When == metaTimeout {
				src := FindNode(w, e.From)
				if src == nil || src.TimeoutSec <= 0 || src.Gate != "required" {
					r.add(SeverityError, e.From, i, fmt.Sprintf("edge %d: _timeout requires source node to have timeout_sec > 0 AND gate: required", i))
				}
				continue
			}
			if e.When == metaRetryExhausted {
				src := FindNode(w, e.From)
				if src == nil || src.Retry == nil || src.Retry.Max <= 1 {
					r.add(SeverityError, e.From, i, fmt.Sprintf("edge %d: _retry_exhausted requires source node to have retry: { max: > 1 }", i))
				}
				continue
			}
			continue
		}
		// Non-meta-decision edge: decisions-declared check.
		decs, hasDecs := decisions[e.From]
		if !hasDecs {
			// Source node has no declared decisions — skip
			continue
		}
		if !decs[e.When] {
			r.add(SeverityWarning, e.From, i, fmt.Sprintf("edge %d: when %q does not match any declared decision of node %q", i, e.When, e.From))
		}
	}

	// Check that there is at most one meta-decision edge per (source, meta-decision) pair
	seen := map[string]map[string]bool{} // from-node-id -> meta-decision -> seen
	for _, e := range w.Edges {
		if !MetaDecisions[e.When] {
			continue
		}
		if seen[e.From] == nil {
			seen[e.From] = map[string]bool{}
		}
		if seen[e.From][e.When] {
			r.add(SeverityError, e.From, -1, fmt.Sprintf(
				"node %q has multiple outgoing edges with when: %q; meta-decision routing must be single-target",
				e.From, e.When,
			))
		}
		seen[e.From][e.When] = true
	}
}

// checkSubworkflowReferences validates that subworkflow nodes reference workflows that exist in the bundle.
func checkSubworkflowReferences(r *ValidationResult, b *Bundle) {
	for wfID, wf := range b.Workflows {
		for _, n := range wf.Nodes {
			if n.Kind != kindSubworkflow || n.Workflow == "" {
				continue
			}
			if _, ok := b.Workflows[n.Workflow]; !ok {
				r.add(SeverityError, n.ID, -1, fmt.Sprintf("workflow %q: node %q references unknown subworkflow %q", wfID, n.ID, n.Workflow))
			}
		}
	}
}

// checkSubworkflowCycles detects cycles in the workflow reference graph using DFS.
func checkSubworkflowCycles(r *ValidationResult, b *Bundle) {
	// Build adjacency list: workflow ID -> set of referenced workflow IDs
	adj := map[string][]string{}
	for wfID, wf := range b.Workflows {
		for _, n := range wf.Nodes {
			if n.Kind == kindSubworkflow && n.Workflow != "" {
				adj[wfID] = append(adj[wfID], n.Workflow)
			}
		}
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully explored
	)

	color := map[string]int{}

	var dfs func(wfID string) bool
	dfs = func(wfID string) bool {
		color[wfID] = gray
		for _, child := range adj[wfID] {
			switch color[child] {
			case gray:
				r.add(SeverityError, "", -1, fmt.Sprintf("subworkflow cycle detected: %s -> %s", wfID, child))
				return true
			case white:
				if dfs(child) {
					return true
				}
			}
		}
		color[wfID] = black
		return false
	}

	for wfID := range b.Workflows {
		if color[wfID] == white {
			if dfs(wfID) {
				return // one cycle is enough
			}
		}
	}
}

// checkRunnerOverrides validates runner_overrides in each workflow:
// 1. Every runner ID must exist in the bundle's Runners map (ERROR).
// 2. A tag that matches no nodes in its workflow is likely a typo (WARNING).
func checkRunnerOverrides(r *ValidationResult, b *Bundle) {
	for wfID, wf := range b.Workflows {
		if len(wf.RunnerOverrides) == 0 {
			continue
		}

		// Collect all tags used by nodes in this workflow
		nodeTags := map[string]bool{}
		for _, n := range wf.Nodes {
			for _, tag := range n.Tags {
				nodeTags[tag] = true
			}
		}

		for tag, runnerID := range wf.RunnerOverrides {
			if b.Runners == nil || b.Runners[runnerID] == nil {
				r.add(SeverityError, "", -1, fmt.Sprintf("workflow %q: runner_overrides tag %q references unknown runner %q", wfID, tag, runnerID))
			}
			if !nodeTags[tag] {
				r.add(SeverityWarning, "", -1, fmt.Sprintf("workflow %q: runner_overrides tag %q matches no nodes in workflow", wfID, tag))
			}
		}
	}
}

// checkMaxTurns warns when max_turns is set on a node whose resolved runner
// does not support it. Only claude and codex runners use max_turns.
func checkMaxTurns(r *ValidationResult, b *Bundle) {
	turnsRunnerTypes := map[string]bool{runnerClaude: true, runnerCodex: true}
	for wfID, wf := range b.Workflows {
		for _, n := range wf.Nodes {
			if n.MaxTurns <= 0 {
				continue
			}
			runnerID := resolveRunnerID(n, wf, b)
			if runnerID == "" {
				continue
			}
			runner, ok := b.Runners[runnerID]
			if !ok {
				continue
			}
			if !turnsRunnerTypes[runner.Type] {
				r.add(SeverityWarning, n.ID, -1,
					fmt.Sprintf("workflow %q: max_turns=%d has no effect on %s runner %q",
						wfID, n.MaxTurns, runner.Type, runnerID))
			}
		}
	}
}

// resolveRunnerID determines which runner a node will use, following the
// resolution priority: node.Runner > runner_overrides (by tag).
func resolveRunnerID(n Node, wf *Workflow, b *Bundle) string {
	if n.Runner != "" {
		return n.Runner
	}
	for _, tag := range n.Tags {
		if override, ok := wf.RunnerOverrides[tag]; ok {
			return override
		}
	}
	return ""
}
