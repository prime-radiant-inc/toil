package interviews

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildID(t *testing.T) {
	id := BuildID("run-abc", "write_code")
	if id != "interview-run-abc-write_code" {
		t.Fatalf("expected %q, got %q", "interview-run-abc-write_code", id)
	}
}

func TestCreateAndLoad(t *testing.T) {
	runDir := t.TempDir()

	now := time.Now().UTC().Truncate(time.Millisecond)
	iv := &Interview{
		ID:                BuildID("run-1", "write_code"),
		RunID:             "run-1",
		NodeID:            "write_code",
		RoleID:            "write_code",
		WorkflowID:        "implement_task",
		OriginalSessionID: "sess-123",
		OriginalOutcome:   "success",
		OriginalAttempts:  2,
		StartedAt:         &now,
	}

	if err := Create(runDir, iv); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Status should default to pending
	if iv.Status != StatusPending {
		t.Fatalf("expected status %q after Create, got %q", StatusPending, iv.Status)
	}

	loaded, err := Load(runDir, "write_code")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != iv.ID {
		t.Fatalf("ID mismatch: want %q, got %q", iv.ID, loaded.ID)
	}
	if loaded.RunID != iv.RunID {
		t.Fatalf("RunID mismatch: want %q, got %q", iv.RunID, loaded.RunID)
	}
	if loaded.NodeID != iv.NodeID {
		t.Fatalf("NodeID mismatch: want %q, got %q", iv.NodeID, loaded.NodeID)
	}
	if loaded.RoleID != iv.RoleID {
		t.Fatalf("RoleID mismatch: want %q, got %q", iv.RoleID, loaded.RoleID)
	}
	if loaded.WorkflowID != iv.WorkflowID {
		t.Fatalf("WorkflowID mismatch: want %q, got %q", iv.WorkflowID, loaded.WorkflowID)
	}
	if loaded.OriginalSessionID != iv.OriginalSessionID {
		t.Fatalf("OriginalSessionID mismatch: want %q, got %q", iv.OriginalSessionID, loaded.OriginalSessionID)
	}
	if loaded.Status != StatusPending {
		t.Fatalf("Status mismatch: want %q, got %q", StatusPending, loaded.Status)
	}
	if loaded.OriginalAttempts != 2 {
		t.Fatalf("OriginalAttempts: want 2, got %d", loaded.OriginalAttempts)
	}
}

