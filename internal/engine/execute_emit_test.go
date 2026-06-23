package engine

import (
	"context"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestExecuteEmitSynthesizesOutput(t *testing.T) {
	node := &definitions.Node{
		ID:   "agg",
		Kind: "emit",
		Inputs: map[string]any{
			"x_msg": "${node.x.message}",
		},
		Output: &definitions.EmitOutput{
			Decision: "escalate",
			Message:  "summary: ${input.x_msg}",
			Data: map[string]any{
				"last_x": "${input.x_msg}",
				"count":  3,
			},
		},
		Decisions: definitions.DecisionList{{ID: "escalate"}},
	}
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{
			"x": {Decision: "ok", Message: "hello from x"},
		},
	}
	out, err := executeEmit(context.Background(), node, ctx, nil)
	if err != nil {
		t.Fatalf("executeEmit: %v", err)
	}
	if out.Decision != "escalate" {
		t.Errorf("Decision=%q want escalate", out.Decision)
	}
	if out.Message != "summary: hello from x" {
		t.Errorf("Message=%q want \"summary: hello from x\"", out.Message)
	}
	if got := out.Data["last_x"]; got != "hello from x" {
		t.Errorf("Data[last_x]=%v want \"hello from x\"", got)
	}
	if got := out.Data["count"]; got != 3 {
		t.Errorf("Data[count]=%v want 3", got)
	}
}

func TestExecuteEmitWithEdgePasses(t *testing.T) {
	// Edge passes layer on top of node inputs (Phase 3).
	node := &definitions.Node{
		ID:   "agg",
		Kind: "emit",
		Inputs: map[string]any{
			"base": "from_node",
		},
		Output: &definitions.EmitOutput{
			Decision: "ok",
			Message:  "base=${input.base} extra=${input.extra}",
			Data:     map[string]any{},
		},
		Decisions: definitions.DecisionList{{ID: "ok"}},
	}
	ctx := &RunContext{
		Outputs: map[string]NodeOutput{},
	}
	edgePasses := map[string]any{
		"extra": "from_edge",
		"base":  "overridden_by_edge", // edge wins per merge precedence
	}
	out, err := executeEmit(context.Background(), node, ctx, edgePasses)
	if err != nil {
		t.Fatalf("executeEmit: %v", err)
	}
	if out.Message != "base=overridden_by_edge extra=from_edge" {
		t.Errorf("Message=%q", out.Message)
	}
}

func TestExecuteEmitMissingOutputBlock(t *testing.T) {
	node := &definitions.Node{
		ID:   "agg",
		Kind: "emit",
		// Output: nil
	}
	ctx := &RunContext{}
	_, err := executeEmit(context.Background(), node, ctx, nil)
	if err == nil {
		t.Fatal("expected error for missing output block")
	}
}
