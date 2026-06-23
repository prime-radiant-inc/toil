package definitions

import (
	"os"
	"strings"
	"testing"
)

// --- ValidateGraph tests ---

func TestValidateGraph_ValidSimpleChain(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("expected no errors, got: %v", result)
	}
}

func TestValidateGraph_ValidWithDecisions(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "yes"},
			{From: "a", To: "c", When: "no"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("expected no errors, got: %v", result)
	}
	if result.HasWarnings() {
		t.Fatalf("expected no warnings, got: %v", result)
	}
}

func TestValidateGraph_DuplicateNodeIDs(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "a", Kind: "system"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for duplicate node IDs")
	}
	if !containsDiagnostic(result, SeverityError, "duplicate node id") {
		t.Fatalf("expected duplicate node id error, got: %v", result)
	}
}

func TestValidateGraph_EdgeToNonexistentNode(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "ghost"},
		},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for dangling edge target")
	}
	if !containsDiagnostic(result, SeverityError, "ghost") {
		t.Fatalf("expected error mentioning 'ghost', got: %v", result)
	}
}

func TestValidateGraph_EdgeFromNonexistentNode(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "ghost", To: "a"},
		},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for dangling edge source")
	}
	if !containsDiagnostic(result, SeverityError, "ghost") {
		t.Fatalf("expected error mentioning 'ghost', got: %v", result)
	}
}

func TestValidateGraph_UnreachableNode(t *testing.T) {
	// "orphan" has an incoming edge but no path from any start node.
	// "a" is the only start node (no incoming edges).
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "orphan", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "orphan", To: "orphan"}, // self-edge makes it non-start
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("unreachable should be warning not error, got: %v", result)
	}
	if !containsDiagnostic(result, SeverityWarning, "orphan") {
		t.Fatalf("expected unreachable warning for 'orphan', got: %v", result)
	}
}

func TestValidateGraph_TerminalDecisionNoWarning(t *testing.T) {
	// A decision without an outgoing edge is valid (terminal decision).
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("done", "retry")},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "retry"},
		},
	}
	result := ValidateGraph(w)
	// "done" is a terminal decision — should NOT produce an uncovered warning
	if containsDiagnostic(result, SeverityWarning, "done") {
		t.Fatalf("terminal decision 'done' should not produce warning, got: %v", result)
	}
}

func TestValidateGraph_UnusedEdgeWhen(t *testing.T) {
	// Edge has when="typo" but node declares decisions [yes, no]
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "typo"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, "typo") {
		t.Fatalf("expected warning for unused edge when 'typo', got: %v", result)
	}
}

func TestValidateGraph_DefaultEdgeNotFlagged(t *testing.T) {
	// "default" when value should not be flagged as unused
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes")},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "default"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "default") {
		t.Fatalf("'default' edge should not be flagged, got: %v", result)
	}
}

func TestValidateGraph_EmptyWhenNotFlagged(t *testing.T) {
	// Empty when (unconditional edge) should not be flagged
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes")},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "unused") {
		t.Fatalf("empty when should not be flagged, got: %v", result)
	}
}

func TestValidateGraph_EmptyWorkflow(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("empty workflow should be valid, got: %v", result)
	}
}

func TestValidateGraph_SelfEdgeValid(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("retry", "done")},
		},
		Edges: []Edge{
			{From: "a", To: "a", When: "retry"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("self-edge should be valid, got: %v", result)
	}
}

func TestValidateGraph_NodeWithoutDeclarations(t *testing.T) {
	// Node has no decisions declared but has outgoing edges with when values.
	// This should NOT warn about unused edge when — the node just doesn't declare decisions.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "something"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "something") {
		t.Fatalf("should not warn about edge when if node has no declared decisions, got: %v", result)
	}
}

// --- ValidateBundle tests ---

func TestValidateBundle_ValidBundle(t *testing.T) {
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if result.HasErrors() {
		t.Fatalf("expected no errors, got: %v", result)
	}
}

func TestValidateBundle_MissingSubworkflow(t *testing.T) {
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "subworkflow", Workflow: "ghost_wf"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for missing subworkflow")
	}
	if !containsDiagnostic(result, SeverityError, "ghost_wf") {
		t.Fatalf("expected error mentioning 'ghost_wf', got: %v", result)
	}
}

func TestValidateBundle_SubworkflowCycle(t *testing.T) {
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"a": {
				ID: "a",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "b"},
				},
				Edges: []Edge{},
			},
			"b": {
				ID: "b",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "a"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for subworkflow cycle")
	}
	if !containsDiagnostic(result, SeverityError, "cycle") {
		t.Fatalf("expected cycle error, got: %v", result)
	}
}

