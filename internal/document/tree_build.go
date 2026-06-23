package document

import (
	"sort"
	"strings"
	"time"

	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/visualize"
)

// buildRunNode constructs a single RunNode for the given run state, including
// its children. Recurses into sub-runs via loader. After children are built,
// post-pass helpers populate Compact/Summary/DurationMs/Decision/DecisionFamily.
func buildRunNode(rs *state.RunState, loader Loader) *RunNode {
	node := &RunNode{
		RunID:        rs.ID,
		WorkflowID:   rs.WorkflowID,
		WorkflowName: rs.WorkflowID,
		Title:        sanitizeTitle(rs.Title),
		Status:       state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure),
	}
	el, _ := loader.(EventLoader)
	var events []state.Event
	if el != nil {
		events = el.LoadEvents(rs.ID)
	}
	// Build a per-run metrics collector once so each RowChild can lookup
	// its node's CostUSD. TODO per-execution cost: NodeMetrics returns the
	// node's total across attempts, so multi-attempt rows show the same
	// total on each row.
	collector := buildCollectorFromEvents(rs, events)
	if len(events) > 0 && hasNodeStartedEvents(events) {
		node.Children = walkTreeEvents(rs, events, loader, collector)
	} else {
		node.Children = walkTreeByState(rs, loader, collector)
	}
	annotateAttemptTotals(node.Children)
	node.Compact = isCleanSubtree(node)
	node.Summary = summarizeChildren(node.Children)
	node.DurationMs = nodeDurationMs(rs)
	node.Decision, node.DecisionFamily = terminalDecision(node)
	if wsl, ok := loader.(WorkflowSnapshotLoader); ok {
		if wf := wsl.LoadWorkflowSnapshot(rs.ID); wf != nil {
			topo := visualize.RunTopologyWithMetrics(wf, rs, collector)
			node.Topology = &topo
		}
	}
	return node
}

// walkTreeByState is the fallback for runs without an event log. Emits one
// RowChild per node sorted by NodeState.StartedAt. Single-iteration ForEach
// is handled; multi-iteration is not (predates event-driven runs).
func walkTreeByState(rs *state.RunState, loader Loader, collector *metrics.Collector) []NodeChild {
	ids := make([]string, 0, len(rs.Nodes))
	for id := range rs.Nodes {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := rs.Nodes[ids[i]], rs.Nodes[ids[j]]
		if a.StartedAt == nil || b.StartedAt == nil {
			return ids[i] < ids[j]
		}
		return a.StartedAt.Before(*b.StartedAt)
	})
	var out []NodeChild
	for _, id := range ids {
		n := rs.Nodes[id]
		if isExpandedIteration(id) {
			continue
		}
		cost := lookupNodeCost(collector, id)
		var durMs int64
		if n.StartedAt != nil && n.EndedAt != nil {
			durMs = n.EndedAt.Sub(*n.StartedAt).Milliseconds()
		}
		out = append(out, RowChild{
			NodeID:         id,
			RunID:          rs.ID,
			WorkflowID:     rs.WorkflowID,
			Role:           id,
			AttemptOrdinal: 1,
			Dispatches:     n.Dispatches,
			Decision:       n.Decision,
			DecisionFamily: classifyDecision(n.Decision),
			Result:         n.Message,
			Status:         n.Status,
			DurationMs:     durMs,
			CostUSD:        cost,
		})
	}
	return out
}

// lookupNodeCost returns the per-node CostUSD from the collector, or nil when
// the collector is nil or the node has no priced events. The returned pointer
// is fresh — callers can keep their RowChild copies independent.
func lookupNodeCost(c *metrics.Collector, nodeID string) *float64 {
	if c == nil {
		return nil
	}
	own, _, ok := c.NodeMetrics(nodeID)
	if !ok || own.CostUSD == nil {
		return nil
	}
	v := *own.CostUSD
	return &v
}

