package engine

import (
	"fmt"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

// --- IsExpression detection ---

func TestIsExpression_PlainStrings(t *testing.T) {
	for _, s := range []string{testDecisionApproved, "changes_requested", testDecisionDone, testDecisionDefault, ""} {
		if IsExpression(s) {
			t.Fatalf("expected %q to NOT be an expression", s)
		}
	}
}

func TestIsExpression_Expressions(t *testing.T) {
	for _, s := range []string{
		"status == 'failed'",
		"decision != 'approved'",
		"decision == 'yes' && status == 'completed'",
		"decision == 'a' || decision == 'b'",
		"node.review.data.score >= 80",
		"node.review.data.score > 0",
		"node.review.data.score <= 100",
		"node.review.data.score < 50",
	} {
		if !IsExpression(s) {
			t.Fatalf("expected %q to be an expression", s)
		}
	}
}

// --- Expression evaluation ---

func TestEvalEdgeExpr_BasicEquality(t *testing.T) {
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'approved'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestEvalEdgeExpr_BasicInequality(t *testing.T) {
	ctx := &EvalContext{Decision: "rejected", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision != 'approved'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestEvalEdgeExpr_StatusFailed(t *testing.T) {
	ctx := &EvalContext{Decision: "", Status: statusFailed}
	ok, err := EvalEdgeExpr("status == 'failed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for failed status")
	}
}

func TestEvalEdgeExpr_StatusFailedNoMatch(t *testing.T) {
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	ok, err := EvalEdgeExpr("status == 'failed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false for completed status")
	}
}

func TestEvalEdgeExpr_CompoundAnd(t *testing.T) {
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'approved' && status == 'completed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}

	ok, err = EvalEdgeExpr("decision == 'approved' && status == 'failed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false when one side is false")
	}
}

func TestEvalEdgeExpr_CompoundOr(t *testing.T) {
	ctx := &EvalContext{Decision: "rejected", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'approved' || decision == 'rejected'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true when one side is true")
	}
}

func TestEvalEdgeExpr_Precedence_AndBeforeOr(t *testing.T) {
	// "a || b && c" should be "a || (b && c)"
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	// decision=='x' || decision=='y' && status=='failed'
	// = true || (false && false) = true
	ok, err := EvalEdgeExpr("decision == 'x' || decision == 'y' && status == 'failed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true due to && binding tighter than ||")
	}
}

func TestEvalEdgeExpr_PathResolution(t *testing.T) {
	resolver := func(path string) (any, error) {
		if path == "node.review.data.score" {
			return 85, nil
		}
		return nil, ErrNodeDataNotFound
	}
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted, Resolve: resolver}
	ok, err := EvalEdgeExpr("node.review.data.score >= 80", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for score 85 >= 80")
	}
}

func TestEvalEdgeExpr_NumericComparisons(t *testing.T) {
	resolver := func(path string) (any, error) {
		if path == "node.review.data.score" {
			return 50, nil
		}
		return nil, ErrNodeDataNotFound
	}
	ctx := &EvalContext{Decision: testDecisionDone, Status: statusCompleted, Resolve: resolver}

	cases := []struct {
		expr   string
		expect bool
	}{
		{"node.review.data.score > 49", true},
		{"node.review.data.score > 50", false},
		{"node.review.data.score >= 50", true},
		{"node.review.data.score < 51", true},
		{"node.review.data.score < 50", false},
		{"node.review.data.score <= 50", true},
	}
	for _, tc := range cases {
		ok, err := EvalEdgeExpr(tc.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: unexpected error: %v", tc.expr, err)
		}
		if ok != tc.expect {
			t.Fatalf("expr %q: expected %v, got %v", tc.expr, tc.expect, ok)
		}
	}
}

func TestEvalEdgeExpr_MalformedExpression(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	_, err := EvalEdgeExpr("== ==", ctx)
	if err == nil {
		t.Fatal("expected error for malformed expression")
	}
}

func TestEvalEdgeExpr_UnresolvablePath(t *testing.T) {
	resolver := func(path string) (any, error) {
		return nil, ErrNodeDataNotFound
	}
	ctx := &EvalContext{Decision: "x", Status: statusCompleted, Resolve: resolver}
	// Unresolvable paths should evaluate to false, not error
	ok, err := EvalEdgeExpr("node.missing.data.value == 'foo'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false for unresolvable path")
	}
}

