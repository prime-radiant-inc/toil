package dashboard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/visualize"
)

// writeSSEEvent writes a single SSE event to the writer and flushes.
func writeSSEEvent(w io.Writer, flusher http.Flusher, eventName string, id string, data string) {
	if eventName != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", eventName)
	}
	if id != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", id)
	}
	for _, line := range strings.Split(data, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
	flusher.Flush()
}

func (server *Server) handleRunStream(w http.ResponseWriter, r *http.Request, runID string) {
	if runID == "" {
		http.Error(w, "missing run ID", http.StatusBadRequest)
		return
	}

	eventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	file, err := os.Open(eventPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	reader := bufio.NewReader(file)
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				select {
				case <-r.Context().Done():
					return
				case <-pingTicker.C:
					_, _ = fmt.Fprint(w, ": ping\n\n")
					flusher.Flush()
				case <-time.After(500 * time.Millisecond):
				}
				continue
			}
			return
		}

		payload := strings.TrimRight(line, "\r\n")
		if payload == "" {
			continue
		}

		var event state.Event
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			slog.Warn("skipping malformed event", "error", err)
			continue
		}

		server.emitSSEEvents(w, flusher, &event, payload)
	}
}

func (server *Server) emitSSEEvents(w io.Writer, flusher http.Flusher, event *state.Event, rawJSON string) {
	switch event.Type {
	case eventNodeOutput:
		server.emitTranscriptItem(w, flusher, event)
		writeSSEEvent(w, flusher, "timeline-refresh", "", "")

	case eventNodeStarted, eventNodeCompleted, eventNodeFailed, eventNodeFailedHandled, eventNodeSkipped:
		server.emitNodeStatus(w, flusher, event)
		server.emitTranscriptDivider(w, flusher, event)
		writeSSEEvent(w, flusher, "timeline-refresh", "", "")
		writeSSEEvent(w, flusher, "graph-update", "", rawJSON)

	case eventRunStarted, eventRunCompleted, eventRunFailed, eventRunCancelled, eventRunPaused, eventRunResumed:
		writeSSEEvent(w, flusher, "timeline-refresh", "", "")
		runStatus := strings.TrimPrefix(event.Type, "run_")
		if event.Type == eventRunCompleted {
			if hu, ok := event.Data["has_unresolved_failure"].(bool); ok && hu {
				runStatus = statusFailed
			}
		}
		writeSSEEvent(w, flusher, "run-status", "", fmt.Sprintf(`{"status":"%s"}`, runStatus))
		writeSSEEvent(w, flusher, "graph-update", "", rawJSON)

	case eventSubworkflowStarted, "subworkflow_completed", "subworkflow_failed":
		writeSSEEvent(w, flusher, "timeline-refresh", "", "")
		writeSSEEvent(w, flusher, "graph-update", "", rawJSON)

	case "subworkflow_pending", "subworkflow_reentry":
		writeSSEEvent(w, flusher, "graph-update", "", rawJSON)

	case eventApprovalRequested, eventApprovalResolved:
		writeSSEEvent(w, flusher, "timeline-refresh", "", "")
		if event.Type == eventApprovalResolved {
			writeSSEEvent(w, flusher, "graph-update", "", rawJSON)
		}

	case "wave_started", "wave_completed":
		// Engine internals — not useful for operators, skip.
	}
}

func (server *Server) emitTranscriptItem(w io.Writer, flusher http.Flusher, event *state.Event) {
	items := ExtractTranscriptItems(event.Text, event.Timestamp)
	for _, item := range items {
		html := server.renderPartial("transcript-item", item)
		if html == "" {
			continue
		}
		id := "nodeId:" + event.NodeID
		if item.ToolUseID != "" {
			id += ",toolUseId:" + item.ToolUseID
		}
		writeSSEEvent(w, flusher, "transcript-item", id, html)
	}
}

