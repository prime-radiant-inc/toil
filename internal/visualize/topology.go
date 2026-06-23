package visualize

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

// TopologyGraph carries graph structure (nodes and edges) without layout
// coordinates. Intended for client-side layout engines like ELK.js.
type TopologyGraph struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
}

// NodeMetricsSummary is a compact view of per-node metrics suitable for
// embedding in topology responses. Rollup, when non-nil, sums over the node's
// descendants and is omitted when equal to the node's own totals.
type NodeMetricsSummary struct {
	DurationMs         int64               `json:"duration_ms"`
	TokensTotal        int                 `json:"tokens_total"`
	CostUSD            *float64            `json:"cost_usd,omitempty"`
	UnpricedEventCount int                 `json:"unpriced_event_count,omitempty"`
	Rollup             *NodeMetricsSummary `json:"rollup,omitempty"`
}

type TopologyNode struct {
	ID            string              `json:"id"`
	Label         string              `json:"label"`
	Description   string              `json:"description,omitempty"` // longer descriptive line shown under Label (e.g., "Spec Implementation")
	Subtitle      string              `json:"subtitle,omitempty"`    // mono identifier line under Description (e.g., run id)
	Parent        string              `json:"parent,omitempty"`
	Status        string              `json:"status,omitempty"`
	Kind          string              `json:"kind,omitempty"`
	Workflow      string              `json:"workflow,omitempty"`      // child workflow ID
	WorkflowName  string              `json:"workflow_name,omitempty"` // human-readable workflow name
	ChildRunID    string              `json:"child_run_id,omitempty"`
	Selected      bool                `json:"selected,omitempty"`
	Current       bool                `json:"current,omitempty"`
	Decision      string              `json:"decision,omitempty"`
	Attempts      int                 `json:"attempts,omitempty"`
	Prompt        string              `json:"prompt,omitempty"`
	PendingReason string              `json:"pending_reason,omitempty"`
	Metrics       *NodeMetricsSummary `json:"metrics,omitempty"`
	// Tags are the workflow-declared semantic labels for this node's
	// decision, materialized onto NodeState at completion time.
	// Renderers treat specific tags as styling cues — e.g. `override`
	// triggers an amber border to distinguish review-escalation
	// waivers from ordinary completions. Unknown tags are passed
	// through; the renderer decides whether to ignore them.
	Tags []string `json:"tags,omitempty"`
}

type TopologyEdge struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	Label    string `json:"label,omitempty"`
	IsEscape bool   `json:"isEscape,omitempty"`
}

// WorkflowTopology converts a workflow definition into a topology graph
// containing only structural information (no run state). Body-template
// nodes referenced by other nodes' for_each.body are hidden — the
// orchestrator subsumes them.
func WorkflowTopology(workflow *definitions.Workflow) TopologyGraph {
	if workflow == nil {
		return emptyTopology()
	}

	templates := buildTemplateToOrchestratorMap(workflow)
	nodes := make([]TopologyNode, 0, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		if _, ok := templates[node.ID]; ok {
			continue
		}
		tn := TopologyNode{
			ID:     node.ID,
			Label:  node.ID,
			Prompt: firstLine(node.Prompt),
		}
		if node.Kind == kindSubworkflow {
			tn.Kind = kindSubworkflow
			tn.Workflow = strings.TrimSpace(node.Workflow)
		}
		nodes = append(nodes, tn)
	}

	edges := buildTopologyEdges(workflow)

	return TopologyGraph{Nodes: nodes, Edges: edges}
}