func TestValidateBundle_SubworkflowDiamondNoCycle(t *testing.T) {
	// A -> B, A -> C, B -> D, C -> D — diamond, not a cycle
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"a": {
				ID: "a",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "b"},
					{ID: "n2", Kind: "subworkflow", Workflow: "c"},
				},
				Edges: []Edge{},
			},
			"b": {
				ID: "b",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "d"},
				},
				Edges: []Edge{},
			},
			"c": {
				ID: "c",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "d"},
				},
				Edges: []Edge{},
			},
			"d": {
				ID: "d",
				Nodes: []Node{
					{ID: "n1", Kind: "role"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if result.HasErrors() {
		t.Fatalf("diamond should not be a cycle, got: %v", result)
	}
}

func TestValidateBundle_SubworkflowSelfReference(t *testing.T) {
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"a": {
				ID: "a",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "a"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for self-referencing subworkflow")
	}
	if !containsDiagnostic(result, SeverityError, "cycle") {
		t.Fatalf("expected cycle error, got: %v", result)
	}
}

func TestValidateBundle_SystemNodeValid(t *testing.T) {
	// A system node should validate without errors
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "system"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if result.HasErrors() {
		t.Fatalf("system node with no role should be valid, got: %v", result)
	}
}

// --- looksLikeExpression tests ---

func TestLooksLikeExpression_Operators(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Should detect expressions
		{"status == 'done'", true},
		{"x != y", true},
		{"count >= 10", true},
		{"count <= 5", true},
		{"a && b", true},
		{"a || b", true},
		{"a == b && c != d", true},
		// Operators embedded in larger strings
		{"this==that", true},
		{"no!=yes", true},

		// Should NOT detect expressions
		{"approved", false},
		{"yes", false},
		{"", false},
		{"default", false},
		{"some_value", false},
		// Single characters that are parts of operators
		{"=", false},
		{"!", false},
		{"&", false},
		{"|", false},
		// Words containing operator chars but not the actual operators
		{"grand", false},
		// `<` and `>` ARE expression operators per engine.IsExpression;
		// the validator must agree to avoid drift on edges like `x > 5`.
		{">", true},
		{"<", true},
		{"x > 5", true},
		{"score < threshold", true},
		{"island", false},
	}
	for _, tt := range tests {
		got := looksLikeExpression(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeExpression(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- ValidationResult method tests ---

func TestValidationResult_EmptyDiagnostics(t *testing.T) {
	r := &ValidationResult{}
	if r.HasErrors() {
		t.Fatal("empty result should not have errors")
	}
	if r.HasWarnings() {
		t.Fatal("empty result should not have warnings")
	}
	if r.Error() != "" {
		t.Fatalf("empty result Error() should be empty, got: %q", r.Error())
	}
}

func TestValidationResult_OnlyWarnings(t *testing.T) {
	r := &ValidationResult{
		Diagnostics: []Diagnostic{
			{Severity: SeverityWarning, Message: "something is off"},
		},
	}
	if r.HasErrors() {
		t.Fatal("result with only warnings should not have errors")
	}
	if !r.HasWarnings() {
		t.Fatal("result with warnings should have warnings")
	}
}

func TestValidationResult_OnlyErrors(t *testing.T) {
	r := &ValidationResult{
		Diagnostics: []Diagnostic{
			{Severity: SeverityError, Message: "bad thing"},
		},
	}
	if !r.HasErrors() {
		t.Fatal("result with errors should have errors")
	}
	if r.HasWarnings() {
		t.Fatal("result with only errors should not have warnings")
	}
}

func TestValidationResult_MixedSeverities(t *testing.T) {
	r := &ValidationResult{
		Diagnostics: []Diagnostic{
			{Severity: SeverityWarning, Message: "warn 1"},
			{Severity: SeverityError, Message: "err 1", NodeID: "n1"},
			{Severity: SeverityWarning, Message: "warn 2", NodeID: "n2"},
		},
	}
	if !r.HasErrors() {
		t.Fatal("should have errors")
	}
	if !r.HasWarnings() {
		t.Fatal("should have warnings")
	}

	errStr := r.Error()
	// Check that all diagnostics appear in order
	if !strings.Contains(errStr, "warning: warn 1") {
		t.Fatalf("Error() should contain warning without node ID, got: %s", errStr)
	}
	if !strings.Contains(errStr, `error: node "n1": err 1`) {
		t.Fatalf("Error() should contain error with node ID, got: %s", errStr)
	}
	if !strings.Contains(errStr, `warning: node "n2": warn 2`) {
		t.Fatalf("Error() should contain warning with node ID, got: %s", errStr)
	}
	// Diagnostics should be separated by semicolons
	if strings.Count(errStr, "; ") != 2 {
		t.Fatalf("expected 2 semicolons separating 3 diagnostics, got: %s", errStr)
	}
}

func TestValidationResult_ErrorFormatNoNodeID(t *testing.T) {
	r := &ValidationResult{
		Diagnostics: []Diagnostic{
			{Severity: SeverityError, Message: "general failure"},
		},
	}
	errStr := r.Error()
	if !strings.Contains(errStr, "error: general failure") {
		t.Fatalf("expected 'error: general failure', got: %s", errStr)
	}
	// Should NOT contain "node" prefix when NodeID is empty
	if strings.Contains(errStr, "node") {
		t.Fatalf("should not contain 'node' when NodeID is empty, got: %s", errStr)
	}
}

// --- Reachability tests ---

func TestValidateGraph_SingleNodeNoEdges(t *testing.T) {
	// Single node with no edges: node has no incoming edges, so it is a start node.
	// It should be reachable from itself.
	w := &Workflow{
		Nodes: []Node{{ID: "only", Kind: "role"}},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if result.HasErrors() || result.HasWarnings() {
		t.Fatalf("single node with no edges should be clean, got: %s", result.Error())
	}
}

func TestValidateGraph_SingleNodeWithSelfEdge(t *testing.T) {
	// Single node with a self-edge: it has an incoming edge, so no zero-incoming nodes.
	// Fallback: Nodes[0] becomes start node. "only" is reachable via the fallback.
	w := &Workflow{
		Nodes: []Node{{ID: "only", Kind: "role"}},
		Edges: []Edge{{From: "only", To: "only"}},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("single node self-edge should be valid, got: %s", result.Error())
	}
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("single node should not be unreachable, got: %s", result.Error())
	}
}

func TestValidateGraph_AllNodesHaveIncomingEdges(t *testing.T) {
	// All nodes have incoming edges, so fallback to Nodes[0] as start.
	// a->b, b->a forms a cycle where both have incoming edges.
	// Nodes[0]="a" is chosen as start. Both are reachable via a->b.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "a"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("mutual cycle should all be reachable via Nodes[0] fallback, got: %s", result.Error())
	}
}

func TestValidateGraph_AllNodesHaveIncomingEdgesWithOrphan(t *testing.T) {
	// a->b, b->a, c->c. All have incoming edges, fallback to Nodes[0]="a".
	// c is not reachable from a.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "a"},
			{From: "c", To: "c"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, `node "c" is unreachable`) {
		t.Fatalf("c should be unreachable from Nodes[0] fallback, got: %s", result.Error())
	}
	// a and b should not be warned about
	if containsDiagnostic(result, SeverityWarning, `node "a"`) {
		t.Fatalf("a should be reachable, got: %s", result.Error())
	}
	if containsDiagnostic(result, SeverityWarning, `node "b"`) {
		t.Fatalf("b should be reachable, got: %s", result.Error())
	}
}

func TestValidateGraph_DiamondTopology(t *testing.T) {
	// a -> b, a -> c, b -> d, c -> d — all reachable from a
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	result := ValidateGraph(w)
	if result.HasWarnings() || result.HasErrors() {
		t.Fatalf("diamond topology should be clean, got: %s", result.Error())
	}
}

func TestValidateGraph_LongChain(t *testing.T) {
	// a -> b -> c -> d -> e: all reachable from a
	nodes := []Node{
		{ID: "a", Kind: "role"},
		{ID: "b", Kind: "role"},
		{ID: "c", Kind: "role"},
		{ID: "d", Kind: "role"},
		{ID: "e", Kind: "role"},
	}
	edges := []Edge{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
		{From: "c", To: "d"},
		{From: "d", To: "e"},
	}
	w := &Workflow{Nodes: nodes, Edges: edges}
	result := ValidateGraph(w)
	if result.HasWarnings() || result.HasErrors() {
		t.Fatalf("long chain should be clean, got: %s", result.Error())
	}
}

func TestValidateGraph_MultipleDisconnectedComponents(t *testing.T) {
	// Two disconnected components: {a->b} and {c->d}
	// a and c both have zero incoming edges, so both are start nodes.
	// All nodes should be reachable.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "c", To: "d"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("expected no errors, got: %s", result.Error())
	}
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("all components should be reachable via multiple start nodes, got: %s", result.Error())
	}
}

func TestValidateGraph_MultipleStartNodes(t *testing.T) {
	// a, b, c all have no incoming edges. a->d, b->d, c->d.
	// All are start nodes or reachable from start nodes.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "d"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("all nodes should be reachable, got: %s", result.Error())
	}
}

func TestValidateGraph_MultipleUnreachableNodes(t *testing.T) {
	// a is start (no incoming). b and c have self-edges so they have incoming.
	// Neither b nor c is reachable from a.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "b", To: "b"},
			{From: "c", To: "c"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, `node "b" is unreachable`) {
		t.Fatalf("b should be unreachable, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, `node "c" is unreachable`) {
		t.Fatalf("c should be unreachable, got: %s", result.Error())
	}
}

// --- Duplicate node detection tests ---

func TestValidateGraph_TriplicateNodeIDs(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "a", Kind: "system"},
			{ID: "a", Kind: "subworkflow"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for triplicate node IDs")
	}
	// Message should mention 3 occurrences
	if !containsDiagnostic(result, SeverityError, "3 occurrences") {
		t.Fatalf("expected '3 occurrences' in error, got: %s", result.Error())
	}
}

func TestValidateGraph_MultipleDifferentDuplicates(t *testing.T) {
	// Two different node IDs each duplicated
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "role"},
			{ID: "b", Kind: "system"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, `duplicate node id "a"`) {
		t.Fatalf("expected duplicate error for 'a', got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, `duplicate node id "b"`) {
		t.Fatalf("expected duplicate error for 'b', got: %s", result.Error())
	}
}

// --- Edge target validation tests ---

func TestValidateGraph_EdgeWithEmptyStringNodeIDs(t *testing.T) {
	// Edge referencing empty string From and To
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "", To: ""},
		},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected errors for empty string node references in edge")
	}
	// Both from and to should be flagged (empty string is not a valid node ID)
	errorCount := 0
	for _, d := range result.Diagnostics {
		if d.Severity == SeverityError && strings.Contains(d.Message, "nonexistent node") {
			errorCount++
		}
	}
	if errorCount < 2 {
		t.Fatalf("expected at least 2 nonexistent node errors (from and to), got %d: %s", errorCount, result.Error())
	}
}

func TestValidateGraph_BothFromAndToNonexistent(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "ghost1", To: "ghost2"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "ghost1") {
		t.Fatalf("expected error for ghost1, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "ghost2") {
		t.Fatalf("expected error for ghost2, got: %s", result.Error())
	}
}

func TestValidateGraph_SelfEdgeToNonexistentNode(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "ghost", To: "ghost"},
		},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for self-referencing edge to nonexistent node")
	}
	// Should get error for both from and to since "ghost" is not in nodeIDs
	errorCount := 0
	for _, d := range result.Diagnostics {
		if d.Severity == SeverityError && strings.Contains(d.Message, "ghost") {
			errorCount++
		}
	}
	if errorCount < 2 {
		t.Fatalf("expected errors for both from and to referencing 'ghost', got %d: %s", errorCount, result.Error())
	}
}

func TestValidateGraph_MultipleEdgesWithSameTarget(t *testing.T) {
	// Multiple edges all pointing to the same valid target — should be fine
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "c"},
			{From: "b", To: "c"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("multiple edges to same target should be valid, got: %s", result.Error())
	}
}

// --- Edge when values tests ---

func TestValidateGraph_ExpressionEdgeSkipped(t *testing.T) {
	// Edges with expression-like when values should be skipped
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
			{ID: "e", Kind: "role"},
			{ID: "f", Kind: "role"},
			{ID: "g", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "status == 'done'"},
			{From: "a", To: "c", When: "result != 'fail'"},
			{From: "a", To: "d", When: "count >= 10"},
			{From: "a", To: "e", When: "count <= 5"},
			{From: "a", To: "f", When: "a && b"},
			{From: "a", To: "g", When: "x || y"},
		},
	}
	result := ValidateGraph(w)
	// None of these should produce warnings since they look like expressions
	for _, d := range result.Diagnostics {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "does not match") {
			t.Fatalf("expression edges should not produce warnings, got: %s", d.Message)
		}
	}
}

func TestValidateGraph_NearExpressionNotSkipped(t *testing.T) {
	// Values that look almost like expressions but aren't
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "approved"},  // not an expression
			{From: "a", To: "c", When: "not_found"}, // contains "!" as part of word, but no !=
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, "approved") {
		t.Fatalf("non-expression 'approved' should trigger warning, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "not_found") {
		t.Fatalf("non-expression 'not_found' should trigger warning, got: %s", result.Error())
	}
}

func TestValidateGraph_AllEdgesMatchDecisions(t *testing.T) {
	// Every edge's when value matches a declared decision — no warnings
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("pass", "fail", "retry")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "pass"},
			{From: "a", To: "c", When: "fail"},
			{From: "a", To: "d", When: "retry"},
		},
	}
	result := ValidateGraph(w)
	if result.HasWarnings() {
		t.Fatalf("all matching decisions should have no warnings, got: %s", result.Error())
	}
}

func TestValidateGraph_NoEdgesMatchDecisions(t *testing.T) {
	// Every edge's when value is wrong
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("pass", "fail")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "typo1"},
			{From: "a", To: "c", When: "typo2"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, "typo1") {
		t.Fatalf("expected warning for typo1, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "typo2") {
		t.Fatalf("expected warning for typo2, got: %s", result.Error())
	}
}

