package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/document"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/visualize"
)

type Server struct {
	mux       *http.ServeMux
	templates *template.Template
	app       *app.App
	runsDir   string
	manager   *orchestrator.Manager
	basePath  string
	devMode   bool
}

func NewServer(application *app.App, runsDir string, manager *orchestrator.Manager, basePath string) *Server {
	if basePath == "" {
		basePath = "/ui"
	}
	tmpl, _ := LoadTemplates()
	server := &Server{
		mux:       http.NewServeMux(),
		templates: tmpl,
		app:       application,
		runsDir:   runsDir,
		manager:   manager,
		basePath:  basePath,
		devMode:   os.Getenv("TOIL_DEV") == "1",
	}
	server.registerRoutes()
	return server
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.mux.ServeHTTP(writer, request)
}

func (server *Server) registerRoutes() {
	server.mux.Handle("/static/", StaticFileHandler())
	server.mux.HandleFunc("/runs", server.handleRuns)
	server.mux.HandleFunc("/runs/", server.handleRunDetail)
	server.mux.HandleFunc("/workflows", server.handleWorkflows)
	server.mux.HandleFunc("/workflows/", server.handleWorkflowDetail)
	server.mux.HandleFunc("/approvals", server.handleApprovals)
	server.mux.HandleFunc("/approvals/", server.handleApprovalResolve)
	server.mux.HandleFunc("/learnings", server.handleLearnings)
	server.mux.HandleFunc("/agents/", server.handleAgentDetail)
	server.mux.HandleFunc("/", server.handleOverview)
}

// countRuns returns the number of run directories that have a state.json,
// without parsing any state files. Used by baseData to populate the
// sidebar count cheaply — we Stat each candidate's state.json so a
// directory created mid-init (mkdir done, state.json not yet written)
// isn't counted, matching the old loadRunSummaries-based count.
func (server *Server) countRuns() int {
	entries, err := os.ReadDir(server.runsDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(server.runsDir, entry.Name(), "state.json")); err == nil {
			n++
		}
	}
	return n
}

func (server *Server) baseData(title string, activeNav string) BasePage {
	workflowCount := 0
	if server.app != nil && server.app.Definitions != nil {
		workflowCount = len(server.app.Definitions.Workflows)
	}
	pendingApprovals, _ := server.loadApprovalCounts()
	return BasePage{
		Title:            title,
		BasePath:         server.basePath,
		ActiveNav:        activeNav,
		WorkflowCount:    workflowCount,
		RunCount:         server.countRuns(),
		PendingApprovals: pendingApprovals,
	}
}

func (server *Server) handleOverview(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		server.renderError(writer, request, http.StatusNotFound, "Page Not Found", "The page you're looking for doesn't exist.")
		return
	}

	runs, err := server.loadRunSummaries()
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Run Load Failed", err.Error())
		return
	}

	groups := buildExecutionGroups(runs)
	activeGroups := 0
	activeRuns := 0
	totalChildRuns := 0
	for _, group := range groups {
		if isActiveStatus(group.GroupStatus) {
			activeGroups++
		}
		activeRuns += group.ActiveRuns
		if group.TotalRuns > 0 {
			totalChildRuns += group.TotalRuns - 1
		}
	}

	data := struct {
		BasePage
		ExecutionGroups int
		ActiveGroups    int
		ActiveRuns      int
		ChildRuns       int
		RecentGroups    []ExecutionGroupSummary
	}{
		BasePage:        server.baseData("Overview", "overview"),
		ExecutionGroups: len(groups),
		ActiveGroups:    activeGroups,
		ActiveRuns:      activeRuns,
		ChildRuns:       totalChildRuns,
		RecentGroups:    takeGroups(groups, 10),
	}

	server.renderTemplate(writer, "overview.html", data)
}

func (server *Server) handleRuns(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		server.handleRunCreate(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	runs, err := server.loadRunSummaries()
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Run Load Failed", err.Error())
		return
	}

	groups := buildExecutionGroups(runs)

	statusFilter := request.URL.Query().Get(fieldStatus)
	matchingIDs := map[string]bool{}
	if statusFilter != "" && statusFilter != "all" {
		// Mark which runs match the filter
		for _, r := range runs {
			if EffectiveStatus(r.Status, r.HasUnresolvedFailure) == statusFilter {
				matchingIDs[r.ID] = true
			}
		}
		// Keep only groups that contain at least one matching run
		filtered := make([]ExecutionGroupSummary, 0, len(groups))
		for _, g := range groups {
			if groupContainsAny(g, matchingIDs) {
				filtered = append(filtered, g)
			}
		}
		groups = filtered
	}

	data := struct {
		BasePage
		Groups       []ExecutionGroupSummary
		MatchingIDs  map[string]bool
		StatusFilter string
		HasFilter    bool
	}{
		BasePage:     server.baseData("Runs", "runs"),
		Groups:       groups,
		MatchingIDs:  matchingIDs,
		StatusFilter: statusFilter,
		HasFilter:    statusFilter != "" && statusFilter != "all",
	}

	server.renderTemplate(writer, "runs.html", data)
}

func groupContainsAny(group ExecutionGroupSummary, ids map[string]bool) bool {
	for _, row := range group.Rows {
		if ids[row.Run.ID] {
			return true
		}
	}
	return false
}

func (server *Server) handleRunCreate(writer http.ResponseWriter, request *http.Request) {
	if server.manager == nil {
		server.renderError(writer, request, http.StatusServiceUnavailable, "Service Unavailable", "Run manager is unavailable.")
		return
	}
	if err := request.ParseForm(); err != nil {
		server.renderError(writer, request, http.StatusBadRequest, "Invalid Form", "Unable to parse form data.")
		return
	}
	workflowID := request.FormValue("workflow_id")
	if workflowID == "" {
		server.renderError(writer, request, http.StatusBadRequest, "Workflow Required", "Workflow id is required.")
		return
	}

	server.startRunFromForm(writer, request, workflowID)
}

