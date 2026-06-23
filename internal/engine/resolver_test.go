package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestResolveExpression(t *testing.T) {
	ctx := &RunContext{
		RunID:  "river-scout-forge",
		Inputs: map[string]any{"idea": testInputHello},
		Outputs: map[string]NodeOutput{
			"node1": {
				Decision: testDecisionApproved,
				Data:     map[string]any{"plan_doc": "plan"},
			},
		},
		OptionalInputs: map[string]bool{"optional": true},
	}

	value, err := ctx.Resolve("input.idea")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != testInputHello {
		t.Fatalf("unexpected value: %v", value)
	}

	value, err = ctx.Resolve("node.node1.decision")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != testDecisionApproved {
		t.Fatalf("unexpected value: %v", value)
	}

	value, err = ctx.Resolve("node.node1.data.plan_doc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "plan" {
		t.Fatalf("unexpected value: %v", value)
	}

	value, err = ctx.Resolve("input.optional")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != nil {
		t.Fatalf("expected nil value: %v", value)
	}

	value, err = ctx.Resolve("run.id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "river-scout-forge" {
		t.Fatalf("unexpected value: %v", value)
	}
}

func TestResolveExpression_BareNodeReturnsFullOutput(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"reviewer": {
				Decision:  "changes_requested",
				Message:   "Requested changes after source review.",
				Artifacts: []string{"out/review.md"},
				Data: map[string]any{
					"issues": []any{
						map[string]any{"file": "a.go", "line": 12},
					},
				},
			},
		},
	}

	value, err := ctx.Resolve("node.reviewer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", value)
	}
	if m["decision"] != "changes_requested" {
		t.Errorf("decision = %v, want changes_requested", m["decision"])
	}
	if m["message"] != "Requested changes after source review." {
		t.Errorf("message = %v", m["message"])
	}
	arts, ok := m["artifacts"].([]string)
	if !ok || len(arts) != 1 || arts[0] != "out/review.md" {
		t.Errorf("artifacts = %v", m["artifacts"])
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", m["data"])
	}
	if _, ok := data["issues"]; !ok {
		t.Errorf("data.issues missing: %v", data)
	}
}

func TestResolver_NodeSessionID(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"plan_tasks": {Decision: "ready_for_review", SessionID: "sess-abc"},
		},
	}
	got, err := ctx.Resolve("node.plan_tasks.session_id")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "sess-abc" {
		t.Fatalf("expected sess-abc, got %v", got)
	}
}

func TestResolver_NodeSessionID_EmptyWhenUnset(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"shell_node": {Decision: "default"},
		},
	}
	got, err := ctx.Resolve("node.shell_node.session_id")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string, got %v", got)
	}
}

func TestResolver_BareNodeIncludesSessionID(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {Decision: "d", Message: "m", SessionID: "s"},
		},
	}
	got, err := ctx.Resolve("node.x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	m := got.(map[string]any)
	if m["session_id"] != "s" {
		t.Fatalf("expected session_id in bare-node map, got %v", m)
	}
}

func TestResolveExpression_NodeWithoutFieldOrID(t *testing.T) {
	ctx := &RunContext{Outputs: map[string]NodeOutput{}}
	if _, err := ctx.Resolve("node."); err == nil {
		t.Errorf("expected error for empty node id")
	}
}

// PRI-1574: tags/status/attempts must resolve, mirroring the
// session_id surface added in 780e9ec.
func TestResolver_NodeTags(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"judge": {Decision: "force_approve", Tags: []string{"override"}},
		},
	}
	got, err := ctx.Resolve("node.judge.tags")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	tags, ok := got.([]string)
	if !ok || len(tags) != 1 || tags[0] != "override" {
		t.Fatalf("expected [override], got %v (%T)", got, got)
	}
}

func TestResolver_NodeStatus(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"engineer": {Decision: "tests_pass", Status: "completed"},
		},
	}
	got, err := ctx.Resolve("node.engineer.status")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "completed" {
		t.Fatalf("expected completed, got %v", got)
	}
}

func TestResolver_NodeAttempts(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"reviewer": {Decision: "changes_requested", Attempts: 4},
		},
	}
	got, err := ctx.Resolve("node.reviewer.attempts")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 4 {
		t.Fatalf("expected 4, got %v", got)
	}
}

