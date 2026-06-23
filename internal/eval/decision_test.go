package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// writeRunFixture creates a run directory with workflow.yaml (raw YAML) and state.json.
// It also sets TOIL_RUNS_DIR so config.RunsDir(root) resolves to root/runs.
func writeRunFixture(t *testing.T, root, runID, workflowYAML string, runState *state.RunState) {
	t.Helper()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "workflow.yaml"), []byte(workflowYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		t.Fatal(err)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// Minimal valid workflow YAML with a review node that has two decisions.
const reviewWorkflowYAML = `
id: test-wf
name: Test Workflow
version: 1
nodes:
  - id: build
    kind: shell
    prompt: "build"
  - id: review
    kind: human
    prompt: "review"
    decisions:
      - approved
      - clarified
      - changes_requested
edges:
  - from: build
    to: review
    when: default
`

// Workflow where "review" node only has pass/fail (no "approved").
const passfailWorkflowYAML = `
id: test-wf
name: Test Workflow
version: 1
nodes:
  - id: build
    kind: shell
    prompt: "build"
  - id: review
    kind: human
    prompt: "review"
    decisions:
      - pass
      - fail
edges:
  - from: build
    to: review
    when: default
`

// Workflow where review has no decisions at all.
const noDecisionsWorkflowYAML = `
id: test-wf
name: Test Workflow
version: 1
nodes:
  - id: review
    kind: human
    prompt: "review"
`

// --- latestIncomingDecision tests ---

func TestLatestIncomingDecision_SingleUpstream(t *testing.T) {
	now := time.Now().UTC()
	// latestIncomingDecision works with in-memory structs, not disk.
	// Import definitions directly for edge/workflow construction.
	workflow := buildWorkflow([]edge{{from: "build", to: "review", when: "default"}})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(now)},
		},
	}

	got := latestIncomingDecision(workflow, runState, "review")
	if got != "ready_for_review" {
		t.Fatalf("latestIncomingDecision = %q, want %q", got, "ready_for_review")
	}
}

func TestLatestIncomingDecision_MultipleUpstream_PicksLatest(t *testing.T) {
	early := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	workflow := buildWorkflow([]edge{
		{from: "build", to: "review", when: "default"},
		{from: "test", to: "review", when: "default"},
	})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "needs_changes", EndedAt: timePtr(early)},
			"test":  {Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(late)},
		},
	}

	got := latestIncomingDecision(workflow, runState, "review")
	if got != "ready_for_review" {
		t.Fatalf("latestIncomingDecision = %q, want %q (should pick latest)", got, "ready_for_review")
	}
}

func TestLatestIncomingDecision_SkipsIncomplete(t *testing.T) {
	now := time.Now().UTC()
	workflow := buildWorkflow([]edge{
		{from: "build", to: "review", when: "default"},
		{from: "test", to: "review", when: "default"},
	})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(now)},
			"test":  {Status: "running"},
		},
	}

	got := latestIncomingDecision(workflow, runState, "review")
	if got != "ready_for_review" {
		t.Fatalf("latestIncomingDecision = %q, want %q", got, "ready_for_review")
	}
}

func TestLatestIncomingDecision_SkipsExpressionEdges(t *testing.T) {
	now := time.Now().UTC()
	workflow := buildWorkflow([]edge{
		{from: "build", to: "review", when: "node.build.decision == 'pass'"},
	})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "pass", EndedAt: timePtr(now)},
		},
	}

	got := latestIncomingDecision(workflow, runState, "review")
	if got != "" {
		t.Fatalf("latestIncomingDecision = %q, want empty (expression edges skipped)", got)
	}
}

func TestLatestIncomingDecision_NoUpstream(t *testing.T) {
	workflow := buildWorkflow([]edge{{from: "a", to: "b"}})
	runState := &state.RunState{Nodes: map[string]*state.NodeState{}}

	got := latestIncomingDecision(workflow, runState, "start")
	if got != "" {
		t.Fatalf("latestIncomingDecision = %q, want empty", got)
	}
}

func TestLatestIncomingDecision_WhenMismatch(t *testing.T) {
	now := time.Now().UTC()
	workflow := buildWorkflow([]edge{
		{from: "build", to: "deploy", when: "pass"},
	})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "fail", EndedAt: timePtr(now)},
		},
	}

	got := latestIncomingDecision(workflow, runState, "deploy")
	if got != "" {
		t.Fatalf("latestIncomingDecision = %q, want empty (when mismatch)", got)
	}
}

