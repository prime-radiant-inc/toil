package engine

import (
	"strings"
	"testing"
)

// PRI-1576: skipped items in a ForEach cascade must carry an
// originating_failure pointer so a debugger can see the root cause
// without traversing into the failed item's child run.
func TestForEachCascade_SkippedItemsCarryOriginatingFailure(t *testing.T) {
	// Build three forEachItemStates in DAG-like layout:
	//   task-0  (failed)
	//   task-1  (depends on task-0)
	//   task-2  (depends on task-1)
	// Calling markSkipped after task-0 fails should cascade an
	// originating_failure summary onto task-1 and task-2.
	states := []forEachItemState{
		{
			ID: "task-0", ExpandedID: "tmpl::0", Status: outcomeFailed,
			FailureContext: map[string]any{
				"error":         "resolve inputs: unknown node field: bogus",
				"last_message":  "resolution attempted at attempt 0",
				"last_decision": "",
				"session_id":    "",
			},
		},
		{ID: "task-1", ExpandedID: "tmpl::1"},
		{ID: "task-2", ExpandedID: "tmpl::2"},
	}
	deps := map[int][]int{0: {1}, 1: {2}}
	itemIDs := []string{"task-0", "task-1", "task-2"}
	expandedIDs := []string{"tmpl::0", "tmpl::1", "tmpl::2"}

	originating := summarizeForOriginatingFailure(itemIDs[0], states[0].FailureContext)
	if originating == nil {
		t.Fatal("summary should be non-nil when failure_context has error")
	}
	if originating["id"] != "task-0" {
		t.Errorf("originating.id = %v, want task-0", originating["id"])
	}
	if got, _ := originating["error"].(string); !strings.Contains(got, "bogus") {
		t.Errorf("originating.error = %q, want it to mention bogus", got)
	}

	markSkipped(states, deps, 0, itemIDs, nil, expandedIDs,
		"", "run-x", nil, "", false, originating)

	for _, idx := range []int{1, 2} {
		st := states[idx]
		if st.Status != statusSkipped {
			t.Errorf("task-%d status = %q, want skipped", idx, st.Status)
		}
		if st.OriginatingFailure == nil {
			t.Fatalf("task-%d missing OriginatingFailure", idx)
		}
		m := st.toMap()
		of, ok := m["originating_failure"].(map[string]any)
		if !ok {
			t.Fatalf("task-%d toMap missing originating_failure: %v", idx, m)
		}
		if of["id"] != "task-0" {
			t.Errorf("task-%d originating_failure.id = %v", idx, of["id"])
		}
		if got, _ := of["error"].(string); !strings.Contains(got, "bogus") {
			t.Errorf("task-%d originating_failure.error = %q", idx, got)
		}
	}
}

// PRI-1576: cancellation cascades don't carry originating_failure
// (there's no underlying failure to point at — operator intent caused
// the skip). markSkipped called with nil originating must leave the
// field unset on dependents.
func TestForEachCascade_CancelCascadeOmitsOriginatingFailure(t *testing.T) {
	states := []forEachItemState{
		{ID: "task-0", ExpandedID: "tmpl::0", Status: statusSkipped, SkipReason: "cancelled"},
		{ID: "task-1", ExpandedID: "tmpl::1"},
	}
	deps := map[int][]int{0: {1}}

	markSkipped(states, deps, 0, []string{"task-0", "task-1"}, nil,
		[]string{"tmpl::0", "tmpl::1"}, "", "run-x", nil, "", false, nil)

	if states[1].OriginatingFailure != nil {
		t.Errorf("cancellation skip should not stamp OriginatingFailure, got %v", states[1].OriginatingFailure)
	}
	if _, has := states[1].toMap()["originating_failure"]; has {
		t.Errorf("toMap should omit originating_failure key when unset")
	}
}

// PRI-1576: summarizeForOriginatingFailure must return nil when the
// failure_context has only the node_id (no diagnostic-quality data).
// Avoids emitting a stub pointer that adds noise without information.
func TestSummarizeForOriginatingFailure_NilWhenEmpty(t *testing.T) {
	if got := summarizeForOriginatingFailure("task-x", nil); got != nil {
		t.Errorf("nil failure_context should yield nil summary, got %v", got)
	}
	if got := summarizeForOriginatingFailure("task-x", map[string]any{
		"node_id":    "x",
		"session_id": "",
	}); got != nil {
		t.Errorf("failure_context with no diagnostic data should yield nil, got %v", got)
	}
}
