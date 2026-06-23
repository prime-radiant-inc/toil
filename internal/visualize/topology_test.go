package visualize

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

func findTopologyNode(nodes []TopologyNode, id string) *TopologyNode {
	for i, n := range nodes {
		if n.ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func TestWorkflowTopology_BasicGraph(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a"},
			{ID: "b"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b", When: "done"},
		},
	}

	topo := WorkflowTopology(workflow)

	if len(topo.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(topo.Nodes))
	}
	if len(topo.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(topo.Edges))
	}

	// Verify node labels are just the node ID
	for _, n := range topo.Nodes {
		if n.Label != n.ID {
			t.Fatalf("expected label %q for node %q, got %q", n.ID, n.ID, n.Label)
		}
	}

	// Verify edge structure
	e := topo.Edges[0]
	if e.Source != "a" || e.Target != "b" {
		t.Fatalf("expected edge a->b, got %s->%s", e.Source, e.Target)
	}
	if e.Label != "done" {
		t.Fatalf("expected edge label 'done', got %q", e.Label)
	}
	if e.IsEscape {
		t.Fatal("expected IsEscape=false for regular edge")
	}

	// Status, Kind, etc. should be zero-values on workflow-only topology
	for _, n := range topo.Nodes {
		if n.Status != "" {
			t.Fatalf("expected empty status for workflow topology node %q, got %q", n.ID, n.Status)
		}
		if n.Kind != "" {
			t.Fatalf("expected empty kind for non-subworkflow node %q, got %q", n.ID, n.Kind)
		}
	}
}

func TestWorkflowTopology_NilWorkflow(t *testing.T) {
	topo := WorkflowTopology(nil)

	if topo.Nodes == nil {
		t.Fatal("expected non-nil Nodes slice")
	}
	if topo.Edges == nil {
		t.Fatal("expected non-nil Edges slice")
	}
	if len(topo.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(topo.Nodes))
	}
	if len(topo.Edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(topo.Edges))
	}
}

func TestWorkflowTopology_LoopExhaustedMetaEdge(t *testing.T) {
	// Verifies that a _loop_exhausted meta-decision edge appears as a regular
	// topology edge. The legacy LoopExhaustedTo field is removed; exhaustion
	// routing is declared via an explicit Edge{When: "_loop_exhausted"}.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "check"},
			{ID: "fallback"},
		},
		Edges: []definitions.Edge{
			{From: "check", To: "fallback", When: "done"},
			{From: "check", To: "fallback", When: "_loop_exhausted"},
		},
	}

	topo := WorkflowTopology(workflow)

	// Both edges should appear as regular topology edges.
	if len(topo.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(topo.Edges))
	}

	foundExhausted := false
	for _, e := range topo.Edges {
		if e.Label == "_loop_exhausted" {
			foundExhausted = true
			if e.Source != "check" || e.Target != "fallback" {
				t.Fatalf("expected _loop_exhausted edge check->fallback, got %s->%s", e.Source, e.Target)
			}
		}
	}
	if !foundExhausted {
		t.Fatal("expected a _loop_exhausted edge in topology")
	}
}

func TestWorkflowTopology_SubworkflowNode(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "start"},
			{ID: "sub_step", Kind: kindSubworkflow, Workflow: "child_workflow"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "sub_step"},
		},
	}

	topo := WorkflowTopology(workflow)

	if len(topo.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(topo.Nodes))
	}

	var subNode *TopologyNode
	for i, n := range topo.Nodes {
		if n.ID == "sub_step" {
			subNode = &topo.Nodes[i]
		}
	}
	if subNode == nil {
		t.Fatal("expected to find node 'sub_step'")
	}
	if subNode.Kind != kindSubworkflow {
		t.Fatalf("expected kind %q, got %q", kindSubworkflow, subNode.Kind)
	}
	if subNode.Workflow != "child_workflow" {
		t.Fatalf("expected workflow 'child_workflow', got %q", subNode.Workflow)
	}
}