// RunTopology converts a workflow definition into a topology graph with run
// state overlaid (status, decision, attempts, ForEach expansions).
func RunTopology(workflow *definitions.Workflow, runState *state.RunState) TopologyGraph {
	if workflow == nil {
		return emptyTopology()
	}

	nodeStates := snapshotNodeStates(runState)
	templates := buildTemplateToOrchestratorMap(workflow)

	nodes := make([]TopologyNode, 0, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		if _, ok := templates[node.ID]; ok {
			continue
		}
		status := statusPending
		attempts := 0
		decision := ""
		var tags []string
		if ns, ok := nodeStates[node.ID]; ok {
			if strings.TrimSpace(ns.Status) != "" {
				status = strings.TrimSpace(ns.Status)
			}
			attempts = ns.Attempts
			decision = strings.TrimSpace(ns.Decision)
			if len(ns.Tags) > 0 {
				tags = append([]string(nil), ns.Tags...)
			}
		}

		tn := TopologyNode{
			ID:       node.ID,
			Label:    node.ID,
			Status:   normalizeStatus(status),
			Attempts: attempts,
			Prompt:   firstLine(node.Prompt),
			Tags:     tags,
		}
		if decision != "" {
			tn.Decision = decision
		}
		tn.Kind = ClassifyNode(workflow, node.ID)
		if node.Kind == kindSubworkflow {
			tn.Workflow = strings.TrimSpace(node.Workflow)
		}
		nodes = append(nodes, tn)
	}

	// Add ForEach expanded iteration nodes from run state.
	// Expanded items have IDs like "{prefix}::{index}". The prefix is the
	// template node for post-split ForEach (body: template) but the UI
	// expects them parented under the orchestrator, not the template.
	// Collect into a slice and sort by (prefix, numeric index) so the
	// result is deterministic AND items appear in natural order
	// (tmpl::2 before tmpl::10, not after as sort.Strings would do).
	templateToOrchestrator := buildTemplateToOrchestratorMap(workflow)
	expandedIDs := make([]string, 0, len(nodeStates))
	for id := range nodeStates {
		expandedIDs = append(expandedIDs, id)
	}
	sort.Slice(expandedIDs, func(i, j int) bool {
		return lessByPrefixAndIndex(expandedIDs[i], expandedIDs[j])
	})
	for _, id := range expandedIDs {
		ns := nodeStates[id]
		parts := strings.SplitN(id, "::", 2)
		if len(parts) != 2 {
			continue
		}
		parentID := parts[0]
		if definitions.FindNode(workflow, parentID) == nil {
			continue
		}
		if orchID, ok := templateToOrchestrator[parentID]; ok {
			parentID = orchID
		}

		status := statusPending
		if strings.TrimSpace(ns.Status) != "" {
			status = strings.TrimSpace(ns.Status)
		}

		nodes = append(nodes, TopologyNode{
			ID:     id,
			Label:  id,
			Parent: parentID,
			Status: normalizeStatus(status),
		})
	}

	edges := buildTopologyEdges(workflow)

	return TopologyGraph{Nodes: nodes, Edges: edges}
}

// RunTopologyWithMetrics is like RunTopology but annotates each node with
// metrics from c when c is non-nil. When c is nil the result is identical to
// RunTopology.
func RunTopologyWithMetrics(workflow *definitions.Workflow, runState *state.RunState, c *metrics.Collector) TopologyGraph {
	topo := RunTopology(workflow, runState)
	if c == nil {
		return topo
	}
	for i := range topo.Nodes {
		own, rollup, ok := c.NodeMetrics(topo.Nodes[i].ID)
		if !ok {
			continue
		}
		s := summaryFromTotals(own)
		if rollup != own {
			r := summaryFromTotals(rollup)
			s.Rollup = &r
		}
		topo.Nodes[i].Metrics = &s
	}
	return topo
}

// summaryFromTotals converts a state.NodeTotals into a NodeMetricsSummary.
func summaryFromTotals(t state.NodeTotals) NodeMetricsSummary {
	return NodeMetricsSummary{
		DurationMs:         t.DurationMs,
		TokensTotal:        t.Tokens.Total,
		CostUSD:            t.CostUSD,
		UnpricedEventCount: t.UnpricedEventCount,
	}
}

func emptyTopology() TopologyGraph {
	return TopologyGraph{Nodes: []TopologyNode{}, Edges: []TopologyEdge{}}
}