// annotateAttemptTotals walks the children list and sets AttemptTotal on each
// RowChild to the count of RowChild items sharing its NodeID. So three rows
// with NodeID "write_code" all get AttemptTotal=3.
func annotateAttemptTotals(children []NodeChild) {
	counts := map[string]int{}
	for _, c := range children {
		if r, ok := c.(RowChild); ok {
			counts[r.NodeID]++
		}
	}
	for i, c := range children {
		if r, ok := c.(RowChild); ok {
			r.AttemptTotal = counts[r.NodeID]
			children[i] = r
		}
	}
}

// walkTreeEvents is the chronological-events path. Emits one RowChild per
// node_started event, matching its completion (node_completed/node_failed)
// to populate decision and timing. Sub-workflow dispatch and ForEach
// expansion land in later tasks.
func walkTreeEvents(rs *state.RunState, events []state.Event, loader Loader, collector *metrics.Collector) []NodeChild {
	var out []NodeChild
	attemptOrdinals := map[string]int{}
	sessionsSeen := map[string]bool{}
	handledSub := map[string]bool{} // nodeID → true once we've emitted its sub-run

	// Pre-pass: count total iterations per ForEach base so each ParallelChild
	// can carry its own "iteration N of M" position.
	forEachTotal := map[string]int{}
	for _, ev := range events {
		if ev.Type != eventNodeStarted {
			continue
		}
		if n := rs.Nodes[ev.NodeID]; n != nil && isForEachBase(n) {
			forEachTotal[ev.NodeID]++
		}
	}
	forEachIndex := map[string]int{}

	for i, ev := range events {
		switch ev.Type {
		case eventSubworkflowStarted:
			nodeID := ev.NodeID
			if isExpandedIteration(nodeID) {
				continue // ForEach iteration; handled in Task 6
			}
			childRunID, _ := ev.Data[artifactChildRun].(string)
			if childRunID == "" || handledSub[nodeID] {
				continue
			}
			handledSub[nodeID] = true
			childRS, err := loader.LoadRun(childRunID)
			if err != nil {
				continue
			}
			out = append(out, SubRunChild{Run: buildRunNode(childRS, loader)})
			continue
		case eventNodeStarted:
			// fall through to row handling below
		default:
			continue
		}

		nodeID := ev.NodeID
		n := rs.Nodes[nodeID]
		if n == nil {
			continue
		}
		if isExpandedIteration(nodeID) {
			continue
		}
		if isForEachBase(n) {
			forEachIndex[nodeID]++
			pc := buildParallelChild(rs, n, events, i, loader)
			pc.Index = forEachIndex[nodeID]
			pc.Total = forEachTotal[nodeID]
			if decision, _, _ := findExecutionCompletion(events, i, nodeID, n.Decision); decision != "" {
				pc.Outcome = decision
			}
			out = append(out, pc)
			continue
		}
		// If we've already emitted a SubRunChild for this node (because
		// subworkflow_started fired first), don't double-emit as a RowChild.
		if handledSub[nodeID] {
			continue
		}
		// Sub-workflow nodes that emit node_started without subworkflow_started
		// (lazy launch). Detect via NodeState.Data["child_run"].
		if childRunID := childRunOf(n); childRunID != "" {
			handledSub[nodeID] = true
			childRS, err := loader.LoadRun(childRunID)
			if err != nil {
				continue
			}
			out = append(out, SubRunChild{Run: buildRunNode(childRS, loader)})
			continue
		}

		attemptOrdinals[nodeID]++
		ordinal := attemptOrdinals[nodeID]

		// Find this execution's matching node_completed/node_failed. The
		// completion is the first one for this nodeID after the node_started
		// at position i (events are 1:1).
		decision, running, endedAt := findExecutionCompletion(events, i, nodeID, n.Decision)

		sessionResume := false
		if n.SessionID != "" {
			sessionResume = sessionsSeen[n.SessionID]
			sessionsSeen[n.SessionID] = true
		}
		var durMs int64
		if !running && !endedAt.IsZero() && !ev.Timestamp.IsZero() {
			durMs = endedAt.Sub(ev.Timestamp).Milliseconds()
		}
		// TODO per-execution cost: collector.NodeMetrics sums cost across
		// attempts, so multi-attempt rows show the same total on each row.
		// Replace with attempt-scoped cost once we have per-execution metrics.
		cost := lookupNodeCost(collector, nodeID)
		out = append(out, RowChild{
			NodeID:         nodeID,
			RunID:          rs.ID,
			WorkflowID:     rs.WorkflowID,
			Role:           nodeID,
			AttemptOrdinal: ordinal,
			Dispatches:     n.Dispatches,
			SessionID:      n.SessionID,
			IsResume:       sessionResume,
			Decision:       decision,
			DecisionFamily: classifyDecision(decision),
			Result:         n.Message,
			Running:        running,
			StartedAt:      ev.Timestamp,
			EndedAt:        endedAt,
			DurationMs:     durMs,
			CostUSD:        cost,
		})
	}
	return out
}