func (server *Server) handleRunDetail(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/runs/")
	if path == "" {
		server.renderError(writer, request, http.StatusNotFound, "Run Not Found", "No run id provided.")
		return
	}

	if strings.HasSuffix(path, "/v2") {
		runID := strings.TrimSuffix(path, "/v2")
		if runID != "" && !strings.Contains(runID, "/") {
			server.handleRunDetailDoc(writer, request, runID)
			return
		}
	}

	if strings.HasSuffix(path, "/stream") {
		server.handleRunStream(writer, request, strings.TrimSuffix(path, "/stream"))
		return
	}

	if strings.HasSuffix(path, "/inputs") {
		runID := strings.TrimSuffix(path, "/inputs")
		if runID != "" && !strings.Contains(runID, "/") {
			server.handleRunInputs(writer, request, runID)
			return
		}
	}

	if strings.HasSuffix(path, "/outputs") {
		runID := strings.TrimSuffix(path, "/outputs")
		if runID != "" && !strings.Contains(runID, "/") {
			server.handleRunOutputs(writer, request, runID)
			return
		}
	}

	if strings.Contains(path, "/nodes/") && strings.HasSuffix(path, "/detail") {
		idx := strings.Index(path, "/")
		if idx < 0 {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Path", "Invalid detail path.")
			return
		}
		runID := path[:idx]
		suffix := path[idx+1:] // "nodes/<id>/detail"
		server.handleNodeDetail(writer, request, runID, suffix)
		return
	}

	if strings.Contains(path, "/nodes/") && strings.HasSuffix(path, "/transcript") {
		idx := strings.Index(path, "/")
		if idx < 0 {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Path", "Invalid transcript path.")
			return
		}
		runID := path[:idx]
		suffix := path[idx+1:] // "nodes/step_1/transcript"
		server.handleNodeTranscript(writer, request, runID, suffix)
		return
	}

	if strings.Contains(path, "/nodes/") && strings.HasSuffix(path, "/subworkflows") {
		idx := strings.Index(path, "/")
		if idx < 0 {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Path", "Invalid subworkflows path.")
			return
		}
		runID := path[:idx]
		suffix := path[idx+1:] // "nodes/implement_tasks/subworkflows"
		server.handleNodeSubworkflows(writer, request, runID, suffix)
		return
	}

	if strings.HasSuffix(path, "/timeline") {
		server.handleRunTimeline(writer, request, strings.TrimSuffix(path, "/timeline"))
		return
	}

	if strings.HasSuffix(path, "/cancel") {
		server.handleRunAction(writer, request, path, "/cancel", "Cancel Failed", func(runID string) error {
			return server.manager.CancelRun(runID)
		})
		return
	}

	if strings.HasSuffix(path, "/resume") {
		server.handleRunAction(writer, request, path, "/resume", "Resume Failed", func(runID string) error {
			return server.manager.ResumeRun(request.Context(), runID)
		})
		return
	}

	if strings.HasSuffix(path, "/retrigger") {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		runID := strings.TrimSuffix(path, "/retrigger")
		if runID == "" {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Run", "Run id is required.")
			return
		}
		if server.manager == nil {
			server.renderError(writer, request, http.StatusServiceUnavailable, "Service Unavailable", "Run manager is unavailable.")
			return
		}
		nodeID := request.FormValue("node_id")
		if nodeID == "" {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Request", "node_id is required.")
			return
		}
		if err := server.manager.RetriggerNode(request.Context(), runID, nodeID); err != nil {
			server.renderError(writer, request, http.StatusInternalServerError, "Retrigger Failed", err.Error())
			return
		}
		http.Redirect(writer, request, fmt.Sprintf("%s/runs/%s", server.basePath, runID), http.StatusFound)
		return
	}

	if strings.HasSuffix(path, "/rerun") {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		runID := strings.TrimSuffix(path, "/rerun")
		if runID == "" {
			server.renderError(writer, request, http.StatusBadRequest, "Invalid Run", "Run id is required.")
			return
		}
		if server.manager == nil {
			server.renderError(writer, request, http.StatusServiceUnavailable, "Service Unavailable", "Run manager is unavailable.")
			return
		}
		rerunState, err := state.LoadState(filepath.Join(server.runsDir, runID, "state.json"))
		if err != nil {
			server.renderError(writer, request, http.StatusNotFound, "Run Not Found", "Run state not found.")
			return
		}
		newRunID, err := server.manager.StartRun(request.Context(), rerunState.WorkflowID, rerunState.Inputs, rerunState.Env, rerunState.CallbackURL)
		if err != nil {
			server.renderError(writer, request, http.StatusInternalServerError, "Re-run Failed", err.Error())
			return
		}
		http.Redirect(writer, request, fmt.Sprintf("%s/runs/%s", server.basePath, newRunID), http.StatusFound)
		return
	}

	// Default: render the document view for /runs/<id> (and unknown suffixes → 404).
	runID := strings.TrimSuffix(path, "/")
	if strings.Contains(runID, "/") {
		server.renderError(writer, request, http.StatusNotFound, "Not Found", "Unknown route.")
		return
	}
	server.handleRunDetailDoc(writer, request, runID)
}

// agentExecutionView is the per-execution payload for the diagnostic
// agent-detail page: identifying tuple, prompt, transcript, artifacts,
// and decision/result captured from this specific node execution.
type agentExecutionView struct {
	RunID      string
	WorkflowID string
	NodeID     string
	Ordinal    int
	Resume     bool
	StartedAt  time.Time
	Prompt     string
	Decision   string
	Message    string
	Artifacts  []document.ArtifactRef
	Transcript *document.Transcript
}

