package document

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestRunNode_JSONMarshalsKind(t *testing.T) {
	node := &RunNode{
		RunID:        "r1",
		WorkflowID:   "implement_spec",
		WorkflowName: "Implement Spec",
		Title:        "tagsrv",
		Status:       "completed",
		Compact:      false,
		Children: []NodeChild{
			RowChild{NodeID: "ensure_repo", RunID: "r1", Role: "ensure_repo"},
			SubRunChild{Run: &RunNode{RunID: "c1", WorkflowID: "impl_task"}},
			ParallelChild{
				ParentNode: "implement_tasks",
				Runs: []*RunNode{
					{RunID: "a", WorkflowID: "impl_task"},
					{RunID: "b", WorkflowID: "impl_task"},
				},
			},
		},
	}
	b, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	// Each child must carry a "kind" discriminator.
	if !strings.Contains(s, `"kind":"row"`) {
		t.Errorf("missing row kind: %s", s)
	}
	if !strings.Contains(s, `"kind":"subrun"`) {
		t.Errorf("missing subrun kind: %s", s)
	}
	if !strings.Contains(s, `"kind":"parallel"`) {
		t.Errorf("missing parallel kind: %s", s)
	}
}

func TestBuildRunNode_TrivialSingleNode(t *testing.T) {
	rs := state.NewRunState("r1", "implement_spec", nil)
	rs.WithNode("hello", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "default"
		n.Message = "hello world"
	})
	loader := &fakeLoader{runs: map[string]*state.RunState{"r1": rs}}
	node := buildRunNode(rs, loader)
	if node == nil {
		t.Fatal("nil node")
	}
	if node.RunID != "r1" {
		t.Errorf("RunID: %q", node.RunID)
	}
	if node.WorkflowID != "implement_spec" {
		t.Errorf("WorkflowID: %q", node.WorkflowID)
	}
	if len(node.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(node.Children))
	}
	row, ok := node.Children[0].(RowChild)
	if !ok {
		t.Fatalf("want RowChild, got %T", node.Children[0])
	}
	if row.NodeID != "hello" {
		t.Errorf("row.NodeID: %q", row.NodeID)
	}
}

func TestWalkTreeEvents_PlainRowsInChronologicalOrder(t *testing.T) {
	rs := state.NewRunState("r1", "build_component", nil)
	rs.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "ready_for_review"
	})
	rs.WithNode("review_plan", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "approved"
	})
	at := func(s int) time.Time {
		return time.Date(2026, 5, 13, 2, 44, s, 0, time.UTC)
	}
	events := []state.Event{
		{Timestamp: at(2), Type: "node_started", RunID: "r1", NodeID: "plan_tasks"},
		{Timestamp: at(3), Type: "node_completed", RunID: "r1", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(3), Type: "node_started", RunID: "r1", NodeID: "review_plan"},
		{Timestamp: at(4), Type: "node_completed", RunID: "r1", NodeID: "review_plan", Data: map[string]any{"decision": "approved"}},
	}
	loader := &fakeLoader{
		runs:   map[string]*state.RunState{"r1": rs},
		events: map[string][]state.Event{"r1": events},
	}
	node := buildRunNode(rs, loader)
	if len(node.Children) != 2 {
		t.Fatalf("want 2 children, got %d", len(node.Children))
	}
	r0, ok := node.Children[0].(RowChild)
	if !ok || r0.NodeID != "plan_tasks" {
		t.Errorf("child 0: want plan_tasks RowChild, got %+v", node.Children[0])
	}
	r1, ok := node.Children[1].(RowChild)
	if !ok || r1.NodeID != "review_plan" {
		t.Errorf("child 1: want review_plan RowChild, got %+v", node.Children[1])
	}
}

