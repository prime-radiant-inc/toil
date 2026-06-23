package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// writeRun creates a runs/<id>/state.json under runsDir with the given
// minimal fields + nodes. Used to compose a fake execution group for
// testing filesystemTreeResolver.
func writeRun(t *testing.T, runsDir, runID, workflowID, parentRun string, nodes map[string]*state.NodeState) {
	t.Helper()
	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rs := &state.RunState{
		ID:         runID,
		WorkflowID: workflowID,
		ParentRun:  parentRun,
		Nodes:      nodes,
	}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// dispatcherNode is a NodeState fixture that simulates a subworkflow
// dispatcher — a node whose data carries `child_run` pointing at a
// child run. The production engine writes this pattern whenever a
// subworkflow or foreach iteration spawns a child run; the narrow
// tree loader walks these pointers to find descendants.
func dispatcherNode(id, childRunID string) *state.NodeState {
	return &state.NodeState{
		ID:     id,
		Status: "completed",
		Data:   map[string]any{"child_run": childRunID},
	}
}

// foreachDispatcherNode is a NodeState fixture for a foreach
// orchestrator whose data.items[].data.child_run references multiple
// child runs.
func foreachDispatcherNode(id string, childRunIDs ...string) *state.NodeState {
	items := make([]any, 0, len(childRunIDs))
	for _, cr := range childRunIDs {
		items = append(items, map[string]any{
			"data": map[string]any{"child_run": cr},
		})
	}
	return &state.NodeState{
		ID:     id,
		Status: "completed",
		Data:   map[string]any{"items": items},
	}
}

// nodeTagged is a compact NodeState fixture: decision + tags + optional message.
func nodeTagged(id, decision, message string, tags ...string) *state.NodeState {
	return &state.NodeState{ID: id, Decision: decision, Message: message, Tags: tags}
}

func TestFilesystemTreeResolver_EmptyRunsDir(t *testing.T) {
	// A completely empty runs directory returns an empty result set
	// rather than erroring — consumers degrade gracefully for brand-new
	// installs or when the current run's state file hasn't landed yet.
	dir := t.TempDir()
	r := NewFilesystemTreeResolver(dir, "nonexistent")

	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatalf("FindNodesByTag: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestFilesystemTreeResolver_EmptyTagErrors(t *testing.T) {
	// An empty tag is a programming error — don't silently return
	// every tagged node.
	dir := t.TempDir()
	r := NewFilesystemTreeResolver(dir, "x")
	_, err := r.FindNodesByTag("")
	if err == nil {
		t.Fatal("expected error for empty tag")
	}
}

func TestFilesystemTreeResolver_FindsTaggedNodesAcrossTree(t *testing.T) {
	// Simulate a tetris-shaped tree:
	//   root (meadow) -> build_component (shadow) -> implement_task (summit)  [override tag]
	//                 -> integrate_component (anchor) -> integrate (brisk)
	//                                                  -> debug_merge (willow)
	//
	// Query for "override" from the deepest node — the walk should
	// cross sibling subtrees to find summit's waiver.
	runsDir := t.TempDir()

	// Each parent run's state carries a dispatcher node pointing at
	// its children — that's what the narrow loader follows downward.
	writeRun(t, runsDir, "meadow", "implement_spec", "", map[string]*state.NodeState{
		"build": dispatcherNode("build", "shadow"),
		"merge": dispatcherNode("merge", "anchor"),
	})
	writeRun(t, runsDir, "shadow", "build_component", "meadow", map[string]*state.NodeState{
		"implement_one_task": dispatcherNode("implement_one_task", "summit"),
	})
	writeRun(t, runsDir, "summit", "implement_task", "shadow", map[string]*state.NodeState{
		"resolve_review_dispute": {
			ID: "resolve_review_dispute", Decision: "force_approve",
			Tags:    []string{"override"},
			Message: "waived scaffold smoke concern",
			Data: map[string]any{
				"waived_concerns": []any{
					map[string]any{"source": "write_code", "concern": "TS exec mode", "justification": "verified"},
				},
			},
		},
		// Nodes without the override tag must not appear.
		"write_code": nodeTagged("write_code", "spec_issue", "cannot resolve"),
	})
	writeRun(t, runsDir, "anchor", "integrate_component", "meadow", map[string]*state.NodeState{
		"verify_integration": dispatcherNode("verify_integration", "brisk"),
	})
	writeRun(t, runsDir, "brisk", "verify_integration", "anchor", map[string]*state.NodeState{
		"debug": dispatcherNode("debug", "willow"),
	})
	writeRun(t, runsDir, "willow", "debug_merge", "brisk", nil)

	r := NewFilesystemTreeResolver(runsDir, "willow")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatalf("FindNodesByTag: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(got), got)
	}
	entry := got[0]
	if entry["run_id"] != "summit" {
		t.Fatalf("run_id = %v, want summit", entry["run_id"])
	}
	if entry["node_id"] != "resolve_review_dispute" {
		t.Fatalf("node_id = %v", entry["node_id"])
	}
	tags, ok := entry["tags"].([]string)
	if !ok || len(tags) == 0 || tags[0] != "override" {
		t.Fatalf("tags field wrong: %+v (type %T)", entry["tags"], entry["tags"])
	}
}

func TestFilesystemTreeResolver_QueryingFromRootFindsDescendant(t *testing.T) {
	runsDir := t.TempDir()
	writeRun(t, runsDir, "root", "implement_spec", "", map[string]*state.NodeState{
		"dispatch": dispatcherNode("dispatch", "child"),
	})
	writeRun(t, runsDir, "child", "implement_task", "root", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "waived", "override"),
	})

	r := NewFilesystemTreeResolver(runsDir, "root")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatalf("FindNodesByTag: %v", err)
	}
	if len(got) != 1 || got[0]["run_id"] != "child" {
		t.Fatalf("expected one match on child, got %v", got)
	}
}