func TestValidateGraph_EdgeFromNonexistentNodeWithWhen(t *testing.T) {
	// Edge from nonexistent node with a when value — should get edge target error.
	// When check should also handle gracefully (no decisions for nonexistent node).
	w := &Workflow{
		Nodes: []Node{
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "ghost", To: "b", When: "something"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "'from' references nonexistent node") {
		t.Fatalf("expected edge target error for ghost, got: %s", result.Error())
	}
	// Should NOT crash or warn about when — ghost has no decisions
	if containsDiagnostic(result, SeverityWarning, "something") {
		t.Fatalf("should not warn about when for nonexistent source node, got: %s", result.Error())
	}
}

func TestValidateGraph_MixedWhenAndDefault(t *testing.T) {
	// Some edges match, one is default, one is a typo
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b", When: "yes"},
			{From: "a", To: "c", When: "default"},
			{From: "a", To: "d", When: "maybe"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "yes") {
		t.Fatalf("matching decision 'yes' should not warn, got: %s", result.Error())
	}
	if containsDiagnostic(result, SeverityWarning, "default") {
		t.Fatalf("default should not warn, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "maybe") {
		t.Fatalf("non-matching 'maybe' should warn, got: %s", result.Error())
	}
}

// --- Context field validation tests ---

func TestValidateGraph_ValidContextValues(t *testing.T) {
	for _, ctx := range []string{"", "full", "fresh", "compact", "summary"} {
		w := &Workflow{
			Nodes: []Node{
				{ID: "a", Kind: "role", Context: ctx},
			},
			Edges: []Edge{},
		}
		result := ValidateGraph(w)
		if result.HasErrors() {
			t.Fatalf("context %q should be valid, got: %s", ctx, result.Error())
		}
	}
}

func TestValidateGraph_InvalidContextValue(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Context: "partial"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for invalid context value")
	}
	if !containsDiagnostic(result, SeverityError, "invalid context") {
		t.Fatalf("expected 'invalid context' error, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "partial") {
		t.Fatalf("error should mention the invalid value 'partial', got: %s", result.Error())
	}
}

func TestValidateGraph_WhitespaceContextValue(t *testing.T) {
	// Whitespace-only context is not a valid value
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Context: " "},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for whitespace context value")
	}
}

func TestValidateGraph_ContextWithTrailingSpace(t *testing.T) {
	// "full " (with space) is not "full"
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Context: "full "},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for 'full ' (with trailing space) context")
	}
}

func TestValidateGraph_MultipleNodesWithInvalidContext(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Context: "bad1"},
			{ID: "b", Kind: "role", Context: "bad2"},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "bad1") {
		t.Fatalf("expected error for bad1, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "bad2") {
		t.Fatalf("expected error for bad2, got: %s", result.Error())
	}
}

// --- Subworkflow cycle tests ---

func TestValidateBundle_ThreeNodeCycle(t *testing.T) {
	// a -> b -> c -> a
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"a": {
				ID: "a",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "b"},
				},
			},
			"b": {
				ID: "b",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "c"},
				},
			},
			"c": {
				ID: "c",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "a"},
				},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for 3-node subworkflow cycle")
	}
	if !containsDiagnostic(result, SeverityError, "cycle") {
		t.Fatalf("expected cycle error, got: %s", result.Error())
	}
}

func TestValidateBundle_DeeplyNestedChainNoCycle(t *testing.T) {
	// a -> b -> c -> d -> e (no cycle)
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"a": {
				ID:    "a",
				Nodes: []Node{{ID: "n1", Kind: "subworkflow", Workflow: "b"}},
			},
			"b": {
				ID:    "b",
				Nodes: []Node{{ID: "n1", Kind: "subworkflow", Workflow: "c"}},
			},
			"c": {
				ID:    "c",
				Nodes: []Node{{ID: "n1", Kind: "subworkflow", Workflow: "d"}},
			},
			"d": {
				ID:    "d",
				Nodes: []Node{{ID: "n1", Kind: "subworkflow", Workflow: "e"}},
			},
			"e": {
				ID:    "e",
				Nodes: []Node{{ID: "n1", Kind: "role"}},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "cycle") {
		t.Fatalf("deep chain should not be a cycle, got: %s", result.Error())
	}
}

func TestValidateBundle_MultipleSubworkflowRefsFromOneWorkflow(t *testing.T) {
	// Workflow "main" references both "helper1" and "helper2" — no cycle
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: "helper1"},
					{ID: "n2", Kind: "subworkflow", Workflow: "helper2"},
				},
			},
			"helper1": {
				ID:    "helper1",
				Nodes: []Node{{ID: "n1", Kind: "role"}},
			},
			"helper2": {
				ID:    "helper2",
				Nodes: []Node{{ID: "n1", Kind: "role"}},
			},
		},
	}
	result := ValidateBundle(b)
	if result.HasErrors() {
		t.Fatalf("multiple subworkflow refs without cycle should be valid, got: %s", result.Error())
	}
}

func TestValidateBundle_SubworkflowNodeWithEmptyWorkflowField(t *testing.T) {
	// A subworkflow node with empty Workflow field should be ignored by cycle detection
	// and subworkflow reference checks
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "n1", Kind: "subworkflow", Workflow: ""},
				},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "subworkflow") {
		t.Fatalf("empty workflow field should be skipped, got: %s", result.Error())
	}
}

func TestValidateBundle_NonSubworkflowNodeWithWorkflowField(t *testing.T) {
	// A non-subworkflow node that happens to have a Workflow field set
	// should not be checked for subworkflow references
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "n1", Kind: "role", Workflow: "nonexistent"},
				},
			},
		},
	}
	result := ValidateBundle(b)
	// Should NOT error — only "subworkflow" kind triggers the check
	if containsDiagnostic(result, SeverityError, "nonexistent") {
		t.Fatalf("non-subworkflow node with Workflow field should be ignored, got: %s", result.Error())
	}
}

// --- ValidateBundle + ValidateGraph interaction tests ---

func TestValidateBundle_DoesNotRunGraphChecks(t *testing.T) {
	// ValidateBundle should only run bundle-level checks (roles, subworkflows, cycles).
	// Graph-level checks (duplicates, edge targets, reachability, when values, context)
	// are run separately by ValidateGraph. Verify they don't overlap.
	b := &Bundle{
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role"},
					{ID: "a", Kind: "role"}, // duplicate — ValidateGraph would catch this
				},
				Edges: []Edge{
					{From: "a", To: "ghost"}, // dangling edge — ValidateGraph would catch this
				},
			},
		},
	}
	bundleResult := ValidateBundle(b)
	// Bundle validation should NOT flag graph-level issues
	if containsDiagnostic(bundleResult, SeverityError, "duplicate") {
		t.Fatalf("ValidateBundle should not check for duplicate nodes, got: %s", bundleResult.Error())
	}
	if containsDiagnostic(bundleResult, SeverityError, "ghost") {
		t.Fatalf("ValidateBundle should not check edge targets, got: %s", bundleResult.Error())
	}

	// But ValidateGraph should catch them
	graphResult := ValidateGraph(b.Workflows["main"])
	if !graphResult.HasErrors() {
		t.Fatal("ValidateGraph should catch duplicate and edge target errors")
	}
}

func TestValidateBundle_EmptyBundle(t *testing.T) {
	b := &Bundle{
		Workflows: map[string]*Workflow{},
	}
	result := ValidateBundle(b)
	if result.HasErrors() || result.HasWarnings() {
		t.Fatalf("empty bundle should be valid, got: %s", result.Error())
	}
}

// --- Integration: validateWorkflow calls ValidateGraph ---

func TestValidateWorkflow_PropagatesGraphErrors(t *testing.T) {
	// validateWorkflow (used by LoadWorkflowFile) calls ValidateGraph
	// and returns an error if HasErrors is true.
	w := &Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "a", Kind: "role"}, // duplicate
		},
		Edges: []Edge{},
	}
	err := validateWorkflow(w)
	if err == nil {
		t.Fatal("expected error from validateWorkflow for duplicate nodes")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate node error, got: %s", err.Error())
	}
}

func TestValidateWorkflow_IgnoresWarnings(t *testing.T) {
	// validateWorkflow should pass even if ValidateGraph produces warnings
	// (only errors cause failure)
	w := &Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "orphan", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "orphan", To: "orphan"},
		},
	}
	err := validateWorkflow(w)
	if err != nil {
		t.Fatalf("warnings should not cause validateWorkflow to fail, got: %v", err)
	}
}

func TestValidateWorkflow_RequiresID(t *testing.T) {
	w := &Workflow{Name: "Test", Version: 1}
	err := validateWorkflow(w)
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
}