func TestRunTopology_OverlaysState(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a"},
			{ID: "b"},
		},
		Edges: []definitions.Edge{
			{From: "a", To: "b"},
		},
	}
	run := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"a": {ID: "a", Status: statusCompleted, Attempts: 2, Decision: "approve"},
			"b": {ID: "b", Status: statusRunning, Attempts: 1},
		},
	}

	topo := RunTopology(workflow, run)

	if len(topo.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(topo.Nodes))
	}

	nodeMap := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		nodeMap[n.ID] = n
	}

	a := nodeMap["a"]
	if a.Status != statusCompleted {
		t.Fatalf("expected status %q for node a, got %q", statusCompleted, a.Status)
	}
	if a.Attempts != 2 {
		t.Fatalf("expected 2 attempts for node a, got %d", a.Attempts)
	}
	if a.Decision != "approve" {
		t.Fatalf("expected decision 'approve' for node a, got %q", a.Decision)
	}

	b := nodeMap["b"]
	if b.Status != statusRunning {
		t.Fatalf("expected status %q for node b, got %q", statusRunning, b.Status)
	}
}

func TestRunTopology_ForEachExpanded(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process", Kind: "system", ForEach: &definitions.ForEach{List: "input.items", Item: "item"}},
			{ID: "done"},
		},
		Edges: []definitions.Edge{
			{From: "process", To: "done"},
		},
	}
	run := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"process":    {ID: "process", Status: statusCompleted},
			"process::0": {ID: "process::0", Status: statusCompleted},
			"process::1": {ID: "process::1", Status: statusRunning},
		},
	}

	topo := RunTopology(workflow, run)

	// Should have: process, done, process::0, process::1 = 4 nodes
	if len(topo.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d: %+v", len(topo.Nodes), topo.Nodes)
	}

	for _, n := range topo.Nodes {
		if strings.HasPrefix(n.ID, "process::") {
			if n.Parent != "process" {
				t.Fatalf("expected parent 'process' for %s, got %q", n.ID, n.Parent)
			}
		}
	}
}

func TestRunTopology_NilRunState(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "a"},
		},
	}

	topo := RunTopology(workflow, nil)

	if len(topo.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(topo.Nodes))
	}
	// With nil run state, status should be pending
	if topo.Nodes[0].Status != statusPending {
		t.Fatalf("expected status %q, got %q", statusPending, topo.Nodes[0].Status)
	}
}

func TestCompoundWorkflowTopology_IncludesSubworkflows(t *testing.T) {
	bundle := &definitions.Bundle{
		Workflows: map[string]*definitions.Workflow{
			"parent_wf": {
				ID:   "parent_wf",
				Name: "Parent Workflow",
				Nodes: []definitions.Node{
					{ID: "init", Kind: "action"},
					{ID: "run_child", Kind: "subworkflow", Workflow: childWfID},
					{ID: "finish", Kind: "action"},
				},
				Edges: []definitions.Edge{
					{From: "init", To: "run_child"},
					{From: "run_child", To: "finish"},
				},
			},
			childWfID: {
				ID:   childWfID,
				Name: "Child Workflow",
				Nodes: []definitions.Node{
					{ID: "step_a", Kind: "action"},
					{ID: "step_b", Kind: "action"},
				},
				Edges: []definitions.Edge{
					{From: "step_a", To: "step_b"},
				},
			},
		},
	}

	topo := CompoundWorkflowTopology(bundle, "parent_wf")

	// 2 parent nodes + 3 children (parent_wf) + 2 children (child_wf) = 7 total
	parentCount := 0
	childCount := 0
	for _, n := range topo.Nodes {
		if n.Parent == "" {
			parentCount++
		} else {
			childCount++
		}
	}
	if parentCount != 2 {
		t.Fatalf("expected 2 parent nodes, got %d", parentCount)
	}
	if childCount != 5 {
		t.Fatalf("expected 5 child nodes, got %d", childCount)
	}

	// Selected workflow parent node should have Selected=true
	pwf := findTopologyNode(topo.Nodes, "parent_wf")
	if pwf == nil {
		t.Fatal("expected parent node parent_wf")
	}
	if !pwf.Selected {
		t.Fatal("expected parent_wf to have Selected=true")
	}

	// child_wf should NOT be selected, but should have Kind=subworkflow
	cwf := findTopologyNode(topo.Nodes, childWfID)
	if cwf == nil {
		t.Fatal("expected parent node child_wf")
	}
	if cwf.Selected {
		t.Fatal("expected child_wf to NOT have Selected=true")
	}
	if cwf.Kind != kindSubworkflow {
		t.Fatalf("expected child_wf kind %q, got %q", kindSubworkflow, cwf.Kind)
	}

	// Children should have correct Parent fields
	for _, id := range []string{"parent_wf::init", "parent_wf::run_child", "parent_wf::finish"} {
		n := findTopologyNode(topo.Nodes, id)
		if n == nil {
			t.Fatalf("expected child node %s", id)
		}
		if n.Parent != "parent_wf" {
			t.Fatalf("expected parent parent_wf for %s, got %q", id, n.Parent)
		}
	}
	for _, id := range []string{"child_wf::step_a", "child_wf::step_b"} {
		n := findTopologyNode(topo.Nodes, id)
		if n == nil {
			t.Fatalf("expected child node %s", id)
		}
		if n.Parent != childWfID {
			t.Fatalf("expected parent child_wf for %s, got %q", id, n.Parent)
		}
	}

	// Subworkflow child node should carry Kind and Workflow
	runChild := findTopologyNode(topo.Nodes, "parent_wf::run_child")
	if runChild == nil {
		t.Fatal("expected child node parent_wf::run_child")
	}
	if runChild.Kind != kindSubworkflow {
		t.Fatalf("expected kind %q for run_child, got %q", kindSubworkflow, runChild.Kind)
	}
	if runChild.Workflow != childWfID {
		t.Fatalf("expected workflow child_wf for run_child, got %q", runChild.Workflow)
	}

	// Should have inter-workflow edge from parent_wf::run_child -> child_wf
	foundInterEdge := false
	for _, e := range topo.Edges {
		if e.Source == "parent_wf::run_child" && e.Target == childWfID {
			foundInterEdge = true
		}
	}
	if !foundInterEdge {
		t.Fatal("expected inter-workflow edge parent_wf::run_child -> child_wf")
	}
}