func TestResolver_BareNodeIncludesNewFields(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {Decision: "d", Tags: []string{"override"}, Status: "completed", Attempts: 2},
		},
	}
	got, err := ctx.Resolve("node.x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	m := got.(map[string]any)
	if tags, _ := m["tags"].([]string); len(tags) != 1 || tags[0] != "override" {
		t.Errorf("tags = %v", m["tags"])
	}
	if m["status"] != "completed" {
		t.Errorf("status = %v", m["status"])
	}
	if m["attempts"] != 2 {
		t.Errorf("attempts = %v", m["attempts"])
	}
}

// PRI-1574: the default branch of the node-field switch must enumerate
// supported fields so workflow authors don't have to read engine source
// to figure out what's available.
func TestResolver_UnknownNodeFieldErrorListsSupported(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {Decision: "ok"},
		},
	}
	_, err := ctx.Resolve("node.x.bogus_field")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, expected := range []string{"bogus_field", "decision", "message", "artifacts", "data", "session_id", "tags", "status", "attempts"} {
		if !strings.Contains(msg, expected) {
			t.Errorf("error should mention %q; got %q", expected, msg)
		}
	}
}

// PRI-2103: definitions.SupportedNodeFields drives load-time validation of
// ${node.X.<field>} references. It must stay in lockstep with the fields the
// resolver actually serves — otherwise `toil validate` would accept a field
// the resolver rejects at runtime, which is the precise drift the shared list
// exists to prevent. This test fails if the two diverge in either direction.
func TestResolver_SupportedNodeFieldsMatchResolverSurface(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {Decision: "d"},
		},
	}
	// Every advertised field must resolve (no "unknown node field" error).
	for _, f := range definitions.SupportedNodeFields {
		if _, err := ctx.Resolve("node.x." + f); err != nil {
			t.Errorf("supported field %q does not resolve: %v", f, err)
		}
	}
	// The bare node.X map must expose exactly the advertised fields — no more,
	// no fewer — so the resolver's own enumerations can't drift from the list.
	got, err := ctx.Resolve("node.x")
	if err != nil {
		t.Fatalf("resolve bare node: %v", err)
	}
	m := got.(map[string]any)
	if len(m) != len(definitions.SupportedNodeFields) {
		t.Errorf("bare node map has %d fields, SupportedNodeFields has %d: %v",
			len(m), len(definitions.SupportedNodeFields), m)
	}
	for _, f := range definitions.SupportedNodeFields {
		if _, ok := m[f]; !ok {
			t.Errorf("bare node map missing advertised field %q", f)
		}
	}
}

func TestResolveExpression_NestedInputPath(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{
			"component": map[string]any{
				"id": "public_blog",
				"meta": map[string]any{
					"owner": "toil",
				},
			},
		},
		Outputs: map[string]NodeOutput{},
	}

	value, err := ctx.Resolve("input.component.id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "public_blog" {
		t.Fatalf("unexpected value: %v", value)
	}

	value, err = ctx.Resolve("input.component.meta.owner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "toil" {
		t.Fatalf("unexpected value: %v", value)
	}
}

func TestResolveExpression_TemplateInterpolation(t *testing.T) {
	ctx := &RunContext{
		RunID: "river-scout-forge",
		Inputs: map[string]any{
			"project_dir": "/workspace/projects/blog",
			"component": map[string]any{
				"id": "admin_interface",
			},
		},
		Outputs: map[string]NodeOutput{},
	}

	value, err := ctx.Resolve("${input.project_dir}/../worktrees/${run.id}/${input.component.id}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "/workspace/projects/blog/../worktrees/river-scout-forge/admin_interface" {
		t.Fatalf("unexpected value: %v", value)
	}
}

func TestResolveInputsOptionalNode(t *testing.T) {
	t.Skip("legacy InputRef optional semantics removed; see Task 30b (required-reference satisfiability) for replacement")
}

