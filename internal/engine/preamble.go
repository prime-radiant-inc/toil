package engine

import (
	"fmt"
	"sort"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

const (
	preambleTruncateLen = 200
	preambleMaxNodes    = 50
)

// resolveContextMode returns the effective context mode for a node.
// Node.Context takes precedence; falls back to Workflow.ContextDefault;
// defaults to "full" when both are empty.
func resolveContextMode(node *definitions.Node, workflow *definitions.Workflow) string {
	if node.Context != "" {
		return node.Context
	}
	if workflow.ContextDefault != "" {
		return workflow.ContextDefault
	}
	return contextModeFull
}

// buildContextPreamble generates a machine-readable preamble summarising
// completed node outputs. Used by compact and summary context modes.
func buildContextPreamble(mode string, workflow *definitions.Workflow, rs *state.RunState) string {
	var b strings.Builder

	b.WriteString("## Prior Context\n\n")
	fmt.Fprintf(&b, "Workflow: %s\n", workflow.Name)

	// Inputs
	if len(rs.Inputs) > 0 {
		b.WriteString("Inputs:\n")
		// Sort keys for deterministic output
		keys := make([]string, 0, len(rs.Inputs))
		for k := range rs.Inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := fmt.Sprintf("%v", rs.Inputs[k])
			fmt.Fprintf(&b, "  %s: %s\n", k, truncate(v, preambleTruncateLen))
		}
	}

	// Collect completed nodes in workflow definition order
	type completedNode struct {
		id       string
		decision string
		message  string
		data     map[string]any
	}
	var completed []completedNode
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, wfNode := range workflow.Nodes {
			ns, ok := nodes[wfNode.ID]
			if !ok || ns.Status != statusCompleted {
				continue
			}
			completed = append(completed, completedNode{
				id:       ns.ID,
				decision: ns.Decision,
				message:  ns.Message,
				data:     ns.Data,
			})
		}
	})

	if len(completed) == 0 {
		return b.String()
	}

	// Cap to most recent completed nodes to prevent unbounded preamble size.
	totalCompleted := len(completed)
	if totalCompleted > preambleMaxNodes {
		completed = completed[totalCompleted-preambleMaxNodes:]
	}

	b.WriteString("\n### Completed Nodes\n")
	if totalCompleted > preambleMaxNodes {
		fmt.Fprintf(&b, "(showing last %d of %d completed nodes)\n", preambleMaxNodes, totalCompleted)
	}

	lastIdx := len(completed) - 1
	for i, cn := range completed {
		isLast := i == lastIdx && mode == contextModeSummary
		msg := cn.message
		if !isLast {
			msg = truncate(msg, preambleTruncateLen)
		}
		fmt.Fprintf(&b, "- %s: decision=%s, message=%s\n", cn.id, cn.decision, msg)

		if isLast && len(cn.data) > 0 {
			dataKeys := make([]string, 0, len(cn.data))
			for k := range cn.data {
				dataKeys = append(dataKeys, k)
			}
			sort.Strings(dataKeys)
			fmt.Fprintf(&b, "  data keys: %s\n", strings.Join(dataKeys, ", "))
		}
	}

	return b.String()
}

// prependPreamble prepends the context preamble to rolePrompt when the
// effective context mode is compact or summary. Returns rolePrompt unchanged
// for other modes.
func prependPreamble(rolePrompt string, node *definitions.Node, workflow *definitions.Workflow, rs *state.RunState) string {
	mode := resolveContextMode(node, workflow)
	if mode != contextModeCompact && mode != contextModeSummary {
		return rolePrompt
	}
	preamble := buildContextPreamble(mode, workflow, rs)
	if preamble == "" {
		return rolePrompt
	}
	if rolePrompt != "" {
		return preamble + "\n" + rolePrompt
	}
	return preamble
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