func TestCompoundExecutionGroupTopology_BasicTree(t *testing.T) {
	wf := &definitions.Workflow{
		ID:   "wf-main",
		Name: "Main Workflow",
		Nodes: []definitions.Node{
			{ID: "build", Kind: "action"},
		},
	}

	rs := state.NewRunState(testRunID1, "wf-main", nil)
	rs.Status = statusRunning
	rs.WithNode("build", func(n *state.NodeState) {
		n.Status = statusRunning
		n.Attempts = 1
	})

	runs := []CompoundRunInput{
		{RunID: testRunID1, Title: "Main Run", Status: statusRunning, Workflow: wf, RunState: rs},
	}

	topo := CompoundExecutionGroupTopology(runs, testRunID1)

	// 1 parent node + 1 child node = 2 total
	if len(topo.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(topo.Nodes))
	}

	// Parent node should be marked Current
	parent := findTopologyNode(topo.Nodes, testRunID1)
	if parent == nil {
		t.Fatal("expected parent node run-1")
	}
	if !parent.Current {
		t.Fatal("expected parent node to have Current=true")
	}
	if parent.Status != statusRunning {
		t.Fatalf("expected parent status %q, got %q", statusRunning, parent.Status)
	}

	// Child node should have status, kind, and attempts
	child := findTopologyNode(topo.Nodes, "run-1::build")
	if child == nil {
		t.Fatal("expected child node run-1::build")
	}
	if child.Parent != testRunID1 {
		t.Fatalf("expected parent %q for child, got %q", testRunID1, child.Parent)
	}
	if child.Status != statusRunning {
		t.Fatalf("expected child status %q, got %q", statusRunning, child.Status)
	}
	if child.Attempts != 1 {
		t.Fatalf("expected child attempts 1, got %d", child.Attempts)
	}
}

