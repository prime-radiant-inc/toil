package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/document"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/inspect"
	"primeradiant.com/toil/internal/interrogate"
	"primeradiant.com/toil/internal/interviews"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/visualize"
)

// Run lifecycle event type strings used in multiple handlers.
const (
	eventTypeRunCompleted = "run_completed"
	eventTypeRunFailed    = "run_failed"
	eventNodeCompleted    = "node_completed"
)

// JSON field and map-key names used across API responses and the OpenAPI spec.
const (
	fieldWorkflowID = "workflow_id"
	fieldInputs     = "inputs"
	fieldNodeID     = "node_id"
	fieldDecision   = "decision"
	fieldMessage    = "message"
	fieldError      = "error"
	fieldProjectDir = "project_dir"
	fieldStatus     = "status"
	fieldDesc       = "desc"
	fieldName       = "name"
	fieldValue      = "value"
	fieldNested     = "nested"
)

// OpenAPI schema type names.
const (
	schemaTypeObject = "object"
	schemaTypeString = "string"
)

// contentTypeJSON is the MIME type for JSON response bodies.
const contentTypeJSON = "application/json"

// Workspace mode names.
const (
	modeProject = "project"
	modeNone    = "none"
)

// queryFollowTrue is the canonical ?follow= value that switches JSON
// endpoints into SSE follow mode.
const queryFollowTrue = "true"

type Server struct {
	App            *app.App
	RunsDir        string
	Manager        *orchestrator.Manager
	Interrogations *interrogate.Manager
}

type runRequest struct {
	WorkflowID  string            `json:"workflow_id"`
	Inputs      map[string]any    `json:"inputs"`
	Env         map[string]string `json:"env,omitempty"`
	CallbackURL string            `json:"callback_url,omitempty"`
}

type runResponse struct {
	RunID string `json:"run_id"`
}

type runSummary struct {
	RunID                string            `json:"run_id"`
	WorkflowID           string            `json:"workflow_id"`
	Status               string            `json:"status"`
	HasUnresolvedFailure bool              `json:"has_unresolved_failure,omitempty"`
	StartedAt            time.Time         `json:"started_at"`
	FinishedAt           *time.Time        `json:"finished_at"`
	CallbackURL          string            `json:"callback_url"`
	Inputs               map[string]any    `json:"inputs"`
	RunTotal             *state.NodeTotals `json:"run_total,omitempty"`
}