func TestEvalEdgeExpr_StringWithSpaces(t *testing.T) {
	ctx := &EvalContext{Decision: "needs review", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'needs review'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

// --- matchEdgesExpr integration ---

func TestMatchEdgesExpr_PlainDecisionStillWorks(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionApproved},
			{From: "a", To: "c", When: "rejected"},
		},
	}
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 1 || edges[0].To != "b" {
		t.Fatalf("expected edge to b, got %v", edges)
	}
}

func TestMatchEdgesExpr_ExpressionEdge(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: "status == 'failed'"},
			{From: "a", To: "c", When: testDecisionApproved},
		},
	}
	ctx := &EvalContext{Decision: "", Status: statusFailed}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 1 || edges[0].To != "b" {
		t.Fatalf("expected edge to b for failed status, got %v", edges)
	}
}

func TestMatchEdgesExpr_DefaultFallback(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionApproved},
			{From: "a", To: "c", When: testDecisionDefault},
		},
	}
	ctx := &EvalContext{Decision: "something_else", Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 1 || edges[0].To != "c" {
		t.Fatalf("expected default edge to c, got %v", edges)
	}
}

func TestMatchEdgesExpr_DefaultDoesNotCatchFailure(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionDefault},
		},
	}
	// Failed nodes should NOT match default edges
	ctx := &EvalContext{Decision: "", Status: statusFailed}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 0 {
		t.Fatalf("default edge should not catch failures, got %v", edges)
	}
}

// ============================================================
// Additional edge-case tests for expressions
// ============================================================

// --- IsExpression edge cases ---

func TestIsExpression_ArrowLikeStrings(t *testing.T) {
	// "=>" should NOT be detected as an expression (it's not a valid operator)
	// but ">" inside it should be caught by containsSingleAngleBracket
	// Let's verify the actual behavior:
	cases := []struct {
		input  string
		expect bool
	}{
		{"=>", true},   // contains '>' that isn't preceded by '=' from our perspective... wait, let's check: i=1, ch='>', s[0]='=' so s[i-1]=='=' -> skip. But i+1 >= len(s) -> true for second condition. Actually: ch=='>' and s[i-1]=='=' so first condition (i==0 || s[i-1]!='=') is FALSE. So it won't match '>'. Let me re-check...
		{"a=>b", true}, // '>' at index 2, s[1]='=' so s[i-1]=='=' is true, so (i==0 || s[i-1]!='=') is false. Won't match. But what about other chars? No == or other operators. Actually wait - should be false.
	}
	// Actually let me just test the function directly and verify
	_ = cases
	// "=>" : position 1 is '>', s[0]='=' so s[i-1]=='=', so first check fails -> not detected
	if IsExpression("=>") {
		t.Fatal("'=>' should not be detected as expression")
	}
	// Single '>' not adjacent to '='
	if !IsExpression("a > b") {
		t.Fatal("'a > b' should be detected as expression")
	}
	// Single '<' not adjacent to '='
	if !IsExpression("a < b") {
		t.Fatal("'a < b' should be detected as expression")
	}
}

func TestIsExpression_SingleCharIdentifiers(t *testing.T) {
	// Single character strings that happen to look like partial operators
	for _, s := range []string{"a", "b", "x", ">"} {
		// ">" alone should be detected
		if s == ">" {
			if !IsExpression(s) {
				t.Fatalf("expected %q to be an expression", s)
			}
		}
	}
}

func TestIsExpression_OperatorSubstringsInContext(t *testing.T) {
	// These contain operator-like sequences but as part of larger strings
	if !IsExpression("foo==bar") {
		t.Fatal("'foo==bar' contains == and should be an expression")
	}
	if !IsExpression("x!=y") {
		t.Fatal("'x!=y' contains != and should be an expression")
	}
}

// --- Tokenizer edge cases ---

func TestTokenize_EmptyString(t *testing.T) {
	tokens, err := tokenize("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("expected no tokens for empty string, got %d", len(tokens))
	}
}