// findExecutionCompletion finds the completion of the execution that started
// at events[startIdx]. The first node_completed/node_failed event for nodeID
// after startIdx is THIS execution's completion (start/complete events for a
// given node are 1:1). A node_started for the same nodeID before any
// completion means the events are malformed and this execution is treated as
// still running.
func findExecutionCompletion(events []state.Event, startIdx int, nodeID string, fallbackDecision string) (string, bool, time.Time) {
	for j := startIdx + 1; j < len(events); j++ {
		ev := events[j]
		if ev.NodeID != nodeID {
			continue
		}
		if ev.Type == eventNodeStarted {
			break
		}
		if ev.Type != eventNodeCompleted && ev.Type != eventNodeFailed {
			continue
		}
		decision := ""
		if d, ok := ev.Data[kindDecision].(string); ok {
			decision = d
		}
		return decision, false, ev.Timestamp
	}
	return fallbackDecision, true, time.Time{}
}

// buildParallelChild produces one ParallelChild for this iteration of a
// ForEach base node. Children are derived from subworkflow_started events
// that fall between events[startIdx] (the node_started that triggered this
// call) and the next node_started/node_completed/node_failed for the same
// base node. Multi-iteration runs produce multiple sibling ParallelChild
// items (one per node_started occurrence).
func buildParallelChild(rs *state.RunState, base *state.NodeState, events []state.Event, startIdx int, loader Loader) ParallelChild {
	baseID := base.ID
	prefix := foreachIterationPrefix(base)
	var childIDs []string
	for j := startIdx + 1; j < len(events); j++ {
		ev := events[j]
		if ev.NodeID == baseID && (ev.Type == eventNodeStarted || ev.Type == eventNodeCompleted || ev.Type == eventNodeFailed) {
			break
		}
		if ev.Type != eventSubworkflowStarted {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(ev.NodeID, prefix+"::") {
				continue
			}
		} else if !isExpandedIteration(ev.NodeID) {
			continue
		}
		if cr, _ := ev.Data[artifactChildRun].(string); cr != "" {
			childIDs = append(childIDs, cr)
		}
	}
	runs := make([]*RunNode, 0, len(childIDs))
	for _, id := range childIDs {
		childRS, err := loader.LoadRun(id)
		if err != nil {
			continue
		}
		runs = append(runs, buildRunNode(childRS, loader))
	}
	return ParallelChild{ParentNode: baseID, Runs: runs}
}

// isCleanSubtree returns true when every leaf row in this subtree completed
// cleanly: no retries (AttemptOrdinal > 1), no bad-family decision, no running
// state. Used to default-Compact a sub-workflow whose execution is uneventful.
func isCleanSubtree(n *RunNode) bool {
	for _, c := range n.Children {
		switch v := c.(type) {
		case RowChild:
			if v.AttemptOrdinal > 1 {
				return false
			}
			if v.DecisionFamily == "bad" {
				return false
			}
			if v.Running {
				return false
			}
		case SubRunChild:
			if v.Run == nil || !isCleanSubtree(v.Run) {
				return false
			}
		case ParallelChild:
			for _, r := range v.Runs {
				if r == nil || !isCleanSubtree(r) {
					return false
				}
			}
		}
	}
	return true
}