func TestFilesystemTreeResolver_OnlyReturnsTaggedNodes(t *testing.T) {
	// Nodes missing the queried tag must not appear. Uses a tag
	// ("audit") that has no legacy backfill, so we're testing the
	// resolver's tag filter in isolation — not the LoadState
	// compatibility migration (see state.TestLoadState_BackfillsLegacyOverrideTags
	// for that path).
	runsDir := t.TempDir()
	writeRun(t, runsDir, "only", "implement_task", "", map[string]*state.NodeState{
		"a": nodeTagged("a", "approved", "tagged audit", "audit"),
		"b": nodeTagged("b", "approved", "normal"),
		"c": nodeTagged("c", "done", "tagged audit", "audit"),
		"d": nodeTagged("d", "send_back", "normal"),
	})
	r := NewFilesystemTreeResolver(runsDir, "only")
	got, err := r.FindNodesByTag("audit")
	if err != nil {
		t.Fatalf("FindNodesByTag: %v", err)
	}
	nodeIDs := make([]string, 0, len(got))
	for _, e := range got {
		nodeIDs = append(nodeIDs, e["node_id"].(string))
	}
	sort.Strings(nodeIDs)
	want := []string{"a", "c"}
	if len(nodeIDs) != len(want) {
		t.Fatalf("matched = %v, want %v", nodeIDs, want)
	}
	for i, n := range want {
		if nodeIDs[i] != n {
			t.Fatalf("match[%d] = %q, want %q", i, nodeIDs[i], n)
		}
	}
}

func TestFilesystemTreeResolver_DifferentTagsIsolated(t *testing.T) {
	// Querying for "override" must not return nodes tagged only
	// "audit", and vice versa. Tag queries are exact-match.
	runsDir := t.TempDir()
	writeRun(t, runsDir, "only", "wf", "", map[string]*state.NodeState{
		"a": nodeTagged("a", "approved", "", "audit"),
		"b": nodeTagged("b", "approved", "", "override"),
		"c": nodeTagged("c", "approved", "", "audit", "override"),
	})
	r := NewFilesystemTreeResolver(runsDir, "only")

	overrides, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatal(err)
	}
	overrideIDs := make([]string, 0, len(overrides))
	for _, e := range overrides {
		overrideIDs = append(overrideIDs, e["node_id"].(string))
	}
	sort.Strings(overrideIDs)
	if len(overrideIDs) != 2 || overrideIDs[0] != "b" || overrideIDs[1] != "c" {
		t.Fatalf("override query = %v, want [b c]", overrideIDs)
	}

	audits, err := r.FindNodesByTag("audit")
	if err != nil {
		t.Fatal(err)
	}
	auditIDs := make([]string, 0, len(audits))
	for _, e := range audits {
		auditIDs = append(auditIDs, e["node_id"].(string))
	}
	sort.Strings(auditIDs)
	if len(auditIDs) != 2 || auditIDs[0] != "a" || auditIDs[1] != "c" {
		t.Fatalf("audit query = %v, want [a c]", auditIDs)
	}
}