func (server *Server) emitTranscriptDivider(w io.Writer, flusher http.Flusher, event *state.Event) {
	if event.NodeID == "" {
		return
	}
	sessionID, _ := event.Data["session_id"].(string)
	var item TranscriptItem
	switch event.Type {
	case eventNodeStarted:
		resume, _ := event.Data["resume"].(bool)
		item = TranscriptItem{
			Kind:      transcriptKindDivider,
			Timestamp: event.Timestamp,
			SessionID: sessionID,
			Text:      resumeLabel(resume),
		}
	case eventNodeCompleted, eventNodeFailed, eventNodeFailedHandled:
		decision, _ := event.Data[fieldDecision].(string)
		var durationMs int64
		if event.DurationMs != nil {
			durationMs = *event.DurationMs
		}
		var status string
		switch event.Type {
		case eventNodeFailed:
			status = statusFailed
		case eventNodeFailedHandled:
			status = statusFailedHandled
		default:
			status = statusCompleted
		}
		item = TranscriptItem{
			Kind:       transcriptKindDivider,
			IsEnd:      true,
			Timestamp:  event.Timestamp,
			SessionID:  sessionID,
			Decision:   decision,
			DurationMs: durationMs,
			Text:       status,
		}
	default:
		return
	}

	html := server.renderPartial("transcript-divider", item)
	if html == "" {
		return
	}
	writeSSEEvent(w, flusher, "transcript-item", "nodeId:"+event.NodeID, html)
}

func (server *Server) emitNodeStatus(w io.Writer, flusher http.Flusher, event *state.Event) {
	status := strings.TrimPrefix(event.Type, "node_")
	data := struct{ ID, Status string }{event.NodeID, status}
	html := server.renderPartial("node-status-badge", data)
	writeSSEEvent(w, flusher, "node-status", event.NodeID, html)
}

// renderPartial renders a named template to a string.
func (server *Server) renderPartial(name string, data any) string {
	var buf bytes.Buffer
	if err := server.templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("failed to render partial", "template", name, "error", err)
		return ""
	}
	return buf.String()
}