func TestResolveInputs_ForEachItemExpressions(t *testing.T) {
	// Verifies that ForEach item variables (injected via the extra map in
	// executeSingle → mergeDispatchInputs) are resolvable as input.* references.
	// The merged context is built by mergeDispatchInputs; evaluatePhase1 then
	// resolves the node's declared inputs against that merged context.
	ctx := &RunContext{
		Inputs: map[string]any{
			"project_dir": "/workspace/projects/blog",
			// ForEach item is injected into the run context before Phase 1 runs.
			"component": map[string]any{
				"id": "public_blog",
			},
		},
		Outputs: map[string]NodeOutput{},
	}

	inputs := map[string]any{
		"project_dir":  "${input.project_dir}/../worktrees/${input.component.id}",
		"component_id": "${input.component.id}",
	}

	resolved, err := evaluatePhase1(ctx, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved["project_dir"] != "/workspace/projects/blog/../worktrees/public_blog" {
		t.Fatalf("unexpected project_dir: %v", resolved["project_dir"])
	}
	if resolved["component_id"] != "public_blog" {
		t.Fatalf("unexpected component_id: %v", resolved["component_id"])
	}
}

func TestResolver_SliceIndexByInteger(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{
			"items": []any{"a", "b", "c"},
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"input.items.0", "a"},
		{"input.items.1", "b"},
		{"input.items.2", "c"},
	}
	for _, c := range cases {
		got, err := ctx.Resolve(c.expr)
		if err != nil {
			t.Errorf("Resolve(%q): %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Resolve(%q): got %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestResolver_SliceIndexOutOfBounds(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{"items": []any{"a"}},
	}
	_, err := ctx.Resolve("input.items.5")
	if err == nil {
		t.Fatalf("expected out-of-bounds error")
	}
}

func TestResolver_NestedSliceWithMap(t *testing.T) {
	// Simulates node.orch.data.items.0.status
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"orch": {
				Data: map[string]any{
					"items": []map[string]any{
						{"status": "succeeded", "id": "a"},
						{"status": "failed", "id": "b"},
					},
				},
			},
		},
	}
	got, err := ctx.Resolve("node.orch.data.items.1.status")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "failed" {
		t.Fatalf("got %v, want failed", got)
	}
}

func TestResolver_NegativeIndexRejected(t *testing.T) {
	// Support ONLY non-negative integers. A segment that looks like "-1" should
	// be treated as a map-key lookup (and fail because the key doesn't exist).
	ctx := &RunContext{
		Inputs: map[string]any{"items": []any{"a"}},
	}
	_, err := ctx.Resolve("input.items.-1")
	if err == nil {
		t.Fatalf("expected error for negative index (should be treated as map key lookup)")
	}
}

func TestResolver_SliceOfAnyType(t *testing.T) {
	// Make sure []any works, not just []map[string]any
	ctx := &RunContext{
		Inputs: map[string]any{"items": []any{1, 2, 3}},
	}
	got, err := ctx.Resolve("input.items.1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 2 {
		t.Fatalf("got %v, want 2", got)
	}
}

// fakeTreeResolver implements TreeResolver for resolver tests.
// Records every call so tests can assert what was queried.
type fakeTreeResolver struct {
	entries      []map[string]any
	err          error
	calledWith   string
	timesInvoked int
}