// CompoundWorkflowTopology builds a topology graph from a bundle of workflow
// definitions. The selected workflow and transitively-referenced subworkflows
// become parent nodes with their steps as namespaced children.
func CompoundWorkflowTopology(bundle *definitions.Bundle, selectedWorkflowID string) TopologyGraph {
	selectedWorkflowID = strings.TrimSpace(selectedWorkflowID)
	if bundle == nil || selectedWorkflowID == "" {
		return emptyTopology()
	}
	if _, ok := bundle.Workflows[selectedWorkflowID]; !ok {
		return emptyTopology()
	}

	included := collectSubworkflows(bundle, selectedWorkflowID)

	// Sort workflow IDs for deterministic output, selected workflow first.
	workflowIDs := make([]string, 0, len(included))
	for id := range included {
		if id != selectedWorkflowID {
			workflowIDs = append(workflowIDs, id)
		}
	}
	sort.Strings(workflowIDs)
	workflowIDs = append([]string{selectedWorkflowID}, workflowIDs...)

	var nodes []TopologyNode
	var edges []TopologyEdge
	edgeCount := 0

	for _, wfID := range workflowIDs {
		wf := bundle.Workflows[wfID]

		// Parent node for the workflow.
		label := strings.TrimSpace(wf.Name)
		if label == "" {
			label = wfID
		}
		parentNode := TopologyNode{
			ID:       wfID,
			Label:    label,
			Selected: wfID == selectedWorkflowID,
		}
		if wfID != selectedWorkflowID {
			parentNode.Kind = kindSubworkflow
		}
		nodes = append(nodes, parentNode)

		// Child nodes for each step in the workflow.
		for _, node := range wf.Nodes {
			childID := fmt.Sprintf("%s::%s", wfID, node.ID)
			child := TopologyNode{
				ID:     childID,
				Label:  node.ID,
				Parent: wfID,
				Prompt: firstLine(node.Prompt),
			}
			if node.Kind == kindSubworkflow {
				child.Kind = kindSubworkflow
				child.Workflow = strings.TrimSpace(node.Workflow)
			}
			nodes = append(nodes, child)
		}

		// Intra-workflow edges with namespaced source/target.
		for _, edge := range wf.Edges {
			edges = append(edges, TopologyEdge{
				ID:     fmt.Sprintf("e%d", edgeCount),
				Source: fmt.Sprintf("%s::%s", wfID, edge.From),
				Target: fmt.Sprintf("%s::%s", wfID, edge.To),
				Label:  strings.TrimSpace(edge.When),
			})
			edgeCount++
		}

		// Inter-workflow edges from subworkflow child nodes to the
		// referenced workflow's parent node.
		for _, node := range wf.Nodes {
			if node.Kind != kindSubworkflow {
				continue
			}
			target := strings.TrimSpace(node.Workflow)
			if target == "" {
				continue
			}
			if _, ok := included[target]; !ok {
				continue
			}
			edges = append(edges, TopologyEdge{
				ID:     fmt.Sprintf("e%d", edgeCount),
				Source: fmt.Sprintf("%s::%s", wfID, node.ID),
				Target: target,
			})
			edgeCount++
		}
	}

	if nodes == nil {
		nodes = []TopologyNode{}
	}
	if edges == nil {
		edges = []TopologyEdge{}
	}
	return TopologyGraph{Nodes: nodes, Edges: edges}
}