// ServeHTTP dispatches API requests. If you add a new route here,
// also update BuildSpec() in openapi.go and the expectedRoutes list
// in openapi_test.go:TestOpenAPISpecCoversAllRoutes.
func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && path == "/workflows":
		server.handleWorkflowsList(writer)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/workflows/") && strings.HasSuffix(path, "/graph"):
		server.handleWorkflowGraph(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/workflows/"):
		server.handleWorkflowShow(writer, request)
	case request.Method == http.MethodPost && path == "/interrogations":
		server.handleInterrogationCreate(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/interrogations/") && strings.HasSuffix(path, "/ask"):
		server.handleInterrogationAsk(writer, request)
	case request.Method == http.MethodGet && path == "/interrogations":
		server.handleInterrogationList(writer)
	case request.Method == http.MethodGet && path == "/runs":
		server.handleRunsList(writer, request)
	case request.Method == http.MethodPost && path == "/runs":
		server.handleRunCreate(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/cancel"):
		server.handleRunCancel(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/resume"):
		server.handleRunResume(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/retrigger"):
		server.handleRunRetrigger(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/events/stream"):
		server.handleRunEventsStream(writer, request)
	case request.Method == http.MethodGet && path == "/approvals":
		server.handleApprovalsList(writer)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/approvals/") && strings.HasSuffix(path, "/resolve"):
		server.handleApprovalResolve(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/interviews"):
		server.handleRunInterviews(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.Contains(path, "/interviews/"):
		server.handleRunInterview(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/execution-group/metrics"):
		server.handleExecutionGroupMetrics(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/metrics"):
		server.handleMetrics(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.Contains(path, "/inspect"):
		server.handleInspect(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.Contains(path, "/session/"):
		server.handleSessionDetail(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.Contains(path, "/document/row/"):
		server.handleDocumentRow(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.HasSuffix(path, "/document"):
		server.handleRunDocument(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/") && strings.Contains(path, "/tools/") && strings.HasSuffix(path, "/raw"):
		rest := strings.TrimPrefix(path, "/runs/")
		// rest is now "{run_id}/tools/{tool_id}/raw"
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			http.NotFound(writer, request)
			break
		}
		runID := rest[:slashIdx]
		after := rest[slashIdx+1:] // "tools/{tool_id}/raw"
		after = strings.TrimPrefix(after, "tools/")
		toolID := strings.TrimSuffix(after, "/raw")
		if toolID == "" {
			http.NotFound(writer, request)
			break
		}
		server.handleToolRaw(writer, request, runID, toolID)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/runs/"):
		server.handleRunDetail(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (server *Server) handleRunsList(writer http.ResponseWriter, request *http.Request) {
	q := request.URL.Query()
	callbackPrefix := q.Get("callback_url")
	workflowFilter := q.Get("workflow")
	statusFilter := q.Get(fieldStatus)
	limitStr := q.Get("limit")

	// `limit` counts as a filter: a client asking for only N most-recent
	// runs should go through the enriched/sorted path that honors limit,
	// not the unfiltered backward-compat list which ignores it.
	hasFilter := callbackPrefix != "" || workflowFilter != "" || statusFilter != "" || limitStr != ""

	// Unfiltered: backward-compatible ID list, sorted by modification time (newest first).
	if !hasFilter {
		entries, err := os.ReadDir(server.RunsDir)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		type dirWithTime struct {
			name    string
			modTime time.Time
		}
		dirs := make([]dirWithTime, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			dirs = append(dirs, dirWithTime{name: entry.Name(), modTime: info.ModTime()})
		}
		sort.Slice(dirs, func(i, j int) bool {
			return dirs[i].modTime.After(dirs[j].modTime)
		})
		ids := make([]string, len(dirs))
		for i, d := range dirs {
			ids[i] = d.name
		}
		respondJSON(writer, ids)
		return
	}

	// Filtered: scan state files, return enriched summaries.
	entries, err := os.ReadDir(server.RunsDir)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	var runs []runSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(server.RunsDir, entry.Name(), "state.json")
		rs, err := state.LoadState(statePath)
		if err != nil {
			continue
		}
		if callbackPrefix != "" && !strings.HasPrefix(rs.CallbackURL, callbackPrefix) {
			continue
		}
		if workflowFilter != "" && rs.WorkflowID != workflowFilter {
			continue
		}
		effectiveStatus := state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure)
		if statusFilter != "" && effectiveStatus != statusFilter {
			continue
		}
		runs = append(runs, runSummary{
			RunID:                rs.ID,
			WorkflowID:           rs.WorkflowID,
			Status:               rs.Status,
			HasUnresolvedFailure: rs.HasUnresolvedFailure,
			StartedAt:            rs.StartedAt,
			FinishedAt:           rs.FinishedAt,
			CallbackURL:          rs.CallbackURL,
			Inputs:               rs.Inputs,
		})
	}
	if runs == nil {
		runs = []runSummary{}
	}

	// Attach run_total to each enriched summary.
	for i := range runs {
		total := server.loadRunTotal(runs[i].RunID)
		if total != nil {
			runs[i].RunTotal = total
		}
	}

	// Sort by started_at descending (most recent first).
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})

	// Apply limit.
	if limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(runs) {
			runs = runs[:limit]
		}
	}

	respondJSON(writer, map[string]any{"runs": runs})
}

func projectDirFromInputs(inputs map[string]any) string {
	if inputs == nil {
		return ""
	}
	if raw, ok := inputs[fieldProjectDir]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func isTerminalRunStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled": //nolint:goconst // terminal statuses are clear as literals
		return true
	default:
		return false
	}
}

func (server *Server) findActiveRootRunForProjectDir(projectDir string) (string, string) {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		return "", ""
	}
	entries, err := os.ReadDir(server.RunsDir)
	if err != nil {
		return "", ""
	}
	target := filepath.Clean(projectDir)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		rs, err := state.LoadState(filepath.Join(server.RunsDir, runID, "state.json"))
		if err != nil || rs == nil {
			continue
		}
		// Only lock out overlapping *root* runs. Child runs belong to their parent execution group.
		if strings.TrimSpace(rs.ParentRun) != "" {
			continue
		}
		if isTerminalRunStatus(rs.Status) {
			continue
		}
		rsProjectDir := projectDirFromInputs(rs.Inputs)
		if rsProjectDir == "" {
			continue
		}
		if filepath.Clean(rsProjectDir) != target {
			continue
		}
		return rs.ID, rs.Status
	}

	return "", ""
}

func (server *Server) handleRunCreate(writer http.ResponseWriter, request *http.Request) {
	var payload runRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if payload.WorkflowID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if payload.Inputs == nil {
		payload.Inputs = map[string]any{}
	}
	if payload.Env == nil {
		payload.Env = map[string]string{}
	}

	// Check for drain pause marker before any other work.
	if config.IsCreatePaused(server.RunsDir) {
		writer.Header().Set("Retry-After", "60")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte("new run creation paused; remove " + config.PausedMarkerPath(server.RunsDir) + " to resume"))
		return
	}

	workflow, ok := server.App.Definitions.Workflows[payload.WorkflowID]
	if !ok {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if err := definitions.ValidateInputs(workflow, payload.Inputs); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(err.Error()))
		return
	}

	if projectDir := projectDirFromInputs(payload.Inputs); projectDir != "" {
		if activeRunID, activeStatus := server.findActiveRootRunForProjectDir(projectDir); activeRunID != "" {
			writer.WriteHeader(http.StatusConflict)
			respondJSON(writer, map[string]any{
				fieldError:      "run_conflict",
				fieldMessage:    "another run is already in progress for this project_dir",
				fieldProjectDir: projectDir,
				"active_run_id": activeRunID,
				"active_status": activeStatus,
			})
			return
		}
	}

	runID, err := server.Manager.StartRun(request.Context(), payload.WorkflowID, payload.Inputs, payload.Env, payload.CallbackURL)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	respondJSON(writer, runResponse{RunID: runID})
}

func (server *Server) handleRunCancel(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/cancel")
	if runID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := server.Manager.CancelRun(runID); err != nil {
		if strings.Contains(err.Error(), "cannot cancel") {
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(err.Error()))
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(err.Error()))
		return
	}
	respondJSON(writer, map[string]string{fieldStatus: "cancelled"})
}

func (server *Server) handleRunResume(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/resume")
	if runID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := server.Manager.ResumeRun(request.Context(), runID); err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	respondJSON(writer, runResponse{RunID: runID})
}

func (server *Server) handleRunRetrigger(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/retrigger")
	if runID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	var payload struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.NodeID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte("node_id is required"))
		return
	}

	if err := server.Manager.RetriggerNode(request.Context(), runID, payload.NodeID); err != nil {
		if strings.Contains(err.Error(), "not terminal") {
			writer.WriteHeader(http.StatusConflict)
			respondJSON(writer, map[string]string{fieldError: err.Error()})
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writer.WriteHeader(http.StatusNotFound)
			respondJSON(writer, map[string]string{fieldError: err.Error()})
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
		respondJSON(writer, map[string]string{fieldError: err.Error()})
		return
	}

	respondJSON(writer, runResponse{RunID: runID})
}

func (server *Server) handleRunDetail(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	if path == "" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	if strings.HasSuffix(path, "/compound-graph") {
		runID := strings.TrimSuffix(path, "/compound-graph")
		respondJSON(writer, server.buildCompoundGraph(runID))
		return
	}

	if strings.HasSuffix(path, "/meta") {
		runID := strings.TrimSuffix(path, "/meta")
		runState, err := state.LoadState(filepath.Join(server.RunsDir, runID, "state.json"))
		if err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		nodes := map[string]map[string]any{}
		runState.WithNodes(func(stateNodes map[string]*state.NodeState) {
			for id, n := range stateNodes {
				node := map[string]any{
					fieldStatus:   n.Status,
					fieldDecision: n.Decision,
				}
				if n.Message != "" {
					node[fieldMessage] = n.Message
				}
				if n.Error != "" {
					node[fieldError] = n.Error
				}
				if len(n.Data) > 0 {
					node["data"] = n.Data
				}
				nodes[id] = node
			}
		})
		respondJSON(writer, map[string]any{
			"run_id":        runState.ID,
			fieldWorkflowID: runState.WorkflowID,
			fieldStatus:     runState.Status,
			fieldError:      runState.Error,
			"title":         runState.Title,
			"description":   runState.Description,
			"summary":       runState.Summary,
			"started_at":    runState.StartedAt,
			"finished_at":   runState.FinishedAt,
			"nodes":         nodes,
			fieldInputs:     runState.Inputs,
			"parent_run":    runState.ParentRun,
		})
		return
	}

	if strings.HasSuffix(path, "/graph") {
		runID := strings.TrimSuffix(path, "/graph")
		runState, err := state.LoadState(filepath.Join(server.RunsDir, runID, "state.json"))
		if err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		workflow, err := definitions.LoadWorkflowSnapshot(filepath.Join(server.RunsDir, runID, "workflow.yaml"))
		if err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		collector := buildRunCollector(server.RunsDir, runState)
		respondJSON(writer, visualize.RunTopologyWithMetrics(workflow, runState, collector))
		return
	}

	if strings.HasSuffix(path, "/events") {
		runID := strings.TrimSuffix(path, "/events")
		data, err := os.ReadFile(filepath.Join(server.RunsDir, runID, "events.jsonl"))
		if err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", contentTypeJSON)
		_, _ = writer.Write(data)
		return
	}

	data, err := os.ReadFile(filepath.Join(server.RunsDir, path, "state.json"))
	if err != nil {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", contentTypeJSON)
	_, _ = writer.Write(data)
}

// handleToolRaw serves the raw tool_result content for a single tool call.
// URL: GET /runs/{run_id}/tools/{tool_id}/raw
func (server *Server) handleToolRaw(writer http.ResponseWriter, request *http.Request, runID, toolID string) {
	eventsPath := filepath.Join(server.RunsDir, runID, "events.jsonl")
	events, _, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(writer, request)
			return
		}
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, ev := range events {
		if ev.Type != "tool_result" {
			continue
		}
		if id, _ := ev.Data["tool_id"].(string); id != toolID {
			continue
		}
		writer.Header().Set("Content-Type", contentTypeJSON)
		_ = json.NewEncoder(writer).Encode(ev.Data)
		return
	}
	http.NotFound(writer, request)
}

// diskRunLoader loads run state and events from the runs directory on disk.
type diskRunLoader struct {
	runsDir string
}

func (l *diskRunLoader) LoadState(runID string) (*state.RunState, error) {
	return state.LoadState(filepath.Join(l.runsDir, runID, "state.json"))
}

func (l *diskRunLoader) LoadEvents(runID string) ([]state.Event, error) {
	return state.ReadEvents(filepath.Join(l.runsDir, runID, "events.jsonl"))
}

func (server *Server) handleInspect(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")

	var runID, nodeID, aspect string
	if idx := strings.Index(path, "/nodes/"); idx >= 0 {
		runID = path[:idx]
		rest := path[idx+len("/nodes/"):]
		if nIdx := strings.Index(rest, "/inspect"); nIdx >= 0 {
			nodeID = rest[:nIdx]
			aspect = strings.TrimPrefix(rest[nIdx:], "/inspect")
			aspect = strings.TrimPrefix(aspect, "/")
		}
	} else if idx := strings.Index(path, "/inspect"); idx >= 0 {
		runID = path[:idx]
		aspect = strings.TrimPrefix(path[idx:], "/inspect")
		aspect = strings.TrimPrefix(aspect, "/")
	}

	if runID == "" {
		respondInspectError(writer, http.StatusBadRequest, "run ID required")
		return
	}

	if aspect == "" {
		aspect = "overview"
	}

	// Parse compare/{other-run-id} before processor lookup
	var otherRunID string
	if strings.HasPrefix(aspect, "compare/") {
		parts := strings.SplitN(aspect, "/", 2)
		aspect = parts[0]
		otherRunID = parts[1]
	}

	// Load run state
	statePath := filepath.Join(server.RunsDir, runID, "state.json")
	runState, err := state.LoadState(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			respondInspectError(writer, http.StatusNotFound, "run not found: "+runID)
			return
		}
		respondInspectError(writer, http.StatusInternalServerError, err.Error())
		return
	}

	// Load events. Use ReadEventsWithOffset so follow-mode can tail
	// from the exact byte we read up to — a separate os.Stat could
	// race with writers appending between read and stat, causing
	// tail to start AFTER those appends and miss them.
	eventsPath := filepath.Join(server.RunsDir, runID, "events.jsonl")
	events, initialBytesRead, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil && !os.IsNotExist(err) {
		respondInspectError(writer, http.StatusInternalServerError, err.Error())
		return
	}

	// If node-scoped, verify node exists and filter events
	if nodeID != "" {
		var nodeExists bool
		runState.WithNodes(func(nodes map[string]*state.NodeState) {
			_, nodeExists = nodes[nodeID]
		})
		if !nodeExists {
			respondInspectError(writer, http.StatusNotFound, "node not found: "+nodeID)
			return
		}

		// Filter events to this node
		var filtered []state.Event
		for _, e := range events {
			if e.NodeID == nodeID {
				filtered = append(filtered, e)
			}
		}

		// Handle ?attempt=N
		if attemptStr := request.URL.Query().Get("attempt"); attemptStr != "" {
			attempt, err := strconv.Atoi(attemptStr)
			if err != nil || attempt < 1 {
				respondInspectError(writer, http.StatusBadRequest, "invalid attempt number")
				return
			}

			// Use attempt boundaries on the full event list (not filtered)
			// because boundaries are detected from node_started events
			boundaries := inspect.DetectAttemptBoundaries(events, nodeID)
			if attempt > len(boundaries) {
				respondInspectError(writer, http.StatusNotFound, "attempt not found")
				return
			}

			// Find the event range for this attempt
			startIdx := boundaries[attempt-1]
			var endIdx int
			if attempt < len(boundaries) {
				endIdx = boundaries[attempt]
			} else {
				endIdx = len(events)
			}

			// Re-filter to events within the attempt range
			filtered = nil
			for i := startIdx; i < endIdx; i++ {
				if events[i].NodeID == nodeID {
					filtered = append(filtered, events[i])
				}
			}
		}

		events = filtered
	}

	// For node-scoped inspect routes, narrow the runState so processors
	// that iterate Nodes (outputs, timing, tokens, etc.) return only the
	// target node's data instead of the whole run.
	procState := runState
	if nodeID != "" {
		procState = runState.NarrowToNode(nodeID)
	}

	// Create processor
	proc, err := inspect.NewProcessor(aspect, procState)
	if err != nil {
		respondInspectError(writer, http.StatusBadRequest, err.Error())
		return
	}

	// Set loader for cross-run aspects (tree, compare)
	type loaderSetter interface {
		SetLoader(inspect.RunLoader)
	}
	if ls, ok := proc.(loaderSetter); ok {
		ls.SetLoader(&diskRunLoader{runsDir: server.RunsDir})
	}

	// Set other run ID for compare aspect
	type otherSetter interface {
		SetOtherRunID(string)
	}
	if setter, ok := proc.(otherSetter); ok && otherRunID != "" {
		setter.SetOtherRunID(otherRunID)
	}

	// Feed events
	for _, e := range events {
		proc.ProcessEvent(e)
	}

	// Check for follow mode
	follow := request.URL.Query().Get("follow") == queryFollowTrue

	if !follow {
		respondJSON(writer, proc.Result())
		return
	}

	// SSE follow mode
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send initial state
	data, _ := json.Marshal(proc.Result())
	fmt.Fprintf(writer, "data: %s\n\n", data)
	flusher.Flush()

	// Stop early if run is already terminal
	if isTerminalRunStatus(runState.Status) {
		return
	}

	// Tail for new events starting exactly where the initial read
	// ended — using initialBytesRead (not a fresh Stat) avoids a race
	// where writers appended between our ReadFile and Stat, which
	// would cause the tail to skip past unseen events.
	ctx := request.Context()
	evCh := state.TailEvents(ctx, eventsPath, initialBytesRead)
	for event := range evCh {
		// When node-scoped, filter tailed events the same as batch events
		if nodeID != "" && event.NodeID != nodeID {
			// Still check for terminal run events
			if event.Type == eventTypeRunCompleted || event.Type == eventTypeRunFailed {
				break
			}
			continue
		}
		proc.ProcessEvent(event)
		if proc.Changed() {
			data, _ := json.Marshal(proc.Result())
			fmt.Fprintf(writer, "data: %s\n\n", data)
			flusher.Flush()
		}

		// Stop when run reaches a terminal state
		if event.Type == eventTypeRunCompleted || event.Type == eventTypeRunFailed {
			break
		}
	}
}

func (server *Server) handleRunEventsStream(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/events/stream")
	if runID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	runDir := filepath.Join(server.RunsDir, runID)
	eventsPath := filepath.Join(runDir, "events.jsonl")
	existing, initialOffset, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	flusher, ok := writer.(http.Flusher)
	if !ok {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")

	// Build the collector and prime with existing events.
	c := metrics.NewCollector()
	if rs, loadErr := state.LoadState(filepath.Join(runDir, "state.json")); loadErr == nil {
		wireParents(c, rs)
	}
	// Emit existing events as SSE lines (preserving prior stream behavior) and
	// feed them into the collector.
	for _, ev := range existing {
		raw, _ := json.Marshal(ev)
		if ev.Type != "" {
			_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", ev.Type, raw)
		} else {
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", raw)
		}
		c.ProcessEvent(ev)
	}

	// Send initial metric snapshot covering all known nodes.
	snapshot := buildMetricUpdate(c, runID, allIDs(c))
	if b, marshalErr := json.Marshal(snapshot); marshalErr == nil {
		_, _ = fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", b)
		flusher.Flush()
	}

	// Tail new events, passing each through as a plain SSE event and also
	// feeding the collector to coalesce metric-update events every 500ms.
	tail := state.TailEvents(request.Context(), eventsPath, initialOffset)
	coalesce := time.NewTimer(time.Hour)
	if !coalesce.Stop() {
		<-coalesce.C
	}
	pending := map[string]bool{}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		update := buildMetricUpdate(c, runID, pending)
		if b, marshalErr := json.Marshal(update); marshalErr == nil {
			_, _ = fmt.Fprintf(writer, "event: metric-update\ndata: %s\n\n", b)
			flusher.Flush()
		}
		pending = map[string]bool{}
	}

	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-request.Context().Done():
			flush()
			return
		case <-pingTicker.C:
			_, _ = writer.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-coalesce.C:
			flush()
		case ev, ok := <-tail:
			if !ok {
				flush()
				return
			}
			// Pass the raw event through (existing behavior, re-marshaled).
			raw, _ := json.Marshal(ev)
			if ev.Type != "" {
				_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", ev.Type, raw)
			} else {
				_, _ = fmt.Fprintf(writer, "data: %s\n\n", raw)
			}
			flusher.Flush()
			// Feed the collector and schedule a metric-update flush.
			c.ProcessEvent(ev)
			drainChanges(c, pending)
			if len(pending) > 0 {
				_ = coalesce.Reset(500 * time.Millisecond)
			}
			if ev.Type == eventTypeRunCompleted || ev.Type == eventTypeRunFailed {
				flush()
				return
			}
		}
	}
}