func TestCompoundExecutionGroupTopology_ForEachInterRunEdge(t *testing.T) {
	// Reproduces a bug where a ForEach-expanded node (build_component::0)
	// spawns a child run. The inter-run edge source must reference the base
	// workflow node (run-1::build_component), not the expanded state node
	// (run-1::build_component::0), because only base nodes exist in the
	// topology node list.
	rootWf := &definitions.Workflow{
		ID: "implement_spec",
		Nodes: []definitions.Node{
			{ID: "build_component", Kind: "subworkflow", Workflow: "build_wf"},
		},
	}
	buildWf := &definitions.Workflow{
		ID:    "build_wf",
		Nodes: []definitions.Node{{ID: "plan"}},
	}

	rootRun := state.NewRunState(testRunID1, "implement_spec", nil)
	rootRun.WithNode("build_component::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]any{"child_run": "child-build"}
	})

	buildRun := state.NewRunState("child-build", "build_wf", nil)
	buildRun.ParentRun = testRunID1

	runs := []CompoundRunInput{
		{RunID: testRunID1, Title: "Root", Status: statusCompleted, Workflow: rootWf, RunState: rootRun},
		{RunID: "child-build", ParentRun: testRunID1, Title: "Build", Status: statusCompleted, Workflow: buildWf, RunState: buildRun},
	}

	topo := CompoundExecutionGroupTopology(runs, testRunID1)

	// Collect all node IDs to verify edge sources/targets exist.
	nodeIDs := map[string]bool{}
	for _, n := range topo.Nodes {
		nodeIDs[n.ID] = true
	}

	for _, edge := range topo.Edges {
		if !nodeIDs[edge.Source] {
			t.Errorf("edge %s references non-existent source node %q", edge.ID, edge.Source)
		}
		if !nodeIDs[edge.Target] {
			t.Errorf("edge %s references non-existent target node %q", edge.ID, edge.Target)
		}
	}

	// The inter-run edge should go from run-1::build_component (base node)
	// to child-build, NOT from run-1::build_component::0 (expanded node).
	expectedSource := testRunID1 + "::build_component"
	hasEdge := false
	for _, edge := range topo.Edges {
		if edge.Source == expectedSource && edge.Target == "child-build" {
			hasEdge = true
		}
	}
	if !hasEdge {
		t.Fatalf("expected inter-run edge %s -> child-build", expectedSource)
	}
}

func TestCompoundExecutionGroupTopology_ForEachMultiItemSiblingFlowEdges(t *testing.T) {
	// ForEach expands build_component into build_component::0, ::1, ::2 and
	// integrate_components into integrate_components::0, ::1, ::2. Each
	// expansion spawns a child run. Workflow edge build_component ->
	// integrate_components must produce sibling flow edges build-item-N ->
	// integrate-item-N (index-paired). The graph build is looped below so
	// Go map iteration non-determinism would surface if the index-pairing
	// sort were ever removed.
	rootWf := &definitions.Workflow{
		ID: "implement_spec",
		Nodes: []definitions.Node{
			{ID: "build_component"},
			{ID: "integrate_components"},
		},
		Edges: []definitions.Edge{
			{From: "build_component", To: "integrate_components"},
		},
	}
	buildWf := &definitions.Workflow{
		ID:    "build_wf",
		Nodes: []definitions.Node{{ID: "compile"}},
	}
	integrateWf := &definitions.Workflow{
		ID:    "integrate_wf",
		Nodes: []definitions.Node{{ID: "merge_branch"}},
	}

	rootRun := state.NewRunState("root", "implement_spec", nil)
	for i := 0; i < 3; i++ {
		idx := i
		rootRun.WithNode(fmt.Sprintf("build_component::%d", idx), func(n *state.NodeState) {
			n.Status = statusCompleted
			n.Data = map[string]any{"child_run": fmt.Sprintf("build-item-%d", idx)}
		})
		rootRun.WithNode(fmt.Sprintf("integrate_components::%d", idx), func(n *state.NodeState) {
			n.Status = statusCompleted
			n.Data = map[string]any{"child_run": fmt.Sprintf("integrate-item-%d", idx)}
		})
	}

	runs := []CompoundRunInput{
		{RunID: "root", Title: "Root", Status: statusCompleted, Workflow: rootWf, RunState: rootRun},
	}
	for i := 0; i < 3; i++ {
		idx := i
		runs = append(runs, CompoundRunInput{
			RunID:     fmt.Sprintf("build-item-%d", idx),
			ParentRun: "root",
			Title:     fmt.Sprintf("Build %d", idx),
			Status:    statusCompleted,
			Workflow:  buildWf,
		})
		runs = append(runs, CompoundRunInput{
			RunID:     fmt.Sprintf("integrate-item-%d", idx),
			ParentRun: "root",
			Title:     fmt.Sprintf("Integrate %d", idx),
			Status:    statusCompleted,
			Workflow:  integrateWf,
		})
	}

	for iter := 0; iter < 50; iter++ {
		topo := CompoundExecutionGroupTopology(runs, "root")

		hasEdge := func(source, target string) bool {
			for _, edge := range topo.Edges {
				if edge.Source == source && edge.Target == target {
					return true
				}
			}
			return false
		}

		// Each build-item-N must pair with integrate-item-N (same index).
		for i := 0; i < 3; i++ {
			build := fmt.Sprintf("build-item-%d", i)
			integrate := fmt.Sprintf("integrate-item-%d", i)
			if !hasEdge(build, integrate) {
				t.Errorf("iter %d: expected sibling flow edge %s -> %s", iter, build, integrate)
			}
			// Cross-index edges must NOT exist.
			for j := 0; j < 3; j++ {
				if j == i {
					continue
				}
				wrongIntegrate := fmt.Sprintf("integrate-item-%d", j)
				if hasEdge(build, wrongIntegrate) {
					t.Errorf("iter %d: unexpected cross-index edge %s -> %s", iter, build, wrongIntegrate)
				}
			}
		}
	}
}