func TestTokenize_WhitespaceOnly(t *testing.T) {
	tokens, err := tokenize("   \t  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("expected no tokens for whitespace-only string, got %d", len(tokens))
	}
}

func TestTokenize_UnterminatedString(t *testing.T) {
	_, err := tokenize("'hello")
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
}

func TestTokenize_EmptyStringLiteral(t *testing.T) {
	tokens, err := tokenize("''")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokString || tokens[0].text != "" {
		t.Fatalf("expected empty string token, got %+v", tokens)
	}
}

func TestTokenize_FloatNumber(t *testing.T) {
	tokens, err := tokenize("3.14")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d: %+v", len(tokens), tokens)
	}
	if tokens[0].kind != tokNumber {
		t.Fatalf("expected number token for 3.14, got kind=%d text=%q", tokens[0].kind, tokens[0].text)
	}
}

func TestTokenize_IntegerNumber(t *testing.T) {
	tokens, err := tokenize("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokNumber || tokens[0].text != "42" {
		t.Fatalf("expected number token '42', got %+v", tokens)
	}
}

func TestTokenize_DottedPathIsIdent(t *testing.T) {
	tokens, err := tokenize("node.review.data.score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokIdent {
		t.Fatalf("expected ident token for dotted path, got %+v", tokens)
	}
}

func TestTokenize_AllOperators(t *testing.T) {
	input := "== != >= <= > < && ||"
	tokens, err := tokenize(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []tokenKind{tokEq, tokNeq, tokGte, tokLte, tokGt, tokLt, tokAnd, tokOr}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %+v", len(expected), len(tokens), tokens)
	}
	for i, exp := range expected {
		if tokens[i].kind != exp {
			t.Fatalf("token %d: expected kind %d, got %d (%q)", i, exp, tokens[i].kind, tokens[i].text)
		}
	}
}

func TestTokenize_UnexpectedCharacter(t *testing.T) {
	_, err := tokenize("@")
	if err == nil {
		t.Fatal("expected error for unexpected character @")
	}
	_, err = tokenize("!")
	if err == nil {
		t.Fatal("expected error for lone !")
	}
}

func TestTokenize_NegativeNumberNotSupported(t *testing.T) {
	// Negative numbers start with '-' which is not isIdentStart or isDigit
	_, err := tokenize("-5")
	if err == nil {
		t.Fatal("expected error for negative number literal (- is not a supported token)")
	}
}

func TestTokenize_StringWithOperatorChars(t *testing.T) {
	// String literals should preserve operator-like content
	tokens, err := tokenize("'a == b'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokString || tokens[0].text != "a == b" {
		t.Fatalf("expected string token containing operators, got %+v", tokens)
	}
}

func TestTokenize_SingleCharIdent(t *testing.T) {
	tokens, err := tokenize("x == 'y'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %+v", len(tokens), tokens)
	}
	if tokens[0].kind != tokIdent || tokens[0].text != "x" {
		t.Fatalf("expected ident 'x', got %+v", tokens[0])
	}
}

func TestTokenize_UnderscoreIdent(t *testing.T) {
	tokens, err := tokenize("_foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokIdent || tokens[0].text != "_foo" {
		t.Fatalf("expected ident '_foo', got %+v", tokens)
	}
}

func TestTokenize_NewlineNotWhitespace(t *testing.T) {
	// Only space and tab are treated as whitespace; newline should error
	_, err := tokenize("a\n== b")
	if err == nil {
		t.Fatal("expected error for newline character (not treated as whitespace)")
	}
}

func TestTokenize_ZeroNumber(t *testing.T) {
	tokens, err := tokenize("0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].kind != tokNumber || tokens[0].text != "0" {
		t.Fatalf("expected number token '0', got %+v", tokens)
	}
}

// --- EvalEdgeExpr edge cases ---

func TestEvalEdgeExpr_EmptyExpression(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	// Empty string tokenizes to no tokens, parseOr -> parseAnd -> parseCompare -> resolveOperand -> "expected operand, got end of expression"
	_, err := EvalEdgeExpr("", ctx)
	if err == nil {
		t.Fatal("expected error for empty expression")
	}
}

func TestEvalEdgeExpr_WhitespaceOnlyExpression(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	_, err := EvalEdgeExpr("   ", ctx)
	if err == nil {
		t.Fatal("expected error for whitespace-only expression")
	}
}

