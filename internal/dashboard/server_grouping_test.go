package dashboard

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
)

const testRunIDRoot = "root"

func TestBuildExecutionGroupsBuildsParentChildTree(t *testing.T) {
	base := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		{
			ID:         "child-b",
			WorkflowID: "task",
			Status:     "completed",
			StartedAt:  base.Add(2 * time.Minute),
			Duration:   "10s",
			ParentRun:  testRunIDRoot,
		},
		{
			ID:         testRunIDRoot,
			WorkflowID: "idea_to_delivery",
			Status:     "running",
			StartedAt:  base,
			Duration:   "1m",
		},
		{
			ID:         "grandchild",
			WorkflowID: "task_review",
			Status:     "paused",
			StartedAt:  base.Add(3 * time.Minute),
			Duration:   "5s",
			ParentRun:  "child-a",
		},
		{
			ID:         "child-a",
			WorkflowID: "team",
			Status:     "running",
			StartedAt:  base.Add(1 * time.Minute),
			Duration:   "20s",
			ParentRun:  testRunIDRoot,
		},
	}

	groups := buildExecutionGroups(runs)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	group := groups[0]
	if group.Root.ID != testRunIDRoot {
		t.Fatalf("expected root run to be root, got %s", group.Root.ID)
	}
	if group.TotalRuns != 4 {
		t.Fatalf("expected total runs to be 4, got %d", group.TotalRuns)
	}
	if group.ActiveRuns != 3 {
		t.Fatalf("expected active runs to be 3, got %d", group.ActiveRuns)
	}
	if group.GroupStatus != "running" {
		t.Fatalf("expected group status running, got %s", group.GroupStatus)
	}
	if len(group.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(group.Rows))
	}
	if group.Rows[0].Run.ID != testRunIDRoot || group.Rows[0].Depth != 0 {
		t.Fatalf("expected row[0] to be root at depth 0, got %s depth %d", group.Rows[0].Run.ID, group.Rows[0].Depth)
	}
	if group.Rows[1].Run.ID != "child-a" || group.Rows[1].Depth != 1 {
		t.Fatalf("expected row[1] to be child-a at depth 1, got %s depth %d", group.Rows[1].Run.ID, group.Rows[1].Depth)
	}
	if group.Rows[2].Run.ID != "grandchild" || group.Rows[2].Depth != 2 {
		t.Fatalf("expected row[2] to be grandchild at depth 2, got %s depth %d", group.Rows[2].Run.ID, group.Rows[2].Depth)
	}
	if group.Rows[3].Run.ID != "child-b" || group.Rows[3].Depth != 1 {
		t.Fatalf("expected row[3] to be child-b at depth 1, got %s depth %d", group.Rows[3].Run.ID, group.Rows[3].Depth)
	}
}

func TestBuildExecutionGroupsHandlesMissingParentAndStatusPriority(t *testing.T) {
	base := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		{
			ID:         "orphan",
			WorkflowID: "task",
			Status:     "failed",
			StartedAt:  base.Add(2 * time.Minute),
			Duration:   "7s",
			ParentRun:  "missing",
		},
		{
			ID:         testRunIDRoot,
			WorkflowID: "workflow",
			Status:     "completed",
			StartedAt:  base,
			Duration:   "30s",
		},
		{
			ID:         "child",
			WorkflowID: "sub",
			Status:     "awaiting_approval",
			StartedAt:  base.Add(1 * time.Minute),
			Duration:   "5s",
			ParentRun:  testRunIDRoot,
		},
	}

	groups := buildExecutionGroups(runs)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	first := groups[0]
	second := groups[1]
	if first.Root.ID != "orphan" {
		t.Fatalf("expected newest root to be orphan, got %s", first.Root.ID)
	}
	if first.GroupStatus != "failed" {
		t.Fatalf("expected orphan group to be failed, got %s", first.GroupStatus)
	}

	if second.Root.ID != testRunIDRoot {
		t.Fatalf("expected second root to be root, got %s", second.Root.ID)
	}
	if second.GroupStatus != "paused" {
		t.Fatalf("expected root group to be paused due to awaiting_approval child, got %s", second.GroupStatus)
	}
	if second.ActiveRuns != 1 {
		t.Fatalf("expected root group active runs to be 1, got %d", second.ActiveRuns)
	}
}

func TestBuildExecutionGroups_CancelledGroupStatus(t *testing.T) {
	runs := []RunSummary{
		{ID: "root", Status: statusCancelled, WorkflowID: "implement_spec"},
		{ID: "child", Status: statusCancelled, ParentRun: "root", WorkflowID: "build"},
	}
	groups := buildExecutionGroups(runs)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].GroupStatus != statusCancelled {
		t.Fatalf("expected group status cancelled, got %s", groups[0].GroupStatus)
	}
}

func TestConnectedWorkflowIDsBuildsConnectedComponent(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"implement_spec": {
				ID: "implement_spec",
				Nodes: []definitions.Node{
					{ID: "init", Kind: "subworkflow", Workflow: "initialize"},
					{ID: "impl", Kind: "subworkflow", Workflow: "implement"},
				},
			},
			"initialize": {
				ID: "initialize",
				Nodes: []definitions.Node{
					{ID: "plan", Kind: "subworkflow", Workflow: "plan"},
				},
			},
			"implement": {ID: "implement"},
			"plan":      {ID: "plan"},
			"isolated":  {ID: "isolated"},
		},
	}

	related := connectedWorkflowIDs(bundle, "implement_spec")
	if len(related) != 4 {
		t.Fatalf("expected 4 related workflows, got %d", len(related))
	}
	for _, id := range []string{"implement_spec", "initialize", "implement", "plan"} {
		if _, ok := related[id]; !ok {
			t.Fatalf("expected %s to be related", id)
		}
	}
	if _, ok := related["isolated"]; ok {
		t.Fatal("did not expect isolated workflow to be related")
	}
}