func TestWalkTreeEvents_MultipleAttemptsSameNode(t *testing.T) {
	rs := state.NewRunState("r1", "build_component", nil)
	rs.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "ready_for_review"
	})
	rs.WithNode("review_plan", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "approved"
	})
	at := func(s int) time.Time {
		return time.Date(2026, 5, 13, 2, 44, s, 0, time.UTC)
	}
	events := []state.Event{
		{Timestamp: at(1), Type: "node_started", RunID: "r1", NodeID: "plan_tasks"},
		{Timestamp: at(2), Type: "node_completed", RunID: "r1", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(2), Type: "node_started", RunID: "r1", NodeID: "review_plan"},
		{Timestamp: at(3), Type: "node_completed", RunID: "r1", NodeID: "review_plan", Data: map[string]any{"decision": "changes_requested"}},
		{Timestamp: at(3), Type: "node_started", RunID: "r1", NodeID: "plan_tasks"},
		{Timestamp: at(4), Type: "node_completed", RunID: "r1", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(4), Type: "node_started", RunID: "r1", NodeID: "review_plan"},
		{Timestamp: at(5), Type: "node_completed", RunID: "r1", NodeID: "review_plan", Data: map[string]any{"decision": "approved"}},
	}
	loader := &fakeLoader{
		runs:   map[string]*state.RunState{"r1": rs},
		events: map[string][]state.Event{"r1": events},
	}
	node := buildRunNode(rs, loader)
	if len(node.Children) != 4 {
		t.Fatalf("want 4 children, got %d", len(node.Children))
	}
	want := []struct {
		nodeID   string
		ordinal  int
		decision string
	}{
		{"plan_tasks", 1, "ready_for_review"},
		{"review_plan", 1, "changes_requested"},
		{"plan_tasks", 2, "ready_for_review"},
		{"review_plan", 2, "approved"},
	}
	for i, w := range want {
		r := node.Children[i].(RowChild)
		if r.NodeID != w.nodeID || r.AttemptOrdinal != w.ordinal || r.Decision != w.decision {
			t.Errorf("child %d: got %s ord=%d dec=%q; want %s ord=%d dec=%q",
				i, r.NodeID, r.AttemptOrdinal, r.Decision, w.nodeID, w.ordinal, w.decision)
		}
	}
}

func TestWalkTreeEvents_NestedSubworkflow(t *testing.T) {
	parent := state.NewRunState("p", "implement_spec", nil)
	parent.WithNode("dispatch", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "c"}
	})
	child := state.NewRunState("c", "impl_task", nil)
	child.ParentRun = "p"
	child.WithNode("checker", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "tests_pass"
	})
	at := func(s int) time.Time { return time.Date(2026, 5, 13, 0, 0, s, 0, time.UTC) }
	loader := &fakeLoader{
		runs: map[string]*state.RunState{"p": parent, "c": child},
		events: map[string][]state.Event{
			"p": {
				{Timestamp: at(1), Type: "node_started", RunID: "p", NodeID: "dispatch"},
				{Timestamp: at(2), Type: "subworkflow_started", RunID: "p", NodeID: "dispatch", Data: map[string]any{"child_run": "c"}},
				{Timestamp: at(5), Type: "node_completed", RunID: "p", NodeID: "dispatch", Data: map[string]any{"decision": "done"}},
			},
			"c": {
				{Timestamp: at(3), Type: "node_started", RunID: "c", NodeID: "checker"},
				{Timestamp: at(4), Type: "node_completed", RunID: "c", NodeID: "checker", Data: map[string]any{"decision": "tests_pass"}},
			},
		},
	}
	node := buildRunNode(parent, loader)
	if len(node.Children) != 1 {
		t.Fatalf("want 1 child, got %d: %+v", len(node.Children), node.Children)
	}
	sub, ok := node.Children[0].(SubRunChild)
	if !ok {
		t.Fatalf("want SubRunChild, got %T", node.Children[0])
	}
	if sub.Run == nil || sub.Run.RunID != "c" {
		t.Fatalf("sub.Run: %+v", sub.Run)
	}
	if len(sub.Run.Children) != 1 {
		t.Fatalf("sub.Run.Children: want 1, got %d", len(sub.Run.Children))
	}
	checker := sub.Run.Children[0].(RowChild)
	if checker.NodeID != "checker" {
		t.Errorf("checker: %s", checker.NodeID)
	}
}