func TestRunTopology_ForEachExpandedParentedToOrchestrator(t *testing.T) {
	// Post-migration form: orchestrator has for_each.body = "template".
	// Expanded items are keyed as "template::N" in run state.
	// RunTopology must parent them under the orchestrator, not the template.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{
				ID:      "process_items",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "item_worker"},
			},
			{ID: "item_worker", Kind: "action"},
			{ID: "done"},
		},
		Edges: []definitions.Edge{
			{From: "process_items", To: "done"},
		},
	}
	run := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"process_items":  {ID: "process_items", Status: statusRunning},
			"item_worker":    {ID: "item_worker", Status: statusPending},
			"done":           {ID: "done", Status: statusPending},
			"item_worker::0": {ID: "item_worker::0", Status: statusCompleted},
			"item_worker::1": {ID: "item_worker::1", Status: statusRunning},
		},
	}

	topo := RunTopology(workflow, run)

	nodeByID := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		nodeByID[n.ID] = n
	}

	for _, id := range []string{"item_worker::0", "item_worker::1"} {
		n, ok := nodeByID[id]
		if !ok {
			t.Fatalf("expected expanded node %s in topology", id)
		}
		if n.Parent != "process_items" {
			t.Fatalf("expanded item %s: expected parent %q (orchestrator), got %q", id, "process_items", n.Parent)
		}
	}
}

func TestCompoundExecutionGroupTopology_ForEachSiblingFlowEdge(t *testing.T) {
	// Reproduces the implement_spec.yaml scenario with the post-migration form:
	// orchestrator (build_component) has for_each.body = template (bc_template).
	// A ForEach expansion (bc_template::0) spawns a child run. Workflow edges
	// reference orchestrator IDs (build_component → integrate_components),
	// so the sibling-flow edge must resolve via the orchestrator key.
	rootWf := &definitions.Workflow{
		ID: "implement_spec",
		Nodes: []definitions.Node{
			{
				ID:      "build_component",
				ForEach: &definitions.ForEach{List: "input.components", Item: "c", Body: "bc_template"},
			},
			{ID: "bc_template", Kind: "subworkflow", Workflow: "build_wf"},
			{
				ID:      "integrate_components",
				ForEach: &definitions.ForEach{List: "input.components", Item: "c", Body: "ic_template"},
			},
			{ID: "ic_template", Kind: "subworkflow", Workflow: "integrate_wf"},
		},
		Edges: []definitions.Edge{
			{From: "build_component", To: "integrate_components"},
		},
	}
	buildWf := &definitions.Workflow{
		ID:    "build_wf",
		Nodes: []definitions.Node{{ID: "compile"}},
	}
	integrateWf := &definitions.Workflow{
		ID:    "integrate_wf",
		Nodes: []definitions.Node{{ID: "merge_branch"}},
	}

	rootRun := state.NewRunState("root", "implement_spec", nil)
	rootRun.WithNode("bc_template::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]any{"child_run": "child-build"}
	})
	rootRun.WithNode("ic_template::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]any{"child_run": "child-integrate"}
	})

	buildRun := state.NewRunState("child-build", "build_wf", nil)
	buildRun.ParentRun = "root"
	integrateRun := state.NewRunState("child-integrate", "integrate_wf", nil)
	integrateRun.ParentRun = "root"

	runs := []CompoundRunInput{
		{RunID: "root", Title: "Root", Status: statusCompleted, Workflow: rootWf, RunState: rootRun},
		{RunID: "child-build", ParentRun: "root", Title: "Build", Status: statusCompleted, Workflow: buildWf, RunState: buildRun},
		{RunID: "child-integrate", ParentRun: "root", Title: "Integrate", Status: statusCompleted, Workflow: integrateWf, RunState: integrateRun},
	}

	topo := CompoundExecutionGroupTopology(runs, "root")

	hasEdge := func(source, target string) bool {
		for _, e := range topo.Edges {
			if e.Source == source && e.Target == target {
				return true
			}
		}
		return false
	}

	if !hasEdge("child-build", "child-integrate") {
		t.Fatal("expected sibling flow edge child-build -> child-integrate in compound topology")
	}

	hasEdgeToChildBuild := false
	for _, e := range topo.Edges {
		if e.Target == "child-build" {
			hasEdgeToChildBuild = true
		}
	}
	if !hasEdgeToChildBuild {
		t.Fatal("expected an inter-run edge targeting child-build")
	}

	for _, e := range topo.Edges {
		if e.Target == "child-integrate" && e.Source != "child-build" {
			t.Fatalf("inter-run edge -> child-integrate should be suppressed (sibling flow target), got source %q", e.Source)
		}
	}
}

