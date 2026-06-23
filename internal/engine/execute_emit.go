package engine

import (
	"context"
	"fmt"

	"primeradiant.com/toil/internal/definitions"
)

// executeEmit dispatches a kind:emit node. Runs the 5-phase pipeline:
// evaluatePhase1 on node.Inputs, merges with optional edge passes,
// then resolves output.Message and output.Data against the merged
// dispatch context. output.Decision is a literal (validated at load
// time to be in the node's Decisions list); no resolution.
//
// Emit nodes do not invoke a runner. They produce a deterministic
// envelope from their inputs and output template. The returned
// NodeOutput is then routed through the standard applyOutput path
// by the caller (executeNode).
func executeEmit(ctx context.Context, node *definitions.Node, runContext *RunContext, edgePasses map[string]any) (NodeOutput, error) {
	if node.Output == nil {
		return NodeOutput{}, fmt.Errorf("emit node %s: missing output: block", node.ID)
	}

	// Phase 1: evaluate node inputs.
	nodeInputs, err := evaluatePhase1(runContext, node.Inputs)
	if err != nil {
		return NodeOutput{}, fmt.Errorf("emit node %s: %w", node.ID, err)
	}

	// Phase 3: merge.
	merged := mergeDispatchInputs(runContext.Inputs, nodeInputs, edgePasses)

	// Phase 4: expose merged map as ${input.X}.
	dispatchCtx := dispatchContext(runContext, merged)

	// Phase 5: resolve output.Message (template) and output.Data
	// (recursive evaluatePhase1 against the dispatch context).
	msgRaw, err := dispatchCtx.Resolve(node.Output.Message)
	if err != nil {
		return NodeOutput{}, fmt.Errorf("emit node %s output.message: %w", node.ID, err)
	}
	msg, ok := msgRaw.(string)
	if !ok {
		msg = fmt.Sprintf("%v", msgRaw)
	}
	data, err := evaluatePhase1(dispatchCtx, node.Output.Data)
	if err != nil {
		return NodeOutput{}, fmt.Errorf("emit node %s output.data: %w", node.ID, err)
	}

	return NodeOutput{
		Decision: node.Output.Decision,
		Message:  msg,
		Data:     data,
	}, nil
}