// allIDs converts c.AllNodeIDs() to a set for use as an initial snapshot set.
func allIDs(c *metrics.Collector) map[string]bool {
	ids := map[string]bool{}
	for _, id := range c.AllNodeIDs() {
		ids[id] = true
	}
	return ids
}

// extractEventType extracts the "type" field from a JSON event line
// without doing a full unmarshal. Returns empty string if not found.
// Handles both `"type":"x"` and `"type": "x"` (with optional space).
func extractEventType(jsonLine string) string {
	for _, prefix := range []string{`"type":"`, `"type": "`} {
		idx := strings.Index(jsonLine, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		end := strings.IndexByte(jsonLine[start:], '"')
		if end < 0 {
			return ""
		}
		return jsonLine[start : start+end]
	}
	return ""
}

type approvalResolveRequest struct {
	Decision string `json:"decision"`
	Message  string `json:"message"`
	Comment  string `json:"comment,omitempty"`
}

func (server *Server) handleApprovalsList(writer http.ResponseWriter) {
	approvalsList, err := approvals.ListAll(server.App.Root)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	respondJSON(writer, approvalsList)
}

func (server *Server) handleApprovalResolve(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/approvals/")
	approvalID := strings.TrimSuffix(path, "/resolve")
	if approvalID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	var payload approvalResolveRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if payload.Decision == "" || payload.Message == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	approval, err := approvals.Resolve(server.App.Root, approvalID, approvals.ResolveInput{
		Decision: payload.Decision,
		Message:  payload.Message,
		Comment:  payload.Comment,
	})
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	_ = server.Manager.NotifyApproval(request.Context(), approval.RunID)

	respondJSON(writer, approval)
}

func (server *Server) handleRunInterviews(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	runID := strings.TrimSuffix(path, "/interviews")
	if runID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	runDir := filepath.Join(server.RunsDir, runID)
	list, err := interviews.ListForRun(runDir)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	respondJSON(writer, list)
}

func (server *Server) handleRunInterview(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	idx := strings.Index(path, "/interviews/")
	if idx < 0 {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	runID := path[:idx]
	nodeID := path[idx+len("/interviews/"):]
	if runID == "" || nodeID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	runDir := filepath.Join(server.RunsDir, runID)
	interview, err := interviews.Load(runDir, nodeID)
	if err != nil {
		if os.IsNotExist(err) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	respondJSON(writer, interview)
}

func (server *Server) handleWorkflowsList(writer http.ResponseWriter) {
	ids := make([]string, 0, len(server.App.Definitions.Workflows))
	for id := range server.App.Definitions.Workflows {
		ids = append(ids, id)
	}
	respondJSON(writer, ids)
}

func (server *Server) handleWorkflowShow(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/workflows/")
	if path == "" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	workflow, ok := server.App.Definitions.Workflows[path]
	if !ok {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if workflow.SourcePath == "" {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	data, err := os.ReadFile(workflow.SourcePath)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/plain")
	_, _ = writer.Write(data)
}

func (server *Server) handleWorkflowGraph(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/workflows/")
	workflowID := strings.TrimSuffix(path, "/graph")
	if workflowID == "" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	workflow, ok := server.App.Definitions.Workflows[workflowID]
	if !ok {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	respondJSON(writer, visualize.WorkflowTopology(workflow))
}

func (server *Server) buildCompoundGraph(runID string) visualize.TopologyGraph {
	return document.BuildCompoundGraph(server.RunsDir, runID)
}

// loadRunTotal returns a run's rolled-up NodeTotals. Same fast/slow
// path as the dashboard's loadRunTotal:
//
//	Fast: rs.Totals is set (engine wrote it at termination) — return.
//	Slow: read events.jsonl, compute. For terminal runs, persist back
//	      to state.json so subsequent reads take the fast path.
func (server *Server) loadRunTotal(runID string) *state.NodeTotals {
	runDir := filepath.Join(server.RunsDir, runID)
	rs, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return nil
	}
	if isTerminalRunStatus(rs.Status) && rs.Totals != nil {
		return rs.Totals
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	events, _, readErr := state.ReadEventsWithOffset(eventsPath)
	if readErr != nil {
		return nil
	}
	c := buildCollectorFromEvents(rs, events)
	if c == nil {
		return nil
	}
	total := c.RunTotal()

	if isTerminalRunStatus(rs.Status) {
		rs.Totals = &total
		_ = state.SaveState(filepath.Join(runDir, "state.json"), rs)
	}
	return &total
}

// buildRunCollector constructs a metrics.Collector primed with the run's
// events and ForEach parent wiring. Returns nil if the events file is
// unreadable — callers treat that as "no metrics for this run" via the
// Collector != nil guard in visualize.CompoundExecutionGroupTopology.
func buildRunCollector(runsDir string, rs *state.RunState) *metrics.Collector {
	if rs == nil {
		return nil
	}
	events, _, err := state.ReadEventsWithOffset(filepath.Join(runsDir, rs.ID, "events.jsonl"))
	if err != nil {
		// Empty collector with parent wiring only.
		return buildCollectorFromEvents(rs, nil)
	}
	return buildCollectorFromEvents(rs, events)
}

// buildCollectorFromEvents is the shared primer used by both the path
// that reads events fresh (buildRunCollector) and the path that has
// already read them once (loadRunTotal, which also needs the byte
// offset for cache freshness checking).
func buildCollectorFromEvents(rs *state.RunState, events []state.Event) *metrics.Collector {
	if rs == nil {
		return nil
	}
	c := metrics.NewCollector()
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id := range nodes {
			if idx := strings.Index(id, "::"); idx > 0 {
				c.SetParent(id, id[:idx])
			}
		}
	})
	for _, ev := range events {
		c.ProcessEvent(ev)
	}
	return c
}

// HealthHandler returns an http.Handler for the /health endpoint.
// The countsFn callback returns (activeRuns, totalRuns).
func HealthHandler(countsFn func() (int, int)) http.Handler {
	startTime := time.Now()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active, total := countsFn()
		respondJSON(w, map[string]any{
			fieldStatus:      "ok",
			"uptime_seconds": int(time.Since(startTime).Seconds()),
			"active_runs":    active,
			"total_runs":     total,
		})
	})
}

func (server *Server) handleInterrogationCreate(writer http.ResponseWriter, request *http.Request) {
	if server.Interrogations == nil {
		respondInspectError(writer, http.StatusServiceUnavailable, "interrogation manager not configured")
		return
	}
	var body struct {
		RunID    string `json:"run_id"`
		NodeID   string `json:"node_id"`
		Question string `json:"question"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		respondInspectError(writer, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.RunID == "" || body.NodeID == "" || body.Question == "" {
		respondInspectError(writer, http.StatusBadRequest, "run_id, node_id, and question are required")
		return
	}

	runState, err := state.LoadState(filepath.Join(server.RunsDir, body.RunID, "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			respondInspectError(writer, http.StatusNotFound, "run not found: "+body.RunID)
			return
		}
		respondInspectError(writer, http.StatusInternalServerError, err.Error())
		return
	}

	workflow, err := definitions.LoadWorkflowSnapshot(filepath.Join(server.RunsDir, body.RunID, "workflow.yaml"))
	if err != nil {
		respondInspectError(writer, http.StatusInternalServerError, "load workflow: "+err.Error())
		return
	}

	// ForEach expanded items use "{templateID}::{index}" naming — these
	// IDs only exist in run state, not in the workflow definition. Fall
	// back to looking up the template node while keeping the original
	// expanded ID for SessionID lookup below.
	node := definitions.FindNode(workflow, body.NodeID)
	if node == nil {
		if idx := strings.Index(body.NodeID, "::"); idx > 0 {
			node = definitions.FindNode(workflow, body.NodeID[:idx])
		}
	}
	if node == nil {
		respondInspectError(writer, http.StatusNotFound, "node not found: "+body.NodeID)
		return
	}

	runnerID, err := engine.ResolveRunnerID(node, workflow)
	if err != nil {
		respondInspectError(writer, http.StatusBadRequest, err.Error())
		return
	}
	runnerDef := server.App.Definitions.Runners[runnerID]
	if runnerDef == nil || runnerDef.Type != "serf" {
		respondInspectError(writer, http.StatusBadRequest, "interrogation only supported for serf runners")
		return
	}

	runner, ok := server.App.Engine.RunnerRegistry.Get(runnerID)
	if !ok {
		respondInspectError(writer, http.StatusInternalServerError, "runner not available: "+runnerID)
		return
	}

	workspace, err := resolveInterrogationWorkspace(workflow, node, runState.Env, server.RunsDir, runState.ID)
	if err != nil {
		respondInspectError(writer, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 120*time.Second)
	defer cancel()

	result, err := server.Interrogations.Create(ctx, interrogate.CreateRequest{
		RunState:  runState,
		NodeID:    body.NodeID,
		Question:  body.Question,
		Runner:    runner,
		Workspace: workspace,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no session") {
			respondInspectError(writer, http.StatusBadRequest, err.Error())
			return
		}
		respondInspectError(writer, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(writer, result)
}

// resolveInterrogationWorkspace picks a working directory for the
// serf-fork that backs an interrogation. Project-mode nodes use their
// declared workspace (preferred — gives the agent file access to the
// code it was reasoning about). Non-project nodes fall back to the
// run's runs-dir directory; serf only needs a CWD for diagnostic
// chat, so this works for `mode: none` and `mode: shared` workflows
// (e.g. learn) that previously hit a hard 400. (PRI-1573)
func resolveInterrogationWorkspace(workflow *definitions.Workflow, node *definitions.Node, env map[string]string, runsDir, runID string) (string, error) {
	ws := node.Workspace
	if ws == nil {
		ws = workflow.WorkspaceDefaults
	}
	if ws == nil || ws.Mode != modeProject {
		mode := modeNone
		if ws != nil {
			mode = ws.Mode
		}
		fallback := filepath.Join(runsDir, runID)
		if info, err := os.Stat(fallback); err == nil && info.IsDir() {
			return fallback, nil
		}
		return "", fmt.Errorf("interrogation workspace unavailable: node's workspace mode is %q and the run dir %s is missing; declare workspace_defaults.mode: project in the workflow or check that the run still exists", mode, fallback)
	}
	rc := &engine.RunContext{}
	rc.PopulateEnv(env)
	v, err := rc.Resolve(ws.Path)
	if err != nil {
		return "", fmt.Errorf("workspace path resolution failed: %w", err)
	}
	path, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("workspace path must resolve to a string, got %T", v)
	}
	if path == "" {
		return "", fmt.Errorf("workspace path is empty after resolution")
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("workspace no longer exists: %s", path)
	}
	return path, nil
}

func (server *Server) handleInterrogationAsk(writer http.ResponseWriter, request *http.Request) {
	if server.Interrogations == nil {
		respondInspectError(writer, http.StatusServiceUnavailable, "interrogation manager not configured")
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/interrogations/")
	id := strings.TrimSuffix(path, "/ask")
	if id == "" {
		respondInspectError(writer, http.StatusBadRequest, "interrogation ID required")
		return
	}

	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Question == "" {
		respondInspectError(writer, http.StatusBadRequest, "question is required")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 120*time.Second)
	defer cancel()

	result, err := server.Interrogations.Ask(ctx, id, body.Question)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondInspectError(writer, http.StatusNotFound, err.Error())
			return
		}
		respondInspectError(writer, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(writer, result)
}

func (server *Server) handleInterrogationList(writer http.ResponseWriter) {
	if server.Interrogations == nil {
		respondJSON(writer, []any{})
		return
	}
	respondJSON(writer, server.Interrogations.List())
}

func respondInspectError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", contentTypeJSON)
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{fieldError: message})
}

func respondJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", contentTypeJSON)
	_ = json.NewEncoder(writer).Encode(payload)
}

func (server *Server) handleDocumentRow(w http.ResponseWriter, r *http.Request) {
	// Path: /runs/<runID>/document/row/<nodeID>?attempt=N
	rest := strings.TrimPrefix(r.URL.Path, "/runs/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 4 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	runID, nodeID := parts[0], parts[3]

	// Optional attempt query param (1-based ordinal). Defaults to 0 which
	// means "all events for this node" — the legacy fallback.
	attemptOrdinal := 0
	if aStr := r.URL.Query().Get("attempt"); aStr != "" {
		if v, convErr := strconv.Atoi(aStr); convErr == nil && v > 0 {
			attemptOrdinal = v
		}
	}

	rs, err := state.LoadState(filepath.Join(server.RunsDir, runID, "state.json"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	var n *state.NodeState
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		n = nodes[nodeID]
	})
	if n == nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	// Load events for the transcript builder.
	allEvents, _, _ := state.ReadEventsWithOffset(filepath.Join(server.RunsDir, runID, "events.jsonl"))
	// Slice to the requested execution window.
	events := document.SliceExecutionEvents(allEvents, nodeID, attemptOrdinal)
	// Build the structured transcript (the new shape).
	transcript := document.BuildTranscript(nodeID, events)
	// Compute diffs for edit_file tool calls and enrich decision messages.
	enrichTranscriptDiffs(&transcript)
	if server.App != nil {
		loader := newDocLoader(server.RunsDir)
		registry := newDocRegistry(server.App.Definitions, loader)
		enrichTranscriptDecisions(&transcript, rs.WorkflowID, registry)
	}

	// Build local prompt using the resolver.
	promptData := map[string]any{"local": "", "boilerplate": "", "boilerplate_bytes": 0}
	if server.App != nil {
		resolver := document.NewWorkflowPromptResolver(server.App.Definitions, server.RunsDir)
		local := resolver.LocalPrompt(rs.WorkflowID, nodeID, runID, 1)
		_, boilerplate := document.ExtractLocalPrompt(localPromptWithBoilerplate(server.App.Definitions, rs.WorkflowID, nodeID, rs.Inputs))
		promptData["local"] = local
		promptData["boilerplate"] = boilerplate
		promptData["boilerplate_bytes"] = len(boilerplate)
	}

	out := map[string]any{
		fieldInputs:  artifactsFromInputs(rs.Inputs),
		"outputs":    artifactsFromNode(n),
		"transcript": transcript, // new Transcript shape
		"prompt":     promptData,
		"run_id":     runID,
		"session_id": n.SessionID,
		"is_resume":  n.Attempts > 1,
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	_ = json.NewEncoder(w).Encode(out)
}

// enrichTranscriptDiffs computes server-side unified diffs for edit_file tool
// calls in the transcript, mirroring the logic in the document package's
// per-row enrichment.
func enrichTranscriptDiffs(transcript *document.Transcript) {
	for ai := range transcript.Attempts {
		for mi := range transcript.Attempts[ai].Messages {
			msg := &transcript.Attempts[ai].Messages[mi]
			if msg.Kind != "tool_call" || msg.ToolCall == nil {
				continue
			}
			if msg.ToolCall.Result == nil || msg.ToolCall.Result.IsError {
				continue
			}
			if msg.ToolCall.ToolName != "edit_file" {
				continue
			}
			oldStr, _ := msg.ToolCall.Args["old_string"].(string)
			newStr, _ := msg.ToolCall.Args["new_string"].(string)
			if oldStr != "" {
				msg.ToolCall.Diff = &document.MessageDiff{Hunks: document.UnifiedDiff(oldStr, newStr, 2)}
			}
		}
	}
}

// enrichTranscriptDecisions backfills description, tags, and family into
// decision messages in the transcript, mirroring the logic in the document
// package's per-row enrichment.
func enrichTranscriptDecisions(transcript *document.Transcript, workflowID string, registry *document.WorkflowRegistry) {
	for ai := range transcript.Attempts {
		for mi := range transcript.Attempts[ai].Messages {
			msg := &transcript.Attempts[ai].Messages[mi]
			if msg.Kind != fieldDecision || msg.Decision == nil {
				continue
			}
			if registry != nil && workflowID != "" {
				if meta := registry.FindDecisionMeta(workflowID, msg.Decision.ID); meta != nil {
					msg.Decision.Description = meta.Description
					msg.Decision.Tags = append([]string(nil), meta.Tags...)
				}
			}
			msg.Decision.Family = document.DecisionFamily(msg.Decision.ID)
		}
	}
}

// localPromptWithBoilerplate returns the full rendered prompt (before
// ExtractLocalPrompt strips it) for boilerplate extraction.
func localPromptWithBoilerplate(bundle *definitions.Bundle, workflowID, nodeID string, inputs map[string]any) string {
	if bundle == nil {
		return ""
	}
	wf, ok := bundle.Workflows[workflowID]
	if !ok || wf == nil {
		return ""
	}
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			return document.ExpandInputExprs(n.Prompt, inputs)
		}
	}
	return ""
}

// renderValue converts an arbitrary value into a human-readable desc and, for
// maps/slices, a recursive nested list of {name, desc, nested} artifacts.
func renderValue(v any) (desc string, value string, nested []map[string]any) {
	if v == nil {
		return "—", "", nil
	}
	switch val := v.(type) {
	case map[string]any:
		desc = fmt.Sprintf("object · %d keys", len(val))
		for k, child := range val {
			childDesc, childValue, childNested := renderValue(child)
			entry := map[string]any{fieldName: k, fieldDesc: childDesc}
			if childValue != "" {
				entry[fieldValue] = childValue
			}
			if len(childNested) > 0 {
				entry[fieldNested] = childNested
			}
			nested = append(nested, entry)
		}
		return desc, "", nested
	case []any:
		desc = fmt.Sprintf("list · %d items", len(val))
		for i, child := range val {
			childDesc, childValue, childNested := renderValue(child)
			entry := map[string]any{fieldName: strconv.Itoa(i), fieldDesc: childDesc}
			if childValue != "" {
				entry[fieldValue] = childValue
			}
			if len(childNested) > 0 {
				entry[fieldNested] = childNested
			}
			nested = append(nested, entry)
		}
		return desc, "", nested
	case string:
		full := val
		if len(full) > 200 {
			return full[:197] + "…", full, nil
		}
		return full, full, nil
	case bool:
		s := strconv.FormatBool(val)
		return s, s, nil
	case float64:
		s := strconv.FormatFloat(val, 'f', -1, 64)
		return s, s, nil
	case int:
		s := strconv.Itoa(val)
		return s, s, nil
	case int64:
		s := strconv.FormatInt(val, 10)
		return s, s, nil
	default:
		s := fmt.Sprintf("%v", val)
		return s, s, nil
	}
}

func artifactsFromInputs(inputs map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(inputs))
	for k, v := range inputs {
		desc, value, nested := renderValue(v)
		entry := map[string]any{fieldName: k, fieldDesc: desc}
		if value != "" {
			entry[fieldValue] = value
		}
		if len(nested) > 0 {
			entry[fieldNested] = nested
		}
		out = append(out, entry)
	}
	return out
}

func artifactsFromNode(n *state.NodeState) []map[string]any {
	if n == nil {
		return nil
	}
	var out []map[string]any
	if n.Data != nil {
		for k, v := range n.Data {
			desc, value, nested := renderValue(v)
			entry := map[string]any{fieldName: k, fieldDesc: desc}
			if value != "" {
				entry[fieldValue] = value
			}
			if len(nested) > 0 {
				entry[fieldNested] = nested
			}
			out = append(out, entry)
		}
	}
	if n.Decision != "" {
		out = append(out, map[string]any{fieldName: fieldDecision, fieldDesc: n.Decision, fieldValue: n.Decision})
	}
	return out
}

// handleSessionDetail returns the per-node-attempt sequence for a given
// session ID. It walks node_completed events in the run's event log and
// collects entries whose data.session_id matches the requested sid.
func (server *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Path: /runs/<runID>/session/<sid>
	rest := strings.TrimPrefix(r.URL.Path, "/runs/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 3 || parts[1] != "session" {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	runID, sid := parts[0], parts[2]
	if runID == "" || sid == "" {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	runDir := filepath.Join(server.RunsDir, runID)
	eventsPath := filepath.Join(runDir, "events.jsonl")
	events, _, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	type sessionPart struct {
		Node     string    `json:"node"`
		Decision string    `json:"decision,omitempty"`
		Message  string    `json:"message,omitempty"`
		Ts       time.Time `json:"ts"`
	}
	var sessionParts []sessionPart

	for _, ev := range events {
		if ev.Type != eventNodeCompleted {
			continue
		}
		evSid, _ := ev.Data["session_id"].(string)
		if evSid != sid {
			continue
		}
		decision, _ := ev.Data[fieldDecision].(string)
		message, _ := ev.Data[fieldMessage].(string)
		sessionParts = append(sessionParts, sessionPart{
			Node:     ev.NodeID,
			Decision: decision,
			Message:  message,
			Ts:       ev.Timestamp,
		})
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": sid,
		"parts":      sessionParts,
	})
}

func (server *Server) handleRunDocument(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/runs/")
	id := strings.TrimSuffix(path, "/document")
	if id == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	loader := newDocLoader(server.RunsDir)
	// Canonicalize: if the requested run has a parent_run, climb to root.
	rs, err := loader.LoadRun(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	for rs.ParentRun != "" {
		next, err := loader.LoadRun(rs.ParentRun)
		if err != nil {
			break
		}
		rs = next
	}
	var bundle *definitions.Bundle
	if server.App != nil {
		bundle = server.App.Definitions
	}
	registry := newDocRegistry(bundle, loader)
	resolver := document.NewWorkflowPromptResolver(bundle, server.RunsDir)
	doc, err := document.BuildDocumentWithRegistryAndResolver(rs.ID, loader, registry, resolver)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
