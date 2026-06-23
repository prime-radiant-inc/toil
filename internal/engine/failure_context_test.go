package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/state"
)

func TestBuildFailureContext_BasicNode(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("write_code", func(n *state.NodeState) {
		n.Status = "failed"
		n.SessionID = "sess-123"
		n.Decision = "code_written"
		n.Message = "timeout after 120s"
		n.Attempts = 3
		n.Error = "exec timeout"
	})

	ctx := buildFailureContext(rs, "write_code", t.TempDir())

	if ctx["node_id"] != "write_code" {
		t.Errorf("node_id = %q", ctx["node_id"])
	}
	if ctx["session_id"] != "sess-123" {
		t.Errorf("session_id = %q", ctx["session_id"])
	}
	if ctx["last_decision"] != "code_written" {
		t.Errorf("last_decision = %q", ctx["last_decision"])
	}
	if ctx["last_message"] != "timeout after 120s" {
		t.Errorf("last_message = %q", ctx["last_message"])
	}
	if ctx["attempts"] != 3 {
		t.Errorf("attempts = %v", ctx["attempts"])
	}
	if ctx["error"] != "exec timeout" {
		t.Errorf("error = %q, want %q", ctx["error"], "exec timeout")
	}
}

// Regression test for PRI-1570: a pre-dispatch failure (e.g. unresolved
// input expression) writes NodeState.Error before any execution begins;
// failure_context.error must surface that text so debuggers can find the
// root cause without traversing engine source.
func TestBuildFailureContext_PreDispatchFailure(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("implement::0", func(n *state.NodeState) {
		n.Status = "failed"
		n.Error = "resolve inputs: unknown node field: session_id"
	})

	ctx := buildFailureContext(rs, "implement::0", t.TempDir())

	if got, ok := ctx["error"].(string); !ok || got == "" {
		t.Fatalf("expected non-empty error string, got %v (%T)", ctx["error"], ctx["error"])
	} else if !strings.Contains(got, "unknown node field") {
		t.Errorf("error = %q, want it to mention the resolver failure", got)
	}
}