func TestValidateWorkflow_RequiresName(t *testing.T) {
	w := &Workflow{ID: "test", Version: 1}
	err := validateWorkflow(w)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateWorkflow_RequiresVersion(t *testing.T) {
	w := &Workflow{ID: "test", Name: "Test"}
	err := validateWorkflow(w)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestValidateWorkflow_NilNodesEdgesInitialized(t *testing.T) {
	// validateWorkflow should initialize nil Nodes/Edges to empty slices
	w := &Workflow{
		ID:      "test",
		Name:    "Test",
		Version: 1,
	}
	err := validateWorkflow(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Nodes == nil {
		t.Fatal("Nodes should be initialized to empty slice, not nil")
	}
	if w.Edges == nil {
		t.Fatal("Edges should be initialized to empty slice, not nil")
	}
}

// --- Edge cases combining multiple validation rules ---

func TestValidateGraph_DuplicateNodeWithEdges(t *testing.T) {
	// Duplicate nodes AND edges referencing them — should get duplicate error.
	// The duplicate still counts as a valid nodeID for edge checking purposes
	// (it's in the nodeIDs set), so edges should not additionally complain.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "duplicate") {
		t.Fatalf("expected duplicate error, got: %s", result.Error())
	}
	// Edge target should NOT additionally complain since "a" is still in nodeIDs
	if containsDiagnostic(result, SeverityError, "nonexistent") {
		t.Fatalf("duplicate node should still be a valid edge target, got: %s", result.Error())
	}
}

func TestValidateGraph_NilWorkflowSlices(t *testing.T) {
	// Passing a workflow with nil slices (not empty) to ValidateGraph directly
	w := &Workflow{
		Nodes: nil,
		Edges: nil,
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("nil slices should be handled gracefully, got: %s", result.Error())
	}
}

func TestValidateGraph_EdgesOnlyNoNodes(t *testing.T) {
	// Edges referencing nodes that don't exist, with no nodes at all
	w := &Workflow{
		Nodes: []Node{},
		Edges: []Edge{
			{From: "a", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected errors for edges referencing nonexistent nodes")
	}
}

func TestValidateGraph_CyclicGraphReachability(t *testing.T) {
	// a -> b -> c -> b (cycle), a has no incoming => start node.
	// All of a, b, c should be reachable.
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
			{From: "c", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("cyclic graph should have all nodes reachable from a, got: %s", result.Error())
	}
}

func TestValidateGraph_ComplexTopology(t *testing.T) {
	// Complex graph: start -> fork1, fork1 -> join, start -> fork2, fork2 -> join,
	// join -> end. Also an island: orphan -> orphan_sink, but orphan has incoming
	// from orphan_source (which is a start node since it has no incoming edges).
	// Everything should be reachable.
	w := &Workflow{
		Nodes: []Node{
			{ID: "start", Kind: "role"},
			{ID: "fork1", Kind: "role"},
			{ID: "fork2", Kind: "role"},
			{ID: "join", Kind: "role"},
			{ID: "end", Kind: "role"},
			{ID: "orphan_source", Kind: "role"},
			{ID: "orphan_sink", Kind: "role"},
		},
		Edges: []Edge{
			{From: "start", To: "fork1"},
			{From: "start", To: "fork2"},
			{From: "fork1", To: "join"},
			{From: "fork2", To: "join"},
			{From: "join", To: "end"},
			{From: "orphan_source", To: "orphan_sink"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() || result.HasWarnings() {
		t.Fatalf("complex topology should be clean, got: %s", result.Error())
	}
}

// --- Diagnostic edge index tests ---

func TestValidateGraph_EdgeErrorsIncludeEdgeIndex(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "a"},       // edge 0: valid
			{From: "a", To: "ghost"},   // edge 1: invalid to
			{From: "phantom", To: "a"}, // edge 2: invalid from
		},
	}
	result := ValidateGraph(w)
	// Check that the diagnostics reference the correct edge indices
	foundEdge1 := false
	foundEdge2 := false
	for _, d := range result.Diagnostics {
		if d.EdgeIdx == 1 && strings.Contains(d.Message, "ghost") {
			foundEdge1 = true
		}
		if d.EdgeIdx == 2 && strings.Contains(d.Message, "phantom") {
			foundEdge2 = true
		}
	}
	if !foundEdge1 {
		t.Fatalf("expected diagnostic for edge 1 (ghost), got: %s", result.Error())
	}
	if !foundEdge2 {
		t.Fatalf("expected diagnostic for edge 2 (phantom), got: %s", result.Error())
	}
}

func TestValidateGraph_DiagnosticNodeIDForWhenWarning(t *testing.T) {
	// When a when value doesn't match, the diagnostic's NodeID should be the source node
	w := &Workflow{
		Nodes: []Node{
			{ID: "decider", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "target", Kind: "role"},
		},
		Edges: []Edge{
			{From: "decider", To: "target", When: "typo"},
		},
	}
	result := ValidateGraph(w)
	found := false
	for _, d := range result.Diagnostics {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "typo") {
			if d.NodeID != "decider" {
				t.Fatalf("expected NodeID 'decider' on when warning, got %q", d.NodeID)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected when warning diagnostic, got: %s", result.Error())
	}
}

// --- Goal gate validation tests ---

func TestValidateGraph_NodeRetryTargetNonexistent(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", GoalGate: true, RetryTarget: "ghost"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for retry_target referencing nonexistent node")
	}
	if !containsDiagnostic(result, SeverityError, "retry_target") {
		t.Fatalf("expected retry_target error, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "ghost") {
		t.Fatalf("expected error mentioning 'ghost', got: %s", result.Error())
	}
}

func TestValidateGraph_WorkflowRetryTargetNonexistent(t *testing.T) {
	w := &Workflow{
		RetryTarget: "ghost",
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for workflow retry_target referencing nonexistent node")
	}
	if !containsDiagnostic(result, SeverityError, "retry_target") {
		t.Fatalf("expected retry_target error, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "ghost") {
		t.Fatalf("expected error mentioning 'ghost', got: %s", result.Error())
	}
}

func TestValidateGraph_GoalGateNoRetryTarget_Warning(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", GoalGate: true},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("goal_gate with no retry_target should be warning not error, got: %s", result.Error())
	}
	if !result.HasWarnings() {
		t.Fatal("expected warning for goal_gate with no retry_target")
	}
	if !containsDiagnostic(result, SeverityWarning, "goal_gate") {
		t.Fatalf("expected goal_gate warning, got: %s", result.Error())
	}
}

func TestValidateGraph_GoalGateWithNodeRetryTarget_NoWarning(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", GoalGate: true, RetryTarget: "a"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "goal_gate") {
		t.Fatalf("goal_gate with node retry_target should not warn, got: %s", result.Error())
	}
}

func TestValidateGraph_GoalGateWithWorkflowRetryTarget_NoWarning(t *testing.T) {
	w := &Workflow{
		RetryTarget: "a",
		Nodes: []Node{
			{ID: "a", Kind: "role", GoalGate: true},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "goal_gate") {
		t.Fatalf("goal_gate with workflow retry_target should not warn, got: %s", result.Error())
	}
}

func TestValidateGraph_NodeRetryTargetValid(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", GoalGate: true, RetryTarget: "b"},
			{ID: "b", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityError, "retry_target") {
		t.Fatalf("valid retry_target should not error, got: %s", result.Error())
	}
}

func TestValidateGraph_WorkflowRetryTargetValid(t *testing.T) {
	w := &Workflow{
		RetryTarget: "a",
		Nodes: []Node{
			{ID: "a", Kind: "role"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityError, "retry_target") {
		t.Fatalf("valid workflow retry_target should not error, got: %s", result.Error())
	}
}

func TestValidateGraph_RetryTargetOnNonGoalGateNode(t *testing.T) {
	// A node can have retry_target without goal_gate (future use or explicit routing)
	// It should still validate the target exists
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", RetryTarget: "ghost"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "ghost") {
		t.Fatalf("retry_target referencing nonexistent node should error even without goal_gate, got: %s", result.Error())
	}
}

// --- ContextDefault validation tests ---

func TestValidateGraph_ValidContextDefault(t *testing.T) {
	for _, ctx := range []string{"", "full", "fresh", "compact", "summary"} {
		w := &Workflow{
			Nodes:          []Node{{ID: "a", Kind: "role"}},
			Edges:          []Edge{},
			ContextDefault: ctx,
		}
		result := ValidateGraph(w)
		if result.HasErrors() {
			t.Fatalf("context_default %q should be valid, got: %s", ctx, result.Error())
		}
	}
}

func TestValidateGraph_InvalidContextDefault(t *testing.T) {
	w := &Workflow{
		Nodes:          []Node{{ID: "a", Kind: "role"}},
		Edges:          []Edge{},
		ContextDefault: "bogus",
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for invalid context_default")
	}
	if !containsDiagnostic(result, SeverityError, "context_default") {
		t.Fatalf("expected error mentioning 'context_default', got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "bogus") {
		t.Fatalf("expected error mentioning 'bogus', got: %s", result.Error())
	}
}

func TestValidateGraph_ValidPromptInputsMode(t *testing.T) {
	for _, mode := range []string{"", "all", "declared", "none"} {
		w := &Workflow{
			PromptInputsMode: mode,
			Nodes: []Node{
				{ID: "a", Kind: "role", PromptInputsMode: mode},
			},
			Edges: []Edge{},
		}
		result := ValidateGraph(w)
		if result.HasErrors() {
			t.Fatalf("prompt_inputs_mode %q should be valid, got: %s", mode, result.Error())
		}
	}
}

func TestValidateGraph_InvalidWorkflowPromptInputsMode(t *testing.T) {
	w := &Workflow{
		PromptInputsMode: "verbose",
		Nodes:            []Node{{ID: "a", Kind: "role"}},
		Edges:            []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for invalid workflow prompt_inputs_mode")
	}
	if !containsDiagnostic(result, SeverityError, "prompt_inputs_mode") {
		t.Fatalf("expected error mentioning prompt_inputs_mode, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "verbose") {
		t.Fatalf("expected error mentioning invalid value, got: %s", result.Error())
	}
}

func TestValidateGraph_InvalidNodePromptInputsMode(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", PromptInputsMode: "expanded"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for invalid node prompt_inputs_mode")
	}
	if !containsDiagnostic(result, SeverityError, "prompt_inputs_mode") {
		t.Fatalf("expected error mentioning prompt_inputs_mode, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "expanded") {
		t.Fatalf("expected error mentioning invalid value, got: %s", result.Error())
	}
}

// --- Timeout validation tests ---

// timeout_default field is removed (Task 32). The _timeout meta-decision fires
// automatically when timeout_sec elapses — no fallback decision needed.

func TestValidateGraph_TimeoutSecNegative_Error(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "human", TimeoutSec: -1},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for negative timeout_sec")
	}
	if !containsDiagnostic(result, SeverityError, "timeout_sec") {
		t.Fatalf("expected timeout_sec error, got: %s", result.Error())
	}
}

func TestValidateGraph_TimeoutSecZero_NoError(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "human", TimeoutSec: 0},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityError, "timeout_sec") {
		t.Fatalf("zero timeout_sec should not error, got: %s", result.Error())
	}
}

func TestValidateGraph_TimeoutSecPositive_NoError(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "human", TimeoutSec: 300},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityError, "timeout_sec") {
		t.Fatalf("positive timeout_sec should not error, got: %s", result.Error())
	}
}