// summarizeChildren returns a "·"-separated list of distinct row roles in the
// given children list. Used as a one-line summary for compact-rendered nodes.
func summarizeChildren(children []NodeChild) string {
	var roles []string
	seen := map[string]bool{}
	for _, c := range children {
		if r, ok := c.(RowChild); ok {
			if !seen[r.Role] {
				seen[r.Role] = true
				roles = append(roles, r.Role)
			}
		}
	}
	if len(roles) == 0 {
		return ""
	}
	out := roles[0]
	for _, r := range roles[1:] {
		out += " · " + r
	}
	return out
}

// nodeDurationMs returns the wall-clock duration of a run in milliseconds,
// or 0 if either timestamp is unset.
func nodeDurationMs(rs *state.RunState) int64 {
	if rs.StartedAt.IsZero() || rs.FinishedAt == nil {
		return 0
	}
	return rs.FinishedAt.Sub(rs.StartedAt).Milliseconds()
}

// terminalDecision walks the children in reverse and returns the most recent
// non-empty decision and its classified family. RowChild matches directly;
// SubRunChild falls back to the sub-run's own terminal decision. ParallelChild
// has no single decision so is skipped.
func terminalDecision(n *RunNode) (string, string) {
	for i := len(n.Children) - 1; i >= 0; i-- {
		switch v := n.Children[i].(type) {
		case RowChild:
			if v.Decision != "" {
				return v.Decision, v.DecisionFamily
			}
		case SubRunChild:
			if v.Run != nil && v.Run.Decision != "" {
				return v.Run.Decision, v.Run.DecisionFamily
			}
		}
	}
	return "", ""
}

// BuildDocument constructs a tree-shaped Document for the given root run id.
// The root RunNode never auto-compacts — the page header IS the root.
func BuildDocument(rootID string, loader Loader) (*Document, error) {
	return BuildDocumentWithRegistryAndResolver(rootID, loader, nil, nil)
}

// BuildDocumentWithRegistry is BuildDocument with a name registry.
func BuildDocumentWithRegistry(rootID string, loader Loader, reg Registry) (*Document, error) {
	return BuildDocumentWithRegistryAndResolver(rootID, loader, reg, nil)
}

// BuildDocumentWithRegistryAndResolver is BuildDocument with a name registry
// and an optional prompt resolver. The returned Document has a populated Root
// *RunNode; the legacy Items slice is left nil.
func BuildDocumentWithRegistryAndResolver(rootID string, loader Loader, reg Registry, resolver PromptResolver) (*Document, error) {
	rs, err := loader.LoadRun(rootID)
	if err != nil {
		return nil, err
	}
	doc := &Document{
		RootRunID:  rs.ID,
		RootTitle:  sanitizeTitle(rs.Title),
		RootStatus: state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure),
	}
	doc.Root = buildRunNode(rs, loader)
	doc.Root.Compact = false // root never auto-compacts; page header IS the root

	enrichRunNode(doc.Root, loader)

	if reg != nil {
		annotateTreeWithRegistry(doc.Root, reg)
		if df, ok := reg.(DecisionFinder); ok && df != nil {
			annotateTreeWithDecisionFinder(doc.Root, df, loader)
		}
	}
	if resolver != nil {
		annotateTreeWithPromptResolver(doc.Root, resolver, loader)
	}

	doc.BriefText, doc.BriefSource, doc.BriefFields = buildBrief(rs, reg)
	doc.TotalRuns = countTotalRuns(doc.Root)
	return doc, nil
}

