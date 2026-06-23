package approvals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_UpdatesApproval(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "res-1", RunID: "run-1", NodeID: "review"}},
	})

	resolved, err := Resolve(root, "res-1", ResolveInput{
		Decision: "approved",
		Message:  "Looks good",
		Comment:  "LGTM",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Status != "resolved" {
		t.Fatalf("Status = %q, want %q", resolved.Status, "resolved")
	}
	if resolved.Decision != "approved" {
		t.Fatalf("Decision = %q, want %q", resolved.Decision, "approved")
	}
	if resolved.Message != "Looks good" {
		t.Fatalf("Message = %q, want %q", resolved.Message, "Looks good")
	}
	if resolved.Comment != "LGTM" {
		t.Fatalf("Comment = %q, want %q", resolved.Comment, "LGTM")
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("ResolvedAt should be set")
	}
}

func TestResolve_PersistsToDisk(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "res-2", RunID: "run-1", NodeID: "review"}},
	})

	_, err := Resolve(root, "res-2", ResolveInput{
		Decision: "rejected",
		Message:  "Needs work",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Re-load from disk and verify the resolution persisted.
	runDir := filepath.Join(root, "runs", "run-1")
	loaded, err := Load(runDir, "res-2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Status != "resolved" {
		t.Fatalf("Status = %q, want %q", loaded.Status, "resolved")
	}
	if loaded.Decision != "rejected" {
		t.Fatalf("Decision = %q, want %q", loaded.Decision, "rejected")
	}
}

func TestResolve_AppendsEvent(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "res-3", RunID: "run-1", NodeID: "review"}},
	})

	_, err := Resolve(root, "res-3", ResolveInput{
		Decision: "approved",
		Message:  "OK",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Check events.jsonl was created and contains an approval_resolved event.
	eventsPath := filepath.Join(root, "runs", "run-1", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile events.jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatal("events.jsonl is empty")
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if event["type"] != "approval_resolved" {
		t.Fatalf("event type = %q, want %q", event["type"], "approval_resolved")
	}
	if event["run_id"] != "run-1" {
		t.Fatalf("event run_id = %q, want %q", event["run_id"], "run-1")
	}
	if event["node_id"] != "review" {
		t.Fatalf("event node_id = %q, want %q", event["node_id"], "review")
	}
}

func TestResolve_NotFound(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "exists", RunID: "run-1", NodeID: "n1"}},
	})

	_, err := Resolve(root, "nonexistent", ResolveInput{Decision: "approved"})
	if err == nil {
		t.Fatal("expected error for missing approval")
	}
}