// --- Runner override validation tests ---

func TestValidateBundle_RunnerOverrideReferencesNonexistentRunner(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"real-runner": {ID: "real-runner", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"gpu"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"gpu": "ghost-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for runner_overrides referencing nonexistent runner")
	}
	if !containsDiagnostic(result, SeverityError, "ghost-runner") {
		t.Fatalf("expected error mentioning 'ghost-runner', got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideReferencesValidRunner(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"gpu-runner": {ID: "gpu-runner", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"gpu"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "gpu-runner") {
		t.Fatalf("valid runner should not produce error, got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideTagMatchesNoNodes(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"gpu-runner": {ID: "gpu-runner", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"cpu"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityWarning, "gpu") {
		t.Fatalf("expected warning for tag 'gpu' matching no nodes, got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideTagMatchesNode(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"gpu-runner": {ID: "gpu-runner", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"gpu"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityWarning, "gpu") {
		t.Fatalf("tag matching a node should not warn, got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideNilRunners(t *testing.T) {
	// If bundle has nil Runners map, all runner references should error
	b := &Bundle{
		Runners: nil,
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"gpu"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"gpu": "gpu-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityError, "gpu-runner") {
		t.Fatalf("expected error for runner ref with nil runners map, got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideNoOverrides(t *testing.T) {
	// No runner_overrides in any workflow — no errors or warnings
	b := &Bundle{
		Runners: map[string]*Runner{},
		Workflows: map[string]*Workflow{
			"main": {
				ID:    "main",
				Nodes: []Node{{ID: "a", Kind: "role"}},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "runner_overrides") {
		t.Fatalf("no overrides should produce no errors, got: %s", result.Error())
	}
}

func TestValidateBundle_RunnerOverrideMultipleWorkflows(t *testing.T) {
	// Runner override errors across multiple workflows
	b := &Bundle{
		Runners: map[string]*Runner{"real": {ID: "real", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"wf1": {
				ID: "wf1",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"t1"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"t1": "ghost1"},
			},
			"wf2": {
				ID: "wf2",
				Nodes: []Node{
					{ID: "b", Kind: "role", Tags: []string{"t2"}},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"t2": "ghost2"},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityError, "ghost1") {
		t.Fatalf("expected error for ghost1, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "ghost2") {
		t.Fatalf("expected error for ghost2, got: %s", result.Error())
	}
}

// --- max_turns validation tests ---

func TestValidateBundle_MaxTurnsOnShellRunnerWarns(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{
			"shell-runner": {ID: "shell-runner", Type: "shell"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "shell-runner", MaxTurns: 10},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("expected warning for max_turns on shell runner, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "shell") {
		t.Fatalf("expected warning mentioning 'shell', got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsOnHumanRunnerWarns(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{
			"human-runner": {ID: "human-runner", Type: "human"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "human-runner", MaxTurns: 5},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("expected warning for max_turns on human runner, got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "human") {
		t.Fatalf("expected warning mentioning 'human', got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsOnClaudeRunnerNoWarning(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{
			"claude-runner": {ID: "claude-runner", Type: "claude"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "claude-runner", MaxTurns: 10},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("max_turns on claude runner should not warn, got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsOnCodexRunnerNoWarning(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{
			"codex-runner": {ID: "codex-runner", Type: "codex"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "codex-runner", MaxTurns: 10},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("max_turns on codex runner should not warn, got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsZeroNoWarning(t *testing.T) {
	// max_turns=0 (default/unset) should not trigger warning even on shell runner
	b := &Bundle{
		Runners: map[string]*Runner{
			"shell-runner": {ID: "shell-runner", Type: "shell"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "shell-runner", MaxTurns: 0},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("max_turns=0 should not warn, got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsViaNodeRunner(t *testing.T) {
	// Runner resolved directly from node.Runner
	b := &Bundle{
		Runners: map[string]*Runner{
			"shell-runner": {ID: "shell-runner", Type: "shell"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "shell-runner", MaxTurns: 3},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("expected warning for max_turns via node.Runner, got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsViaRunnerOverride(t *testing.T) {
	// Runner resolved via runner_overrides (tag match)
	b := &Bundle{
		Runners: map[string]*Runner{
			"claude-runner": {ID: "claude-runner", Type: "claude"},
			"shell-runner":  {ID: "shell-runner", Type: "shell"},
		},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Tags: []string{"override-me"}, MaxTurns: 5},
				},
				Edges:           []Edge{},
				RunnerOverrides: map[string]string{"override-me": "shell-runner"},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("expected warning for max_turns with shell runner override, got: %s", result.Error())
	}
}

func TestValidateBundle_MaxTurnsNoRunner(t *testing.T) {
	// Node has max_turns but no runner can be resolved — skip (no warning)
	b := &Bundle{
		Runners: map[string]*Runner{},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", MaxTurns: 5},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityWarning, "max_turns") {
		t.Fatalf("max_turns with no resolvable runner should not warn, got: %s", result.Error())
	}
}

// --- _loop_exhausted meta-decision edge validation ---
// (The legacy loop_exhausted_to field is removed. Exhaustion routing is now
// declared via an explicit Edge{When: "_loop_exhausted"}. No dedicated
// validator is needed; the existing unknown-node edge checks cover bad targets.)

// --- Expression role reference tests ---

// --- Join Validation tests ---

func TestValidateGraph_JoinConditionalWhenEdge_Error(t *testing.T) {
	// Rule 1: Incoming edge with specific when clause into join → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j", When: "yes"},
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "conditional") {
		t.Fatalf("expected error for conditional edge into join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinExpressionEdge_Error(t *testing.T) {
	// Rule 1: Expression edge into join → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j", When: "status == 'done'"},
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "conditional") {
		t.Fatalf("expected error for expression edge into join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinDefaultEdgeWithCompetingEdge_Error(t *testing.T) {
	// Rule 1 caveat: default edge to join, but predecessor has other specific edges
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Decisions: StringDecisions("yes", "no")},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},              // default/empty edge to join
			{From: "a", To: "b", When: "yes"}, // competing specific edge from same predecessor
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "conditional") {
		t.Fatalf("expected error for default edge into join with competing edges, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinForEachDirectEdge_Error(t *testing.T) {
	// Rule 4: ForEach node has direct edge to join → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "fe", Kind: "role", ForEach: &ForEach{List: "items", Item: "item"}},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "fe", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "foreach") {
		t.Fatalf("expected error for foreach edge to join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinSelfLoop_Error(t *testing.T) {
	// Rule 5: Self-loop on join node → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "b", To: "j"},
			{From: "j", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "self-loop") {
		t.Fatalf("expected error for self-loop on join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinSamePredecessorMultiEdge_Error(t *testing.T) {
	// Rule 6: Same predecessor has multiple edges to join → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "a", To: "j"},
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "multiple edges") {
		t.Fatalf("expected error for same predecessor multi-edge, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinZeroIncoming_Error(t *testing.T) {
	// Rule 7: Zero incoming edges on join node → error
	w := &Workflow{
		Nodes: []Node{
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "zero incoming") {
		t.Fatalf("expected error for zero incoming edges on join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinOneIncoming_Warning(t *testing.T) {
	// Rule 8: One incoming edge on join → warning
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, "one incoming") {
		t.Fatalf("expected warning for one incoming edge on join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinGoalGate_Warning(t *testing.T) {
	// Rule 9: goal_gate on join node → warning
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all", GoalGate: true},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityWarning, "goal_gate") {
		t.Fatalf("expected warning for goal_gate on join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinValid3Predecessors(t *testing.T) {
	// Valid: 3-predecessor join with unconditional edges
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "c", Kind: "role"},
			{ID: "j", Kind: "role", Join: "all"},
			{ID: "d", Kind: "role"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "b", To: "j"},
			{From: "c", To: "j"},
			{From: "j", To: "d"},
		},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("expected no errors for valid 3-predecessor join, got: %s", result.Error())
	}
}

func TestValidateGraph_JoinInvalidValue_Error(t *testing.T) {
	// join: "first" is not a valid join value
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role"},
			{ID: "b", Kind: "role"},
			{ID: "j", Kind: "role", Join: "first"},
		},
		Edges: []Edge{
			{From: "a", To: "j"},
			{From: "b", To: "j"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "invalid join value") {
		t.Fatalf("expected error for invalid join value, got: %s", result.Error())
	}
}

func TestValidateGraph_CheckReachability_LoopExhaustedMetaEdge(t *testing.T) {
	// Nodes reachable only via a _loop_exhausted meta-decision edge should not
	// be flagged as unreachable. The legacy loop_exhausted_to field is removed;
	// exhaustion routing is declared via an explicit Edge{When: "_loop_exhausted"}.
	w := &Workflow{
		Nodes: []Node{
			{ID: "start", Kind: "role"},
			{ID: "looper", Kind: "role"},
			{ID: "handler", Kind: "role"},
		},
		Edges: []Edge{
			{From: "start", To: "looper"},
			{From: "looper", To: "start"},
			{From: "looper", To: "handler", When: "_loop_exhausted"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "unreachable") {
		t.Fatalf("handler should be reachable via _loop_exhausted edge, got: %s", result.Error())
	}
}

func TestValidateGraph_ExistingWorkflowsNoSpuriousJoinErrors(t *testing.T) {
	// Regression: all existing workflow YAMLs should pass validation with no join errors
	workflows, err := LoadWorkflowsDirSnapshot("../../definitions/workflows")
	if err != nil {
		t.Fatalf("failed to load workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("expected at least one workflow")
	}
	for id, wf := range workflows {
		result := ValidateGraph(wf)
		if result.HasErrors() {
			t.Errorf("workflow %q has validation errors: %s", id, result.Error())
		}
	}
}

func TestValidateGraph_ForEachDependsOnEmpty(t *testing.T) {
	w := &Workflow{
		ID:      "wf",
		Name:    "Test",
		Version: 1,
		Nodes: []Node{
			{ID: "process_item", Kind: "subworkflow", Workflow: "child"},
			{
				ID: "process",
				ForEach: &ForEach{
					List:      "input.items",
					Item:      "item",
					DependsOn: "",
					Body:      "process_item",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("empty DependsOn should be valid, got: %v", result.Error())
	}
}

func TestValidateGraph_ForEachDependsOnValid(t *testing.T) {
	w := &Workflow{
		ID:      "wf",
		Name:    "Test",
		Version: 1,
		Nodes: []Node{
			{ID: "process_item", Kind: "subworkflow", Workflow: "child"},
			{
				ID: "process",
				ForEach: &ForEach{
					List:      "input.items",
					Item:      "item",
					DependsOn: "depends_on",
					Body:      "process_item",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if result.HasErrors() {
		t.Fatalf("valid DependsOn should pass, got: %v", result.Error())
	}
}

// --- Helper ---

func containsDiagnostic(result *ValidationResult, severity Severity, substring string) bool {
	for _, d := range result.Diagnostics {
		if d.Severity == severity && strings.Contains(strings.ToLower(d.Message), strings.ToLower(substring)) {
			return true
		}
	}
	return false
}

// --- checkConvergentEdgePasses tests ---

func TestConvergentEdgePassesOverlap_DirectInspect(t *testing.T) {
	w := &Workflow{
		ID:      "tw",
		Name:    "tw",
		Version: 1,
		Nodes: []Node{
			{ID: "a", Decisions: DecisionList{{ID: "ok"}}},
			{ID: "b", Decisions: DecisionList{{ID: "ok"}}},
			{ID: "c"},
		},
		Edges: []Edge{
			{
				From: "a", To: "c", When: "ok",
				Passes: map[string]any{"shared": "from_a", "a_only": "val_a"},
			},
			{
				From: "b", To: "c", When: "ok",
				Passes: map[string]any{"shared": "from_b", "b_only": "val_b"},
			},
		},
	}
	result := ValidateGraph(w)

	// Must not produce errors or warnings — INFO only.
	if result.HasErrors() {
		t.Fatalf("expected no errors, got: %s", result.Error())
	}
	if result.HasWarnings() {
		t.Fatalf("expected no warnings, got: %s", result.Error())
	}
	if !result.HasInfo() {
		t.Fatal("expected HasInfo() == true for overlapping convergent passes key")
	}
	if !containsDiagnostic(result, SeverityInfo, `"shared"`) {
		t.Fatalf("expected INFO entry about overlapping convergent passes key 'shared'; got: %#v", result.Diagnostics)
	}
	if !containsDiagnostic(result, SeverityInfo, `"c"`) {
		t.Fatalf("expected INFO entry mentioning destination node 'c'; got: %#v", result.Diagnostics)
	}
}

func TestConvergentEdgePassesNoOverlap_Silent(t *testing.T) {
	w := &Workflow{
		ID:      "tw",
		Name:    "tw",
		Version: 1,
		Nodes: []Node{
			{ID: "a", Decisions: DecisionList{{ID: "ok"}}},
			{ID: "b", Decisions: DecisionList{{ID: "ok"}}},
			{ID: "c"},
		},
		Edges: []Edge{
			{
				From: "a", To: "c", When: "ok",
				Passes: map[string]any{"a_only": "from_a"},
			},
			{
				From: "b", To: "c", When: "ok",
				Passes: map[string]any{"b_only": "from_b"},
			},
		},
	}
	result := ValidateGraph(w)

	if result.HasInfo() {
		t.Fatalf("expected no INFO diagnostics for non-overlapping passes, got: %s", result.Error())
	}
	if result.HasErrors() {
		t.Fatalf("unexpected errors: %s", result.Error())
	}
}

func TestConvergentEdgePassesOverlap_LoadFile(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    decisions: [ok]
  - id: c
    kind: role
    runner: shell
edges:
  - from: a
    to: c
    when: ok
    passes:
      shared: "static_a"
      a_only: "from_a"
  - from: b
    to: c
    when: ok
    passes:
      shared: "static_b"
      b_only: "from_b"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// LoadWorkflowFile should succeed — INFO does not elevate to error.
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile should not error on Info-level message: %v", err)
	}
}

func TestConvergentEdgePassesNoOverlap_LoadFile(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    decisions: [ok]
  - id: c
    kind: role
    runner: shell
edges:
  - from: a
    to: c
    when: ok
    passes:
      a_only: "from_a"
  - from: b
    to: c
    when: ok
    passes:
      b_only: "from_b"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

// --- Node runner field validation tests ---

func TestValidateBundle_NodeRunnerReferencesNonexistentRunner(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"serf": {ID: "serf", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "serf-agent"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if !result.HasErrors() {
		t.Fatal("expected error for node.runner referencing nonexistent runner")
	}
	if !containsDiagnostic(result, SeverityError, "serf-agent") {
		t.Fatalf("expected error mentioning 'serf-agent', got: %s", result.Error())
	}
	if !containsDiagnostic(result, SeverityError, "serf") {
		t.Fatalf("expected error to include valid runner suggestion, got: %s", result.Error())
	}
}

func TestValidateBundle_NodeRunnerReferencesValidRunner(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"serf": {ID: "serf", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role", Runner: "serf"},
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "runner") {
		t.Fatalf("valid runner should not produce error, got: %s", result.Error())
	}
}

func TestValidateBundle_NodeRunnerEmptyIsFine(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"serf": {ID: "serf", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "role"}, // no explicit runner — uses default
				},
				Edges: []Edge{},
			},
		},
	}
	result := ValidateBundle(b)
	// Nodes without explicit runner should not trigger this check
	if containsDiagnostic(result, SeverityError, "unknown runner") {
		t.Fatalf("empty runner should not produce error, got: %s", result.Error())
	}
}

func TestValidateBundle_SubworkflowNodesSkippedForRunnerCheck(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"serf": {ID: "serf", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"main": {
				ID: "main",
				Nodes: []Node{
					{ID: "a", Kind: "subworkflow", Workflow: "other"},
				},
				Edges: []Edge{},
			},
			"other": {
				ID:    "other",
				Nodes: []Node{{ID: "x", Kind: "role", Runner: "serf"}},
			},
		},
	}
	result := ValidateBundle(b)
	if containsDiagnostic(result, SeverityError, "runner") {
		t.Fatalf("subworkflow nodes should not trigger runner check, got: %s", result.Error())
	}
}

func TestValidateBundle_NodeRunnerTypoInMultipleWorkflows(t *testing.T) {
	b := &Bundle{
		Runners: map[string]*Runner{"serf": {ID: "serf", Type: "cli"}},
		Workflows: map[string]*Workflow{
			"wf1": {
				ID:    "wf1",
				Nodes: []Node{{ID: "a", Kind: "role", Runner: "serf-agent"}},
			},
			"wf2": {
				ID:    "wf2",
				Nodes: []Node{{ID: "b", Kind: "role", Runner: "shel"}},
			},
		},
	}
	result := ValidateBundle(b)
	if !containsDiagnostic(result, SeverityError, "serf-agent") {
		t.Errorf("expected error mentioning 'serf-agent'")
	}
	if !containsDiagnostic(result, SeverityError, "shel") {
		t.Errorf("expected error mentioning 'shel'")
	}
}

func TestValidateGraph_ForEachBodyReferencesMissingNode(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orchestrator", ForEach: &ForEach{List: "input.x", Item: "i", Body: "missing"}},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error for missing body reference")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.NodeID == "orchestrator" && strings.Contains(d.Message, "missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic mentioning 'missing' on orchestrator, got %v", r.Diagnostics)
	}
}

func TestValidateGraph_ForEachBodyValid(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orchestrator", ForEach: &ForEach{List: "input.x", Item: "i", Body: "template"}},
			{ID: "template", Kind: "system"},
		},
	}
	r := ValidateGraph(w)
	for _, d := range r.Diagnostics {
		if d.NodeID == "orchestrator" && d.Severity == SeverityError {
			t.Fatalf("unexpected error on orchestrator: %s", d.Message)
		}
	}
}

func TestValidateGraph_TemplateEdgeMustRouteToOrchestrator(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "template"}},
			{ID: "template", Kind: "system"},
			{ID: "other", Kind: "system"},
		},
		Edges: []Edge{
			{From: "template", To: "other", When: "status == 'failed'"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error: template edge routes to non-orchestrator node")
	}
}

func TestValidateGraph_TemplateEdgeToOrchestratorIsValid(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{
				ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "template"},
				Decisions: DecisionList{{ID: "all_succeeded"}, {ID: "some_failed"}, {ID: "all_failed"}},
			},
			{ID: "template", Kind: "system"},
		},
		Edges: []Edge{
			{From: "template", To: "orch", When: "status == 'failed'"},
		},
	}
	r := ValidateGraph(w)
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			t.Fatalf("unexpected error: %s", d.Message)
		}
	}
}

func TestValidateGraph_InlineFormRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{
				ID:       "legacy",
				Kind:     "subworkflow",
				Workflow: "other",
				ForEach:  &ForEach{List: "input.x", Item: "i", Body: "template"},
			},
			{ID: "template", Kind: "system"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected inline ForEach to be rejected")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.NodeID == "legacy" && strings.Contains(d.Message, "move those fields to the template node") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic about inline form (kind/runner/workflow) on 'legacy', got %v", r.Diagnostics)
	}
}

func TestValidateGraph_ForEachMissingBodyRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i"}}, // no Body
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected missing body to be rejected")
	}
}

func TestValidateGraph_NestedForEachRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "outer", ForEach: &ForEach{List: "input.x", Item: "i", Body: "inner_orch"}},
			{ID: "inner_orch", ForEach: &ForEach{List: "input.y", Item: "j", Body: "inner_tmpl"}},
			{ID: "inner_tmpl", Kind: "system"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error for nested ForEach")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.NodeID == "outer" && strings.Contains(d.Message, "nested ForEach") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic about nested ForEach on 'outer', got %v", r.Diagnostics)
	}
}

func TestValidateGraph_SelfReferenceBodyRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "self", ForEach: &ForEach{List: "input.x", Item: "i", Body: "self"}},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error for self-reference body")
	}
}

func TestValidateGraph_TemplateSharedBetweenOrchestratorsRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch1", ForEach: &ForEach{List: "input.x", Item: "i", Body: "shared"}},
			{ID: "orch2", ForEach: &ForEach{List: "input.y", Item: "j", Body: "shared"}},
			{ID: "shared", Kind: "system"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error: two orchestrators share template 'shared'")
	}
	found := false
	for _, d := range r.Diagnostics {
		if strings.Contains(d.Message, "shared") && strings.Contains(d.Message, "multiple") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic mentioning shared template, got %v", r.Diagnostics)
	}
}

func TestValidateGraph_TemplateWithIncomingEdgesRejected(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "template"}},
			{ID: "template", Kind: "system"},
			{ID: "other", Kind: "system"},
		},
		Edges: []Edge{
			{From: "other", To: "template"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error: template has incoming edge")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.NodeID == "template" && strings.Contains(d.Message, "incoming edge") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic about template incoming edge, got %v", r.Diagnostics)
	}
}

func TestValidateGraph_DecisionsMustIncludeFailureWhenFailureEdgeExists(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{
				ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "tmpl"},
				Decisions: DecisionList{{ID: "all_succeeded"}},
			},
			{ID: "tmpl", Kind: "system"},
		},
		Edges: []Edge{
			{From: "tmpl", To: "orch", When: "status == 'failed'"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error: orchestrator decisions must include some_failed/all_failed")
	}
}

func TestValidateGraph_DecisionsMustExcludeFailureWhenNoFailureEdge(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{
				ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "tmpl"},
				Decisions: DecisionList{{ID: "all_succeeded"}, {ID: "some_failed"}},
			},
			{ID: "tmpl", Kind: "system"},
		},
	}
	r := ValidateGraph(w)
	if !r.HasErrors() {
		t.Fatalf("expected error: some_failed unreachable without failure edge")
	}
}

func TestValidateGraph_SharedTemplateSuppressesEdgeTargetDiag(t *testing.T) {
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch1", ForEach: &ForEach{List: "input.x", Item: "i", Body: "shared"}},
			{ID: "orch2", ForEach: &ForEach{List: "input.y", Item: "j", Body: "shared"}},
			{ID: "shared", Kind: "system"},
		},
		Edges: []Edge{
			{From: "shared", To: "orch2", When: "status == 'failed'"},
		},
	}
	r := ValidateGraph(w)
	// The shared-template error should fire
	hasSharedErr := false
	hasEdgeTargetErr := false
	for _, d := range r.Diagnostics {
		if strings.Contains(d.Message, "multiple orchestrators") {
			hasSharedErr = true
		}
		if strings.Contains(d.Message, "must route to its orchestrator") {
			hasEdgeTargetErr = true
		}
	}
	if !hasSharedErr {
		t.Fatalf("expected shared-template error")
	}
	if hasEdgeTargetErr {
		t.Fatalf("did not expect edge-target error when template is already flagged as shared")
	}
}

func TestEdgeMatchesFailedStatus(t *testing.T) {
	cases := []struct {
		when string
		want bool
	}{
		{"", false},
		{"default", false},
		{"approved", false},
		{"status == 'failed'", true},
		{"status=='failed'", true},
		{"   status   ==   'failed'   ", true},
		{"status == 'failed' && decision == 'x'", true},
		{"decision == 'x' || status == 'failed'", true},
		{"status != 'failed'", false}, // regression: was false-positive
		{"status!='failed'", false},
		{"status != 'failed' && x == 1", false},
		{"decision == 'failed'", false}, // decision channel, not status
		// Double-quoted form must be recognized equivalently to single-quoted.
		{`status == "failed"`, true},
		{`status != "failed"`, false},
		// node.X.status == 'failed' is a property of a different node, not
		// the current edge's bare status — must not match.
		{"node.preflight.status == 'failed'", false},
		{`node.preflight.status == "failed"`, false},
		// Mixed: bare status AND a qualified status in the same expression.
		{"status == 'failed' || node.X.status == 'failed'", true},
		// Regression: the inverse-check rejection must also use the
		// prefix-guard. `node.X.status != 'failed'` includes the substring
		// `status!='failed'` after compaction; without the guard it would
		// short-circuit and falsely return false even when the edge has a
		// genuine `status == 'failed'` subterm.
		{"status == 'failed' && node.X.status != 'failed'", true},
		{"node.X.status != 'failed' && status == 'failed'", true},
		{`status == 'failed' && node.X.status != "failed"`, true},
		{"node.X.status != 'failed'", false},
		// Identifier-suffix matches must NOT count as bare `status`.
		// Before the round-8 fix, only '.' was rejected as a prefix —
		// any letter/digit/underscore would slip through.
		{"mystatus == 'failed'", false},
		{"complete_status == 'failed'", false},
		{"prevstatus == 'failed'", false},
		{"x9status == 'failed'", false},
		{"_status == 'failed'", false},
		// And the inverse check must use the same guard.
		{"complete_status != 'failed'", false},
		{"complete_status != 'failed' && status == 'failed'", true},
		// Compound with both positive AND inverse: positive wins.
		// Pre-fix the inverse check ran first and short-circuited to false.
		{"status == 'failed' && status != 'failed'", true},
		{"status != 'failed' || status == 'failed'", true},
		// `!status` is a parse error at runtime; treat as not-a-failure-edge.
		{"!status == 'failed'", false},
	}
	for _, c := range cases {
		if got := edgeMatchesFailedStatus(c.when); got != c.want {
			t.Errorf("edgeMatchesFailedStatus(%q) = %v, want %v", c.when, got, c.want)
		}
	}
}

func TestValidateGraph_NodeIDWithDoubleColonRejected(t *testing.T) {
	// Reserved substring: ForEach expansion uses "nodeID::N" naming, so a
	// node whose literal ID contains "::" would collide with expansion
	// lookups in the visualizer and state layer.
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "normal_node", Kind: "system"},
			{ID: "bad::id", Kind: "system"},
		},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, `reserved substring "::"`) {
		t.Fatalf("expected error for node id containing \"::\", got: %v", result)
	}
	// The "::" error must mention only "bad::id", not "normal_node".
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "reserved substring") && d.NodeID != "bad::id" {
			t.Fatalf("\"::\" error wrongly attributed to %q: %s", d.NodeID, d.Message)
		}
	}
}