func TestEvalEdgeExpr_SingleIdentTruthyCheck(t *testing.T) {
	// A single identifier without comparison should be a truthy check
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected truthy for non-empty decision")
	}

	// Empty decision should be falsy
	ctx2 := &EvalContext{Decision: "", Status: statusCompleted}
	ok, err = EvalEdgeExpr("decision", ctx2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected falsy for empty decision")
	}
}

func TestEvalEdgeExpr_UnresolvableIdentIsFalsy(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	// "unknown_var" is not decision/status/message and no resolver -> nil -> falsy
	ok, err := EvalEdgeExpr("unknown_var", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected falsy for unresolvable identifier")
	}
}

func TestEvalEdgeExpr_NilResolverPathFallsToNil(t *testing.T) {
	// Resolve is nil, dotted path should resolve to nil
	ctx := &EvalContext{Decision: "x", Status: statusCompleted, Resolve: nil}
	ok, err := EvalEdgeExpr("node.foo.bar == 'baz'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false when resolver is nil")
	}
}

func TestEvalEdgeExpr_MessageVariable(t *testing.T) {
	ctx := &EvalContext{Decision: testDecisionDone, Status: statusCompleted, Message: "all good"}
	ok, err := EvalEdgeExpr("message == 'all good'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for message match")
	}
}

func TestEvalEdgeExpr_NumericStringComparison(t *testing.T) {
	// When resolver returns a string that looks like a number, toFloat should convert it
	resolver := func(path string) (any, error) {
		if path == "node.score.data.val" {
			return "75", nil // string, not int
		}
		return nil, ErrNodeDataNotFound
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.score.data.val >= 70", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true: string '75' should be comparable to number 70")
	}
}

func TestEvalEdgeExpr_FloatComparison(t *testing.T) {
	resolver := func(path string) (any, error) {
		if path == "node.a.data.ratio" {
			return 0.75, nil
		}
		return nil, ErrNodeDataNotFound
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.a.data.ratio > 0.5", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for 0.75 > 0.5")
	}
}

func TestEvalEdgeExpr_NilVsString(t *testing.T) {
	// Comparing nil (unresolvable) to a string: nil formats as "<nil>" via Sprintf
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	ok, err := EvalEdgeExpr("node.missing == ''", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// nil -> fmt.Sprint(nil) = "<nil>", not "" -> should be false
	if ok {
		t.Fatal("expected false: nil != empty string")
	}
}

func TestEvalEdgeExpr_NilNeqString(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	ok, err := EvalEdgeExpr("node.missing != 'something'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// nil -> "<nil>" != "something" -> true
	if !ok {
		t.Fatal("expected true: nil != 'something'")
	}
}

func TestEvalEdgeExpr_DeeplyNestedAndOr(t *testing.T) {
	// a && b && c || d && e
	// = ((a && b && c) || (d && e))
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'x' && status == 'completed' && message == '' || decision == 'y' && status == 'failed'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// (true && true && true) || (false && false) = true || false = true
	if !ok {
		t.Fatal("expected true")
	}
}

func TestEvalEdgeExpr_AllFalseOrChain(t *testing.T) {
	ctx := &EvalContext{Decision: "z", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision == 'a' || decision == 'b' || decision == 'c'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false when no branch matches")
	}
}

func TestEvalEdgeExpr_MultipleAndAllTrue(t *testing.T) {
	ctx := &EvalContext{Decision: testDecisionDone, Status: statusCompleted, Message: "ok"}
	ok, err := EvalEdgeExpr("decision == 'done' && status == 'completed' && message == 'ok'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true when all AND conditions are true")
	}
}

func TestEvalEdgeExpr_OperatorAtEndOfExpression(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	_, err := EvalEdgeExpr("decision ==", ctx)
	if err == nil {
		t.Fatal("expected error for operator at end of expression")
	}
}

func TestEvalEdgeExpr_DoubleOperator(t *testing.T) {
	ctx := &EvalContext{Decision: "x", Status: statusCompleted}
	_, err := EvalEdgeExpr("decision == == 'x'", ctx)
	if err == nil {
		t.Fatal("expected error for double operator")
	}
}

func TestEvalEdgeExpr_IntReturnedByResolver(t *testing.T) {
	resolver := func(path string) (any, error) {
		return int64(100), nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.count == 100", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true: int64(100) == 100")
	}
}

