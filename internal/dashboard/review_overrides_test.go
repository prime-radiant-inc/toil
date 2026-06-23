package dashboard

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestCollectTaggedNodes_NilGroup(t *testing.T) {
	if got := CollectTaggedNodes(nil, "override"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCollectTaggedNodes_EmptyTagReturnsNil(t *testing.T) {
	// Empty tag is a programming error — the function guards with nil
	// rather than returning every tagged node.
	group := &ExecutionGroupSummary{Tree: []RunTreeNode{
		{Run: RunSummary{ID: "r", TaggedNodes: map[string][]state.TaggedNode{"override": {{NodeID: "n"}}}}},
	}}
	if got := CollectTaggedNodes(group, ""); got != nil {
		t.Fatalf("empty tag must return nil, got %+v", got)
	}
}

func TestCollectTaggedNodes_FlattensTree(t *testing.T) {
	// Two-level tree; overrides sit on leaves. Output must include
	// all entries, annotated with the recording run's identity.
	now := time.Now().UTC()
	grandchild := RunTreeNode{
		Run: RunSummary{
			ID:           "summit",
			Title:        "Implement Task: scaffold",
			WorkflowName: "Implement Task",
			TaggedNodes: map[string][]state.TaggedNode{
				"override": {
					{NodeID: "resolve_review_dispute", Decision: "force_approve", Message: "waived ts mode", EmittedAt: now},
					{NodeID: "some_other_node", Decision: "skip_task", Message: "moved on", EmittedAt: now},
				},
			},
		},
	}
	sibling := RunTreeNode{
		Run: RunSummary{
			ID:           "echo",
			Title:        "Implement Task: engine",
			WorkflowName: "Implement Task",
			TaggedNodes: map[string][]state.TaggedNode{
				"override": {{NodeID: "resolve_review_dispute", Decision: "force_approve", Message: "waived renderer review", EmittedAt: now}},
			},
		},
	}
	planAndBuild := RunTreeNode{
		Run:      RunSummary{ID: "canyon", WorkflowName: "Plan Component"},
		Children: []RunTreeNode{grandchild, sibling},
	}
	root := RunTreeNode{
		Run:      RunSummary{ID: "meadow", WorkflowName: "Implement Spec"},
		Children: []RunTreeNode{planAndBuild},
	}

	group := &ExecutionGroupSummary{Tree: []RunTreeNode{root}}
	got := CollectTaggedNodes(group, OverrideTag)
	if len(got) != 3 {
		t.Fatalf("expected 3 overrides, got %d: %+v", len(got), got)
	}

	byRun := map[string]int{}
	for _, entry := range got {
		byRun[entry.RunID]++
	}
	if byRun["summit"] != 2 || byRun["echo"] != 1 {
		t.Fatalf("wrong distribution by run: %v", byRun)
	}
}

func TestCollectTaggedNodes_DifferentTagsDontBleed(t *testing.T) {
	// A node tagged only with "audit" should not appear in an "override"
	// query, and vice versa. Tag queries are exact.
	group := &ExecutionGroupSummary{Tree: []RunTreeNode{
		{Run: RunSummary{
			ID: "a",
			TaggedNodes: map[string][]state.TaggedNode{
				"override": {{NodeID: "override_node"}},
				"audit":    {{NodeID: "audit_node"}},
			},
		}},
	}}

	overrides := CollectTaggedNodes(group, "override")
	if len(overrides) != 1 || overrides[0].Node.NodeID != "override_node" {
		t.Fatalf("override query wrong: %+v", overrides)
	}
	audits := CollectTaggedNodes(group, "audit")
	if len(audits) != 1 || audits[0].Node.NodeID != "audit_node" {
		t.Fatalf("audit query wrong: %+v", audits)
	}
}

func TestCollectTaggedNodes_RunsWithoutTagsContributeNothing(t *testing.T) {
	group := &ExecutionGroupSummary{Tree: []RunTreeNode{
		{Run: RunSummary{ID: "a"}},
		{Run: RunSummary{ID: "b", TaggedNodes: map[string][]state.TaggedNode{}}},
	}}
	if got := CollectTaggedNodes(group, "override"); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}