func TestValidateGraph_NodeIDForEachPairAccepted(t *testing.T) {
	// Counter-example: a legitimate orchestrator/template pair (neither ID
	// contains "::") must NOT trigger the reserved-substring check.
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{ID: "orch", ForEach: &ForEach{List: "input.x", Item: "i", Body: "tmpl"}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	result := ValidateGraph(w)
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "reserved substring") {
			t.Fatalf("ForEach pair wrongly flagged for \"::\": %s", d.Message)
		}
	}
}

func TestValidateGraph_StatusNotFailedEdgeDoesNotRequireFailureDecisions(t *testing.T) {
	// Regression: a template edge with `status != 'failed'` is a
	// "success-only" routing filter, not a failure edge. The validator
	// previously classified it as a failure edge via the naive
	// strings.Contains heuristic and demanded some_failed/all_failed
	// decisions. That's wrong — the engine treats this edge as non-failure
	// at runtime.
	w := &Workflow{
		ID: "wf",
		Nodes: []Node{
			{
				ID:        "orch",
				ForEach:   &ForEach{List: "input.x", Item: "i", Body: "tmpl"},
				Decisions: DecisionList{{ID: "all_succeeded"}},
			},
			{ID: "tmpl", Kind: "system"},
		},
		Edges: []Edge{
			{From: "tmpl", To: "orch", When: "status != 'failed'"},
		},
	}
	r := ValidateGraph(w)
	for _, d := range r.Diagnostics {
		if d.NodeID == "orch" && d.Severity == SeverityError {
			t.Fatalf("unexpected error on orch: %s", d.Message)
		}
	}
}