func TestEvalEdgeExpr_BoolReturnedByResolver(t *testing.T) {
	resolver := func(path string) (any, error) {
		return true, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	// Single truthy check on a bool
	ok, err := EvalEdgeExpr("node.x.data.flag", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected truthy for bool true")
	}
}

func TestEvalEdgeExpr_BoolFalseIsFalsy(t *testing.T) {
	resolver := func(path string) (any, error) {
		return false, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.flag", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected falsy for bool false")
	}
}

func TestEvalEdgeExpr_ZeroIsFalsy(t *testing.T) {
	resolver := func(path string) (any, error) {
		return 0, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.count", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected falsy for int 0")
	}
}

func TestEvalEdgeExpr_FloatZeroIsFalsy(t *testing.T) {
	resolver := func(path string) (any, error) {
		return 0.0, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.ratio", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected falsy for float64 0.0")
	}
}

func TestEvalEdgeExpr_NonZeroNumberIsTruthy(t *testing.T) {
	resolver := func(path string) (any, error) {
		return 42, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.count", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected truthy for int 42")
	}
}

func TestEvalEdgeExpr_StringComparisonFallback(t *testing.T) {
	// When both sides are non-numeric strings, compare lexicographically
	ctx := &EvalContext{Decision: "banana", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision > 'apple'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true: 'banana' > 'apple' lexicographically")
	}
}

func TestEvalEdgeExpr_StringLessThan(t *testing.T) {
	ctx := &EvalContext{Decision: "alpha", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision < 'beta'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true: 'alpha' < 'beta' lexicographically")
	}
}

func TestEvalEdgeExpr_LargeNumber(t *testing.T) {
	resolver := func(path string) (any, error) {
		return float64(1e18), nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.x.data.big > 999999999999", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for large number comparison")
	}
}

func TestEvalEdgeExpr_TrailingWhitespace(t *testing.T) {
	ctx := &EvalContext{Decision: "yes", Status: statusCompleted}
	ok, err := EvalEdgeExpr("  decision == 'yes'  ", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true with leading/trailing whitespace")
	}
}

func TestEvalEdgeExpr_TabSeparated(t *testing.T) {
	ctx := &EvalContext{Decision: "yes", Status: statusCompleted}
	ok, err := EvalEdgeExpr("decision\t==\t'yes'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true with tab-separated tokens")
	}
}

// --- isTruthy edge cases ---

func TestIsTruthy_Comprehensive(t *testing.T) {
	cases := []struct {
		value  any
		expect bool
		desc   string
	}{
		{nil, false, "nil is falsy"},
		{"", false, "empty string is falsy"},
		{testInputHello, true, "non-empty string is truthy"},
		{0, false, "int 0 is falsy"},
		{1, true, "int 1 is truthy"},
		{-1, true, "int -1 is truthy"},
		{float64(0), false, "float64 0 is falsy"},
		{float64(0.1), true, "float64 0.1 is truthy"},
		{true, true, "bool true is truthy"},
		{false, false, "bool false is falsy"},
		{[]int{1, 2}, true, "non-nil slice is truthy (unknown type fallback)"},
		{map[string]int{}, true, "non-nil map is truthy (unknown type fallback)"},
	}
	for _, tc := range cases {
		got := isTruthy(tc.value)
		if got != tc.expect {
			t.Fatalf("%s: isTruthy(%v) = %v, want %v", tc.desc, tc.value, got, tc.expect)
		}
	}
}

// --- toFloat edge cases ---