func TestLatestIncomingDecision_FallsBackToStartedAt(t *testing.T) {
	now := time.Now().UTC()
	workflow := buildWorkflow([]edge{
		{from: "build", to: "review", when: "default"},
	})
	runState := &state.RunState{
		Nodes: map[string]*state.NodeState{
			"build": {Status: "completed", Decision: "ready_for_review", StartedAt: timePtr(now)},
		},
	}

	got := latestIncomingDecision(workflow, runState, "review")
	if got != "ready_for_review" {
		t.Fatalf("latestIncomingDecision = %q, want %q", got, "ready_for_review")
	}
}

// --- inferDecision tests (require disk fixtures) ---

func TestInferDecision_NeedsMoreInfo_ReturnsClarified(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "needs_more_info", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	decision, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if !ok {
		t.Fatal("inferDecision returned ok=false")
	}
	if decision != "clarified" {
		t.Fatalf("decision = %q, want %q", decision, "clarified")
	}
}

func TestInferDecision_ReadyForReview_ReturnsApproved(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	decision, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if !ok {
		t.Fatal("inferDecision returned ok=false")
	}
	if decision != "approved" {
		t.Fatalf("decision = %q, want %q", decision, "approved")
	}
}

func TestInferDecision_NeedsChanges_ReturnsChangesRequested(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "needs_changes", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	decision, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if !ok {
		t.Fatal("inferDecision returned ok=false")
	}
	if decision != "changes_requested" {
		t.Fatalf("decision = %q, want %q", decision, "changes_requested")
	}
}

func TestInferDecision_FallsBackToApproved(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "some_unknown_decision", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	decision, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if !ok {
		t.Fatal("inferDecision returned ok=false")
	}
	if decision != "approved" {
		t.Fatalf("decision = %q, want %q", decision, "approved")
	}
}

func TestInferDecision_FallsBackToFirstDecision(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "unknown", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", passfailWorkflowYAML, rs)

	decision, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if !ok {
		t.Fatal("inferDecision returned ok=false")
	}
	if decision != "pass" {
		t.Fatalf("decision = %q, want %q (first decision)", decision, "pass")
	}
}

func TestInferDecision_NoDecisions_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	writeRunFixture(t, root, "run-1", noDecisionsWorkflowYAML, rs)

	_, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if ok {
		t.Fatal("inferDecision should return ok=false when node has no decisions")
	}
}

func TestInferDecision_NodeNotFound_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	writeRunFixture(t, root, "run-1", noDecisionsWorkflowYAML, rs)

	_, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "nonexistent"})
	if ok {
		t.Fatal("inferDecision should return ok=false when node not found")
	}
}

func TestInferDecision_MissingWorkflowFile_ReturnsFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, ok := inferDecision(root, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if ok {
		t.Fatal("inferDecision should return ok=false when workflow.yaml missing")
	}
}

// --- decisionForApproval tests ---

func TestDecisionForApproval_ExplicitConfig(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		Approvals: map[string]ApprovalSpec{
			"review": {Decision: "rejected", Message: "No way", Comment: "eval says no"},
		},
	}

	decision, message, comment := decisionForApproval(root, spec, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if decision != "rejected" {
		t.Fatalf("decision = %q, want %q", decision, "rejected")
	}
	if message != "No way" {
		t.Fatalf("message = %q, want %q", message, "No way")
	}
	if comment != "eval says no" {
		t.Fatalf("comment = %q, want %q", comment, "eval says no")
	}
}

func TestDecisionForApproval_InfersFromWorkflow(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	spec := &Spec{}
	decision, message, comment := decisionForApproval(root, spec, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if decision != "approved" {
		t.Fatalf("decision = %q, want %q", decision, "approved")
	}
	if message == "" {
		t.Fatal("message should not be empty")
	}
	if comment != "eval auto-approval" {
		t.Fatalf("comment = %q, want %q", comment, "eval auto-approval")
	}
}

func TestDecisionForApproval_DefaultsToApproved(t *testing.T) {
	root := t.TempDir()
	// No workflow files, no explicit config — should default to approved.
	decision, _, _ := decisionForApproval(root, &Spec{}, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if decision != "approved" {
		t.Fatalf("decision = %q, want %q", decision, "approved")
	}
}

func TestDecisionForApproval_ConfigWithoutDecision_UsesInference(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	rs := state.NewRunState("run-1", "test-wf", map[string]any{})
	rs.Nodes["build"] = &state.NodeState{
		Status: "completed", Decision: "ready_for_review", EndedAt: timePtr(now),
	}
	writeRunFixture(t, root, "run-1", reviewWorkflowYAML, rs)

	spec := &Spec{
		Approvals: map[string]ApprovalSpec{
			"review": {Message: "Custom msg", Comment: "Custom comment"},
		},
	}
	decision, message, comment := decisionForApproval(root, spec, &approvals.Approval{RunID: "run-1", NodeID: "review"})
	if decision != "approved" {
		t.Fatalf("decision = %q, want %q", decision, "approved")
	}
	if message != "Custom msg" {
		t.Fatalf("message = %q, want %q", message, "Custom msg")
	}
	if comment != "Custom comment" {
		t.Fatalf("comment = %q, want %q", comment, "Custom comment")
	}
}

// --- defaultApprovalMessage tests ---

func TestDefaultApprovalMessage_Approved(t *testing.T) {
	root := t.TempDir()
	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "approved")
	if got != "Approved." {
		t.Fatalf("got %q, want %q", got, "Approved.")
	}
}

func TestDefaultApprovalMessage_ChangesRequested(t *testing.T) {
	root := t.TempDir()
	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "changes_requested")
	if got != "Please revise the work based on feedback." {
		t.Fatalf("got %q, want %q", got, "Please revise the work based on feedback.")
	}
}