// --- outputs_schema validation tests ---

func TestValidateOutputsSchema_Valid(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:     "producer",
				Kind:   "role",
				Runner: "serf",
				OutputsSchema: map[string]any{
					"type":     "object",
					"required": []any{"plan"},
					"properties": map[string]any{
						"plan": map[string]any{"type": "string"},
					},
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	for _, d := range result.Diagnostics {
		if d.NodeID == "producer" && d.Severity == SeverityError {
			t.Fatalf("did not expect error for valid outputs_schema, got: %s", d.Message)
		}
	}
}

func TestValidateOutputsSchema_Invalid(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:     "bad",
				Kind:   "role",
				Runner: "serf",
				OutputsSchema: map[string]any{
					"type": "notathing",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "outputs_schema") {
		t.Fatalf("expected error for invalid outputs_schema, got: %s", result.Error())
	}
}

func TestValidateOutputsSchema_OnShellNodeErrors(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:     "shell_node",
				Kind:   "role",
				Runner: "shell",
				OutputsSchema: map[string]any{
					"type": "object",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "outputs_schema is not supported on shell nodes") {
		t.Fatalf("expected error for outputs_schema on shell node, got: %s", result.Error())
	}
}

func TestValidateOutputsSchema_OnSubworkflowErrors(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:       "sub_node",
				Kind:     "subworkflow",
				Workflow: "child_wf",
				OutputsSchema: map[string]any{
					"type": "object",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "outputs_schema is not supported on subworkflow nodes") {
		t.Fatalf("expected error for outputs_schema on subworkflow node, got: %s", result.Error())
	}
}

func TestValidateOutputsSchema_OnHumanNodeErrors(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:     "human_node",
				Kind:   "human",
				Runner: "human-runner",
				OutputsSchema: map[string]any{
					"type": "object",
				},
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !containsDiagnostic(result, SeverityError, "outputs_schema is not supported on human nodes") {
		t.Fatalf("expected error for outputs_schema on human node, got: %s", result.Error())
	}
}

func TestValidateOutputsSchema_NilOnNonShellIsFine(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{
				ID:     "agent",
				Kind:   "role",
				Runner: "serf",
				// No OutputsSchema.
			},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	for _, d := range result.Diagnostics {
		if d.NodeID == "agent" && d.Severity == SeverityError && strings.Contains(d.Message, "outputs_schema") {
			t.Fatalf("did not expect outputs_schema error when field is nil, got: %s", d.Message)
		}
	}
}

// --- Meta-decision validation tests (Tasks 26, 27, 28) ---

func TestCheckEdgeWhenValues_AcceptsMetaDecisions(t *testing.T) {
	// A workflow with a self-loop and a _loop_exhausted outgoing edge
	// must load cleanly.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: stuck
    kind: role
    runner: shell
edges:
  - from: a
    to: a
    when: ok
  - from: a
    to: stuck
    when: _loop_exhausted
    failed: true
limits:
  max_loop_iterations: 3
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

func TestCheckEdgeWhenValues_RejectsUnknownMetaDecision(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: _bogus
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error for unknown _bogus meta-decision")
	}
}

func TestLoopExhaustedRequiresLoopableSCC(t *testing.T) {
	// a is NOT in any loop, but has _loop_exhausted edge — unreachable.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: stuck
    kind: role
    runner: shell
edges:
  - from: a
    to: stuck
    when: _loop_exhausted
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: a is not in any loop")
	}
}