// CompoundExecutionGroupTopology builds a topology graph for an execution
// group. Each run becomes a parent node with workflow steps as namespaced
// children.
// RunTreeTopology returns a minimal run-hierarchy graph: one node per run
// (no internal workflow nodes), edges from parent run to child run. Use
// this when you want the structural overview of an execution group
// without expanding every workflow's interior — the run-view's
// orientation strip wants exactly this.
func RunTreeTopology(runs []CompoundRunInput, currentRunID string) TopologyGraph {
	if len(runs) == 0 {
		return emptyTopology()
	}
	var nodes []TopologyNode
	var edges []TopologyEdge
	known := map[string]bool{}
	for _, r := range runs {
		known[r.RunID] = true
	}
	for _, r := range runs {
		workflowName := ""
		if r.Workflow != nil {
			workflowName = strings.TrimSpace(r.Workflow.Name)
		}
		// Title is typically "Workflow Name: Description"; split so the graph
		// can show workflow name and description on separate lines.
		title := strings.TrimSpace(r.Title)
		description := title
		if workflowName != "" && strings.HasPrefix(title, workflowName) {
			rest := strings.TrimPrefix(title, workflowName)
			rest = strings.TrimLeft(rest, " \t:·-—")
			if rest != "" {
				description = rest
			}
		}
		// Label is the workflow name when available; falls back to the full
		// title, then the run id.
		label := workflowName
		if label == "" {
			label = title
		}
		if label == "" {
			label = r.RunID
		}
		// Description shouldn't duplicate the label.
		if description == label {
			description = ""
		}
		var summary *NodeMetricsSummary
		if r.Collector != nil {
			s := summaryFromTotals(r.Collector.RunTotal())
			summary = &s
		}
		nodes = append(nodes, TopologyNode{
			ID:           r.RunID,
			Label:        label,
			Description:  description,
			Subtitle:     r.RunID,
			Status:       normalizeStatus(r.Status),
			Current:      r.RunID == currentRunID,
			WorkflowName: workflowName,
			Workflow:     workflowOf(r),
			Metrics:      summary,
		})
		if r.ParentRun != "" && known[r.ParentRun] {
			edges = append(edges, TopologyEdge{
				ID:     "e-" + r.ParentRun + "-" + r.RunID,
				Source: r.ParentRun,
				Target: r.RunID,
			})
		}
	}
	return TopologyGraph{Nodes: nodes, Edges: edges}
}

func workflowOf(r CompoundRunInput) string {
	if r.Workflow != nil {
		return r.Workflow.ID
	}
	return ""
}