func TestBuildFailureContext_SubworkflowWithChildRun(t *testing.T) {
	runsDir := t.TempDir()

	// Create a child run with decision history
	childRunID := "child-run-1"
	childRunDir := filepath.Join(runsDir, childRunID)
	if err := os.MkdirAll(childRunDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write child run events
	events := []state.Event{
		{Type: "node_completed", NodeID: "reviewer", Data: map[string]any{
			"decision": "changes_requested", "message": "CSS issue",
		}},
		{Type: "node_completed", NodeID: "engineer", Data: map[string]any{
			"decision": "tests_passing", "message": "Fixed",
		}},
		{Type: "node_completed", NodeID: "reviewer", Data: map[string]any{
			"decision": "changes_requested", "message": "CSS issue again",
		}},
	}
	eventsPath := filepath.Join(childRunDir, "events.jsonl")
	f, _ := os.Create(eventsPath)
	enc := json.NewEncoder(f)
	for _, e := range events {
		_ = enc.Encode(e)
	}
	_ = f.Close()

	// Write child run state with a failed node
	childRS := state.NewRunState(childRunID, "implement_task", nil)
	childRS.WithNode("reviewer", func(n *state.NodeState) {
		n.Status = "failed"
		n.Message = "loop exhausted"
		n.Error = "max iterations"
		n.SessionID = "child-sess-456"
	})
	childRS.WithNode("engineer", func(n *state.NodeState) {
		n.Status = "completed"
	})
	if err := state.SaveState(filepath.Join(childRunDir, "state.json"), childRS); err != nil {
		t.Fatal(err)
	}

	// Parent run state with child_run reference
	rs := state.NewRunState("run-1", "build_component", nil)
	rs.WithNode("implement_tasks", func(n *state.NodeState) {
		n.Status = "failed"
		n.Message = "child failed"
		n.Data = map[string]any{"child_run": childRunID}
	})

	ctx := buildFailureContext(rs, "implement_tasks", runsDir)

	if ctx["child_run"] != childRunID {
		t.Errorf("child_run = %v", ctx["child_run"])
	}

	history, ok := ctx["decision_history"].([]map[string]string)
	if !ok {
		t.Fatalf("decision_history type = %T", ctx["decision_history"])
	}
	if len(history) != 3 {
		t.Fatalf("decision_history len = %d, want 3", len(history))
	}
	if history[0]["node"] != "reviewer" || history[0]["decision"] != "changes_requested" {
		t.Errorf("history[0] = %v", history[0])
	}
	if history[1]["node"] != "engineer" || history[1]["decision"] != "tests_passing" {
		t.Errorf("history[1] = %v", history[1])
	}

	child, ok := ctx["failed_child"].(map[string]string)
	if !ok {
		t.Fatalf("failed_child type = %T", ctx["failed_child"])
	}
	if child["node_id"] != "reviewer" {
		t.Errorf("failed_child node_id = %q", child["node_id"])
	}
	if child["session_id"] != "child-sess-456" {
		t.Errorf("failed_child session_id = %q", child["session_id"])
	}
}

func TestBuildFailureContext_NoSessionID(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	rs.WithNode("shell_node", func(n *state.NodeState) {
		n.Status = "failed"
		n.Message = "exit code 1"
	})

	ctx := buildFailureContext(rs, "shell_node", t.TempDir())

	if ctx["session_id"] != "" {
		t.Errorf("expected empty session_id, got %q", ctx["session_id"])
	}
	if ctx["last_message"] != "exit code 1" {
		t.Errorf("last_message = %q", ctx["last_message"])
	}
}

func TestExtractDecisionHistory_MultipleDecisions(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	events := []state.Event{
		{Type: "node_started", NodeID: "a"},
		{Type: "node_completed", NodeID: "a", Data: map[string]any{
			"decision": "approved", "message": "looks good",
		}},
		{Type: "node_completed", NodeID: "b", Data: map[string]any{
			"decision": "rejected", "message": "bad code",
		}},
		{Type: "node_completed", NodeID: "a", Data: map[string]any{
			"decision": "approved", "message": "fixed now",
		}},
		{Type: "wave_completed", NodeID: ""},
	}
	f, _ := os.Create(filepath.Join(runDir, "events.jsonl"))
	enc := json.NewEncoder(f)
	for _, e := range events {
		_ = enc.Encode(e)
	}
	_ = f.Close()

	history := extractDecisionHistory(dir, runID)

	if len(history) != 3 {
		t.Fatalf("len = %d, want 3", len(history))
	}
	if history[0]["node"] != "a" || history[0]["decision"] != "approved" {
		t.Errorf("[0] = %v", history[0])
	}
	if history[1]["node"] != "b" || history[1]["decision"] != "rejected" {
		t.Errorf("[1] = %v", history[1])
	}
	if history[2]["decision"] != "approved" || history[2]["message"] != "fixed now" {
		t.Errorf("[2] = %v", history[2])
	}
}

func TestExtractDecisionHistory_EmptyEvents(t *testing.T) {
	history := extractDecisionHistory(t.TempDir(), "nonexistent")
	if history != nil {
		t.Fatalf("expected nil, got %v", history)
	}
}

func TestExtractDecisionHistory_NoCompletedEvents(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := []state.Event{
		{Type: "node_started", NodeID: "a"},
		{Type: "wave_started"},
	}
	f, _ := os.Create(filepath.Join(runDir, "events.jsonl"))
	enc := json.NewEncoder(f)
	for _, e := range events {
		_ = enc.Encode(e)
	}
	_ = f.Close()

	history := extractDecisionHistory(dir, "run-1")
	if len(history) != 0 {
		t.Fatalf("expected empty, got %v", history)
	}
}

func TestExtractFailedChild_FindsFailedNode(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "child-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("child-1", "implement_task", nil)
	rs.WithNode("write_code", func(n *state.NodeState) {
		n.Status = "completed"
		n.Decision = "code_written"
	})
	rs.WithNode("review_code_quality", func(n *state.NodeState) {
		n.Status = "failed"
		n.Message = "loop exhausted"
		n.Error = "max iterations exceeded"
		n.SessionID = "sess-qr"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	result := extractFailedChild(dir, "child-1")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["node_id"] != "review_code_quality" {
		t.Errorf("node_id = %q", result["node_id"])
	}
	if result["session_id"] != "sess-qr" {
		t.Errorf("session_id = %q", result["session_id"])
	}
	if result["error"] != "max iterations exceeded" {
		t.Errorf("error = %q", result["error"])
	}
}

func TestExtractFailedChild_NoFailedNodes(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "child-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rs := state.NewRunState("child-1", "wf", nil)
	rs.WithNode("a", func(n *state.NodeState) {
		n.Status = "completed"
	})
	if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
		t.Fatal(err)
	}

	result := extractFailedChild(dir, "child-1")
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}