func TestWalkTreeEvents_SingleIterationForEach(t *testing.T) {
	parent := state.NewRunState("p", "build_component", nil)
	parent.WithNode("implement_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "all_succeeded"
		n.Data = map[string]any{
			"items": []any{
				map[string]any{"expanded_id": "implement_one_task::0", "data": map[string]any{"child_run": "a"}},
				map[string]any{"expanded_id": "implement_one_task::1", "data": map[string]any{"child_run": "b"}},
			},
		}
	})
	mk := func(id string) *state.RunState {
		s := state.NewRunState(id, "implement_task", nil)
		s.ParentRun = "p"
		s.WithNode("checker", func(n *state.NodeState) {
			n.Status = "completed"
			n.Decision = "tests_pass"
		})
		return s
	}
	loader := &fakeLoader{
		runs: map[string]*state.RunState{"p": parent, "a": mk("a"), "b": mk("b")},
		events: map[string][]state.Event{
			"p": {
				{Timestamp: time.Unix(1, 0), Type: "node_started", RunID: "p", NodeID: "implement_tasks"},
				{Timestamp: time.Unix(1, 0), Type: "subworkflow_started", RunID: "p", NodeID: "implement_one_task::0", Data: map[string]any{"child_run": "a"}},
				{Timestamp: time.Unix(1, 0), Type: "subworkflow_started", RunID: "p", NodeID: "implement_one_task::1", Data: map[string]any{"child_run": "b"}},
				{Timestamp: time.Unix(10, 0), Type: "node_completed", RunID: "p", NodeID: "implement_tasks", Data: map[string]any{"decision": "all_succeeded"}},
			},
		},
	}
	node := buildRunNode(parent, loader)
	if len(node.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(node.Children))
	}
	pc, ok := node.Children[0].(ParallelChild)
	if !ok {
		t.Fatalf("want ParallelChild, got %T", node.Children[0])
	}
	if pc.ParentNode != "implement_tasks" {
		t.Errorf("ParentNode: %q", pc.ParentNode)
	}
	if len(pc.Runs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(pc.Runs))
	}
	if pc.Runs[0].RunID != "a" || pc.Runs[1].RunID != "b" {
		t.Errorf("run order: %s, %s", pc.Runs[0].RunID, pc.Runs[1].RunID)
	}
}