func TestFilterExecutionGroupsByWorkflowIDs(t *testing.T) {
	base := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		{
			ID:         "child-match",
			WorkflowID: "build_component",
			Status:     "completed",
			StartedAt:  base.Add(1 * time.Minute),
			Duration:   "5s",
			ParentRun:  "root-a",
		},
		{
			ID:         "root-a",
			WorkflowID: "implement_spec",
			Status:     "running",
			StartedAt:  base,
			Duration:   "20s",
		},
		{
			ID:         "root-b",
			WorkflowID: "unrelated_workflow",
			Status:     "completed",
			StartedAt:  base.Add(2 * time.Minute),
			Duration:   "10s",
		},
	}
	groups := buildExecutionGroups(runs)

	filtered := filterExecutionGroupsByWorkflowIDs(groups, map[string]struct{}{
		"build_component": {},
		"implement_spec":  {},
	})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 matching group, got %d", len(filtered))
	}
	if filtered[0].Root.ID != "root-a" {
		t.Fatalf("expected root-a group, got %s", filtered[0].Root.ID)
	}
	if got := countRunsInGroups(filtered); got != 2 {
		t.Fatalf("expected related run count 2, got %d", got)
	}
}

func TestBuildTreeConvertsFlatRowsToNestedTree(t *testing.T) {
	rows := []RunTreeRow{
		{Run: RunSummary{ID: "root", ParentRun: ""}, Depth: 0, HasChildren: true},
		{Run: RunSummary{ID: "child-a", ParentRun: "root"}, Depth: 1, HasChildren: true},
		{Run: RunSummary{ID: "grandchild", ParentRun: "child-a"}, Depth: 2, HasChildren: false},
		{Run: RunSummary{ID: "child-b", ParentRun: "root"}, Depth: 1, HasChildren: false},
	}

	tree := buildTree(rows)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root node, got %d", len(tree))
	}
	if tree[0].Run.ID != "root" {
		t.Fatalf("expected root node ID 'root', got %s", tree[0].Run.ID)
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("expected root to have 2 children, got %d", len(tree[0].Children))
	}
	if tree[0].Children[0].Run.ID != "child-a" {
		t.Fatalf("expected first child 'child-a', got %s", tree[0].Children[0].Run.ID)
	}
	if len(tree[0].Children[0].Children) != 1 {
		t.Fatalf("expected child-a to have 1 child, got %d", len(tree[0].Children[0].Children))
	}
	if tree[0].Children[0].Children[0].Run.ID != "grandchild" {
		t.Fatalf("expected grandchild, got %s", tree[0].Children[0].Children[0].Run.ID)
	}
	if tree[0].Children[1].Run.ID != "child-b" {
		t.Fatalf("expected second child 'child-b', got %s", tree[0].Children[1].Run.ID)
	}
	if len(tree[0].Children[1].Children) != 0 {
		t.Fatalf("expected child-b to have 0 children, got %d", len(tree[0].Children[1].Children))
	}
}

func TestBuildTreeEmptyInput(t *testing.T) {
	tree := buildTree(nil)
	if len(tree) != 0 {
		t.Fatalf("expected 0 nodes for nil input, got %d", len(tree))
	}
	tree = buildTree([]RunTreeRow{})
	if len(tree) != 0 {
		t.Fatalf("expected 0 nodes for empty input, got %d", len(tree))
	}
}

func TestBuildTreeSingleRootNoChildren(t *testing.T) {
	rows := []RunTreeRow{
		{Run: RunSummary{ID: "solo"}, Depth: 0, HasChildren: false},
	}
	tree := buildTree(rows)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	if tree[0].Run.ID != "solo" {
		t.Fatalf("expected 'solo', got %s", tree[0].Run.ID)
	}
	if len(tree[0].Children) != 0 {
		t.Fatalf("expected 0 children, got %d", len(tree[0].Children))
	}
}

func TestFindExecutionGroupByRunID(t *testing.T) {
	base := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		{
			ID:         "child-a",
			WorkflowID: "build_component",
			Status:     "completed",
			StartedAt:  base.Add(1 * time.Minute),
			Duration:   "6s",
			ParentRun:  "root-a",
		},
		{
			ID:         "root-a",
			WorkflowID: "implement_spec",
			Status:     "running",
			StartedAt:  base,
			Duration:   "1m",
		},
		{
			ID:         "root-b",
			WorkflowID: "brainstorm",
			Status:     "completed",
			StartedAt:  base.Add(2 * time.Minute),
			Duration:   "4s",
		},
	}
	groups := buildExecutionGroups(runs)

	group := findExecutionGroupByRunID(groups, "child-a")
	if group == nil {
		t.Fatal("expected group for child-a")
	}
	if group.Root.ID != "root-a" {
		t.Fatalf("expected root-a group, got %s", group.Root.ID)
	}

	if missing := findExecutionGroupByRunID(groups, "missing-run"); missing != nil {
		t.Fatal("expected nil for missing run id")
	}
}
