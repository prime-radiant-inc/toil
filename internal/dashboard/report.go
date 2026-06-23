package dashboard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

const (
	eventNodeStarted          = "node_started"
	eventNodeCompleted        = "node_completed"
	eventNodeFailed           = "node_failed"
	eventNodeFailedHandled    = "node_failed_handled"
	eventNodeSkipped          = "node_skipped"
	eventNodeOutput           = "node_output"
	eventApprovalRequested    = "approval_requested"
	eventApprovalResolved     = "approval_resolved"
	eventSubworkflowStarted   = "subworkflow_started"
	eventSubworkflowCompleted = "subworkflow_completed"
	eventSubworkflowFailed    = "subworkflow_failed"
	eventRunStarted           = "run_started"
	eventRunCompleted         = "run_completed"
	eventRunFailed            = "run_failed"
	eventRunCancelled         = "run_cancelled"
	eventRunPaused            = "run_paused"
	eventRunResumed           = "run_resumed"
)

// ChildRunLink is a lightweight reference to a child run for in-page linking.
type ChildRunLink struct {
	ID    string
	Title string
}

// ReportNode represents a workflow node within a run, with its status and
// any communicate messages extracted from the run's event log.
type ReportNode struct {
	ID          string // run_id::node_id
	RunID       string
	Label       string
	Status      string
	Decision    string
	Messages    []string
	StartedAt   *time.Time
	FinishedAt  *time.Time
	DurationMs  int64
	Artifacts   []string
	ChildRuns   []ChildRunLink // legacy link list (kept for other consumers)
	SpawnedRuns []RunTreeNode  // full subtrees of runs this node dispatched, rendered inline
}

// RunReport holds report nodes split around the ForEach boundary so that
// nodes executing after child runs render in the correct visual position.
type RunReport struct {
	PreChildren  []ReportNode // nodes before (and including) the ForEach trigger
	PostChildren []ReportNode // nodes that execute after child runs complete
}

// BuildReportByRun walks the execution group and extracts communicate messages
// from each run's events, returning a map from run ID to its report.
// Each dispatcher node carries the full RunTreeNode(s) of any child runs it
// spawned, so the template renders the child-run subtree inline at the
// dispatcher's position.
func (server *Server) BuildReportByRun(group *ExecutionGroupSummary) map[string]RunReport {
	if group == nil {
		return nil
	}

	// Build map from every run ID in the tree → its full RunTreeNode.
	treeByRunID := map[string]RunTreeNode{}
	var indexTree func(nodes []RunTreeNode)
	indexTree = func(nodes []RunTreeNode) {
		for _, n := range nodes {
			treeByRunID[n.Run.ID] = n
			indexTree(n.Children)
		}
	}
	indexTree(group.Tree)

	result := make(map[string]RunReport)
	for _, row := range group.Rows {
		runID := row.Run.ID
		nodeData := server.extractRunNodeData(runID)
		wf, _ := loadRunWorkflow(filepath.Join(server.runsDir, runID))
		bodyToOrch := foreachBodyOrchestratorMap(wf)
		spawnsByBaseNode := remapForeachBodySpawns(server.extractRunSpawnMap(runID), bodyToOrch)
		if len(nodeData) == 0 {
			continue
		}
		sort.Slice(nodeData, func(i, j int) bool {
			if nodeData[i].startedAt == nil {
				return true
			}
			if nodeData[j].startedAt == nil {
				return false
			}
			return nodeData[i].startedAt.Before(*nodeData[j].startedAt)
		})

		// Synthesize base-node entries for ::N iterations whose base has no
		// events of its own. ForEach body iterations (body::N) are deliberately
		// excluded — the body is a template, and its spawned child runs are
		// already attributed to the orchestrator via the bodyToOrch remap above.
		baseHasEntry := map[string]bool{}
		for _, nd := range nodeData {
			if !strings.Contains(nd.nodeID, "::") {
				baseHasEntry[nd.nodeID] = true
			}
		}
		synthesized := map[string]*nodeEventData{}
		for _, nd := range nodeData {
			idx := strings.Index(nd.nodeID, "::")
			if idx < 0 {
				continue
			}
			base := nd.nodeID[:idx]
			if baseHasEntry[base] {
				continue
			}
			if _, isForeachBody := bodyToOrch[base]; isForeachBody {
				continue
			}
			existing, ok := synthesized[base]
			if !ok {
				existing = &nodeEventData{
					nodeID:     base,
					status:     nd.status,
					startedAt:  nd.startedAt,
					finishedAt: nd.finishedAt,
					durationMs: nd.durationMs,
				}
				synthesized[base] = existing
				continue
			}
			// Accumulate: earliest start, latest finish, sum of durations.
			if nd.startedAt != nil && (existing.startedAt == nil || nd.startedAt.Before(*existing.startedAt)) {
				existing.startedAt = nd.startedAt
			}
			if nd.finishedAt != nil && (existing.finishedAt == nil || nd.finishedAt.After(*existing.finishedAt)) {
				existing.finishedAt = nd.finishedAt
			}
			existing.durationMs += nd.durationMs
		}
		for _, s := range synthesized {
			nodeData = append(nodeData, *s)
		}
		sort.Slice(nodeData, func(i, j int) bool {
			if nodeData[i].startedAt == nil {
				return true
			}
			if nodeData[j].startedAt == nil {
				return false
			}
			return nodeData[i].startedAt.Before(*nodeData[j].startedAt)
		})

		nodes := make([]ReportNode, 0, len(nodeData))
		for _, nd := range nodeData {
			// ForEach iteration state entries are accounted for by their
			// base dispatcher node; skip emitting a separate row for them.
			if strings.Contains(nd.nodeID, "::") {
				continue
			}
			if nd.status == "" && len(nd.messages) == 0 && len(spawnsByBaseNode[nd.nodeID]) == 0 {
				continue
			}

			rn := ReportNode{
				ID:         runID + "::" + nd.nodeID,
				RunID:      runID,
				Label:      nd.nodeID,
				Status:     nd.status,
				Decision:   nd.decision,
				Messages:   nd.messages,
				StartedAt:  nd.startedAt,
				FinishedAt: nd.finishedAt,
				DurationMs: nd.durationMs,
				Artifacts:  nd.artifacts,
			}
			// Attach any child-run subtrees this node dispatched.
			for _, childRunID := range spawnsByBaseNode[nd.nodeID] {
				subtree, ok := treeByRunID[childRunID]
				if !ok {
					continue
				}
				rn.SpawnedRuns = append(rn.SpawnedRuns, subtree)
				title := subtree.Run.Title
				if title == "" {
					title = subtree.Run.ID
				}
				rn.ChildRuns = append(rn.ChildRuns, ChildRunLink{ID: subtree.Run.ID, Title: title})
			}
			nodes = append(nodes, rn)
		}

		// Append pending nodes: any workflow-defined nodes that haven't been
		// reached yet. They slot in workflow-definition order, after every
		// node that has already run. Cross-referenced against the workflow
		// loaded from this run's workflow.yaml snapshot.
		nodes = appendPendingNodes(runID, wf, bodyToOrch, nodes)

		// With inline spawned subtrees, child runs render at their
		// dispatcher's position — no pre/post split needed. Keep
		// everything in PreChildren; PostChildren stays empty.
		result[runID] = RunReport{PreChildren: nodes}
	}

	return result
}