func (f *fakeTreeResolver) FindNodesByTag(tag string) ([]map[string]any, error) {
	f.calledWith = tag
	f.timesInvoked++
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func TestResolveTree_NoResolverErrors(t *testing.T) {
	// A context with no Tree cannot answer tree.* expressions.
	// The error message must mention TreeResolver so debugging is obvious.
	ctx := &RunContext{RunID: "r1"}
	_, err := ctx.Resolve("tree.tagged.override")
	if err == nil {
		t.Fatalf("expected error when Tree is nil")
	}
	if !strings.Contains(err.Error(), "TreeResolver") {
		t.Fatalf("error should mention TreeResolver, got: %v", err)
	}
}

func TestResolveTree_TaggedPassesTagToResolver(t *testing.T) {
	// tree.tagged.<tag> must pass the tag segment through verbatim.
	// The tag is workflow-declared; the harness doesn't care what the
	// name is, only that it reaches the resolver intact.
	fake := &fakeTreeResolver{}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	_, err := ctx.Resolve("tree.tagged.override")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.timesInvoked != 1 {
		t.Fatalf("expected 1 invocation, got %d", fake.timesInvoked)
	}
	if fake.calledWith != "override" {
		t.Fatalf("resolver called with tag %q, want override", fake.calledWith)
	}
}

func TestResolveTree_TaggedSupportsArbitraryTagNames(t *testing.T) {
	// Different workflows declare different tag vocabularies. The
	// resolver passes any single-segment tag through.
	fake := &fakeTreeResolver{}
	ctx := &RunContext{RunID: "r1", Tree: fake}
	for _, tag := range []string{"audit", "waiver", "flake", "custom-tag", "snake_case_tag"} {
		_, err := ctx.Resolve("tree.tagged." + tag)
		if err != nil {
			t.Errorf("tree.tagged.%s errored: %v", tag, err)
			continue
		}
		if fake.calledWith != tag {
			t.Errorf("resolver got %q, want %q", fake.calledWith, tag)
		}
	}
}

func TestResolveTree_TaggedReturnsResolverOutput(t *testing.T) {
	// Output passes through unchanged — the YAML expression consumer
	// sees exactly what the resolver produced.
	entries := []map[string]any{
		{
			"run_id":      "summit-sparrow-raven",
			"workflow_id": "implement_task",
			"node_id":     "resolve_review_dispute",
			"decision":    "force_approve",
			"tags":        []string{"override"},
		},
	}
	fake := &fakeTreeResolver{entries: entries}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	got, err := ctx.Resolve("tree.tagged.override")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotList, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", got)
	}
	if len(gotList) != 1 || gotList[0]["decision"] != "force_approve" {
		t.Fatalf("output mutated: %+v", gotList)
	}
}

func TestResolveTree_TaggedRequiresTagName(t *testing.T) {
	// Missing tag segment is an authoring error. Must surface, not
	// silently query empty.
	fake := &fakeTreeResolver{}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	_, err := ctx.Resolve("tree.tagged")
	if err == nil {
		t.Fatalf("expected error for missing tag segment")
	}
	if !strings.Contains(err.Error(), "tag name") {
		t.Fatalf("error should explain the missing tag, got: %v", err)
	}
	if fake.timesInvoked != 0 {
		t.Fatalf("resolver should not be invoked for malformed expression")
	}
}

func TestResolveTree_TaggedRejectsDottedTagName(t *testing.T) {
	// Tags are single-segment identifiers. Multi-dot expressions are
	// a reserved shape for future hierarchical projections, not
	// alternative tag syntax.
	fake := &fakeTreeResolver{}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	_, err := ctx.Resolve("tree.tagged.a.b")
	if err == nil {
		t.Fatalf("expected error for multi-segment tag")
	}
	if !strings.Contains(err.Error(), "single-segment") {
		t.Fatalf("error should explain single-segment rule, got: %v", err)
	}
	if fake.timesInvoked != 0 {
		t.Fatal("resolver should not be invoked")
	}
}

func TestResolveTree_UnknownProjectionErrors(t *testing.T) {
	// Unknown tree.* projections must error with supported-names hint.
	fake := &fakeTreeResolver{}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	_, err := ctx.Resolve("tree.unknown_projection")
	if err == nil {
		t.Fatalf("expected error for unknown projection")
	}
	if !strings.Contains(err.Error(), "tagged") {
		t.Fatalf("error should list supported projections, got: %v", err)
	}
	if fake.timesInvoked != 0 {
		t.Fatalf("resolver should not be called for unknown projections")
	}
}