func (server *Server) handleNodeTranscript(w http.ResponseWriter, r *http.Request, runID string, suffix string) {
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	nodeID := parts[1]

	eventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	items, err := server.loadNodeTranscript(nodeID, eventPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		slog.Error("loadNodeTranscript failed", "runID", runID, "nodeID", nodeID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	merged := MergeTranscriptItems(items)

	tmplName := "transcript-item"
	if r.URL.Query().Get("compact") == "1" {
		tmplName = "transcript-item-compact"
	}

	var buf bytes.Buffer
	for _, item := range merged {
		if err := server.templates.ExecuteTemplate(&buf, tmplName, item); err != nil {
			slog.Error("failed to render transcript item", "error", err)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// TimelineEntry is a single event in a chronological run timeline.
type TimelineEntry struct {
	Kind       string // "turn", "dispatch", "completion", "node_end", "failure", "approval", "run_event"
	Timestamp  time.Time
	NodeID     string
	Decision   string
	Message    string
	ChildRunID string
	ChildWf    string
	DurationMs int64
	BasePath   string

	// Additional fields for integrated event types.
	ErrorText    string
	ApprovalHTML template.HTML
	EventType    string // raw event type for run_event/failure kinds
	TypeLabel    string // human-readable label
	BadgeClass   string // CSS class for badge

	// Parent context for run lifecycle events.
	ParentRunID string
}

// ChildRunTimeline wraps metadata about a child run plus its flat timeline.
type ChildRunTimeline struct {
	RunID      string
	Title      string
	Status     string
	StartedAt  time.Time
	DurationMs int64
	Entries    []TimelineEntry
	BasePath   string
}

func (server *Server) handleNodeSubworkflows(w http.ResponseWriter, r *http.Request, runID string, suffix string) {
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	nodeID := parts[1]

	// First, find child runs from the parent run's events
	parentEventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	childRefs, err := collectChildRunRefs(nodeID, parentEventPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		slog.Error("collectChildRunRefs failed", "runID", runID, "nodeID", nodeID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var timelines []ChildRunTimeline
	for _, cr := range childRefs {
		tl := server.buildChildRunTimeline(cr)
		timelines = append(timelines, tl)
	}

	var buf bytes.Buffer
	if err := server.templates.ExecuteTemplate(&buf, "subworkflow-timeline", timelines); err != nil {
		slog.Error("failed to render subworkflow timeline", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

type childRunRef struct {
	runID      string
	startedAt  time.Time
	durationMs int64
}

func collectChildRunRefs(nodeID, eventPath string) ([]childRunRef, error) {
	file, err := os.Open(eventPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	prefix := nodeID + "::"
	seen := map[string]*childRunRef{}
	var order []string

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.NodeID != nodeID && !strings.HasPrefix(event.NodeID, prefix) {
			continue
		}
		if !strings.HasPrefix(event.Type, "subworkflow_") {
			continue
		}

		childRun, _ := event.Data["child_run"].(string)
		if childRun == "" {
			continue
		}

		if _, ok := seen[childRun]; !ok {
			ref := &childRunRef{
				runID:     childRun,
				startedAt: event.Timestamp,
			}
			seen[childRun] = ref
			order = append(order, childRun)
		}

		if event.DurationMs != nil {
			seen[childRun].durationMs = *event.DurationMs
		}
	}

	var refs []childRunRef
	for _, id := range order {
		refs = append(refs, *seen[id])
	}
	return refs, scanner.Err()
}

func (server *Server) buildChildRunTimeline(ref childRunRef) ChildRunTimeline {
	tl := ChildRunTimeline{
		RunID:      ref.runID,
		StartedAt:  ref.startedAt,
		DurationMs: ref.durationMs,
		BasePath:   server.basePath,
		Status:     "unknown",
	}

	statePath := filepath.Join(server.runsDir, ref.runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		slog.Debug("could not load child run state", "runID", ref.runID, "error", err)
		return tl
	}
	tl.Title = rs.Title
	tl.Status = EffectiveStatus(rs.Status, rs.HasUnresolvedFailure)

	entries, err := extractRunTimeline(filepath.Join(server.runsDir, ref.runID, "events.jsonl"), server.basePath, rs.ParentRun)
	if err != nil {
		slog.Debug("could not extract timeline", "runID", ref.runID, "error", err)
		return tl
	}
	tl.Entries = entries
	return tl
}

// extractRunTimeline reads a run's events.jsonl and produces a flat
// chronological list of timeline entries covering all operator-visible events:
// node lifecycle, communicate turns, subworkflow dispatches, failures,
// approvals, and run lifecycle.
func extractRunTimeline(eventPath, basePath, parentRunID string) ([]TimelineEntry, error) {
	file, err := os.Open(eventPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var entries []TimelineEntry
	// Track the last turn index per node so we can attach decisions from
	// node_completed to the correct turn entry.
	lastTurnIdx := map[string]int{}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		switch event.Type {
		case eventNodeStarted:
			entries = append(entries, TimelineEntry{
				Kind:       kindNodeEvent,
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				EventType:  event.Type,
				TypeLabel:  humanizeStatus(event.Type),
				BadgeClass: EventBadgeClass(event.Type),
			})

		case eventNodeOutput:
			if msg := extractCommunicateMessage(event.Text); msg != "" {
				lastTurnIdx[event.NodeID] = len(entries)
				entries = append(entries, TimelineEntry{
					Kind:      "turn",
					Timestamp: event.Timestamp,
					NodeID:    event.NodeID,
					Message:   msg,
				})
			}

		case eventNodeCompleted:
			decision, _ := event.Data[fieldDecision].(string)
			var durationMs int64
			if event.DurationMs != nil {
				durationMs = *event.DurationMs
			}
			// Attach decision to the last turn entry for this node, if any
			if idx, ok := lastTurnIdx[event.NodeID]; ok {
				entries[idx].Decision = decision
				delete(lastTurnIdx, event.NodeID)
			}
			entries = append(entries, TimelineEntry{
				Kind:       kindNodeEvent,
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				Decision:   decision,
				DurationMs: durationMs,
				EventType:  event.Type,
				TypeLabel:  humanizeStatus(event.Type),
				BadgeClass: EventBadgeClass(event.Type),
			})

		case eventNodeSkipped:
			entries = append(entries, TimelineEntry{
				Kind:       kindNodeEvent,
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				EventType:  event.Type,
				TypeLabel:  humanizeStatus(event.Type),
				BadgeClass: EventBadgeClass(event.Type),
			})

		case eventNodeFailed, eventNodeFailedHandled:
			kind := "failure"
			if event.Type == eventNodeFailedHandled {
				kind = kindNodeEvent
			}
			entries = append(entries, TimelineEntry{
				Kind:       kind,
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				ErrorText:  event.Text,
				EventType:  event.Type,
				TypeLabel:  humanizeStatus(event.Type),
				BadgeClass: EventBadgeClass(event.Type),
			})

		case eventSubworkflowStarted:
			childRun, _ := event.Data["child_run"].(string)
			childWf, _ := event.Data["child_workflow"].(string)
			entries = append(entries, TimelineEntry{
				Kind:       "dispatch",
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				ChildRunID: childRun,
				ChildWf:    childWf,
				BasePath:   basePath,
			})

		case "subworkflow_completed", "subworkflow_failed":
			childRun, _ := event.Data["child_run"].(string)
			var durationMs int64
			if event.DurationMs != nil {
				durationMs = *event.DurationMs
			}
			entries = append(entries, TimelineEntry{
				Kind:       "completion",
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				ChildRunID: childRun,
				DurationMs: durationMs,
				BasePath:   basePath,
			})

		case eventRunStarted, eventRunCompleted, eventRunFailed, eventRunCancelled, eventRunPaused, eventRunResumed:
			entries = append(entries, TimelineEntry{
				Kind:        "run_event",
				Timestamp:   event.Timestamp,
				EventType:   event.Type,
				TypeLabel:   humanizeStatus(event.Type),
				BadgeClass:  EventBadgeClass(event.Type),
				ErrorText:   event.Text,
				ParentRunID: parentRunID,
				BasePath:    basePath,
			})

		case eventApprovalRequested:
			entries = append(entries, TimelineEntry{
				Kind:         "approval",
				Timestamp:    event.Timestamp,
				NodeID:       event.NodeID,
				ApprovalHTML: renderMarkdown(event.Text),
				EventType:    event.Type,
				TypeLabel:    humanizeStatus(event.Type),
				BadgeClass:   EventBadgeClass(event.Type),
			})

		case eventApprovalResolved:
			decision, _ := event.Data[fieldDecision].(string)
			entries = append(entries, TimelineEntry{
				Kind:       "approval",
				Timestamp:  event.Timestamp,
				NodeID:     event.NodeID,
				Decision:   decision,
				EventType:  event.Type,
				TypeLabel:  humanizeStatus(event.Type),
				BadgeClass: EventBadgeClass(event.Type),
			})
		}
	}

	return entries, scanner.Err()
}

func (server *Server) handleRunTimeline(w http.ResponseWriter, r *http.Request, runID string) {
	eventPath := filepath.Join(server.runsDir, runID, "events.jsonl")

	// Load parent run context from state.json if available.
	var parentRunID string
	statePath := filepath.Join(server.runsDir, runID, "state.json")
	if rs, err := state.LoadState(statePath); err == nil {
		parentRunID = rs.ParentRun
	}

	entries, err := extractRunTimeline(eventPath, server.basePath, parentRunID)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		slog.Error("extractRunTimeline failed", "runID", runID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := server.templates.ExecuteTemplate(&buf, "timeline-entries", entries); err != nil {
		slog.Error("failed to render timeline entries", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (server *Server) loadNodeTranscript(nodeID, eventPath string) ([]TranscriptItem, error) {
	file, err := os.Open(eventPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var items []TranscriptItem
	execution := 0
	lastEndFailed := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // up to 4MB per line
	for scanner.Scan() {
		var event state.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.NodeID != nodeID {
			continue
		}
		switch event.Type {
		case eventNodeStarted:
			execution++
			sessionID, _ := event.Data["session_id"].(string)
			resume, _ := event.Data["resume"].(bool)
			// First execution is always a cycle. After that, a retry
			// follows a failure; anything else is a new graph cycle.
			isCycle := execution == 1 || !lastEndFailed
			items = append(items, TranscriptItem{
				Kind:      transcriptKindDivider,
				Timestamp: event.Timestamp,
				Attempt:   execution,
				SessionID: sessionID,
				IsCycle:   isCycle,
				Text:      resumeLabel(resume),
			})
		case eventNodeOutput:
			parsed := ExtractTranscriptItems(event.Text, event.Timestamp)
			items = append(items, parsed...)
		case eventNodeCompleted, eventNodeFailed, eventNodeFailedHandled:
			sessionID, _ := event.Data["session_id"].(string)
			decision, _ := event.Data[fieldDecision].(string)
			var durationMs int64
			if event.DurationMs != nil {
				durationMs = *event.DurationMs
			}
			failed := event.Type == eventNodeFailed
			lastEndFailed = failed
			var status string
			switch event.Type {
			case eventNodeFailed:
				status = statusFailed
			case eventNodeFailedHandled:
				status = statusFailedHandled
			default:
				status = statusCompleted
			}
			items = append(items, TranscriptItem{
				Kind:       transcriptKindDivider,
				IsEnd:      true,
				Timestamp:  event.Timestamp,
				Attempt:    execution,
				SessionID:  sessionID,
				Decision:   decision,
				DurationMs: durationMs,
				Text:       status,
			})
		}
	}
	return items, scanner.Err()
}

func resumeLabel(resume bool) string {
	if resume {
		return "resumed"
	}
	return "new"
}

// handleNodeDetail is the unified node-detail endpoint. It inspects node
// state and workflow definition, classifies the node, and delegates to
// the appropriate render path. Every kind returns the same shape: an
// HTML fragment with a root element carrying data-node-detail-kind.
func (server *Server) handleNodeDetail(w http.ResponseWriter, r *http.Request, runID string, suffix string) {
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	nodeID := parts[1]

	runDir := filepath.Join(server.runsDir, runID)
	workflow, err := loadRunWorkflow(runDir)
	if err != nil {
		slog.Error("handleNodeDetail: load workflow", "runID", runID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	kind := visualize.ClassifyNode(workflow, nodeID)
	switch kind {
	case "leaf-role", "leaf-system":
		if decision, _ := server.lookupSkip(runID, nodeID); decision != "" {
			server.renderSkippedDetail(w, runID, nodeID)
			return
		}
		server.renderLeafDetail(w, r, runID, nodeID, kind)
	case "foreach-iteration":
		// A ForEach iteration of a subworkflow has no direct transcript — the
		// content lives in the dispatched child run. Route to that view.
		baseID := nodeID
		if idx := strings.Index(nodeID, "::"); idx >= 0 {
			baseID = nodeID[:idx]
		}
		if base := definitions.FindNode(workflow, baseID); base != nil && isSubworkflowDispatcher(workflow, base) {
			if cr := server.lookupChildRun(runID, nodeID); cr != "" {
				server.renderForeachIterationChildRun(w, runID, nodeID, cr)
				return
			}
		}
		if decision, _ := server.lookupSkip(runID, nodeID); decision != "" {
			server.renderSkippedDetail(w, runID, nodeID)
			return
		}
		server.renderLeafDetail(w, r, runID, nodeID, kind)
	case kindSubworkflowBaseKind:
		server.renderSubworkflowDetail(w, r, runID, nodeID, kind)
	case "subworkflow-foreach":
		server.renderForeachDispatcherDetail(w, r, runID, nodeID)
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div data-node-detail-kind="unknown" class="text-muted text-xs p-2">Node not found.</div>`))
	}
}

// kindSubworkflowBaseKind is the workflow-definition kind value for
// dispatcher nodes. Distinct from the topology Kind enum.
const kindSubworkflowBaseKind = "subworkflow"

// kindNodeEvent is the timeline-entry kind for node lifecycle events.
const kindNodeEvent = "node_event"

// isSubworkflowDispatcher reports whether node spawns child runs — either
// directly (kind=subworkflow) or indirectly via a ForEach whose body
// references a subworkflow template node.
func isSubworkflowDispatcher(workflow *definitions.Workflow, node *definitions.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == kindSubworkflowBaseKind {
		return true
	}
	if node.ForEach != nil && node.ForEach.Body != "" {
		if tmpl := definitions.FindNode(workflow, node.ForEach.Body); tmpl != nil && tmpl.Kind == kindSubworkflowBaseKind {
			return true
		}
	}
	return false
}

// lookupChildRun finds the child_run data field on the given node's state
// entry (if any). Used for foreach-iteration rendering of subworkflows.
func (server *Server) lookupChildRun(runID, nodeID string) string {
	path := filepath.Join(server.runsDir, runID, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e state.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.NodeID != nodeID {
			continue
		}
		if cr, ok := e.Data["child_run"].(string); ok && cr != "" {
			return cr
		}
	}
	return ""
}

// renderForeachIterationChildRun renders a foreach-iteration subworkflow
// node by showing the dispatched child run's own timeline, with the
// iteration breadcrumb.
func (server *Server) renderForeachIterationChildRun(w http.ResponseWriter, runID, nodeID, childRunID string) {
	timeline := server.buildChildRunTimeline(childRunRef{runID: childRunID})
	baseID, iterSuffix := nodeID, ""
	if idx := strings.Index(nodeID, "::"); idx >= 0 {
		baseID = nodeID[:idx]
		iterSuffix = nodeID[idx+2:]
	}
	data := struct {
		BaseID     string
		IterSuffix string
		Timeline   ChildRunTimeline
	}{BaseID: baseID, IterSuffix: iterSuffix, Timeline: timeline}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "node-detail-foreach-iteration", data); err != nil {
		slog.Error("renderForeachIterationChildRun: execute template", "error", err)
	}
	_ = runID
}

// lookupSkip scans the events file for a node_skipped event for this node
// and returns the decision + timestamp if present.
func (server *Server) lookupSkip(runID, nodeID string) (string, time.Time) {
	path := filepath.Join(server.runsDir, runID, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return "", time.Time{}
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e state.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.NodeID != nodeID || e.Type != "node_skipped" {
			continue
		}
		decision, _ := e.Data[fieldDecision].(string)
		if decision == "" {
			decision = "skip"
		}
		return decision, e.Timestamp
	}
	return "", time.Time{}
}

func (server *Server) renderSkippedDetail(w http.ResponseWriter, runID, nodeID string) {
	workflow, _ := loadRunWorkflow(filepath.Join(server.runsDir, runID))
	baseID := nodeID
	if idx := strings.Index(nodeID, "::"); idx >= 0 {
		baseID = nodeID[:idx]
	}
	var node *definitions.Node
	if workflow != nil {
		node = definitions.FindNode(workflow, baseID)
	}
	decision, when := server.lookupSkip(runID, nodeID)
	data := struct {
		NodeID    string
		Decision  string
		Prompt    string
		Timestamp time.Time
	}{NodeID: nodeID, Decision: decision, Timestamp: when}
	if node != nil {
		data.Prompt = strings.TrimSpace(node.Prompt)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "node-detail-skipped", data); err != nil {
		slog.Error("renderSkippedDetail: execute template", "error", err)
	}
}

func (server *Server) renderLeafDetail(w http.ResponseWriter, r *http.Request, runID, nodeID, kind string) {
	eventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	items, err := server.loadNodeTranscript(nodeID, eventPath)
	if err != nil && !os.IsNotExist(err) {
		slog.Error("renderLeafDetail: loadNodeTranscript", "runID", runID, "nodeID", nodeID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	merged := MergeTranscriptItems(items)

	if len(merged) == 0 {
		server.renderPendingDetail(w, runID, nodeID)
		return
	}

	var buf bytes.Buffer
	buf.WriteString(`<div data-node-detail-kind="` + kind + `" class="transcript-compact space-y-1">`)
	if kind == "foreach-iteration" {
		baseID, iterSuffix := nodeID, ""
		if idx := strings.Index(nodeID, "::"); idx >= 0 {
			baseID = nodeID[:idx]
			iterSuffix = nodeID[idx+2:]
		}
		buf.WriteString(`<div class="text-xs text-muted mb-2"><span class="font-mono">`)
		buf.WriteString(template.HTMLEscapeString(baseID))
		buf.WriteString(`</span> → iteration `)
		buf.WriteString(template.HTMLEscapeString(iterSuffix))
		buf.WriteString(`</div>`)
	}
	for _, item := range merged {
		if err := server.templates.ExecuteTemplate(&buf, "transcript-item", item); err != nil {
			slog.Error("renderLeafDetail: render item", "error", err)
		}
	}
	buf.WriteString(`</div>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (server *Server) renderPendingDetail(w http.ResponseWriter, runID, nodeID string) {
	workflow, _ := loadRunWorkflow(filepath.Join(server.runsDir, runID))
	baseID := nodeID
	if idx := strings.Index(nodeID, "::"); idx >= 0 {
		baseID = nodeID[:idx]
	}
	var node *definitions.Node
	if workflow != nil {
		node = definitions.FindNode(workflow, baseID)
	}
	data := struct {
		NodeID        string
		Prompt        string
		PendingReason string
	}{
		NodeID: nodeID,
	}
	if node != nil {
		data.Prompt = strings.TrimSpace(node.Prompt)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "node-detail-pending", data); err != nil {
		slog.Error("renderPendingDetail: execute template", "error", err)
	}
}

func loadRunWorkflow(runDir string) (*definitions.Workflow, error) {
	return definitions.LoadWorkflowFile(filepath.Join(runDir, "workflow.yaml"))
}

func (server *Server) renderSubworkflowDetail(w http.ResponseWriter, _ *http.Request, runID, nodeID, kind string) {
	parentEventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	childRefs, err := collectChildRunRefs(nodeID, parentEventPath)
	if err != nil && !os.IsNotExist(err) {
		slog.Error("renderSubworkflowDetail: collectChildRunRefs", "runID", runID, "nodeID", nodeID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var timelines []ChildRunTimeline
	for _, cr := range childRefs {
		timelines = append(timelines, server.buildChildRunTimeline(cr))
	}

	data := struct {
		Kind      string
		Timelines []ChildRunTimeline
	}{Kind: kind, Timelines: timelines}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "node-detail-subworkflow", data); err != nil {
		slog.Error("renderSubworkflowDetail: execute template", "error", err)
	}
}

// ForeachIterationView pairs an iteration index with its child-run timeline.
type ForeachIterationView struct {
	Index    int
	Timeline *ChildRunTimeline
}

func (server *Server) renderForeachDispatcherDetail(w http.ResponseWriter, _ *http.Request, runID, nodeID string) {
	parentEventPath := filepath.Join(server.runsDir, runID, "events.jsonl")
	iterations, err := collectForeachIterations(nodeID, parentEventPath)
	if err != nil && !os.IsNotExist(err) {
		slog.Error("renderForeachDispatcherDetail: collectForeachIterations", "runID", runID, "nodeID", nodeID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]ForeachIterationView, 0, len(iterations))
	for _, it := range iterations {
		var tl *ChildRunTimeline
		if it.childRunID != "" {
			built := server.buildChildRunTimeline(childRunRef{runID: it.childRunID, startedAt: it.startedAt})
			tl = &built
		}
		views = append(views, ForeachIterationView{Index: it.index, Timeline: tl})
	}

	data := struct {
		Iterations []ForeachIterationView
	}{Iterations: views}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "node-detail-foreach-dispatcher", data); err != nil {
		slog.Error("renderForeachDispatcherDetail: execute template", "error", err)
	}
}

type foreachIteration struct {
	index      int
	stateID    string
	childRunID string
	startedAt  time.Time
}

// collectForeachIterations scans the parent events.jsonl for
// subworkflow_started events where NodeID == parent::N, pairing each
// iteration index with the child run ID. Sorted by index.
func collectForeachIterations(parentNodeID, eventPath string) ([]foreachIteration, error) {
	file, err := os.Open(eventPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	seen := map[int]foreachIteration{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	prefix := parentNodeID + "::"
	for scanner.Scan() {
		var e state.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if !strings.HasPrefix(e.NodeID, prefix) {
			continue
		}
		if e.Type != eventSubworkflowStarted {
			continue
		}
		suffix := strings.TrimPrefix(e.NodeID, prefix)
		idx, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		childRun, _ := e.Data["child_run"].(string)
		if existing, ok := seen[idx]; ok && existing.childRunID != "" {
			continue
		}
		seen[idx] = foreachIteration{
			index:      idx,
			stateID:    e.NodeID,
			childRunID: childRun,
			startedAt:  e.Timestamp,
		}
	}

	out := make([]foreachIteration, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].index < out[j].index })
	return out, nil
}