// appendPendingNodes appends ReportNodes for any workflow-defined nodes not
// already present in `nodes`. Pending nodes get status="pending" and no timing;
// they render at the end in workflow-definition order so the user can see
// what's yet to run. ForEach bodies are skipped — they are templates expanded
// into ::N iterations, not runtime entities.
func appendPendingNodes(runID string, wf *definitions.Workflow, bodyToOrch map[string]string, nodes []ReportNode) []ReportNode {
	if wf == nil {
		return nodes
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n.Label] = true
	}
	for _, wfNode := range wf.Nodes {
		if seen[wfNode.ID] {
			continue
		}
		if _, isForeachBody := bodyToOrch[wfNode.ID]; isForeachBody {
			continue
		}
		nodes = append(nodes, ReportNode{
			ID:     runID + "::" + wfNode.ID,
			RunID:  runID,
			Label:  wfNode.ID,
			Status: statusPending,
		})
	}
	return nodes
}

// foreachBodyOrchestratorMap returns a map from ForEach body node ID to its
// orchestrator node ID. Used to reattribute spawned child runs from the body
// (a template) to the orchestrator (the runtime dispatcher).
func foreachBodyOrchestratorMap(wf *definitions.Workflow) map[string]string {
	result := map[string]string{}
	if wf == nil {
		return result
	}
	for _, n := range wf.Nodes {
		if n.ForEach != nil && n.ForEach.Body != "" {
			result[n.ForEach.Body] = n.ID
		}
	}
	return result
}

// remapForeachBodySpawns rewrites spawn-map keys from ForEach body IDs to
// their orchestrator IDs. Called on the output of extractRunSpawnMap so that
// child runs spawned by iterations attribute to the dispatcher node that
// users actually see in the tree.
func remapForeachBodySpawns(spawns map[string][]string, bodyToOrch map[string]string) map[string][]string {
	out := map[string][]string{}
	for base, runs := range spawns {
		if orch, ok := bodyToOrch[base]; ok {
			out[orch] = append(out[orch], runs...)
		} else {
			out[base] = append(out[base], runs...)
		}
	}
	return out
}