func TestResolveTree_ResolverErrorPropagates(t *testing.T) {
	fake := &fakeTreeResolver{err: errors.New("runs dir unreadable")}
	ctx := &RunContext{RunID: "r1", Tree: fake}

	_, err := ctx.Resolve("tree.tagged.override")
	if err == nil {
		t.Fatalf("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "runs dir unreadable") {
		t.Fatalf("error should propagate resolver error, got: %v", err)
	}
}

func TestResolverEnvNamespace(t *testing.T) {
	ctx := &RunContext{
		Env: map[string]string{
			"PROJECT_DIR": "/some/path",
			"FOO_BAR":     "hello",
		},
	}
	cases := []struct {
		expr string
		want any
		err  bool
	}{
		{"env.PROJECT_DIR", "/some/path", false},
		{"env.FOO_BAR", "hello", false},
		{"env.MISSING", nil, true},
	}
	for _, tc := range cases {
		got, err := ctx.Resolve(tc.expr)
		if tc.err {
			if err == nil {
				t.Errorf("%s: want error, got %v", tc.expr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestResolverWorkflowInputNamespace(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{
			"task":  map[string]any{"id": "T1"},
			"label": "alpha",
		},
	}
	cases := []struct {
		expr string
		want any
		err  bool
	}{
		{"workflow_input.label", "alpha", false},
		{"workflow_input.task.id", "T1", false},
		{"workflow_input.missing", nil, true},
	}
	for _, tc := range cases {
		got, err := ctx.Resolve(tc.expr)
		if tc.err {
			if err == nil {
				t.Errorf("%s: want error, got %v", tc.expr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestResolverNewEnvelopeFields(t *testing.T) {
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {
				Decision:            "ok",
				Message:             "all good",
				LastRoutingDecision: "_loop_exhausted",
				LoopIterations:      5,
			},
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"node.x.last_routing_decision", "_loop_exhausted"},
		{"node.x.loop_iterations", 5},
	}
	for _, tc := range cases {
		got, err := ctx.Resolve(tc.expr)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestResolverTypePreservation(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{
			"obj":  map[string]any{"k": "v"},
			"list": []any{1, 2, 3},
			"num":  float64(7),
		},
	}
	// Single-expression: preserves native type (map).
	got, err := ctx.Resolve("${workflow_input.obj}")
	if err != nil {
		t.Fatalf("Resolve(map): %v", err)
	}
	if m, ok := got.(map[string]any); !ok || m["k"] != "v" {
		t.Errorf("single ${...} should preserve map; got %v (%T)", got, got)
	}
	// Single-expression list.
	got, err = ctx.Resolve("${workflow_input.list}")
	if err != nil {
		t.Fatalf("Resolve(list): %v", err)
	}
	if _, ok := got.([]any); !ok {
		t.Errorf("single ${...} should preserve list; got %v (%T)", got, got)
	}
	// Template (surrounding text): stringified.
	got, err = ctx.Resolve("count=${workflow_input.num}")
	if err != nil {
		t.Fatalf("Resolve(template): %v", err)
	}
	if s, ok := got.(string); !ok || s != "count=7" {
		t.Errorf("template should stringify; got %v (%T) want \"count=7\"", got, got)
	}
}

func TestResolverRequiredMarker(t *testing.T) {
	ctx := &RunContext{
		Inputs: map[string]any{"present": "yes"},
	}
	// Required-and-present: returns the value.
	got, err := ctx.Resolve("${workflow_input.present!}")
	if err != nil {
		t.Fatalf("Resolve(required+present): %v", err)
	}
	if got != "yes" {
		t.Errorf("got %v want yes", got)
	}
	// Required-and-missing: error mentions "unresolved required reference".
	_, err = ctx.Resolve("${workflow_input.missing!}")
	if err == nil {
		t.Fatalf("expected error for required missing reference")
	}
	if !strings.Contains(err.Error(), "unresolved required reference") {
		t.Errorf("error %v should mention 'unresolved required reference'", err)
	}
	// Optional-and-missing: returns nil without error.
	got, err = ctx.Resolve("${workflow_input.missing}")
	if err != nil {
		t.Errorf("optional missing should not error: %v", err)
	}
	if got != nil {
		t.Errorf("optional missing should return nil; got %v", got)
	}
}

func TestResolverDoubleDollarEscape(t *testing.T) {
	ctx := &RunContext{}
	got, err := ctx.Resolve("price is $${notinterpolated}")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "price is ${notinterpolated}" {
		t.Errorf("got %q want %q", got, "price is ${notinterpolated}")
	}
	// Pure $${ at start (no text around).
	got, err = ctx.Resolve("$${foo}")
	if err != nil {
		t.Fatalf("Resolve($${foo}): %v", err)
	}
	if got != "${foo}" {
		t.Errorf("got %q want %q", got, "${foo}")
	}
}