// handleAgentDetail renders a plain diagnostic page for one agent
// session: every (run, node, attempt) that shared the given session id,
// with prompt, transcript, communicate-tool messages, and outputs.
func (server *Server) handleAgentDetail(writer http.ResponseWriter, request *http.Request) {
	const prefix = "/agents/"
	sessionID := strings.TrimPrefix(request.URL.Path, prefix)
	sessionID = strings.TrimSuffix(sessionID, "/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		server.renderError(writer, request, http.StatusNotFound, "Agent Not Found", "Missing session id.")
		return
	}
	execs := document.FindAgentExecutions(server.runsDir, sessionID)
	if len(execs) == 0 {
		server.renderError(writer, request, http.StatusNotFound, "Agent Not Found",
			fmt.Sprintf("No executions found for session %s", sessionID))
		return
	}
	views := make([]agentExecutionView, 0, len(execs))
	for _, ex := range execs {
		events, _, err := state.ReadEventsWithOffset(filepath.Join(server.runsDir, ex.RunID, "events.jsonl"))
		if err != nil {
			continue
		}
		slice := document.SliceExecutionEvents(events, ex.NodeID, ex.Ordinal)
		v := agentExecutionView{
			RunID:      ex.RunID,
			WorkflowID: ex.WorkflowID,
			NodeID:     ex.NodeID,
			Ordinal:    ex.Ordinal,
			Resume:     ex.Resume,
			StartedAt:  ex.StartedAt,
		}
		if raw := document.FindPromptForExecution(events, ex.NodeID, ex.Ordinal); raw != "" {
			if local, _ := document.ExtractLocalPrompt(raw); local != "" {
				v.Prompt = local
			} else {
				v.Prompt = raw
			}
		}
		transcript := document.BuildTranscript(ex.NodeID, slice)
		v.Transcript = &transcript
		rs, err := state.LoadState(filepath.Join(server.runsDir, ex.RunID, "state.json"))
		if err == nil && rs != nil && rs.Nodes[ex.NodeID] != nil {
			n := rs.Nodes[ex.NodeID]
			v.Decision = n.Decision
			v.Message = n.Message
			v.Artifacts = document.RowArtifacts(n)
		}
		views = append(views, v)
	}

	data := struct {
		BasePage
		SessionID  string
		Executions []agentExecutionView
	}{
		BasePage:   server.baseData("Agent", "runs"),
		SessionID:  sessionID,
		Executions: views,
	}
	server.renderTemplate(writer, "agent_detail.html", data)
}

// handleRunDetailDoc renders the document view for a run. Each run id
// — root or sub-run — gets its own standalone page rooted at that run.
// The parent run id (when set) is surfaced so the template can render
// a back-link to the parent's page.
func (server *Server) handleRunDetailDoc(writer http.ResponseWriter, request *http.Request, runID string) {
	loader := document.NewRunStoreLoader(server.runsDir)
	rs, err := loader.LoadRun(runID)
	if err != nil {
		server.renderError(writer, request, http.StatusNotFound, "Run Not Found", err.Error())
		return
	}
	parentRun := rs.ParentRun
	var bundle *definitions.Bundle
	if server.app != nil {
		bundle = server.app.Definitions
	}
	registry := document.NewWorkflowRegistry(bundle, loader)
	resolver := document.NewWorkflowPromptResolver(bundle, server.runsDir)
	doc, err := document.BuildDocumentWithRegistryAndResolver(rs.ID, loader, registry, resolver)
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Document Build Failed", err.Error())
		return
	}

	// Marshal Doc.Root to map[string]any so the template can branch via index / isRow helpers.
	rootBytes, err := json.Marshal(doc.Root)
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Document Marshal Failed", err.Error())
		return
	}
	var rootMap map[string]any
	if err := json.Unmarshal(rootBytes, &rootMap); err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Document Unmarshal Failed", err.Error())
		return
	}

	type templateDoc struct {
		RootRunID     string
		RootTitle     string
		RootStatus    string
		ParentRun     string
		BriefText     string
		BriefHTML     template.HTML
		BriefSource   string
		BriefFields   []document.BriefField
		TotalRuns     int
		Root          map[string]any
		CompoundGraph visualize.TopologyGraph
	}
	td := templateDoc{
		RootRunID:     doc.RootRunID,
		RootTitle:     doc.RootTitle,
		RootStatus:    doc.RootStatus,
		ParentRun:     parentRun,
		BriefText:     doc.BriefText,
		BriefHTML:     renderMarkdown(doc.BriefText),
		BriefSource:   doc.BriefSource,
		BriefFields:   doc.BriefFields,
		TotalRuns:     doc.TotalRuns,
		Root:          rootMap,
		CompoundGraph: document.BuildRunTreeGraph(server.runsDir, rs.ID),
	}

	data := struct {
		BasePage
		Doc templateDoc
	}{
		BasePage: server.baseData("Run", "runs"),
		Doc:      td,
	}

	server.renderTemplate(writer, "run_detail_doc.html", data)
}

// handleRunAction handles POST actions on a run (cancel, resume). It extracts
// the runID from the path suffix, validates preconditions, invokes the action,
// and redirects back to the run detail page.
func (server *Server) handleRunAction(writer http.ResponseWriter, request *http.Request, path, suffix, errorTitle string, action func(runID string) error) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := strings.TrimSuffix(path, suffix)
	if runID == "" {
		server.renderError(writer, request, http.StatusBadRequest, "Invalid Run", "Run id is required.")
		return
	}
	if server.manager == nil {
		server.renderError(writer, request, http.StatusServiceUnavailable, "Service Unavailable", "Run manager is unavailable.")
		return
	}
	if err := action(runID); err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, errorTitle, err.Error())
		return
	}
	http.Redirect(writer, request, fmt.Sprintf("%s/runs/%s", server.basePath, runID), http.StatusFound)
}

func (server *Server) handleWorkflows(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	workflows := server.loadWorkflowSummaries()
	data := struct {
		BasePage
		Workflows []WorkflowSummary
	}{
		BasePage:  server.baseData("Workflows", "workflows"),
		Workflows: workflows,
	}

	server.renderTemplate(writer, "workflows.html", data)
}

func (server *Server) handleWorkflowDetail(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/workflows/")
	if path == "" {
		server.renderError(writer, request, http.StatusNotFound, "Workflow Not Found", "No workflow id provided.")
		return
	}

	if strings.HasSuffix(path, "/run") {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		workflowID := strings.TrimSuffix(path, "/run")
		server.handleWorkflowRun(writer, request, workflowID)
		return
	}

	workflowID := strings.TrimSuffix(path, "/")
	workflow, ok := server.app.Definitions.Workflows[workflowID]
	if !ok {
		server.renderError(writer, request, http.StatusNotFound, "Workflow Not Found", "Workflow not found.")
		return
	}

	source := ""
	if workflow.SourcePath != "" {
		data, err := os.ReadFile(workflow.SourcePath)
		if err == nil {
			source = string(data)
		}
	}

	inputs := []InputField{}
	for key, description := range workflow.Inputs {
		inputs = append(inputs, InputField{Key: key, Label: key, Description: description})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Key < inputs[j].Key })

	pendingApprovals, err := server.loadApprovalsForWorkflow(workflowID)
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Approval Load Failed", err.Error())
		return
	}

	runs, err := server.loadRunSummaries()
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Run Load Failed", err.Error())
		return
	}
	allGroups := buildExecutionGroups(runs)
	relatedWorkflowIDs := connectedWorkflowIDs(server.app.Definitions, workflowID)
	relatedGroups := filterExecutionGroupsByWorkflowIDs(allGroups, relatedWorkflowIDs)
	relatedGroups = takeGroups(relatedGroups, 12)

	data := struct {
		BasePage
		Workflow              *definitions.Workflow
		Inputs                []InputField
		Approvals             []ApprovalView
		RelatedGroups         []ExecutionGroupSummary
		RelatedRunCount       int
		Source                string
		CompoundWorkflowGraph visualize.TopologyGraph
	}{
		BasePage:              server.baseData("Workflow", "workflows"),
		Workflow:              workflow,
		Inputs:                inputs,
		Approvals:             pendingApprovals,
		RelatedGroups:         relatedGroups,
		RelatedRunCount:       countRunsInGroups(relatedGroups),
		Source:                source,
		CompoundWorkflowGraph: visualize.CompoundWorkflowTopology(server.app.Definitions, workflow.ID),
	}

	server.renderTemplate(writer, "workflow_detail.html", data)
}