func TestDefaultApprovalMessage_ClarifiedWithContext(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "wf", map[string]any{"context": "Build a CLI tool"})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "clarified")
	if got != "Build a CLI tool" {
		t.Fatalf("got %q, want %q", got, "Build a CLI tool")
	}
}

func TestDefaultApprovalMessage_ClarifiedFallsBackToIdea(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "wf", map[string]any{"idea": "Make a todo app"})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "clarified")
	if got != "Make a todo app" {
		t.Fatalf("got %q, want %q", got, "Make a todo app")
	}
}

func TestDefaultApprovalMessage_ClarifiedNoInputs(t *testing.T) {
	root := t.TempDir()
	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "clarified")
	if got != "Clarified." {
		t.Fatalf("got %q, want %q", got, "Clarified.")
	}
}

func TestDefaultApprovalMessage_UnknownDecision(t *testing.T) {
	root := t.TempDir()
	got := defaultApprovalMessage(root, &approvals.Approval{RunID: "run-1"}, "custom_decision")
	want := "Auto-approved by eval (custom_decision)."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// --- saveResult tests ---

func TestSaveResult(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result := &Result{
		ID: "eval-1", Name: "Test Eval", RunID: "run-1", Status: "passed",
	}
	if err := saveResult(root, "run-1", result); err != nil {
		t.Fatalf("saveResult: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runDir, "eval.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded Result
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.ID != "eval-1" {
		t.Fatalf("ID = %q, want %q", loaded.ID, "eval-1")
	}
	if loaded.Status != "passed" {
		t.Fatalf("Status = %q, want %q", loaded.Status, "passed")
	}
}

func TestSaveResult_MissingRunDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	err := saveResult(root, "nonexistent", &Result{ID: "e1", Status: "passed"})
	if err == nil {
		t.Fatal("expected error when run dir doesn't exist")
	}
}

// --- loadRunInput tests ---

func TestLoadRunInput_Found(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "wf", map[string]any{"idea": "Build a CLI"})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	got := loadRunInput(root, "run-1", "idea")
	if got != "Build a CLI" {
		t.Fatalf("loadRunInput = %q, want %q", got, "Build a CLI")
	}
}

func TestLoadRunInput_NotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "wf", map[string]any{})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	got := loadRunInput(root, "run-1", "missing")
	if got != "" {
		t.Fatalf("loadRunInput = %q, want empty", got)
	}
}

func TestLoadRunInput_NonStringValue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rs := state.NewRunState("run-1", "wf", map[string]any{"count": 42})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	got := loadRunInput(root, "run-1", "count")
	if got != "" {
		t.Fatalf("loadRunInput = %q, want empty (non-string)", got)
	}
}

func TestLoadRunInput_MissingStateFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	got := loadRunInput(root, "nonexistent", "key")
	if got != "" {
		t.Fatalf("loadRunInput = %q, want empty", got)
	}
}

// --- helpers ---

// edge is a minimal edge descriptor for latestIncomingDecision tests.
type edge struct {
	from, to, when string
}

// buildWorkflow creates a minimal definitions.Workflow with just edges
// (latestIncomingDecision only reads workflow.Edges).
func buildWorkflow(edges []edge) *definitions.Workflow {
	wf := &definitions.Workflow{ID: "test", Name: "Test", Version: 1}
	for _, e := range edges {
		wf.Edges = append(wf.Edges, definitions.Edge{From: e.from, To: e.to, When: e.when})
	}
	return wf
}
