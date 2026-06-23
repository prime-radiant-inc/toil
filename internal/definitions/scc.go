package definitions

import "sort"

// nodeInLoopableScc returns true if `nodeID` is in a non-trivial SCC of
// the workflow's decision-edges graph. Filters: includes edges whose
// `when:` is a literal decision (not an expression like "status ==
// 'failed'", not the `_loop_exhausted` edge itself). Self-loops qualify
// (size-1 SCC with self-edge). Does NOT cross subworkflow boundaries.
func nodeInLoopableScc(w *Workflow, nodeID string) bool {
	adj := buildDecisionEdgeAdjacency(w)
	sccs := tarjanSCC(adj)
	for _, scc := range sccs {
		if len(scc) == 0 {
			continue
		}
		var inThis bool
		for _, id := range scc {
			if id == nodeID {
				inThis = true
				break
			}
		}
		if !inThis {
			continue
		}
		// Size-1 component qualifies only if it has a self-edge.
		if len(scc) == 1 {
			for _, target := range adj[nodeID] {
				if target == nodeID {
					return true
				}
			}
			return false
		}
		return true
	}
	return false
}

func buildDecisionEdgeAdjacency(w *Workflow) map[string][]string {
	adj := map[string][]string{}
	for _, e := range w.Edges {
		// Exclude expression-style when (e.g., "status == 'failed'").
		if looksLikeExpression(e.When) {
			continue
		}
		// Exclude the _loop_exhausted edge itself to avoid trivial cycle.
		if e.When == metaLoopExhausted {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	// Ensure every node has an entry (for nodes with no outgoing edges).
	for _, n := range w.Nodes {
		if _, ok := adj[n.ID]; !ok {
			adj[n.ID] = nil
		}
	}
	return adj
}

// tarjanSCC returns the strongly-connected components of the given
// adjacency map. Each component is a slice of node IDs.
func tarjanSCC(adj map[string][]string) [][]string {
	idx := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var result [][]string
	counter := 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		idx[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, ok := idx[w]; !ok {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if idx[w] < low[v] {
					low[v] = idx[w]
				}
			}
		}
		if low[v] == idx[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			result = append(result, comp)
		}
	}

	// Sort for deterministic order.
	nodes := make([]string, 0, len(adj))
	for v := range adj {
		nodes = append(nodes, v)
	}
	sort.Strings(nodes)
	for _, v := range nodes {
		if _, ok := idx[v]; !ok {
			strongconnect(v)
		}
	}
	return result
}
