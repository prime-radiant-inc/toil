package engine

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// nodeWithOverrideTag is a fixture: a role node whose force_approve
// decision carries the "override" tag. Drives the materialization
// path through markNodeCompleted without needing a full workflow def.
func nodeWithOverrideTag() *definitions.Node {
	return &definitions.Node{
		ID:   "resolve_review_dispute",
		Kind: "role",
		Decisions: definitions.DecisionList{
			{ID: "force_approve", Tags: []string{"override"}},
			{ID: "send_back"},
			{ID: "skip_task", Tags: []string{"override"}},
		},
	}
}

func TestMarkNodeCompleted_MaterializesTagsFromDecision(t *testing.T) {
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState("r1", "implement_task", nil)
	runState.WithNode("resolve_review_dispute", func(n *state.NodeState) {
		n.Status = statusPending
	})

	output := NodeOutput{
		Decision: "force_approve",
		Message:  "waived scaffold smoke concern",
		Data: map[string]any{
			"waived_concerns": []any{
				map[string]any{"source": "write_code", "concern": "TS exec mode", "justification": "verified"},
			},
		},
	}
	markNodeCompleted(runState, logger, "r1", "resolve_review_dispute", nodeWithOverrideTag(), time.Now().Add(-time.Second), &output)
	_ = logger.Close()

	// Tags must be copied onto NodeState at emit time.
	var ns *state.NodeState
	runState.WithNode("resolve_review_dispute", func(n *state.NodeState) { ns = n })
	if len(ns.Tags) != 1 || ns.Tags[0] != "override" {
		t.Fatalf("NodeState.Tags = %v, want [override]", ns.Tags)
	}

	// node_completed event data must include the tags so live SSE
	// consumers can filter without re-resolving the decision.
	events := parseEvents(t, logPath)
	completed := findEvent(events, "node_completed")
	if completed == nil {
		t.Fatal("node_completed event missing")
	}
	tagsAny, ok := completed.Data["tags"]
	if !ok {
		t.Fatalf("node_completed event missing tags key; data = %+v", completed.Data)
	}
	tags, ok := tagsAny.([]any)
	if !ok {
		// JSON decode may keep []string as []any — accept either shape.
		if strs, ok2 := tagsAny.([]string); ok2 {
			if len(strs) != 1 || strs[0] != "override" {
				t.Fatalf("tags = %v", strs)
			}
		} else {
			t.Fatalf("tags wrong type: %T", tagsAny)
		}
	} else if len(tags) != 1 || tags[0] != "override" {
		t.Fatalf("tags = %v", tags)
	}

	// Derived query must find it.
	if got := runState.NodesTagged("override"); len(got) != 1 {
		t.Fatalf("NodesTagged(override) = %d entries", len(got))
	}
}

func TestMarkNodeCompleted_NoTagsForDecisionsWithoutTags(t *testing.T) {
	// Decisions without tags declared — no tags materialized, no tags
	// key in the event data. Most completions in most workflows go
	// through this path.
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState("r", "wf", nil)
	runState.WithNode("n", func(s *state.NodeState) { s.Status = statusPending })

	node := &definitions.Node{
		ID: "n",
		Decisions: definitions.DecisionList{
			{ID: "approved"},
			{ID: "send_back"},
		},
	}
	markNodeCompleted(runState, logger, "r", "n", node, time.Now().Add(-time.Second), &NodeOutput{
		Decision: "approved", Message: "ok",
	})
	_ = logger.Close()

	events := parseEvents(t, logPath)
	completed := findEvent(events, "node_completed")
	if completed == nil {
		t.Fatal("node_completed missing")
	}
	if _, hasTags := completed.Data["tags"]; hasTags {
		t.Fatalf("event should have no tags key when decision carries none: %+v", completed.Data)
	}
	if len(runState.NodesTagged("override")) != 0 {
		t.Fatal("untagged decision must not appear in NodesTagged")
	}
}

func TestMarkNodeCompleted_NilNodeToleratesLookup(t *testing.T) {
	// Callers in older-path executors might pass nil — the function
	// must not panic and should record the node with no tags.
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState("r", "wf", nil)
	runState.WithNode("n", func(s *state.NodeState) { s.Status = statusPending })

	markNodeCompleted(runState, logger, "r", "n", nil, time.Now().Add(-time.Second), &NodeOutput{
		Decision: "force_approve", Message: "no-op",
	})

	var ns *state.NodeState
	runState.WithNode("n", func(s *state.NodeState) { ns = s })
	if len(ns.Tags) != 0 {
		t.Fatalf("nil node → no tags, got %v", ns.Tags)
	}
}

func TestMarkNodeCompleted_UnknownDecisionRecordsNoTags(t *testing.T) {
	// If the runner returns a decision not in the node's decision list
	// (normally caught by validation), we degrade gracefully and
	// don't crash looking up its tags.
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState("r", "wf", nil)
	runState.WithNode("n", func(s *state.NodeState) { s.Status = statusPending })
	node := &definitions.Node{
		ID: "n",
		Decisions: definitions.DecisionList{
			{ID: "approved"},
		},
	}
	markNodeCompleted(runState, logger, "r", "n", node, time.Now().Add(-time.Second), &NodeOutput{
		Decision: "something_else", Message: "rogue",
	})
	var ns *state.NodeState
	runState.WithNode("n", func(s *state.NodeState) { ns = s })
	if len(ns.Tags) != 0 {
		t.Fatalf("unknown decision → no tags, got %v", ns.Tags)
	}
}

func TestMarkNodeCompleted_RetryToUntaggedDecisionSupersedes(t *testing.T) {
	// A node decides force_approve (tagged "override") on attempt 1,
	// then retries and decides send_back (untagged). Derivation shows
	// no override — stale tags from the first attempt are overwritten.
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	node := nodeWithOverrideTag()
	runState := state.NewRunState("r", "implement_task", nil)
	runState.WithNode("resolve_review_dispute", func(s *state.NodeState) { s.Status = statusPending })

	markNodeCompleted(runState, logger, "r", "resolve_review_dispute", node, time.Now().Add(-2*time.Second), &NodeOutput{
		Decision: "force_approve", Message: "initial waiver",
	})
	if len(runState.NodesTagged("override")) != 1 {
		t.Fatal("expected 1 override after attempt 1")
	}

	markNodeCompleted(runState, logger, "r", "resolve_review_dispute", node, time.Now().Add(-time.Second), &NodeOutput{
		Decision: "send_back", Message: "re-review after fix",
	})
	if got := runState.NodesTagged("override"); len(got) != 0 {
		t.Fatalf("expected 0 overrides after retry to send_back, got %+v", got)
	}
}