func TestToFloat_Types(t *testing.T) {
	cases := []struct {
		value    any
		expectOk bool
		expectF  float64
		desc     string
	}{
		{float64(3.14), true, 3.14, "float64"},
		{int(42), true, 42.0, "int"},
		{int64(100), true, 100.0, "int64"},
		{"123", true, 123.0, "string numeric"},
		{"3.14", true, 3.14, "string float"},
		{"not_a_number", false, 0, "string non-numeric"},
		{nil, false, 0, "nil"},
		{true, false, 0, "bool"},
		{[]int{}, false, 0, "slice"},
	}
	for _, tc := range cases {
		f, ok := toFloat(tc.value)
		if ok != tc.expectOk {
			t.Fatalf("%s: toFloat ok=%v, want %v", tc.desc, ok, tc.expectOk)
		}
		if ok && f != tc.expectF {
			t.Fatalf("%s: toFloat=%v, want %v", tc.desc, f, tc.expectF)
		}
	}
}

// --- compare edge cases ---

func TestCompare_MixedTypes(t *testing.T) {
	// One side numeric, other side non-numeric string -> falls back to string comparison
	// Left: int 5, Right: "abc" -> toFloat(5)=ok, toFloat("abc")=fail -> string comparison
	result := compare(5, tokEq, "abc")
	if result {
		t.Fatal("expected false: 5 != 'abc'")
	}

	// Both non-numeric strings
	result = compare("foo", tokEq, "foo")
	if !result {
		t.Fatal("expected true: 'foo' == 'foo'")
	}

	// Number vs numeric string should use numeric comparison
	result = compare(42, tokEq, "42")
	if !result {
		t.Fatal("expected true: 42 == '42' via numeric comparison")
	}
}

func TestCompare_NilHandling(t *testing.T) {
	// nil vs string: toFloat(nil) fails, falls to string: "<nil>" vs testInputHello
	result := compare(nil, tokEq, testInputHello)
	if result {
		t.Fatal("expected false: nil != 'hello'")
	}
	result = compare(nil, tokNeq, testInputHello)
	if !result {
		t.Fatal("expected true: nil != 'hello'")
	}
	// nil vs nil
	result = compare(nil, tokEq, nil)
	if !result {
		t.Fatal("expected true: nil == nil (both format to '<nil>')")
	}
}

// --- matchEdgesExpr integration edge cases ---

func TestMatchEdgesExpr_MultipleMatchingExpressions(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: "status == 'completed'"},
			{From: "a", To: "c", When: "decision == 'approved'"},
		},
	}
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 2 {
		t.Fatalf("expected 2 matching edges, got %d: %v", len(edges), edges)
	}
}

func TestMatchEdgesExpr_ExpressionErrorSilentlySkipped(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: "== =="}, // malformed expression
			{From: "a", To: "c", When: "decision == 'yes'"},
		},
	}
	ctx := &EvalContext{Decision: "yes", Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	// The malformed expression should be silently skipped (err != nil -> not matched)
	if len(edges) != 1 || edges[0].To != "c" {
		t.Fatalf("expected only edge to c, got %v", edges)
	}
}

func TestMatchEdgesExpr_MixedPlainAndExpression(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionApproved},    // plain
			{From: "a", To: "c", When: "status == 'completed'"}, // expression
		},
	}
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	// Both should match: plain testDecisionApproved matches decision, expression matches status
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %v", len(edges), edges)
	}
}

func TestMatchEdgesExpr_EmptyWhenFallback(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: ""},     // empty = fallback
			{From: "a", To: "c", When: "nope"}, // won't match
		},
	}
	ctx := &EvalContext{Decision: "anything", Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	// "nope" doesn't match, so no explicit match. Falls back to default/empty.
	if len(edges) != 1 || edges[0].To != "b" {
		t.Fatalf("expected fallback to empty-when edge to b, got %v", edges)
	}
}

func TestMatchEdgesExpr_EmptyWhenNotUsedWhenExplicitMatch(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: ""},                   // empty = fallback
			{From: "a", To: "c", When: testDecisionApproved}, // matches
		},
	}
	ctx := &EvalContext{Decision: testDecisionApproved, Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	// testDecisionApproved matches, so default fallback should NOT be used
	if len(edges) != 1 || edges[0].To != "c" {
		t.Fatalf("expected explicit match to c only, got %v", edges)
	}
}

func TestMatchEdgesExpr_FailedStatusWithExpressionCatch(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "error_handler", When: "status == 'failed'"},
			{From: "a", To: "b", When: testDecisionDefault},
		},
	}
	ctx := &EvalContext{Decision: "", Status: statusFailed}
	edges := matchEdgesExpr(wf, "a", ctx)
	// Expression should match, default should not be reached as fallback
	if len(edges) != 1 || edges[0].To != "error_handler" {
		t.Fatalf("expected expression edge to error_handler, got %v", edges)
	}
}