func TestFilesystemTreeResolver_SkipsSiblingGroups(t *testing.T) {
	// Two separate execution groups in the same runs dir must not
	// bleed into each other.
	runsDir := t.TempDir()
	writeRun(t, runsDir, "group-a-root", "implement_spec", "", map[string]*state.NodeState{
		"dispatch": dispatcherNode("dispatch", "group-a-child"),
	})
	writeRun(t, runsDir, "group-a-child", "implement_task", "group-a-root", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "in-group", "override"),
	})
	writeRun(t, runsDir, "group-b-root", "implement_spec", "", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "OUT-OF-GROUP", "override"),
	})
	r := NewFilesystemTreeResolver(runsDir, "group-a-child")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 in-group match, got %d: %v", len(got), got)
	}
	if got[0]["message"] != "in-group" {
		t.Fatalf("matched wrong group; message = %v", got[0]["message"])
	}
}

func TestFilesystemTreeResolver_ReturnsRootOnlyIfNoDescendants(t *testing.T) {
	runsDir := t.TempDir()
	writeRun(t, runsDir, "solo", "implement_task", "", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "alone", "override"),
	})
	r := NewFilesystemTreeResolver(runsDir, "solo")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
}

func TestFilesystemTreeResolver_TraversesForeachDispatchers(t *testing.T) {
	// The narrow loader must follow child_run references inside
	// foreach dispatchers (data.items[].data.child_run), not just
	// direct data.child_run.
	runsDir := t.TempDir()
	writeRun(t, runsDir, "root", "implement_spec", "", map[string]*state.NodeState{
		"implement_tasks": foreachDispatcherNode("implement_tasks", "iter-0", "iter-1", "iter-2"),
	})
	writeRun(t, runsDir, "iter-0", "implement_task", "root", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "iter-0 waiver", "override"),
	})
	writeRun(t, runsDir, "iter-1", "implement_task", "root", nil)
	writeRun(t, runsDir, "iter-2", "implement_task", "root", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "skip_task", "iter-2 waiver", "override"),
	})

	r := NewFilesystemTreeResolver(runsDir, "root")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatalf("FindNodesByTag: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matches from foreach iterations, got %d: %v", len(got), got)
	}
}

func TestFilesystemTreeResolver_DoesNotLoadUnrelatedRuns(t *testing.T) {
	// Narrow loader should not touch runs outside the current run's
	// execution group — that's the whole point of swapping away from
	// the scan-all approach. Verify by placing a sibling-group run
	// with an override that SHOULD NOT appear.
	runsDir := t.TempDir()
	writeRun(t, runsDir, "in-group", "implement_task", "", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "in-group", "override"),
	})
	// Unrelated run with no ancestor connection. Has an override; must
	// be invisible from an in-group query.
	writeRun(t, runsDir, "unrelated", "implement_task", "", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "UNRELATED — MUST NOT APPEAR", "override"),
	})

	r := NewFilesystemTreeResolver(runsDir, "in-group")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match (in-group only), got %d: %v", len(got), got)
	}
	if got[0]["message"] != "in-group" {
		t.Fatalf("wrong run returned: %v", got[0]["message"])
	}
}

func TestFilesystemTreeResolver_IgnoresCorruptedState(t *testing.T) {
	// Corrupted state.json in one run dir skips that dir rather than
	// failing the whole query.
	runsDir := t.TempDir()
	writeRun(t, runsDir, "good", "implement_task", "", map[string]*state.NodeState{
		"esc": nodeTagged("esc", "force_approve", "valid", "override"),
	})
	bad := filepath.Join(runsDir, "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewFilesystemTreeResolver(runsDir, "good")
	got, err := r.FindNodesByTag("override")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 despite corrupted sibling, got %d", len(got))
	}
}