func TestWalkTreeEvents_MultiIterationForEach_RealRun(t *testing.T) {
	// Mirrors run pebble-meadow-eagle/nebula-velvet-delta: plan_tasks × 2 →
	// implement_tasks (5 children, some_failed) → plan_tasks × 2 → implement_tasks
	// (2 children, all_succeeded) → commit_component.
	rs := state.NewRunState("n", "build_component", nil)
	rs.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "ready_for_review"
	})
	rs.WithNode("review_plan", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "approved"
	})
	rs.WithNode("implement_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "all_succeeded"
		n.Data = map[string]any{
			"items": []any{
				map[string]any{"expanded_id": "implement_one_task::0", "data": map[string]any{"child_run": "copper-blaze-voyage"}},
				map[string]any{"expanded_id": "implement_one_task::1", "data": map[string]any{"child_run": "topaz-mesa-prairie"}},
			},
		}
	})
	rs.WithNode("commit_component", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "default"
	})
	childIDs := []string{
		"jet-thistle-beacon", "cirrus-mosaic-crane", "otter-river-forge", "swift-cirrus-fjord", "lagoon-north-quest",
		"copper-blaze-voyage", "topaz-mesa-prairie",
	}
	runs := map[string]*state.RunState{"n": rs}
	for _, c := range childIDs {
		cs := state.NewRunState(c, "implement_task", nil)
		cs.ParentRun = "n"
		cs.WithNode("noop", func(n *state.NodeState) { n.Status = "completed"; n.Decision = "tests_pass" })
		runs[c] = cs
	}
	at := func(h, m, s int) time.Time { return time.Date(2026, 5, 13, h, m, s, 0, time.UTC) }
	events := []state.Event{
		{Timestamp: at(2, 44, 2), Type: "node_started", RunID: "n", NodeID: "plan_tasks"},
		{Timestamp: at(2, 45, 43), Type: "node_completed", RunID: "n", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(2, 45, 43), Type: "node_started", RunID: "n", NodeID: "review_plan"},
		{Timestamp: at(2, 46, 13), Type: "node_completed", RunID: "n", NodeID: "review_plan", Data: map[string]any{"decision": "changes_requested"}},
		{Timestamp: at(2, 46, 13), Type: "node_started", RunID: "n", NodeID: "plan_tasks"},
		{Timestamp: at(2, 47, 26), Type: "node_completed", RunID: "n", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(2, 47, 26), Type: "node_started", RunID: "n", NodeID: "review_plan"},
		{Timestamp: at(2, 47, 41), Type: "node_completed", RunID: "n", NodeID: "review_plan", Data: map[string]any{"decision": "approved"}},
		{Timestamp: at(2, 47, 41), Type: "node_started", RunID: "n", NodeID: "implement_tasks"},
		{Timestamp: at(2, 47, 41), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::0", Data: map[string]any{"child_run": "jet-thistle-beacon"}},
		{Timestamp: at(2, 53, 18), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::2", Data: map[string]any{"child_run": "cirrus-mosaic-crane"}},
		{Timestamp: at(2, 53, 18), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::1", Data: map[string]any{"child_run": "otter-river-forge"}},
		{Timestamp: at(2, 58, 55), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::3", Data: map[string]any{"child_run": "swift-cirrus-fjord"}},
		{Timestamp: at(3, 4, 7), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::4", Data: map[string]any{"child_run": "lagoon-north-quest"}},
		{Timestamp: at(3, 37, 59), Type: "node_completed", RunID: "n", NodeID: "implement_tasks", Data: map[string]any{"decision": "some_failed"}},
		{Timestamp: at(3, 37, 59), Type: "node_started", RunID: "n", NodeID: "plan_tasks"},
		{Timestamp: at(3, 39, 17), Type: "node_completed", RunID: "n", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(3, 39, 17), Type: "node_started", RunID: "n", NodeID: "review_plan"},
		{Timestamp: at(3, 39, 32), Type: "node_completed", RunID: "n", NodeID: "review_plan", Data: map[string]any{"decision": "changes_requested"}},
		{Timestamp: at(3, 39, 32), Type: "node_started", RunID: "n", NodeID: "plan_tasks"},
		{Timestamp: at(3, 39, 49), Type: "node_completed", RunID: "n", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
		{Timestamp: at(3, 39, 49), Type: "node_started", RunID: "n", NodeID: "review_plan"},
		{Timestamp: at(3, 40, 8), Type: "node_completed", RunID: "n", NodeID: "review_plan", Data: map[string]any{"decision": "approved"}},
		{Timestamp: at(3, 40, 8), Type: "node_started", RunID: "n", NodeID: "implement_tasks"},
		{Timestamp: at(3, 40, 8), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::1", Data: map[string]any{"child_run": "topaz-mesa-prairie"}},
		{Timestamp: at(3, 40, 8), Type: "subworkflow_started", RunID: "n", NodeID: "implement_one_task::0", Data: map[string]any{"child_run": "copper-blaze-voyage"}},
		{Timestamp: at(3, 49, 45), Type: "node_completed", RunID: "n", NodeID: "implement_tasks", Data: map[string]any{"decision": "all_succeeded"}},
		{Timestamp: at(3, 49, 45), Type: "node_started", RunID: "n", NodeID: "commit_component"},
		{Timestamp: at(3, 49, 45), Type: "node_completed", RunID: "n", NodeID: "commit_component", Data: map[string]any{"decision": "default"}},
	}
	loader := &fakeLoader{runs: runs, events: map[string][]state.Event{"n": events}}
	node := buildRunNode(rs, loader)

	if len(node.Children) != 11 {
		t.Fatalf("want 11 children, got %d", len(node.Children))
	}
	asRow := func(idx int) RowChild {
		r, ok := node.Children[idx].(RowChild)
		if !ok {
			t.Fatalf("child %d: want RowChild, got %T", idx, node.Children[idx])
		}
		return r
	}
	asParallel := func(idx int) ParallelChild {
		p, ok := node.Children[idx].(ParallelChild)
		if !ok {
			t.Fatalf("child %d: want ParallelChild, got %T", idx, node.Children[idx])
		}
		return p
	}
	// Each row's decision must come from THIS execution's completion event,
	// not the Nth completion overall — see findExecutionCompletion. The real
	// run's review_plan decisions are: 1=changes_requested, 2=approved,
	// 3=changes_requested, 4=approved.
	wantRows := []struct {
		idx      int
		nodeID   string
		ordinal  int
		decision string
	}{
		{0, "plan_tasks", 1, "ready_for_review"},
		{1, "review_plan", 1, "changes_requested"},
		{2, "plan_tasks", 2, "ready_for_review"},
		{3, "review_plan", 2, "approved"},
		{5, "plan_tasks", 3, "ready_for_review"},
		{6, "review_plan", 3, "changes_requested"},
		{7, "plan_tasks", 4, "ready_for_review"},
		{8, "review_plan", 4, "approved"},
		{10, "commit_component", 1, "default"},
	}
	for _, w := range wantRows {
		r := asRow(w.idx)
		if r.NodeID != w.nodeID || r.AttemptOrdinal != w.ordinal || r.Decision != w.decision {
			t.Errorf("child %d: got %s ord=%d dec=%q; want %s ord=%d dec=%q",
				w.idx, r.NodeID, r.AttemptOrdinal, r.Decision, w.nodeID, w.ordinal, w.decision)
		}
	}
	p1 := asParallel(4)
	if p1.ParentNode != "implement_tasks" || len(p1.Runs) != 5 {
		t.Errorf("child 4 (first ParallelChild): parent=%q, runs=%d", p1.ParentNode, len(p1.Runs))
	}
	p2 := asParallel(9)
	if p2.ParentNode != "implement_tasks" || len(p2.Runs) != 2 {
		t.Errorf("child 9 (second ParallelChild): parent=%q, runs=%d", p2.ParentNode, len(p2.Runs))
	}
}

func TestBuildRunNode_CompactWhenClean(t *testing.T) {
	rs := state.NewRunState("r1", "impl_task", nil)
	rs.WithNode("write_tests", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "correct_failure"
	})
	rs.WithNode("write_code", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "tests_pass"
	})
	at := func(s int) time.Time { return time.Date(2026, 5, 13, 0, 0, s, 0, time.UTC) }
	events := []state.Event{
		{Timestamp: at(1), Type: "node_started", RunID: "r1", NodeID: "write_tests"},
		{Timestamp: at(2), Type: "node_completed", RunID: "r1", NodeID: "write_tests", Data: map[string]any{"decision": "correct_failure"}},
		{Timestamp: at(3), Type: "node_started", RunID: "r1", NodeID: "write_code"},
		{Timestamp: at(4), Type: "node_completed", RunID: "r1", NodeID: "write_code", Data: map[string]any{"decision": "tests_pass"}},
	}
	loader := &fakeLoader{
		runs:   map[string]*state.RunState{"r1": rs},
		events: map[string][]state.Event{"r1": events},
	}
	node := buildRunNode(rs, loader)
	if !node.Compact {
		t.Errorf("want Compact=true for clean subtree; got false")
	}
	if node.Summary != "write_tests · write_code" {
		t.Errorf("Summary: %q", node.Summary)
	}
}

func TestBuildRunNode_NotCompactWhenSubtreeHasBadDecision(t *testing.T) {
	rs := state.NewRunState("r1", "impl_task", nil)
	rs.WithNode("write_tests", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "tests_fail"
	})
	at := func(s int) time.Time { return time.Date(2026, 5, 13, 0, 0, s, 0, time.UTC) }
	events := []state.Event{
		{Timestamp: at(1), Type: "node_started", RunID: "r1", NodeID: "write_tests"},
		{Timestamp: at(2), Type: "node_completed", RunID: "r1", NodeID: "write_tests", Data: map[string]any{"decision": "tests_fail"}},
	}
	loader := &fakeLoader{
		runs:   map[string]*state.RunState{"r1": rs},
		events: map[string][]state.Event{"r1": events},
	}
	node := buildRunNode(rs, loader)
	if node.Compact {
		t.Errorf("want Compact=false when a row has DecisionFamily=bad")
	}
}

func TestBuildDocument_TreeEntryPoint(t *testing.T) {
	rs := state.NewRunState("r1", "implement_spec", nil)
	rs.WithNode("hello", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "default"
		n.Message = "hello"
	})
	loader := &fakeLoader{runs: map[string]*state.RunState{"r1": rs}}
	doc, err := BuildDocument("r1", loader)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if doc.Root == nil {
		t.Fatal("Root is nil")
	}
	if doc.Root.RunID != "r1" {
		t.Errorf("Root.RunID: %q", doc.Root.RunID)
	}
	if doc.Root.Compact {
		t.Errorf("root should not auto-compact")
	}
	if len(doc.Root.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(doc.Root.Children))
	}
	if doc.TotalRuns != 1 {
		t.Errorf("TotalRuns: %d", doc.TotalRuns)
	}
}

func TestEnrichTree_PerExecutionPrompt(t *testing.T) {
	rs := state.NewRunState("r1", "build_component", nil)
	rs.WithNode("plan_tasks", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "ready_for_review"
	})
	now := time.Now()
	events := []state.Event{
		{Timestamp: now.Add(0), Type: "node_prompt", RunID: "r1", NodeID: "plan_tasks", Text: "<!-- LOCAL -->\nFirst plan\n<!-- /LOCAL -->"},
		{Timestamp: now.Add(1), Type: "node_started", RunID: "r1", NodeID: "plan_tasks"},
		{Timestamp: now.Add(2), Type: "node_completed", RunID: "r1", NodeID: "plan_tasks", Data: map[string]any{"decision": "rejected"}},
		{Timestamp: now.Add(3), Type: "node_prompt", RunID: "r1", NodeID: "plan_tasks", Text: "<!-- LOCAL -->\nRevised plan\n<!-- /LOCAL -->"},
		{Timestamp: now.Add(4), Type: "node_started", RunID: "r1", NodeID: "plan_tasks"},
		{Timestamp: now.Add(5), Type: "node_completed", RunID: "r1", NodeID: "plan_tasks", Data: map[string]any{"decision": "ready_for_review"}},
	}
	loader := &fakeLoader{runs: map[string]*state.RunState{"r1": rs}, events: map[string][]state.Event{"r1": events}}
	node := buildRunNode(rs, loader)
	enrichRunNode(node, loader)
	if len(node.Children) != 2 {
		t.Fatalf("want 2 rows, got %d", len(node.Children))
	}
	r1 := node.Children[0].(RowChild)
	r2 := node.Children[1].(RowChild)
	if !strings.Contains(r1.Prompt, "First plan") {
		t.Errorf("row 1 prompt: %q", r1.Prompt)
	}
	if !strings.Contains(r2.Prompt, "Revised plan") {
		t.Errorf("row 2 prompt: %q", r2.Prompt)
	}
}