func (server *Server) handleWorkflowRun(writer http.ResponseWriter, request *http.Request, workflowID string) {
	if server.manager == nil {
		server.renderError(writer, request, http.StatusServiceUnavailable, "Service Unavailable", "Run manager is unavailable.")
		return
	}
	if err := request.ParseForm(); err != nil {
		server.renderError(writer, request, http.StatusBadRequest, "Invalid Form", "Unable to parse form data.")
		return
	}

	server.startRunFromForm(writer, request, workflowID)
}

// startRunFromForm extracts input_* form fields and project_dir, starts a run,
// and redirects to the run detail page.
func (server *Server) startRunFromForm(writer http.ResponseWriter, request *http.Request, workflowID string) {
	inputs := map[string]any{}
	for key, values := range request.Form {
		if !strings.HasPrefix(key, "input_") {
			continue
		}
		if len(values) == 0 || values[0] == "" {
			continue
		}
		inputKey := strings.TrimPrefix(key, "input_")
		inputs[inputKey] = values[0]
	}

	env := map[string]string{}
	if projectDir := request.FormValue("project_dir"); projectDir != "" {
		env["PROJECT_DIR"] = projectDir
		if _, ok := inputs["project_dir"]; !ok {
			inputs["project_dir"] = projectDir
		}
	}

	runID, err := server.manager.StartRun(request.Context(), workflowID, inputs, env, "")
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Run Start Failed", err.Error())
		return
	}

	http.Redirect(writer, request, fmt.Sprintf("%s/runs/%s", server.basePath, runID), http.StatusFound)
}

func (server *Server) handleApprovals(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	pending, resolved, err := server.loadApprovals()
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Approval Load Failed", err.Error())
		return
	}

	data := struct {
		BasePage
		Pending  []ApprovalView
		Resolved []ApprovalView
	}{
		BasePage: server.baseData("Approvals", "approvals"),
		Pending:  pending,
		Resolved: resolved,
	}

	server.renderTemplate(writer, "approvals.html", data)
}

func (server *Server) handleApprovalResolve(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/approvals/")
	approvalID := strings.TrimSuffix(path, "/resolve")
	if approvalID == "" {
		server.renderError(writer, request, http.StatusBadRequest, "Approval Required", "Approval id is required.")
		return
	}
	if err := request.ParseForm(); err != nil {
		server.renderError(writer, request, http.StatusBadRequest, "Invalid Form", "Unable to parse form data.")
		return
	}
	decision := request.FormValue(fieldDecision)
	message := request.FormValue(transcriptKindMessage)
	comment := request.FormValue("comment")
	if decision == "" || message == "" {
		server.renderError(writer, request, http.StatusBadRequest, "Missing Data", "Decision and message are required.")
		return
	}

	approval, err := approvals.Resolve(server.runRoot(), approvalID, approvals.ResolveInput{
		Decision: decision,
		Message:  message,
		Comment:  comment,
	})
	if err != nil {
		server.renderError(writer, request, http.StatusInternalServerError, "Approval Failed", err.Error())
		return
	}

	if server.manager != nil {
		_ = server.manager.NotifyApproval(context.Background(), approval.RunID)
	}

	http.Redirect(writer, request, fmt.Sprintf("%s/approvals", server.basePath), http.StatusFound)
}

// getTemplates returns the template set to use for rendering.
// In dev mode, templates are re-parsed from disk on every call.
// In prod mode, the cached server.templates is returned.
func (server *Server) getTemplates() *template.Template {
	if server.devMode {
		tmpl, err := LoadTemplates()
		if err != nil {
			slog.Error("dev mode: failed to reload templates", "error", err)
			return server.templates // fallback to cached
		}
		return tmpl
	}
	return server.templates
}

func (server *Server) renderTemplate(writer http.ResponseWriter, name string, data any) {
	tmpl := server.getTemplates()
	if tmpl == nil || tmpl.Lookup(name) == nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, name, data); err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html")
	_, _ = buffer.WriteTo(writer)
}

// LearningProposal is a single parsed proposal from the synthesizer.
type LearningProposal struct {
	TargetFile   string
	Summary      string
	ProposedText string
}

// LearningsGroup groups proposals by learn run with source context.
type LearningsGroup struct {
	LearnRunID       string
	SourceRunID      string
	SourceWorkflowID string
	SourceTitle      string
	StartedAt        time.Time
	Proposals        []LearningProposal
}

func (server *Server) handleLearnings(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	filterWorkflow := request.URL.Query().Get("workflow_id")
	groups := server.loadLearnings(filterWorkflow)

	// Collect unique workflow IDs for the filter sidebar.
	allGroups := server.loadLearnings("")
	workflowCounts := map[string]int{}
	for _, g := range allGroups {
		workflowCounts[g.SourceWorkflowID] += len(g.Proposals)
	}
	type WorkflowFilter struct {
		ID    string
		Count int
	}
	var filters []WorkflowFilter
	seen := map[string]bool{}
	for _, g := range allGroups {
		if !seen[g.SourceWorkflowID] {
			seen[g.SourceWorkflowID] = true
			filters = append(filters, WorkflowFilter{
				ID:    g.SourceWorkflowID,
				Count: workflowCounts[g.SourceWorkflowID],
			})
		}
	}
	sort.Slice(filters, func(i, j int) bool {
		return filters[i].ID < filters[j].ID
	})

	data := struct {
		BasePage
		Groups         []LearningsGroup
		Filters        []WorkflowFilter
		ActiveFilter   string
		TotalProposals int
	}{
		BasePage:     server.baseData("Learnings", "learnings"),
		Groups:       groups,
		Filters:      filters,
		ActiveFilter: filterWorkflow,
	}
	for _, g := range groups {
		data.TotalProposals += len(g.Proposals)
	}

	server.renderTemplate(writer, "learnings.html", data)
}

