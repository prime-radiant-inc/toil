package engine

import (
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestSynthesizeMetaCompletionRetryExhausted(t *testing.T) {
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "worker"},
			{ID: "recover"},
		},
		Edges: []definitions.Edge{
			{From: "worker", To: "recover", When: "_retry_exhausted"},
		},
	}
	rs := state.NewRunState("r2", "tw", map[string]any{})
	rs.Nodes["worker"] = &state.NodeState{
		ID: "worker", Status: "failed", Decision: "",
		Message: "every attempt failed", RetryCount: 3,
	}
	ctx := &RunContext{
		RunID: "r2",
		Outputs: map[string]NodeOutput{
			"worker": {Decision: "", Message: "every attempt failed"},
		},
	}
	dir := t.TempDir()
	logger, err := state.NewLogger(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()
	var ready []readyNode
	arrived := map[string]map[string]bool{}
	incoming := map[string]int{}
	fired := map[string]bool{}
	if err := synthesizeMetaCompletion(
		"r2", wf, ctx, rs, logger, "worker", MetaDecisionRetryExhausted,
		&ready, arrived, incoming, fired,
	); err != nil {
		t.Fatalf("synthesizeMetaCompletion: %v", err)
	}

	// Real envelope preserved (Decision was empty, stays empty).
	if rs.Nodes["worker"].Decision != "" {
		t.Errorf("Decision=%q want preserved empty", rs.Nodes["worker"].Decision)
	}
	// Routing decision recorded.
	if rs.Nodes["worker"].LastRoutingDecision != MetaDecisionRetryExhausted {
		t.Errorf("LastRoutingDecision=%q want %q", rs.Nodes["worker"].LastRoutingDecision, MetaDecisionRetryExhausted)
	}
	// Downstream edge fired.
	if len(ready) != 1 || ready[0].ID != "recover" {
		t.Fatalf("ready=%v want [{recover}]", ready)
	}
	// runContext.Outputs surfaces the routing metadata.
	if ctx.Outputs["worker"].LastRoutingDecision != MetaDecisionRetryExhausted {
		t.Errorf("Outputs[worker].LastRoutingDecision=%q want %q", ctx.Outputs["worker"].LastRoutingDecision, MetaDecisionRetryExhausted)
	}
	// Original message preserved.
	if ctx.Outputs["worker"].Message != "every attempt failed" {
		t.Errorf("Outputs[worker].Message=%q want preserved \"every attempt failed\"", ctx.Outputs["worker"].Message)
	}
}

func TestSynthesizeMetaCompletionRoutesAndPreservesEnvelope(t *testing.T) {
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "x"},
			{ID: "stuck"},
		},
		Edges: []definitions.Edge{
			{
				From: "x", To: "stuck", When: "_loop_exhausted",
				Passes: map[string]any{"last_msg": "${node.x.message}"},
			},
		},
	}
	rs := state.NewRunState("r1", "tw", map[string]any{})
	rs.Nodes["x"] = &state.NodeState{
		ID: "x", Status: "completed", Decision: "fix_failed",
		Message: "last attempt failed", LoopIterations: 5,
	}
	ctx := &RunContext{
		RunID: "r1",
		Outputs: map[string]NodeOutput{
			"x": {Decision: "fix_failed", Message: "last attempt failed", LoopIterations: 5},
		},
	}
	dir := t.TempDir()
	logger, err := state.NewLogger(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()
	var ready []readyNode
	arrived := map[string]map[string]bool{}
	incoming := map[string]int{}
	fired := map[string]bool{}
	if err := synthesizeMetaCompletion(
		"r1", wf, ctx, rs, logger, "x", MetaDecisionLoopExhausted,
		&ready, arrived, incoming, fired,
	); err != nil {
		t.Fatalf("synthesizeMetaCompletion: %v", err)
	}

	// Real envelope preserved.
	if rs.Nodes["x"].Decision != "fix_failed" {
		t.Errorf("Decision=%q want preserved fix_failed", rs.Nodes["x"].Decision)
	}
	// Routing decision recorded.
	if rs.Nodes["x"].LastRoutingDecision != MetaDecisionLoopExhausted {
		t.Errorf("LastRoutingDecision=%q want %q", rs.Nodes["x"].LastRoutingDecision, MetaDecisionLoopExhausted)
	}
	// LoopIterations preserved (not reset).
	if rs.Nodes["x"].LoopIterations != 5 {
		t.Errorf("LoopIterations=%d want preserved 5", rs.Nodes["x"].LoopIterations)
	}
	// Downstream edge fired.
	if len(ready) != 1 || ready[0].ID != "stuck" {
		t.Fatalf("ready=%v want [{stuck}]", ready)
	}
	// Passes were evaluated from the real envelope.
	if got := ready[0].Passes["last_msg"]; got != "last attempt failed" {
		t.Errorf("Passes[last_msg]=%v want \"last attempt failed\"", got)
	}

	// runContext.Outputs[x] now also surfaces the routing metadata so
	// ${node.x.last_routing_decision} and ${node.x.loop_iterations} resolve.
	if ctx.Outputs["x"].LastRoutingDecision != MetaDecisionLoopExhausted {
		t.Errorf("Outputs[x].LastRoutingDecision=%q want %q", ctx.Outputs["x"].LastRoutingDecision, MetaDecisionLoopExhausted)
	}
	if ctx.Outputs["x"].LoopIterations != 5 {
		t.Errorf("Outputs[x].LoopIterations=%d want 5", ctx.Outputs["x"].LoopIterations)
	}
	// And the real fields are still preserved.
	if ctx.Outputs["x"].Message != "last attempt failed" {
		t.Errorf("Outputs[x].Message=%q want preserved \"last attempt failed\"", ctx.Outputs["x"].Message)
	}
}
