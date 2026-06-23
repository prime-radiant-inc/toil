package definitions

import "testing"

func TestSCCSelfLoop(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{{ID: "a"}},
		Edges: []Edge{{From: "a", To: "a", When: "ok"}},
	}
	if !nodeInLoopableScc(w, "a") {
		t.Fatalf("self-loop node a should be in loopable SCC")
	}
}

func TestSCCTwoNodeCycle(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{{ID: "a"}, {ID: "b"}},
		Edges: []Edge{
			{From: "a", To: "b", When: "ok"},
			{From: "b", To: "a", When: "fail"},
		},
	}
	if !nodeInLoopableScc(w, "a") {
		t.Fatalf("a should be in loopable SCC with b")
	}
	if !nodeInLoopableScc(w, "b") {
		t.Fatalf("b should be in loopable SCC with a")
	}
}

func TestSCCStraightLine(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []Edge{
			{From: "a", To: "b", When: "ok"},
			{From: "b", To: "c", When: "ok"},
		},
	}
	if nodeInLoopableScc(w, "a") {
		t.Fatalf("straight-line node a should NOT be in loopable SCC")
	}
}

func TestSCCExcludesLoopExhaustedEdge(t *testing.T) {
	w := &Workflow{
		Nodes: []Node{{ID: "a"}, {ID: "b"}, {ID: "stuck"}},
		Edges: []Edge{
			{From: "a", To: "b", When: "ok"},
			{From: "b", To: "a", When: "retry"},
			{From: "a", To: "stuck", When: "_loop_exhausted"},
		},
	}
	if !nodeInLoopableScc(w, "a") {
		t.Fatalf("a should be in loopable SCC despite presence of _loop_exhausted edge")
	}
}