func TestRunTopology_ForEachExpansionsNumericSortOrder(t *testing.T) {
	// Regression guard: expanded items {prefix}::{N} must appear in
	// natural numeric order. sort.Strings would put tmpl::10 before
	// tmpl::2; we want tmpl::2 first.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "orch", ForEach: &definitions.ForEach{List: "input.x", Item: "i", Body: "tmpl"}},
			{ID: "tmpl", Kind: "system"},
		},
	}
	run := &state.RunState{
		Nodes: map[string]*state.NodeState{},
	}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("tmpl::%d", i)
		run.Nodes[id] = &state.NodeState{ID: id, Status: statusCompleted}
	}

	topo := RunTopology(workflow, run)

	// Collect the order of tmpl::N nodes as they appear in topo.Nodes.
	var seenIDs []string
	for _, n := range topo.Nodes {
		if strings.HasPrefix(n.ID, "tmpl::") {
			seenIDs = append(seenIDs, n.ID)
		}
	}
	want := []string{
		"tmpl::0", "tmpl::1", "tmpl::2", "tmpl::3", "tmpl::4", "tmpl::5",
		"tmpl::6", "tmpl::7", "tmpl::8", "tmpl::9", "tmpl::10", "tmpl::11",
	}
	if len(seenIDs) != len(want) {
		t.Fatalf("expected %d expansion nodes, got %d: %v", len(want), len(seenIDs), seenIDs)
	}
	for i, got := range seenIDs {
		if got != want[i] {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, got, want[i], seenIDs)
		}
	}
}

func TestClassifyNode(t *testing.T) {
	wf := &definitions.Workflow{Nodes: []definitions.Node{
		{ID: "runner_node", Role: "dev"},
		{ID: "system_node", Kind: "system"},
		{ID: "sub_plain", Kind: kindSubworkflow, Workflow: "child_wf"},
		{ID: "sub_foreach", Kind: kindSubworkflow, Workflow: "child_wf", ForEach: &definitions.ForEach{List: "input.items", Item: "it"}},
	}}
	cases := []struct {
		nodeID string
		want   string
	}{
		{"runner_node", kindLeafRole},
		{"system_node", kindLeafSystem},
		{"sub_plain", kindSubworkflow},
		{"sub_foreach", kindSubworkflowForeach},
		{"sub_foreach::0", kindForeachIteration},
		{"missing_node", ""},
	}
	for _, tc := range cases {
		got := ClassifyNode(wf, tc.nodeID)
		if got != tc.want {
			t.Errorf("ClassifyNode(%q) = %q; want %q", tc.nodeID, got, tc.want)
		}
	}
}