func (server *Server) loadLearnings(filterWorkflow string) []LearningsGroup {
	entries, err := os.ReadDir(server.runsDir)
	if err != nil {
		return nil
	}

	var groups []LearningsGroup
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		rs, err := state.LoadState(filepath.Join(server.runsDir, runID, "state.json"))
		if err != nil || rs.WorkflowID != "learn" {
			continue
		}
		proposals := parseLearningProposals(rs)
		if len(proposals) == 0 {
			continue
		}

		sourceRunID, _ := rs.Inputs["run_id"].(string)
		sourceWorkflowID := ""
		sourceTitle := sourceRunID
		if sourceRunID != "" {
			if src, err := state.LoadState(filepath.Join(server.runsDir, sourceRunID, "state.json")); err == nil {
				sourceWorkflowID = src.WorkflowID
				if strings.TrimSpace(src.Title) != "" {
					sourceTitle = strings.TrimSpace(src.Title)
				}
			}
		}

		if filterWorkflow != "" && sourceWorkflowID != filterWorkflow {
			continue
		}

		groups = append(groups, LearningsGroup{
			LearnRunID:       runID,
			SourceRunID:      sourceRunID,
			SourceWorkflowID: sourceWorkflowID,
			SourceTitle:      sourceTitle,
			StartedAt:        rs.StartedAt,
			Proposals:        proposals,
		})
	}

	// Newest first.
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].StartedAt.After(groups[j].StartedAt)
	})
	return groups
}

func parseLearningProposals(rs *state.RunState) []LearningProposal {
	if rs == nil || rs.Nodes == nil {
		return nil
	}
	synth, ok := rs.Nodes["synthesize"]
	if !ok || synth.Data == nil {
		return nil
	}
	rawProposals, ok := synth.Data["proposals"]
	if !ok {
		return nil
	}
	arr, ok := rawProposals.([]any)
	if !ok {
		return nil
	}

	var results []LearningProposal
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			p := parsePipeProposal(v)
			if p.Summary != "" || p.TargetFile != "" {
				results = append(results, p)
			}
		case map[string]any:
			results = append(results, LearningProposal{
				TargetFile:   stringVal(v, "target_file"),
				Summary:      stringVal(v, "summary"),
				ProposedText: stringVal(v, "proposed_text"),
			})
		}
	}
	return results
}

func parsePipeProposal(s string) LearningProposal {
	p := LearningProposal{}
	for _, part := range strings.Split(s, " | ") {
		k, v, ok := strings.Cut(part, ": ")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "target_file":
			p.TargetFile = strings.TrimSpace(v)
		case "summary":
			p.Summary = strings.TrimSpace(v)
		case "proposed_text":
			p.ProposedText = strings.TrimSpace(v)
		}
	}
	return p
}

func stringVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (server *Server) renderError(writer http.ResponseWriter, request *http.Request, status int, title string, message string) {
	writer.WriteHeader(status)
	data := struct {
		BasePage
		Message string
	}{
		BasePage: server.baseData(title, ""),
		Message:  message,
	}
	server.renderTemplate(writer, "error.html", data)
}

func (server *Server) loadRunSummaries() ([]RunSummary, error) {
	entries, err := os.ReadDir(server.runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []RunSummary{}, nil
		}
		return nil, err
	}

	runs := []RunSummary{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runState, err := state.LoadState(filepath.Join(server.runsDir, entry.Name(), "state.json"))
		if err != nil {
			continue
		}
		workflowName := runState.WorkflowID
		if server.app != nil && server.app.Definitions != nil {
			if workflow, ok := server.app.Definitions.Workflows[runState.WorkflowID]; ok {
				if strings.TrimSpace(workflow.Name) != "" {
					workflowName = workflow.Name
				}
			}
		}
		title := strings.TrimSpace(runState.Title)
		if title == "" {
			title = workflowName
		}
		runTotal := loadRunTotal(filepath.Join(server.runsDir, entry.Name()), runState)
		runs = append(runs, RunSummary{
			ID:                   runState.ID,
			Title:                title,
			Description:          strings.TrimSpace(runState.Description),
			Summary:              strings.TrimSpace(runState.Summary),
			WorkflowID:           runState.WorkflowID,
			WorkflowName:         workflowName,
			Status:               runState.Status,
			HasUnresolvedFailure: runState.HasUnresolvedFailure,
			StartedAt:            runState.StartedAt,
			FinishedAt:           runState.FinishedAt,
			Duration:             formatDuration(runState.StartedAt, runState.FinishedAt),
			ParentRun:            runState.ParentRun,
			RunTotal:             runTotal,
			TaggedNodes:          runState.NodesByTag(),
		})
	}

	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	return runs, nil
}

func (server *Server) loadWorkflowSummaries() []WorkflowSummary {
	workflows := []WorkflowSummary{}
	if server.app == nil || server.app.Definitions == nil {
		return workflows
	}
	for _, workflow := range server.app.Definitions.Workflows {
		tags := strings.Join(workflow.Tags, ", ")
		workflows = append(workflows, WorkflowSummary{
			ID:          workflow.ID,
			Name:        workflow.Name,
			Description: workflow.Description,
			NodeCount:   len(workflow.Nodes),
			Tags:        tags,
		})
	}
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].ID < workflows[j].ID })
	return workflows
}

func (server *Server) loadApprovals() ([]ApprovalView, []ApprovalView, error) {
	approvalsList, err := approvals.ListAll(server.runRoot())
	if err != nil {
		return nil, nil, err
	}

	pending := []ApprovalView{}
	resolved := []ApprovalView{}
	for _, approval := range approvalsList {
		view := server.approvalViewFor(approval)

		if approval.Status == statusResolved {
			resolved = append(resolved, view)
		} else {
			pending = append(pending, view)
		}
	}

	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt > pending[j].CreatedAt })
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ResolvedAt > resolved[j].ResolvedAt })

	return pending, resolved, nil
}