// extractRunSpawnMap scans a run's events for subworkflow_started events and
// returns a map of base node ID (stripped of any ::N iteration suffix) →
// ordered list of child run IDs that node spawned.
func (server *Server) extractRunSpawnMap(runID string) map[string][]string {
	path := filepath.Join(server.runsDir, runID, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	spawns := map[string][]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e state.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.Type != eventSubworkflowStarted {
			continue
		}
		cr, _ := e.Data["child_run"].(string)
		if cr == "" {
			continue
		}
		base := e.NodeID
		if idx := strings.Index(base, "::"); idx >= 0 {
			base = base[:idx]
		}
		spawns[base] = append(spawns[base], cr)
	}
	return spawns
}

type nodeEventData struct {
	nodeID     string
	status     string
	decision   string
	messages   []string
	startedAt  *time.Time
	finishedAt *time.Time
	durationMs int64
	artifacts  []string
}

// extractRunNodeData loads events for a run and extracts communicate messages
// and status information per node.
func (server *Server) extractRunNodeData(runID string) []nodeEventData {
	eventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	file, err := os.Open(eventPath)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	// Track per-node data
	nodeMap := make(map[string]*nodeEventData)
	nodeOrder := make([]string, 0)

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 256*1024)
	scanner.Buffer(buf, 2*1024*1024)

	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.NodeID == "" {
			continue
		}

		nd, exists := nodeMap[event.NodeID]
		if !exists {
			nd = &nodeEventData{nodeID: event.NodeID}
			nodeMap[event.NodeID] = nd
			nodeOrder = append(nodeOrder, event.NodeID)
		}

		// Track first event time as startedAt
		if nd.startedAt == nil && !event.Timestamp.IsZero() {
			t := event.Timestamp
			nd.startedAt = &t
		}

		// Track status from lifecycle events
		switch event.Type {
		case eventNodeStarted, eventSubworkflowStarted:
			if nd.status == "" {
				nd.status = "started"
			}
		case eventNodeCompleted, eventSubworkflowCompleted:
			nd.status = statusCompleted
			if d, ok := event.Data[fieldDecision]; ok {
				if ds, ok := d.(string); ok {
					nd.decision = ds
				}
			}
			ft := event.Timestamp
			nd.finishedAt = &ft
			if event.DurationMs != nil {
				nd.durationMs = *event.DurationMs
			}
			if arts, ok := event.Data["artifacts"].([]any); ok {
				for _, a := range arts {
					if s, ok := a.(string); ok {
						nd.artifacts = append(nd.artifacts, s)
					}
				}
			}
		case eventNodeFailed, eventSubworkflowFailed:
			nd.status = statusFailed
			ft := event.Timestamp
			nd.finishedAt = &ft
			if event.DurationMs != nil {
				nd.durationMs = *event.DurationMs
			}
		case eventNodeFailedHandled:
			nd.status = statusFailedHandled
			ft := event.Timestamp
			nd.finishedAt = &ft
			if event.DurationMs != nil {
				nd.durationMs = *event.DurationMs
			}
		case eventNodeSkipped:
			nd.status = statusSkipped
		case eventNodeOutput:
			if msg := extractCommunicateMessage(event.Text); msg != "" {
				nd.messages = append(nd.messages, msg)
			}
		}
	}

	// Return in order of first appearance
	result := make([]nodeEventData, 0, len(nodeOrder))
	for _, nid := range nodeOrder {
		result = append(result, *nodeMap[nid])
	}
	return result
}

// extractCommunicateMessage extracts the message from a communicate tool call
// in a node_output event text. Handles serf log records, Anthropic format,
// and toil message envelopes.
func extractCommunicateMessage(text string) string {
	var val map[string]any
	if err := json.Unmarshal([]byte(text), &val); err != nil {
		return ""
	}

	// Serf log record: {"kind":"TOOL_CALL_START","data":{"tool_name":"communicate",...}}
	if kind, ok := val["kind"].(string); ok {
		if kind == "TOOL_CALL_START" {
			data, _ := val[fieldData].(map[string]any)
			if data == nil {
				return ""
			}
			toolName, _ := data["tool_name"].(string)
			if toolName != "communicate" {
				return ""
			}
			argsJSON, _ := data["arguments_json"].(string)
			if argsJSON == "" {
				return ""
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return ""
			}
			msg, _ := args[transcriptKindMessage].(string)
			return msg
		}
		return ""
	}

	// Anthropic format: {"content":[{"type":"tool_use","name":"communicate","input":{...}}]}
	if content, ok := val[fieldContent].([]any); ok {
		return findCommunicateInContent(content)
	}

	// Toil envelope: {"message":{"content":[...]}}
	if message, ok := val[transcriptKindMessage].(map[string]any); ok {
		if content, ok := message[fieldContent].([]any); ok {
			return findCommunicateInContent(content)
		}
	}

	return ""
}

func findCommunicateInContent(content []any) string {
	for _, block := range content {
		bm, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if bm["type"] != "tool_use" || bm["name"] != "communicate" {
			continue
		}
		input, ok := bm["input"].(map[string]any)
		if !ok {
			continue
		}
		msg, _ := input[transcriptKindMessage].(string)
		return msg
	}
	return ""
}