func CompoundExecutionGroupTopology(runs []CompoundRunInput, currentRunID string) TopologyGraph {
	if len(runs) == 0 {
		return emptyTopology()
	}

	var nodes []TopologyNode
	var edges []TopologyEdge
	edgeCount := 0

	// Workflow ID → friendly Name, so subworkflow dispatcher nodes can
	// display the target workflow's human-readable name alongside its ID.
	workflowNameByID := map[string]string{}
	for _, run := range runs {
		if run.Workflow == nil {
			continue
		}
		if name := strings.TrimSpace(run.Workflow.Name); name != "" {
			workflowNameByID[run.Workflow.ID] = name
		}
	}

	// Reverse lookup: childRunID → parentRunID::spawningNodeID.
	// Forward lookup: parentRunID::nodeID → []indexedChild sorted by expansion index.
	spawningNode := map[string]string{}
	spawnedChild := map[string][]indexedChild{}

	for _, run := range runs {
		// Parent node for the run itself.
		label := strings.TrimSpace(run.Title)
		if label == "" {
			label = run.RunID
		}
		parentNode := TopologyNode{
			ID:      run.RunID,
			Label:   label,
			Status:  normalizeStatus(run.Status),
			Current: run.RunID == currentRunID,
			Prompt:  runDescription(run),
		}
		if run.Collector != nil {
			rt := run.Collector.RunTotal()
			sum := summaryFromTotals(rt)
			parentNode.Metrics = &sum
		}
		nodes = append(nodes, parentNode)

		if run.Workflow == nil {
			continue
		}

		nodeStates := snapshotNodeStates(run.RunState)
		templateToOrchestrator := buildTemplateToOrchestratorMap(run.Workflow)

		// Child nodes for each workflow step. Skip for_each body templates —
		// the orchestrator represents the step in the graph, and emitting
		// the template would produce an island node with no edges.
		for _, wfNode := range run.Workflow.Nodes {
			if _, ok := templateToOrchestrator[wfNode.ID]; ok {
				continue
			}
			childID := fmt.Sprintf("%s::%s", run.RunID, wfNode.ID)
			status := statusPending
			attempts := 0
			decision := ""
			var tags []string
			ns, hasState := nodeStates[wfNode.ID]
			if hasState {
				if s := strings.TrimSpace(ns.Status); s != "" {
					status = s
				}
				attempts = ns.Attempts
				decision = strings.TrimSpace(ns.Decision)
				if len(ns.Tags) > 0 {
					tags = append([]string(nil), ns.Tags...)
				}
			}

			child := TopologyNode{
				ID:       childID,
				Label:    wfNode.ID,
				Parent:   run.RunID,
				Status:   normalizeStatus(status),
				Attempts: attempts,
				Prompt:   firstLine(wfNode.Prompt),
				Tags:     tags,
			}
			if decision != "" {
				child.Decision = decision
			}
			child.Kind = ClassifyNode(run.Workflow, wfNode.ID)
			if wfNode.Kind == kindSubworkflow {
				child.Workflow = strings.TrimSpace(wfNode.Workflow)
				child.WorkflowName = workflowNameByID[child.Workflow]
				// For non-ForEach dispatchers, surface the dispatched
				// child run ID on the node so graph viewers can tell
				// which run it spawned without drilling in.
				if hasState && ns.Data != nil {
					if cr, ok := ns.Data["child_run"].(string); ok {
						child.ChildRunID = cr
					}
				}
			}
			if run.Collector != nil {
				own, rollup, ok := run.Collector.NodeMetrics(wfNode.ID)
				if ok {
					sum := summaryFromTotals(own)
					if rollup != own {
						r := summaryFromTotals(rollup)
						sum.Rollup = &r
					}
					child.Metrics = &sum
				}
			}
			nodes = append(nodes, child)
		}

		// Emit ForEach iteration children for dispatchers with per-iteration state.
		// Also track each dispatcher's set of iteration-child_runs so we can
		// summarise them on the dispatcher parent node below.
		iterChildRunsByParent := map[string][]string{}
		for stateNodeID, ns := range nodeStates {
			idx := strings.Index(stateNodeID, "::")
			if idx < 0 {
				continue
			}
			baseID := stateNodeID[:idx]
			iterSuffix := stateNodeID[idx+2:]
			baseNode := definitions.FindNode(run.Workflow, baseID)
			if baseNode == nil {
				continue
			}
			// With the body-ref shape, iterations are keyed by the template
			// ID (e.g. `build_one_component::0`). The template itself is
			// hidden; the iteration's visible parent is the orchestrator.
			parentKeyID := baseID
			if orchID, ok := templateToOrchestrator[baseID]; ok {
				parentKeyID = orchID
			}
			parentID := fmt.Sprintf("%s::%s", run.RunID, parentKeyID)
			iterStatus := statusPending
			if s := strings.TrimSpace(ns.Status); s != "" {
				iterStatus = s
			}
			iterID := fmt.Sprintf("%s::%s", parentID, iterSuffix)
			iterNode := TopologyNode{
				ID:     iterID,
				Label:  "iteration " + iterSuffix,
				Parent: parentID,
				Status: normalizeStatus(iterStatus),
				Kind:   kindForeachIteration,
			}
			if baseNode.Kind == kindSubworkflow {
				iterNode.Workflow = strings.TrimSpace(baseNode.Workflow)
				iterNode.WorkflowName = workflowNameByID[iterNode.Workflow]
			}
			if ns.Data != nil {
				if cr, ok := ns.Data["child_run"].(string); ok {
					iterNode.ChildRunID = cr
					iterChildRunsByParent[parentID] = append(iterChildRunsByParent[parentID], cr)
				}
			}
			if run.Collector != nil {
				own, rollup, ok := run.Collector.NodeMetrics(stateNodeID)
				if ok {
					sum := summaryFromTotals(own)
					if rollup != own {
						r := summaryFromTotals(rollup)
						sum.Rollup = &r
					}
					iterNode.Metrics = &sum
				}
			}
			nodes = append(nodes, iterNode)
		}

		// Summarize iteration child_runs on the ForEach dispatcher nodes
		// themselves, so the compound parent container shows a run ID
		// (single iteration) or a count (multiple).
		for i := range nodes {
			childRuns, ok := iterChildRunsByParent[nodes[i].ID]
			if !ok || len(childRuns) == 0 {
				continue
			}
			switch len(childRuns) {
			case 1:
				nodes[i].ChildRunID = childRuns[0]
			default:
				nodes[i].ChildRunID = fmt.Sprintf("%d runs", len(childRuns))
			}
		}

		// Scan all state nodes for child_run data.
		for stateNodeID, ns := range nodeStates {
			if ns.Data == nil {
				continue
			}
			cr, ok := ns.Data["child_run"].(string)
			if !ok || cr == "" {
				continue
			}
			baseNodeID := stateNodeID
			expansionIndex := 0
			if idx := strings.LastIndex(stateNodeID, "::"); idx >= 0 {
				suffix := stateNodeID[idx+2:]
				if n, err := strconv.Atoi(suffix); err == nil {
					baseNodeID = stateNodeID[:idx]
					expansionIndex = n
				}
			}
			// Post-split ForEach: iterations live under the template ID,
			// but the template node is hidden (subsumed by its orchestrator).
			// Index spawningNode under the orchestrator key so inter-run
			// edges land on the visible orchestrator, not the hidden
			// template. Also index spawnedChild under the orchestrator so
			// sibling-flow edge lookup finds these children.
			visibleBaseID := baseNodeID
			if orchID, isTemplate := templateToOrchestrator[baseNodeID]; isTemplate {
				visibleBaseID = orchID
			}
			baseKey := fmt.Sprintf("%s::%s", run.RunID, visibleBaseID)
			spawningNode[cr] = baseKey
			spawnedChild[baseKey] = append(spawnedChild[baseKey], indexedChild{index: expansionIndex, runID: cr})
		}

		// Intra-run edges for workflow transitions. Skip edges whose
		// endpoints are for_each body templates — those nodes are hidden
		// (subsumed by the orchestrator) so the edge would point at a
		// node that isn't in the graph.
		//
		// Why drop rather than rewrite to the orchestrator? Validation
		// (validateTemplateIncomingEdges + validateTemplateEdgeTargets)
		// guarantees that in a valid workflow the only edge touching a
		// template is `template → orchestrator` — per-item failure-routing
		// metadata the engine consumes directly. Rewriting both endpoints
		// to the orchestrator would collapse it to a self-loop which we'd
		// skip anyway, so drop is equivalent for valid input and safer
		// for invalid input (rewriting a template-as-source edge would
		// invent a visible flow edge on the orchestrator that wasn't in
		// the definition).
		for _, edge := range run.Workflow.Edges {
			if _, ok := templateToOrchestrator[edge.From]; ok {
				continue
			}
			if _, ok := templateToOrchestrator[edge.To]; ok {
				continue
			}
			edges = append(edges, TopologyEdge{
				ID:     fmt.Sprintf("e%d", edgeCount),
				Source: fmt.Sprintf("%s::%s", run.RunID, edge.From),
				Target: fmt.Sprintf("%s::%s", run.RunID, edge.To),
				Label:  strings.TrimSpace(edge.When),
			})
			edgeCount++
		}

	}

	// Sort each spawnedChild slice by expansion index so that ForEach items
	// are always in ascending order regardless of Go map iteration order.
	for key := range spawnedChild {
		sort.Slice(spawnedChild[key], func(i, j int) bool {
			return spawnedChild[key][i].index < spawnedChild[key][j].index
		})
	}

	// Sibling flow edges: when two adjacent nodes in a parent workflow both
	// spawned child runs, connect those child runs to show execution order.
	// For ForEach nodes each position i in the from-slice pairs with position i
	// in the to-slice (index-paired), so item N always maps to item N.
	siblingFlowTarget := map[string]bool{}
	for _, run := range runs {
		if run.Workflow == nil {
			continue
		}
		for _, edge := range run.Workflow.Edges {
			fromID := fmt.Sprintf("%s::%s", run.RunID, edge.From)
			toID := fmt.Sprintf("%s::%s", run.RunID, edge.To)
			fromChildren := spawnedChild[fromID]
			toChildren := spawnedChild[toID]
			if len(fromChildren) == 0 || len(toChildren) == 0 {
				continue
			}
			// Pair by position: fromChildren[i] -> toChildren[i].
			// If the slices differ in length, pair up to the shorter length.
			pairLen := len(fromChildren)
			if len(toChildren) < pairLen {
				pairLen = len(toChildren)
			}
			for i := 0; i < pairLen; i++ {
				edges = append(edges, TopologyEdge{
					ID:     fmt.Sprintf("e%d", edgeCount),
					Source: fromChildren[i].runID,
					Target: toChildren[i].runID,
				})
				edgeCount++
				siblingFlowTarget[toChildren[i].runID] = true
			}
		}
	}

	// Workflow-definition-based spawning node lookup: for each child run,
	// find the subworkflow node in the parent's workflow whose target
	// workflow matches the child's workflow ID. This works even when
	// child_run state data is absent.
	runsByID := map[string]CompoundRunInput{}
	for _, run := range runs {
		runsByID[run.RunID] = run
	}
	for _, run := range runs {
		if run.ParentRun == "" {
			continue
		}
		if _, ok := spawningNode[run.RunID]; ok {
			continue // already matched via child_run state data
		}
		parent, ok := runsByID[run.ParentRun]
		if !ok || parent.Workflow == nil {
			continue
		}
		childWorkflowID := ""
		if run.Workflow != nil {
			childWorkflowID = run.Workflow.ID
		} else if run.RunState != nil {
			childWorkflowID = run.RunState.WorkflowID
		}
		if childWorkflowID == "" {
			continue
		}
		parentTemplates := buildTemplateToOrchestratorMap(parent.Workflow)
		for _, wfNode := range parent.Workflow.Nodes {
			if wfNode.Kind != kindSubworkflow || strings.TrimSpace(wfNode.Workflow) != childWorkflowID {
				continue
			}
			// If the matching node is a for_each body template, attribute
			// spawning to its (visible) orchestrator instead.
			visibleID := wfNode.ID
			if orchID, isTemplate := parentTemplates[wfNode.ID]; isTemplate {
				visibleID = orchID
			}
			baseKey := fmt.Sprintf("%s::%s", run.ParentRun, visibleID)
			spawningNode[run.RunID] = baseKey
			spawnedChild[baseKey] = append(spawnedChild[baseKey], indexedChild{index: 0, runID: run.RunID})
			break
		}
	}

	// Inter-run edges: spawning node -> child run.
	for _, run := range runs {
		if run.ParentRun == "" {
			continue
		}
		if siblingFlowTarget[run.RunID] {
			continue
		}
		source := run.ParentRun
		if sn, ok := spawningNode[run.RunID]; ok {
			source = sn
		}
		edges = append(edges, TopologyEdge{
			ID:     fmt.Sprintf("e%d", edgeCount),
			Source: source,
			Target: run.RunID,
		})
		edgeCount++
	}

	if nodes == nil {
		nodes = []TopologyNode{}
	}
	if edges == nil {
		edges = []TopologyEdge{}
	}
	return TopologyGraph{Nodes: nodes, Edges: edges}
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// runDescription returns the best available description for a run container.
// Uses the run's LLM-generated description if it looks specific (not identical
// to the workflow description). Falls back to the workflow description.
func runDescription(run CompoundRunInput) string {
	runDesc := ""
	wfDesc := ""
	if run.RunState != nil {
		runDesc = strings.TrimSpace(run.RunState.Description)
	}
	if run.Workflow != nil {
		wfDesc = strings.TrimSpace(run.Workflow.Description)
	}
	// Prefer the run description if it differs from the workflow boilerplate.
	if runDesc != "" && runDesc != wfDesc {
		return firstLine(runDesc)
	}
	if wfDesc != "" {
		return firstLine(wfDesc)
	}
	return ""
}

// lessByPrefixAndIndex orders ForEach expansion IDs of the form
// "{prefix}::{index}" by prefix lexicographically, then by numeric index
// (so tmpl::2 sorts before tmpl::10). IDs without a parseable numeric
// suffix sort lexicographically among themselves.
func lessByPrefixAndIndex(a, b string) bool {
	aPrefix, aIdx, aOK := splitExpandedID(a)
	bPrefix, bIdx, bOK := splitExpandedID(b)
	if aOK && bOK {
		if aPrefix != bPrefix {
			return aPrefix < bPrefix
		}
		return aIdx < bIdx
	}
	return a < b
}

// splitExpandedID parses an expansion ID of the form "{prefix}::{index}".
// Uses strings.Index (first occurrence) to match the consumer code which
// uses strings.SplitN(id, "::", 2) for parent inference. The validator
// now rejects "::" in node IDs, so legitimate expansion IDs have exactly
// one "::" and the choice doesn't matter for valid inputs — but
// consistency keeps the sort and the parent lookup in agreement on any
// pathological input that slips through (e.g. manually-edited state).
func splitExpandedID(id string) (prefix string, index int, ok bool) {
	idx := strings.Index(id, "::")
	if idx < 0 {
		return id, 0, false
	}
	n, err := strconv.Atoi(id[idx+2:])
	if err != nil {
		return id, 0, false
	}
	return id[:idx], n, true
}

// buildTopologyEdges converts workflow edges (including loop-exhausted escape
// edges) into topology edges with IsEscape set appropriately. Edges
// touching for_each body templates are dropped — the template node is
// hidden (subsumed by its orchestrator), so those edges would point at
// nodes not present in the graph.
//
// Why drop rather than rewrite to the orchestrator? Validation
// (validateTemplateIncomingEdges + validateTemplateEdgeTargets in
// internal/definitions/graph_validation.go) guarantees that in a valid
// workflow the only edge touching a template is `template → orchestrator`
// — per-item failure-routing metadata the engine consumes directly.
// Rewriting both endpoints to the orchestrator would collapse that to a
// self-loop we'd skip anyway, so drop is equivalent for valid input and
// safer for invalid input (rewriting a template-as-source edge would
// invent a visible flow edge on the orchestrator that wasn't in the
// definition).
func buildTopologyEdges(workflow *definitions.Workflow) []TopologyEdge {
	templates := buildTemplateToOrchestratorMap(workflow)
	edges := make([]TopologyEdge, 0, len(workflow.Edges))
	for i, edge := range workflow.Edges {
		if _, ok := templates[edge.From]; ok {
			continue
		}
		if _, ok := templates[edge.To]; ok {
			continue
		}
		edges = append(edges, TopologyEdge{
			ID:     fmt.Sprintf("e%d", i),
			Source: edge.From,
			Target: edge.To,
			Label:  strings.TrimSpace(edge.When),
		})
	}

	return edges
}

// ClassifyNode returns the TopologyNode.Kind value for a node ID in the
// given workflow. Accepts ForEach iteration IDs (parent::N). Returns ""
// when the node is unknown.
func ClassifyNode(workflow *definitions.Workflow, nodeID string) string {
	if workflow == nil || nodeID == "" {
		return ""
	}
	baseID := nodeID
	isIteration := false
	if idx := strings.Index(nodeID, "::"); idx >= 0 {
		baseID = nodeID[:idx]
		isIteration = true
	}
	node := definitions.FindNode(workflow, baseID)
	if node == nil {
		return ""
	}
	if isIteration {
		return kindForeachIteration
	}
	// A ForEach orchestrator is any node that drives foreach. The legacy
	// inline shape had Kind==subworkflow alongside ForEach; the body-ref
	// shape has no Kind on the orchestrator (only on the body template).
	if node.ForEach != nil {
		return kindSubworkflowForeach
	}
	if node.Kind == kindSubworkflow {
		return kindSubworkflow
	}
	if strings.TrimSpace(node.Role) != "" {
		return kindLeafRole
	}
	return kindLeafSystem
}