func (server *Server) loadApprovalsForWorkflow(workflowID string) ([]ApprovalView, error) {
	approvalsList, err := approvals.ListAll(server.runRoot())
	if err != nil {
		return nil, err
	}

	pending := []ApprovalView{}
	for _, approval := range approvalsList {
		if approval.Status == statusResolved {
			continue
		}
		runState, err := state.LoadState(filepath.Join(server.runsDir, approval.RunID, "state.json"))
		if err != nil {
			continue
		}
		if runState.WorkflowID != workflowID {
			continue
		}
		pending = append(pending, server.approvalViewFor(approval))
	}

	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt > pending[j].CreatedAt })
	return pending, nil
}

func (server *Server) approvalViewFor(approval *approvals.Approval) ApprovalView {
	view := ApprovalView{
		ID:        approval.ID,
		RunID:     approval.RunID,
		NodeID:    approval.NodeID,
		Question:  strings.TrimSpace(approval.Question),
		Decision:  approval.Decision,
		Message:   approval.Message,
		CreatedAt: approval.CreatedAt.Local().Format("2006-01-02 15:04:05"),
	}
	if approval.ResolvedAt != nil {
		view.ResolvedAt = approval.ResolvedAt.Local().Format("2006-01-02 15:04:05")
	}

	view.NodeLabel = approval.NodeID
	if workflow := server.loadRunWorkflowSnapshot(approval.RunID); workflow != nil {
		if node := definitions.FindNode(workflow, approval.NodeID); node != nil {
			if node.Role != "" {
				view.NodeLabel = fmt.Sprintf("%s (%s)", node.ID, node.Role)
			}
			if len(node.Decisions) > 0 {
				view.DecisionOptions = node.Decisions.IDs()
			}
			if view.Question == "" && strings.TrimSpace(node.Prompt) != "" {
				view.Question = strings.TrimSpace(node.Prompt)
			}
		}
	}

	view.QuestionHTML = renderMarkdown(view.Question)
	if approval.TimeoutSec > 0 && approval.Default != "" {
		view.TimeoutSec = approval.TimeoutSec
		view.TimeoutDefault = approval.Default
		view.DeadlineUnix = approval.CreatedAt.Add(time.Duration(approval.TimeoutSec) * time.Second).Unix()
	}
	return view
}

func (server *Server) loadApprovalCounts() (int, error) {
	approvalsList, err := approvals.ListAll(server.runRoot())
	if err != nil {
		return 0, err
	}
	count := 0
	for _, approval := range approvalsList {
		if approval.Status != statusResolved {
			count++
		}
	}
	return count, nil
}

func (server *Server) loadRunWorkflowSnapshot(runID string) *definitions.Workflow {
	path := filepath.Join(server.runsDir, runID, "workflow.yaml")
	workflow, err := definitions.LoadWorkflowSnapshot(path)
	if err != nil {
		return nil
	}
	return workflow
}

func (server *Server) runRoot() string {
	return filepath.Dir(server.runsDir)
}

func isTerminalStatus(status string) bool {
	return status == statusCompleted || status == statusFailed || status == statusCancelled
}

