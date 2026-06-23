package engine

import (
	"reflect"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestApplyOutputRoutingCapturesPasses(t *testing.T) {
	wf := &definitions.Workflow{
		Edges: []definitions.Edge{
			{
				From: "a", To: "b", When: "ok",
				Passes: map[string]any{"k": "${node.a.message}"},
			},
		},
	}
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"a": {Decision: "ok", Message: "hello"},
		},
	}
	rs := state.NewRunState("r1", "tw", map[string]any{})
	var ready []readyNode
	arrived := map[string]map[string]bool{}
	incoming := map[string]int{}
	fired := map[string]bool{}
	if err := applyOutputRouting(wf, ctx, rs, "a", "ok", &ready, arrived, incoming, fired, nil, ""); err != nil {
		t.Fatalf("applyOutputRouting: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("want 1 ready entry, got %d", len(ready))
	}
	if ready[0].EdgeIndex != 0 {
		t.Errorf("EdgeIndex=%d want 0", ready[0].EdgeIndex)
	}
	want := map[string]any{"k": "hello"}
	if !reflect.DeepEqual(ready[0].Passes, want) {
		t.Errorf("Passes=%v want %v", ready[0].Passes, want)
	}
}

func TestApplyOutputRoutingJoinPasses(t *testing.T) {
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "j", Join: "all"},
		},
		Edges: []definitions.Edge{
			{
				From: "a", To: "j", When: "ok",
				Passes: map[string]any{"a_msg": "${node.a.message}"},
			},
			{
				From: "b", To: "j", When: "ok",
				Passes: map[string]any{"b_msg": "${node.b.message}"},
			},
		},
	}
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"a": {Decision: "ok", Message: "hi-a"},
			"b": {Decision: "ok", Message: "hi-b"},
		},
	}
	rs := state.NewRunState("r1", "tw", map[string]any{})
	var ready []readyNode
	arrived := map[string]map[string]bool{}
	incoming := map[string]int{"j": 2}
	fired := map[string]bool{}
	if err := applyOutputRouting(wf, ctx, rs, "a", "ok", &ready, arrived, incoming, fired, nil, ""); err != nil {
		t.Fatalf("applyOutputRouting(a): %v", err)
	}
	if err := applyOutputRouting(wf, ctx, rs, "b", "ok", &ready, arrived, incoming, fired, nil, ""); err != nil {
		t.Fatalf("applyOutputRouting(b): %v", err)
	}
	// Join fires only after second arrival.
	if len(ready) != 1 || ready[0].ID != "j" {
		t.Fatalf("want 1 ready=j, got %v", ready)
	}
	// Per-edge passes are in JoinState.
	js := rs.JoinState["j"]
	if js == nil {
		t.Fatalf("JoinState[j] not set")
	}
	if got := js.Passes[0]["a_msg"]; got != "hi-a" {
		t.Errorf("Passes[0][a_msg]=%v want hi-a", got)
	}
	if got := js.Passes[1]["b_msg"]; got != "hi-b" {
		t.Errorf("Passes[1][b_msg]=%v want hi-b", got)
	}
}

func TestFailureEdgePassesEvaluated(t *testing.T) {
	// A node fails; the failure edge has a passes: block referencing
	// ${node.<failed>.message}. The recovery node's readyNode entry
	// must carry the evaluated passes.
	//
	// We can't easily test the full run-loop path here without a lot
	// of setup; instead, exercise the underlying evaluation directly
	// by simulating the failure-block code path: write a synthetic
	// output to runContext.Outputs, then verify evaluatePhase2 resolves
	// the passes correctly.
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"a": {
				Decision: "",
				Message:  "boom",
				Status:   "failed",
				Data:     map[string]any{"error_kind": "panic"},
			},
		},
	}
	passes := map[string]any{
		"recovery_msg":  "${node.a.message}",
		"recovery_kind": "${node.a.data.error_kind}",
	}
	evaluated, err := evaluatePhase2(ctx, passes)
	if err != nil {
		t.Fatalf("evaluatePhase2: %v", err)
	}
	if evaluated["recovery_msg"] != "boom" {
		t.Errorf("recovery_msg=%v want boom", evaluated["recovery_msg"])
	}
	if evaluated["recovery_kind"] != "panic" {
		t.Errorf("recovery_kind=%v want panic", evaluated["recovery_kind"])
	}
}

func TestMergeJoinPasses(t *testing.T) {
	js := &state.JoinNodeState{
		Passes: map[int]map[string]any{
			2: {"shared": "from_low", "low_only": 1},
			5: {"shared": "from_high", "high_only": 2},
		},
	}
	got := mergeJoinPasses(js)
	want := map[string]any{
		"shared":    "from_high", // higher edge index wins
		"low_only":  1,
		"high_only": 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}

	// Nil/empty JoinState returns nil.
	if got := mergeJoinPasses(nil); got != nil {
		t.Errorf("nil js: got %v want nil", got)
	}
	if got := mergeJoinPasses(&state.JoinNodeState{}); got != nil {
		t.Errorf("empty js: got %v want nil", got)
	}
}