func TestMatchEdgesExpr_NoEdgesFromNode(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "b", To: "c", When: testDecisionDefault},
		},
	}
	ctx := &EvalContext{Decision: testDecisionDone, Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 0 {
		t.Fatalf("expected no edges from node a, got %v", edges)
	}
}

func TestMatchEdgesExpr_DefaultAndEmptyBothFallback(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionDefault},
			{From: "a", To: "c", When: ""},
		},
	}
	ctx := &EvalContext{Decision: "unmatched", Status: statusCompleted}
	edges := matchEdgesExpr(wf, "a", ctx)
	// Both default and empty should match as fallback
	if len(edges) != 2 {
		t.Fatalf("expected 2 fallback edges, got %d: %v", len(edges), edges)
	}
}

func TestMatchEdgesExpr_FailedWithNoFailureEdge(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: testDecisionApproved},
		},
	}
	ctx := &EvalContext{Decision: "", Status: statusFailed}
	edges := matchEdgesExpr(wf, "a", ctx)
	if len(edges) != 0 {
		t.Fatalf("expected no edges for failed node without failure handler, got %v", edges)
	}
}

// --- containsSingleAngleBracket edge cases ---

func TestContainsSingleAngleBracket(t *testing.T) {
	cases := []struct {
		input  string
		expect bool
		desc   string
	}{
		{">", true, "bare >"},
		{"<", true, "bare <"},
		{">=", false, "> followed by ="},
		{"<=", false, "< followed by ="},
		{"=>", false, "= followed by >"},
		{"a > b", true, "> with spaces"},
		{"a < b", true, "< with spaces"},
		{"a >= b", false, ">= with spaces"},
		{"a <= b", false, "<= with spaces"},
		{">>", true, ">> first > is not preceded by =, second > is not preceded by ="},
		{"a > b >= c", true, "mixed > and >="},
	}
	for _, tc := range cases {
		got := containsSingleAngleBracket(tc.input)
		if got != tc.expect {
			t.Fatalf("%s: containsSingleAngleBracket(%q) = %v, want %v", tc.desc, tc.input, got, tc.expect)
		}
	}
}

// --- Long/complex expression stress tests ---

func TestEvalEdgeExpr_LongOrChain(t *testing.T) {
	ctx := &EvalContext{Decision: "z", Status: statusCompleted}
	// Build: decision == 'a' || decision == 'b' || ... || decision == 'z'
	expr := ""
	for c := byte('a'); c <= byte('z'); c++ {
		if expr != "" {
			expr += " || "
		}
		expr += "decision == '" + string(c) + "'"
	}
	ok, err := EvalEdgeExpr(expr, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true: decision 'z' should match one of the 26 options")
	}
}

func TestEvalEdgeExpr_LongAndChain(t *testing.T) {
	ctx := &EvalContext{Decision: "yes", Status: statusCompleted, Message: "ok"}
	expr := "decision == 'yes' && status == 'completed' && message == 'ok' && decision != 'no' && status != 'failed'"
	ok, err := EvalEdgeExpr(expr, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for long AND chain where all conditions are true")
	}
}

func TestEvalEdgeExpr_NumberEqualityWithFloat(t *testing.T) {
	resolver := func(path string) (any, error) {
		return 3.14, nil
	}
	ctx := &EvalContext{Resolve: resolver}
	ok, err := EvalEdgeExpr("node.a.data.pi == 3.14", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true for float equality")
	}
}

func TestEvalEdgeExpr_ResolverErrorTreatedAsNil(t *testing.T) {
	callCount := 0
	resolver := func(path string) (any, error) {
		callCount++
		return nil, fmt.Errorf("resolver exploded")
	}
	ctx := &EvalContext{Resolve: resolver}
	// Should not error out, just treat as nil
	ok, err := EvalEdgeExpr("node.broken.data.x == 'hello'", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false when resolver errors")
	}
	if callCount != 1 {
		t.Fatalf("expected resolver to be called once, got %d", callCount)
	}
}