// loadRunTotal returns rolled-up NodeTotals for one run.
//
// Fast path: terminal runs persist their totals on RunState.Totals
// (engine writes them at termination, since PRI-1351). Just return
// that — no I/O.
//
// Slow path: legacy runs without persisted Totals, or active runs
// whose events.jsonl is still growing. Read events, compute, and
// (for terminal runs only) write the result back to state.json so
// subsequent reads take the fast path.
//
// Returns nil when events cannot be read on the slow path.
func loadRunTotal(runDir string, rs *state.RunState) *state.NodeTotals {
	if rs == nil {
		return nil
	}
	if isTerminalStatus(rs.Status) && rs.Totals != nil {
		return rs.Totals
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	events, _, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil {
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
	total := c.RunTotal()

	if isTerminalStatus(rs.Status) {
		rs.Totals = &total
		// Best-effort persist back. Failure is non-fatal — the next
		// read will retry.
		_ = state.SaveState(filepath.Join(runDir, "state.json"), rs)
	}

	return &total
}

func formatDuration(start time.Time, finish *time.Time) string {
	if start.IsZero() {
		return "-"
	}
	if finish == nil {
		return time.Since(start).Truncate(time.Second).String()
	}
	return finish.Sub(start).Truncate(time.Second).String()
}

func formatOutputValue(value any) string {
	if value == nil {
		return ""
	}
	switch value.(type) {
	case map[string]any, []any, map[string]string, []string:
		data, err := json.MarshalIndent(value, "", "  ")
		if err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", value)
}

func takeGroups(groups []ExecutionGroupSummary, limit int) []ExecutionGroupSummary {
	if len(groups) <= limit {
		return groups
	}
	return groups[:limit]
}

func buildExecutionGroups(runs []RunSummary) []ExecutionGroupSummary {
	if len(runs) == 0 {
		return []ExecutionGroupSummary{}
	}

	runsByID := make(map[string]RunSummary, len(runs))
	for _, run := range runs {
		runsByID[run.ID] = run
	}

	childrenByParent := map[string][]RunSummary{}
	roots := make([]RunSummary, 0, len(runs))
	for _, run := range runs {
		parentID := strings.TrimSpace(run.ParentRun)
		if parentID == "" {
			roots = append(roots, run)
			continue
		}
		if _, ok := runsByID[parentID]; !ok {
			roots = append(roots, run)
			continue
		}
		childrenByParent[parentID] = append(childrenByParent[parentID], run)
	}

	sort.Slice(roots, func(i, j int) bool { return roots[i].StartedAt.After(roots[j].StartedAt) })
	for parentID := range childrenByParent {
		sort.Slice(childrenByParent[parentID], func(i, j int) bool {
			left := childrenByParent[parentID][i]
			right := childrenByParent[parentID][j]
			if left.StartedAt.Equal(right.StartedAt) {
				return left.ID < right.ID
			}
			return left.StartedAt.Before(right.StartedAt)
		})
	}

	groups := make([]ExecutionGroupSummary, 0, len(roots))
	for _, root := range roots {
		groups = append(groups, buildExecutionGroup(root, childrenByParent))
	}
	return groups
}

func buildExecutionGroup(root RunSummary, childrenByParent map[string][]RunSummary) ExecutionGroupSummary {
	rows := []RunTreeRow{}
	totalRuns := 0
	activeRuns := 0
	hasFailed := false
	hasRunning := false
	hasPaused := false
	hasCancelled := false

	var walk func(run RunSummary, depth int)
	walk = func(run RunSummary, depth int) {
		totalRuns++
		if isActiveStatus(run.Status) {
			activeRuns++
		}

		switch EffectiveStatus(run.Status, run.HasUnresolvedFailure) {
		case statusFailed:
			hasFailed = true
		case statusRunning:
			hasRunning = true
		case statusPaused, statusAwaitingApproval:
			hasPaused = true
		case statusCancelled:
			hasCancelled = true
		case statusCompleted, statusSkipped:
		default:
			hasRunning = true
		}

		children := childrenByParent[run.ID]
		rows = append(rows, RunTreeRow{
			Run:         run,
			Depth:       depth,
			IndentPx:    depth * 20,
			HasChildren: len(children) > 0,
		})

		for _, child := range children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)

	var groupStatus string
	switch {
	case hasFailed:
		groupStatus = statusFailed
	case hasRunning:
		groupStatus = statusRunning
	case hasPaused:
		groupStatus = statusPaused
	case hasCancelled:
		groupStatus = statusCancelled
	default:
		groupStatus = statusCompleted
	}

	return ExecutionGroupSummary{
		Root:        root,
		GroupStatus: groupStatus,
		TotalRuns:   totalRuns,
		ActiveRuns:  activeRuns,
		Rows:        rows,
		Tree:        buildTree(rows),
		GroupTotal:  sumRowTotals(rows),
	}
}

// sumRowTotals adds up the cached RunTotal on every row in the group,
// producing the group-wide duration / tokens / cost shown on the
// Execution Group card. Returns nil when no row has a total yet.
func sumRowTotals(rows []RunTreeRow) *state.NodeTotals {
	var out state.NodeTotals
	any := false
	for _, r := range rows {
		if r.Run.RunTotal == nil {
			continue
		}
		any = true
		t := *r.Run.RunTotal
		out.Tokens.Input += t.Tokens.Input
		out.Tokens.Output += t.Tokens.Output
		out.Tokens.CacheRead += t.Tokens.CacheRead
		out.Tokens.CacheWrite += t.Tokens.CacheWrite
		out.Tokens.CacheWrite1h += t.Tokens.CacheWrite1h
		out.Tokens.Reasoning += t.Tokens.Reasoning
		if t.CostUSD != nil {
			if out.CostUSD == nil {
				v := *t.CostUSD
				out.CostUSD = &v
			} else {
				v := *out.CostUSD + *t.CostUSD
				out.CostUSD = &v
			}
		}
		out.UnpricedEventCount += t.UnpricedEventCount
		if t.DurationMs > out.DurationMs {
			out.DurationMs = t.DurationMs
		}
	}
	if !any {
		return nil
	}
	out.Tokens.Total = out.Tokens.Input + out.Tokens.Output +
		out.Tokens.CacheRead + out.Tokens.CacheWrite + out.Tokens.CacheWrite1h
	return &out
}

func buildTree(rows []RunTreeRow) []RunTreeNode {
	type node struct {
		run      RunSummary
		children []*node
	}
	byID := make(map[string]*node, len(rows))
	var roots []*node

	for _, row := range rows {
		byID[row.Run.ID] = &node{run: row.Run}
	}
	for _, row := range rows {
		n := byID[row.Run.ID]
		if p, ok := byID[row.Run.ParentRun]; ok {
			p.children = append(p.children, n)
		} else {
			roots = append(roots, n)
		}
	}

	var toTree func(*node) RunTreeNode
	toTree = func(n *node) RunTreeNode {
		out := RunTreeNode{Run: n.run}
		for _, c := range n.children {
			out.Children = append(out.Children, toTree(c))
		}
		return out
	}

	result := make([]RunTreeNode, 0, len(roots))
	for _, r := range roots {
		result = append(result, toTree(r))
	}
	return result
}

func isActiveStatus(status string) bool {
	switch status {
	case statusRunning, statusPaused, statusAwaitingApproval:
		return true
	default:
		return false
	}
}

func connectedWorkflowIDs(bundle *definitions.Bundle, selectedWorkflowID string) map[string]struct{} {
	related := map[string]struct{}{}
	if strings.TrimSpace(selectedWorkflowID) == "" {
		return related
	}
	if bundle == nil || len(bundle.Workflows) == 0 {
		related[selectedWorkflowID] = struct{}{}
		return related
	}

	adjacency := map[string]map[string]struct{}{}
	for workflowID, workflow := range bundle.Workflows {
		if _, ok := adjacency[workflowID]; !ok {
			adjacency[workflowID] = map[string]struct{}{}
		}
		for _, node := range workflow.Nodes {
			if node.Kind != kindSubworkflowBaseKind || strings.TrimSpace(node.Workflow) == "" {
				continue
			}
			target := strings.TrimSpace(node.Workflow)
			if _, ok := adjacency[target]; !ok {
				adjacency[target] = map[string]struct{}{}
			}
			adjacency[workflowID][target] = struct{}{}
			adjacency[target][workflowID] = struct{}{}
		}
	}
	if _, ok := adjacency[selectedWorkflowID]; !ok {
		adjacency[selectedWorkflowID] = map[string]struct{}{}
	}

	queue := []string{selectedWorkflowID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, seen := related[current]; seen {
			continue
		}
		related[current] = struct{}{}
		for neighbor := range adjacency[current] {
			if _, seen := related[neighbor]; !seen {
				queue = append(queue, neighbor)
			}
		}
	}
	return related
}

func filterExecutionGroupsByWorkflowIDs(groups []ExecutionGroupSummary, workflowIDs map[string]struct{}) []ExecutionGroupSummary {
	if len(workflowIDs) == 0 {
		return []ExecutionGroupSummary{}
	}
	filtered := make([]ExecutionGroupSummary, 0, len(groups))
	for _, group := range groups {
		include := false
		for _, row := range group.Rows {
			if _, ok := workflowIDs[row.Run.WorkflowID]; ok {
				include = true
				break
			}
		}
		if include {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

func countRunsInGroups(groups []ExecutionGroupSummary) int {
	total := 0
	for _, group := range groups {
		total += group.TotalRuns
	}
	return total
}

// handleRunInputs serves an HTML fragment of a run's inputs for the
// transcript drawer (or any htmx consumer).
func (server *Server) handleRunInputs(w http.ResponseWriter, r *http.Request, runID string) {
	runState, workflow, ok := server.loadRunAndWorkflow(w, r, runID)
	if !ok {
		return
	}
	_ = workflow
	stories := parseStoryCards(runState.Inputs["stories"])
	data := struct {
		Title   string
		RunID   string
		KVs     []KeyValue
		Stories []StoryCard
	}{
		Title:   "Inputs",
		RunID:   runID,
		KVs:     buildInputKVs(runState, stories),
		Stories: stories,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "run-kv-panel", data); err != nil {
		slog.Error("handleRunInputs: execute template", "error", err)
	}
}

// handleRunOutputs serves an HTML fragment of a run's outputs.
func (server *Server) handleRunOutputs(w http.ResponseWriter, r *http.Request, runID string) {
	runState, workflow, ok := server.loadRunAndWorkflow(w, r, runID)
	if !ok {
		return
	}
	data := struct {
		Title   string
		RunID   string
		KVs     []KeyValue
		Stories []StoryCard
	}{
		Title: "Outputs",
		RunID: runID,
		KVs:   buildOutputKVs(runState, workflow),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := server.templates.ExecuteTemplate(w, "run-kv-panel", data); err != nil {
		slog.Error("handleRunOutputs: execute template", "error", err)
	}
}

// loadRunAndWorkflow reads the run state + workflow snapshot for a given
// run ID. Handles 404/500 error responses itself; returns ok=false when
// the caller should return without writing more.
func (server *Server) loadRunAndWorkflow(w http.ResponseWriter, _ *http.Request, runID string) (*state.RunState, *definitions.Workflow, bool) {
	runDir := filepath.Join(server.runsDir, runID)
	runStatePath := filepath.Join(runDir, "state.json")
	runState, err := state.LoadState(runStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, nil)
			return nil, nil, false
		}
		slog.Error("loadRunAndWorkflow: read state", "runID", runID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	workflow, err := loadRunWorkflow(runDir)
	if err != nil {
		slog.Error("loadRunAndWorkflow: read workflow", "runID", runID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	return runState, workflow, true
}

// buildInputKVs formats a run's inputs as sorted KV entries for rendering.
// The "stories" key is omitted when it has already been parsed into
// StoryCards rendered separately.
func buildInputKVs(runState *state.RunState, stories []StoryCard) []KeyValue {
	kvs := []KeyValue{}
	if runState == nil {
		return kvs
	}
	for key, value := range runState.Inputs {
		if key == "stories" && len(stories) > 0 {
			continue
		}
		text, html, isJSON := formatValueWithHTML(value)
		kvs = append(kvs, KeyValue{Key: key, Value: text, HTML: html, IsJSON: isJSON})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].Key < kvs[j].Key })
	return kvs
}

// buildOutputKVs resolves the workflow's declared outputs against the run's
// state context and returns them as sorted KV entries.
func buildOutputKVs(runState *state.RunState, workflow *definitions.Workflow) []KeyValue {
	kvs := []KeyValue{}
	if runState == nil || workflow == nil || len(workflow.Outputs) == 0 {
		return kvs
	}
	runContext := engine.RunContextFromState(runState, workflow)
	for key, expression := range workflow.Outputs {
		value, err := runContext.Resolve(expression)
		if err != nil {
			text, html, isJSON := formatValueWithHTML(fmt.Sprintf("error: %v", err))
			kvs = append(kvs, KeyValue{Key: key, Value: text, HTML: html, IsJSON: isJSON})
			continue
		}
		text, html, isJSON := formatValueWithHTML(value)
		kvs = append(kvs, KeyValue{Key: key, Value: text, HTML: html, IsJSON: isJSON})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].Key < kvs[j].Key })
	return kvs
}

func findExecutionGroupByRunID(groups []ExecutionGroupSummary, runID string) *ExecutionGroupSummary {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	for _, group := range groups {
		for _, row := range group.Rows {
			if row.Run.ID == runID {
				copy := group
				return &copy
			}
		}
	}
	return nil
}

func parseStoryCards(raw any) []StoryCard {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	cards := make([]StoryCard, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		card := StoryCard{
			ID:       stringField(m, "id"),
			Title:    stringField(m, fieldTitle),
			Status:   stringField(m, fieldStatus),
			Filename: stringField(m, "filename"),
		}
		// Build body from description + acceptance_criteria
		body := stringField(m, fieldDescription)
		if ac, ok := m["acceptance_criteria"].([]any); ok && len(ac) > 0 {
			body += "\n\n**Acceptance Criteria:**\n"
			for _, criterion := range ac {
				body += fmt.Sprintf("- %v\n", criterion)
			}
		}
		// If there's a content field with YAML frontmatter, strip it
		if content := stringField(m, fieldContent); content != "" {
			body = stripYAMLFrontmatter(content)
		}
		card.Body = strings.TrimSpace(body)
		if card.Body != "" {
			card.BodyHTML = renderMarkdown(card.Body)
		}
		if card.Title == "" && card.ID == "" {
			continue
		}
		cards = append(cards, card)
	}
	return cards
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func stripYAMLFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := content[3:]
	idx := strings.Index(rest, "---")
	if idx == -1 {
		return content
	}
	return strings.TrimSpace(rest[idx+3:])
}

func buildNodeSummaries(workflow *definitions.Workflow, rs *state.RunState) []NodeSummary {
	if workflow == nil {
		return nil
	}
	summaries := make([]NodeSummary, 0, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		status := statusPending
		if rs != nil {
			rs.WithNodes(func(nodes map[string]*state.NodeState) {
				if ns, ok := nodes[node.ID]; ok {
					status = ns.Status
				}
			})
		}
		label := node.ID
		if node.Role != "" {
			label = fmt.Sprintf("%s (%s)", node.ID, node.Role)
		}
		summaries = append(summaries, NodeSummary{
			ID:     node.ID,
			Label:  label,
			Status: status,
		})
	}
	return summaries
}