func TestRunTopology_ClassifiesKinds(t *testing.T) {
	wf := &definitions.Workflow{Nodes: []definitions.Node{
		{ID: "dev", Role: "dev"},
		{ID: "prep", Kind: "system"},
		{ID: "sub", Kind: kindSubworkflow, Workflow: "child"},
	}}
	rs := state.NewRunState("r1", "wf", nil)
	topo := RunTopology(wf, rs)
	byID := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}
	if got := byID["dev"].Kind; got != kindLeafRole {
		t.Errorf("dev.Kind = %q, want %q", got, kindLeafRole)
	}
	if got := byID["prep"].Kind; got != kindLeafSystem {
		t.Errorf("prep.Kind = %q, want %q", got, kindLeafSystem)
	}
	if got := byID["sub"].Kind; got != kindSubworkflow {
		t.Errorf("sub.Kind = %q, want %q", got, kindSubworkflow)
	}
}

func TestRunTopology_PropagatesNodeTags(t *testing.T) {
	// Tags are materialized onto NodeState.Tags by the engine at
	// emit time. Topology simply copies them through so graph
	// renderers can style tagged decisions (e.g. "override" → amber
	// border) without duplicating the tag-vocabulary in Go.
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "forced"},
			{ID: "audit"},
			{ID: "approved"},
			{ID: "untagged"},
		},
	}
	run := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"forced":   {ID: "forced", Status: statusCompleted, Decision: "force_approve", Tags: []string{"override"}},
			"audit":    {ID: "audit", Status: statusCompleted, Decision: "done", Tags: []string{"audit", "override"}},
			"approved": {ID: "approved", Status: statusCompleted, Decision: "approved"},
			"untagged": {ID: "untagged", Status: statusCompleted, Decision: "force_approve"},
		},
	}

	topo := RunTopology(workflow, run)

	byID := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}

	assertHasTag := func(t *testing.T, tags []string, want string) {
		t.Helper()
		for _, got := range tags {
			if got == want {
				return
			}
		}
		t.Errorf("tag %q missing from %v", want, tags)
	}

	assertHasTag(t, byID["forced"].Tags, "override")

	auditTags := byID["audit"].Tags
	if len(auditTags) != 2 {
		t.Errorf("audit node should carry both tags, got %v", auditTags)
	}

	if len(byID["approved"].Tags) != 0 {
		t.Errorf("untagged decision must not gain tags, got %v", byID["approved"].Tags)
	}

	// Crucial: a node whose decision name happens to be "force_approve"
	// but lacks tags on its NodeState must not be marked. The harness
	// has no hardcoded knowledge of decision semantics.
	if len(byID["untagged"].Tags) != 0 {
		t.Errorf("decision name alone must not imply tags, got %v", byID["untagged"].Tags)
	}
}

func TestCompoundExecutionGroupTopology_EmitsForeachIterations(t *testing.T) {
	wf := &definitions.Workflow{Nodes: []definitions.Node{
		{ID: "dispatch", Kind: kindSubworkflow, Workflow: "child", ForEach: &definitions.ForEach{List: "input.items", Item: "it"}},
	}}
	rs := state.NewRunState("r1", "wf", nil)
	rs.WithNode("dispatch::0", func(n *state.NodeState) {
		n.Status = "completed"
		n.Data = map[string]any{"child_run": "child-1"}
	})
	rs.WithNode("dispatch::1", func(n *state.NodeState) {
		n.Status = "running"
		n.Data = map[string]any{"child_run": "child-2"}
	})

	runs := []CompoundRunInput{{
		RunID:    "r1",
		Title:    "Parent",
		Status:   "running",
		Workflow: wf,
		RunState: rs,
	}}
	topo := CompoundExecutionGroupTopology(runs, "r1")

	byID := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}
	disp, ok := byID["r1::dispatch"]
	if !ok {
		ids := make([]string, 0, len(topo.Nodes))
		for _, n := range topo.Nodes {
			ids = append(ids, n.ID)
		}
		t.Fatalf("missing dispatch node; got IDs: %v", ids)
	}
	if disp.Kind != kindSubworkflowForeach {
		t.Errorf("dispatch.Kind = %q, want %q", disp.Kind, kindSubworkflowForeach)
	}
	for i, suffix := range []string{"::0", "::1"} {
		id := "r1::dispatch" + suffix
		iter, ok := byID[id]
		if !ok {
			t.Fatalf("missing iteration %s", id)
		}
		if iter.Parent != "r1::dispatch" {
			t.Errorf("%s Parent = %q, want r1::dispatch", id, iter.Parent)
		}
		if iter.Kind != kindForeachIteration {
			t.Errorf("%s Kind = %q, want %q", id, iter.Kind, kindForeachIteration)
		}
		wantLabel := "iteration " + strconv.Itoa(i)
		if iter.Label != wantLabel {
			t.Errorf("%s Label = %q, want %q", id, iter.Label, wantLabel)
		}
	}
}