func TestTimeoutMetaDecisionRequiresApprovalGate(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: _timeout
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: a has no timeout_sec / gate:required")
	}
}

func TestTimeoutInverseRule(t *testing.T) {
	// Approval gate with timeout_sec, no _timeout edge — error.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: human
    gate: required
    timeout_sec: 3600
    decisions: [approved]
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: approved
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: a has timeout_sec+gate:required but no _timeout edge")
	}
}

// --- Convergent edge prompt tests (Task 29) ---

func TestConvergentDifferingPromptsRejected(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    decisions: [ok]
  - id: c
    kind: role
    runner: shell
edges:
  - from: a
    to: c
    when: ok
    prompt: "from a"
  - from: b
    to: c
    when: ok
    prompt: "from b"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: convergent edges to c with differing prompts")
	}
}

func TestConvergentIdenticalPromptsAccepted(t *testing.T) {
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
    decisions: [ok]
  - id: c
    kind: role
    runner: shell
edges:
  - from: a
    to: c
    when: ok
    prompt: "shared"
  - from: b
    to: c
    when: ok
    prompt: "shared"
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("LoadWorkflowFile: %v", err)
	}
}

// --- Loop exhaustion coverage lint tests ---

func TestLoopExhaustionCoverage_FiresOnUnprotectedLoop(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "looper", Kind: "role", Runner: "shell", Decisions: StringDecisions("retry", "done")},
			{ID: "terminal", Kind: "role", Runner: "shell"},
		},
		Edges: []Edge{
			{From: "looper", To: "looper", When: "retry"},
			{From: "looper", To: "terminal", When: "done"},
		},
		Limits: map[string]int{"max_loop_iterations": 3},
	}
	result := ValidateGraph(w)
	if !result.HasWarnings() {
		t.Fatal("expected warning for looping node without _loop_exhausted edge")
	}
	if !containsDiagnostic(result, SeverityWarning, "looper") {
		t.Fatalf("warning should mention looper, got: %v", result.Error())
	}
	if !containsDiagnostic(result, SeverityWarning, "_loop_exhausted") {
		t.Fatalf("warning should mention _loop_exhausted, got: %v", result.Error())
	}
}

func TestLoopExhaustionCoverage_SuppressedByFatalOptOut(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "looper", Kind: "role", Runner: "shell", LoopExhaustionPolicy: "fatal", Decisions: StringDecisions("retry", "done")},
			{ID: "terminal", Kind: "role", Runner: "shell"},
		},
		Edges: []Edge{
			{From: "looper", To: "looper", When: "retry"},
			{From: "looper", To: "terminal", When: "done"},
		},
		Limits: map[string]int{"max_loop_iterations": 3},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "_loop_exhausted") {
		t.Fatalf("no warning expected when loop_exhaustion: fatal is set, got: %v", result.Error())
	}
	if result.HasErrors() {
		t.Fatalf("no errors expected, got: %v", result.Error())
	}
}

func TestLoopExhaustionCoverage_SuppressedByGracefulEdge(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "looper", Kind: "role", Runner: "shell", Decisions: StringDecisions("retry", "done")},
			{ID: "terminal", Kind: "role", Runner: "shell"},
			{ID: "stuck_handler", Kind: "role", Runner: "shell"},
		},
		Edges: []Edge{
			{From: "looper", To: "looper", When: "retry"},
			{From: "looper", To: "terminal", When: "done"},
			{From: "looper", To: "stuck_handler", When: "_loop_exhausted"},
		},
		Limits: map[string]int{"max_loop_iterations": 3},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "_loop_exhausted") {
		t.Fatalf("no warning expected when graceful exhaustion edge exists, got: %v", result.Error())
	}
}

func TestLoopExhaustionCoverage_NoLimitNoWarning(t *testing.T) {
	// Without max_loop_iterations the lint is silent even if a node loops.
	w := &Workflow{
		Nodes: []Node{
			{ID: "looper", Kind: "role", Runner: "shell", Decisions: StringDecisions("retry", "done")},
			{ID: "terminal", Kind: "role", Runner: "shell"},
		},
		Edges: []Edge{
			{From: "looper", To: "looper", When: "retry"},
			{From: "looper", To: "terminal", When: "done"},
		},
	}
	result := ValidateGraph(w)
	if containsDiagnostic(result, SeverityWarning, "_loop_exhausted") {
		t.Fatalf("no warning expected without max_loop_iterations, got: %v", result.Error())
	}
}

func TestLoopExhaustionPolicyValidation(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{
			{ID: "a", Kind: "role", Runner: "shell", LoopExhaustionPolicy: "bogus", Decisions: StringDecisions("ok")},
		},
		Edges: []Edge{},
	}
	result := ValidateGraph(w)
	if !result.HasErrors() {
		t.Fatal("expected error for invalid loop_exhaustion value 'bogus'")
	}
	if !containsDiagnostic(result, SeverityError, "bogus") {
		t.Fatalf("error should mention 'bogus', got: %v", result.Error())
	}
}

// --- _retry_exhausted meta-decision validation tests ---

func TestRetryExhaustedRequiresRetryDeclaration(t *testing.T) {
	// _retry_exhausted edge on a node with no retry: declaration → error.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: _retry_exhausted
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: a has no retry: declaration")
	}
}

func TestRetryExhaustedRequiresMaxGreaterThan1(t *testing.T) {
	// _retry_exhausted edge on a node with retry: { max: 1 } (only 1 retry, same as no retry) → error.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
    retry:
      max: 1
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: _retry_exhausted
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err == nil {
		t.Fatalf("expected error: retry.max=1 is not > 1")
	}
}

func TestRetryExhaustedAcceptsRetryDeclaration(t *testing.T) {
	// _retry_exhausted edge on a node with retry: { max: 3 } → loads cleanly.
	yamlSrc := []byte(`
id: tw
name: tw
version: 1
nodes:
  - id: a
    kind: role
    runner: shell
    decisions: [ok]
    retry:
      max: 3
      backoff: fixed
      initial_delay: 1s
      max_delay: 10s
  - id: b
    kind: role
    runner: shell
edges:
  - from: a
    to: b
    when: _retry_exhausted
    failed: true
`)
	tmp := t.TempDir() + "/tw.yaml"
	if err := os.WriteFile(tmp, yamlSrc, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadWorkflowFile(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