// annotateTreeWithRegistry sets WorkflowName on each RunNode and Role on each
// RowChild based on registry lookups. Mirrors the registry annotation loop in
// BuildDocumentWithRegistryAndResolverLegacy.
func annotateTreeWithRegistry(n *RunNode, reg Registry) {
	if n == nil {
		return
	}
	if name := reg.WorkflowName(n.WorkflowID); name != "" {
		n.WorkflowName = name
	}
	n.Title = trimWorkflowNamePrefix(n.Title, n.WorkflowName)
	for i, c := range n.Children {
		switch row := c.(type) {
		case RowChild:
			// Legacy passes RunID as the first arg of RoleForNode. Mirror that
			// behavior here so the registry adapter receives the same input.
			if r := reg.RoleForNode(row.RunID, row.NodeID); r != "" {
				row.Role = r
			}
			if rn := reg.RunnerForNode(row.WorkflowID, row.NodeID); rn != "" {
				row.Runner = rn
			}
			if row.Decision != "" {
				if tgt := reg.NextNode(row.WorkflowID, row.NodeID, row.Decision); tgt != "" {
					row.NextTarget = tgt
				}
			}
			n.Children[i] = row
		case SubRunChild:
			annotateTreeWithRegistry(row.Run, reg)
		case ParallelChild:
			for _, r := range row.Runs {
				annotateTreeWithRegistry(r, reg)
			}
		}
	}
}

// annotateTreeWithPromptResolver populates Row.Prompt from the resolver
// when the enrich pass did not find a node_prompt event (typical for the
// fallback walkByState path or static workflow prompts).
func annotateTreeWithPromptResolver(n *RunNode, resolver PromptResolver, loader Loader) {
	if n == nil {
		return
	}
	rs, _ := loader.LoadRun(n.RunID)
	for i, c := range n.Children {
		switch row := c.(type) {
		case RowChild:
			if row.Prompt == "" && rs != nil {
				local := resolver.LocalPrompt(rs.WorkflowID, row.NodeID, row.RunID, row.AttemptOrdinal)
				if local != "" {
					row.Prompt = local
				}
			}
			n.Children[i] = row
		case SubRunChild:
			annotateTreeWithPromptResolver(row.Run, resolver, loader)
		case ParallelChild:
			for _, r := range row.Runs {
				annotateTreeWithPromptResolver(r, resolver, loader)
			}
		}
	}
}

// annotateTreeWithDecisionFinder fills DecisionDescription on each RowChild
// from the DecisionFinder. Mirrors the equivalent legacy annotation. The
// workflowID for the lookup comes from the RowChild's run state.
//
// DecisionMessage is sourced from the per-execution node_completed event
// in enrichRunNode — there's nothing to populate from DecisionMeta, which
// only carries Description and Tags.
func annotateTreeWithDecisionFinder(n *RunNode, df DecisionFinder, loader Loader) {
	if n == nil {
		return
	}
	rs, _ := loader.LoadRun(n.RunID)
	for i, c := range n.Children {
		switch row := c.(type) {
		case RowChild:
			if row.Decision != "" && rs != nil {
				if meta := df.FindDecisionMeta(rs.WorkflowID, row.Decision); meta != nil {
					row.DecisionDescription = meta.Description
					n.Children[i] = row
				}
			}
		case SubRunChild:
			annotateTreeWithDecisionFinder(row.Run, df, loader)
		case ParallelChild:
			for _, r := range row.Runs {
				annotateTreeWithDecisionFinder(r, df, loader)
			}
		}
	}
}

// countTotalRuns walks the tree counting every RunNode (root + subs + parallels).
func countTotalRuns(n *RunNode) int {
	if n == nil {
		return 0
	}
	count := 1
	for _, c := range n.Children {
		switch v := c.(type) {
		case SubRunChild:
			count += countTotalRuns(v.Run)
		case ParallelChild:
			for _, r := range v.Runs {
				count += countTotalRuns(r)
			}
		}
	}
	return count
}