func TestRunTopology_PopulatesMetricsWhenCollectorProvided(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "n"},
		},
	}
	rs := state.NewRunState("r1", "wf", nil)
	rs.WithNode("n", func(ns *state.NodeState) { ns.Status = "completed" })

	c := metrics.NewCollector()
	t0 := time.Unix(0, 0)
	c.ProcessEvent(state.Event{Type: "node_started", NodeID: "n", Timestamp: t0})
	c.ProcessEvent(state.Event{Type: "node_completed", NodeID: "n", Timestamp: t0.Add(500 * time.Millisecond)})

	topo := RunTopologyWithMetrics(workflow, rs, c)

	node := findTopologyNode(topo.Nodes, "n")
	if node == nil {
		t.Fatal("expected node 'n' in topology")
	}
	if node.Metrics == nil {
		t.Fatal("expected Metrics to be non-nil when collector is provided")
	}
	if node.Metrics.DurationMs != 500 {
		t.Errorf("DurationMs = %d, want 500", node.Metrics.DurationMs)
	}
	// No tokens consumed, so Rollup should be nil (rollup == own).
	if node.Metrics.Rollup != nil {
		t.Errorf("expected nil Rollup when rollup equals own, got %+v", node.Metrics.Rollup)
	}
}

func TestRunTopology_NoMetricsWhenCollectorOmitted(t *testing.T) {
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "n"},
		},
	}
	rs := state.NewRunState("r1", "wf", nil)
	rs.WithNode("n", func(ns *state.NodeState) { ns.Status = "completed" })

	topo := RunTopologyWithMetrics(workflow, rs, nil)

	node := findTopologyNode(topo.Nodes, "n")
	if node == nil {
		t.Fatal("expected node 'n' in topology")
	}
	if node.Metrics != nil {
		t.Errorf("expected Metrics to be nil when collector is nil, got %+v", node.Metrics)
	}
}

func TestCompoundExecutionGroupTopology_WithMetrics(t *testing.T) {
	parentWf := &definitions.Workflow{ID: "p", Nodes: []definitions.Node{{ID: "a"}}}
	childWf := &definitions.Workflow{ID: "c", Nodes: []definitions.Node{{ID: "b"}}}
	parentRS := state.NewRunState("r_p", "p", nil)
	childRS := state.NewRunState("r_c", "c", nil)

	parentC := metrics.NewCollector()
	parentC.ProcessEvent(state.Event{
		Type: "node_output", NodeID: "a",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5.4","usage":{"input_tokens":100,"output_tokens":0}}}`,
	})
	childC := metrics.NewCollector()
	childC.ProcessEvent(state.Event{
		Type: "node_output", NodeID: "b",
		Text: `{"kind":"ASSISTANT_TEXT_END","data":{"model":"gpt-5.4","usage":{"input_tokens":200,"output_tokens":0}}}`,
	})

	inputs := []CompoundRunInput{
		{RunID: "r_p", Workflow: parentWf, RunState: parentRS, Collector: parentC},
		{RunID: "r_c", Workflow: childWf, RunState: childRS, ParentRun: "r_p", Collector: childC},
	}
	topo := CompoundExecutionGroupTopology(inputs, "r_p")

	byID := map[string]TopologyNode{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}
	if m := byID["r_p::a"].Metrics; m == nil || m.TokensTotal != 100 {
		t.Errorf("parent node a: got %+v, want TokensTotal=100", m)
	}
	if m := byID["r_c::b"].Metrics; m == nil || m.TokensTotal != 200 {
		t.Errorf("child node b: got %+v, want TokensTotal=200", m)
	}
}