func TestCreate_RequiresID(t *testing.T) {
	runDir := t.TempDir()
	iv := &Interview{}
	err := Create(runDir, iv)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestCreate_DefaultsStatus(t *testing.T) {
	runDir := t.TempDir()
	iv := &Interview{
		ID:     "test-1",
		NodeID: "node1",
	}
	if err := Create(runDir, iv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iv.Status != StatusPending {
		t.Fatalf("expected status %q, got %q", StatusPending, iv.Status)
	}
}

func TestSave_UpdatesStatus(t *testing.T) {
	runDir := t.TempDir()
	iv := &Interview{
		ID:     "test-save",
		NodeID: "node1",
	}
	if err := Create(runDir, iv); err != nil {
		t.Fatalf("Create: %v", err)
	}

	iv.Status = StatusInProgress
	iv.InterviewSessionID = "sess-456"
	iv.Responses = map[string]any{
		"q1": "answer1",
		"q2": 42,
	}

	if err := Save(runDir, iv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(runDir, "node1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Status != StatusInProgress {
		t.Fatalf("expected status %q, got %q", StatusInProgress, loaded.Status)
	}
	if loaded.InterviewSessionID != "sess-456" {
		t.Fatalf("InterviewSessionID: want %q, got %q", "sess-456", loaded.InterviewSessionID)
	}
	if loaded.Responses["q1"] != "answer1" {
		t.Fatalf("Responses[q1]: want %q, got %v", "answer1", loaded.Responses["q1"])
	}
}

func TestSave_RequiresID(t *testing.T) {
	runDir := t.TempDir()
	iv := &Interview{}
	err := Save(runDir, iv)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestLoad_ForEachExpansion(t *testing.T) {
	runDir := t.TempDir()
	iv := &Interview{
		ID:     "run-1-write_code::0",
		NodeID: "write_code::0",
		RoleID: "write_code",
	}
	if err := Create(runDir, iv); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := Load(runDir, "write_code::0")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.NodeID != "write_code::0" {
		t.Fatalf("NodeID: want %q, got %q", "write_code::0", loaded.NodeID)
	}
}

func TestListForRun(t *testing.T) {
	runDir := t.TempDir()

	nodes := []string{"write_code", "write_tests", "debugger"}
	for _, node := range nodes {
		iv := &Interview{
			ID:     BuildID("run-1", node),
			NodeID: node,
			RoleID: node,
		}
		if err := Create(runDir, iv); err != nil {
			t.Fatalf("Create %s: %v", node, err)
		}
	}

	list, err := ListForRun(runDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 interviews, got %d", len(list))
	}

	// Verify all node IDs are present
	found := map[string]bool{}
	for _, iv := range list {
		found[iv.NodeID] = true
	}
	for _, node := range nodes {
		if !found[node] {
			t.Fatalf("missing interview for node %q", node)
		}
	}
}

func TestListForRun_Empty(t *testing.T) {
	runDir := t.TempDir()
	list, err := ListForRun(runDir)
	if err != nil {
		t.Fatalf("ListForRun: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interviews, got %d", len(list))
	}
}

func TestListForRun_MissingDir(t *testing.T) {
	list, err := ListForRun("/nonexistent/run/dir")
	if err != nil {
		t.Fatalf("ListForRun should not error on missing dir: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 interviews, got %d", len(list))
	}
}

func TestLoad_NotFound(t *testing.T) {
	runDir := t.TempDir()
	_, err := Load(runDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent interview")
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	runDir := t.TempDir()
	dir := filepath.Join(runDir, "interviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(runDir, "bad")
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestInterviewPath(t *testing.T) {
	path := InterviewPath("/tmp/runs/run-1", "write_code")
	want := filepath.Join("/tmp/runs/run-1", "interviews", "write_code.json")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestInterviewPath_ForEach(t *testing.T) {
	path := InterviewPath("/tmp/runs/run-1", "write_code::0")
	want := filepath.Join("/tmp/runs/run-1", "interviews", "write_code::0.json")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &Interview{
		ID:                 "roundtrip-1",
		RunID:              "run-1",
		NodeID:             "write_code",
		RoleID:             "write_code",
		WorkflowID:         "implement_task",
		OriginalSessionID:  "sess-orig",
		InterviewSessionID: "sess-interview",
		Status:             StatusCompleted,
		OriginalOutcome:    "success",
		OriginalAttempts:   3,
		StartedAt:          &now,
		CompletedAt:        &now,
		Responses:          map[string]any{"key": "value"},
		Error:              "",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Interview
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Fatalf("ID: want %q, got %q", original.ID, decoded.ID)
	}
	if decoded.Status != original.Status {
		t.Fatalf("Status: want %q, got %q", original.Status, decoded.Status)
	}
	if decoded.OriginalAttempts != original.OriginalAttempts {
		t.Fatalf("OriginalAttempts: want %d, got %d", original.OriginalAttempts, decoded.OriginalAttempts)
	}
}

func TestJSONOmitEmpty(t *testing.T) {
	iv := &Interview{
		ID:     "omit-1",
		Status: StatusPending,
	}

	data, err := json.Marshal(iv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"original_session_id", "interview_session_id", "original_outcome", "started_at", "completed_at", "responses", "error"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("expected %q to be omitted from JSON when empty/zero", key)
		}
	}
}

func TestStatusConstants(t *testing.T) {
	statuses := []string{StatusPending, StatusInProgress, StatusCompleted, StatusFailed, StatusSkipped, StatusDegraded}
	expected := []string{"pending", "in_progress", "completed", "failed", "skipped", "degraded"}

	if len(statuses) != len(expected) {
		t.Fatalf("expected %d status constants, got %d", len(expected), len(statuses))
	}
	for i, want := range expected {
		if statuses[i] != want {
			t.Fatalf("status[%d]: want %q, got %q", i, want, statuses[i])
		}
	}
}
