package approvals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApprovalPath(t *testing.T) {
	got := ApprovalPath("/tmp/runs/run-1", "approval-abc")
	want := "/tmp/runs/run-1/approvals/approval-abc.json"
	if got != want {
		t.Fatalf("ApprovalPath = %q, want %q", got, want)
	}
}

func TestBuildID(t *testing.T) {
	tests := []struct {
		runID, nodeID string
		attempt       int
		want          string
	}{
		{"run-1", "review", 0, "run-1-review-0"},
		{"run-1", "review", 1, "run-1-review-1"},
		{"abc", "node-with-dashes", 42, "abc-node-with-dashes-42"},
	}
	for _, tt := range tests {
		got := BuildID(tt.runID, tt.nodeID, tt.attempt)
		if got != tt.want {
			t.Errorf("BuildID(%q, %q, %d) = %q, want %q", tt.runID, tt.nodeID, tt.attempt, got, tt.want)
		}
	}
}

func TestCreate_SetsDefaults(t *testing.T) {
	runDir := t.TempDir()
	before := time.Now().UTC()

	approval := &Approval{ID: "test-1", RunID: "run-1", NodeID: "review"}
	if err := Create(runDir, approval); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if approval.Status != "pending" {
		t.Fatalf("Status = %q, want %q", approval.Status, "pending")
	}
	if approval.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt %v is before test start %v", approval.CreatedAt, before)
	}

	// Verify file was written.
	path := ApprovalPath(runDir, "test-1")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestCreate_PreservesExplicitValues(t *testing.T) {
	runDir := t.TempDir()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	approval := &Approval{
		ID:        "test-2",
		RunID:     "run-1",
		NodeID:    "review",
		Status:    "resolved",
		CreatedAt: ts,
	}
	if err := Create(runDir, approval); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if approval.Status != "resolved" {
		t.Fatalf("Status = %q, want %q (should preserve explicit value)", approval.Status, "resolved")
	}
	if !approval.CreatedAt.Equal(ts) {
		t.Fatalf("CreatedAt = %v, want %v (should preserve explicit value)", approval.CreatedAt, ts)
	}
}

func TestCreate_RequiresID(t *testing.T) {
	runDir := t.TempDir()
	err := Create(runDir, &Approval{})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	runDir := t.TempDir()
	original := &Approval{
		ID:       "load-1",
		RunID:    "run-1",
		NodeID:   "review",
		Question: "Approve?",
		Choices:  []string{"approved", "rejected"},
		Default:  "approved",
	}
	if err := Create(runDir, original); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := Load(runDir, "load-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != original.ID {
		t.Fatalf("ID = %q, want %q", loaded.ID, original.ID)
	}
	if loaded.RunID != original.RunID {
		t.Fatalf("RunID = %q, want %q", loaded.RunID, original.RunID)
	}
	if loaded.Question != original.Question {
		t.Fatalf("Question = %q, want %q", loaded.Question, original.Question)
	}
	if len(loaded.Choices) != len(original.Choices) {
		t.Fatalf("Choices length = %d, want %d", len(loaded.Choices), len(original.Choices))
	}
	if loaded.Default != original.Default {
		t.Fatalf("Default = %q, want %q", loaded.Default, original.Default)
	}
	if loaded.Status != "pending" {
		t.Fatalf("Status = %q, want %q", loaded.Status, "pending")
	}
}

func TestLoad_NotFound(t *testing.T) {
	runDir := t.TempDir()
	_, err := Load(runDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing approval")
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	runDir := t.TempDir()
	approvalDir := filepath.Join(runDir, "approvals")
	if err := os.MkdirAll(approvalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(approvalDir, "bad.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(runDir, "bad")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestSave_RequiresID(t *testing.T) {
	runDir := t.TempDir()
	err := Save(runDir, &Approval{})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestSave_SetsDefaultStatus(t *testing.T) {
	runDir := t.TempDir()
	// Create approvals dir so Save can write.
	if err := os.MkdirAll(filepath.Join(runDir, "approvals"), 0o755); err != nil {
		t.Fatal(err)
	}

	approval := &Approval{ID: "save-1"}
	if err := Save(runDir, approval); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if approval.Status != "pending" {
		t.Fatalf("Status = %q, want %q", approval.Status, "pending")
	}
}

func TestSave_Overwrites(t *testing.T) {
	runDir := t.TempDir()
	approval := &Approval{ID: "save-2", RunID: "run-1", NodeID: "review", Message: "first"}
	if err := Create(runDir, approval); err != nil {
		t.Fatalf("Create: %v", err)
	}

	approval.Message = "updated"
	if err := Save(runDir, approval); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(runDir, "save-2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Message != "updated" {
		t.Fatalf("Message = %q, want %q", loaded.Message, "updated")
	}
}

// setupRuns creates a root directory with the standard runs/ layout and
// populates it with approvals. Returns the root path.
func setupRuns(t *testing.T, approvalsByRun map[string][]*Approval) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	for runID, approvals := range approvalsByRun {
		runDir := filepath.Join(root, "runs", runID)
		for _, a := range approvals {
			if err := Create(runDir, a); err != nil {
				t.Fatalf("Create(%s/%s): %v", runID, a.ID, err)
			}
		}
	}
	return root
}

func TestListAll_MultipleRunsMultipleApprovals(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {
			{ID: "a1", RunID: "run-1", NodeID: "n1"},
			{ID: "a2", RunID: "run-1", NodeID: "n2"},
		},
		"run-2": {
			{ID: "a3", RunID: "run-2", NodeID: "n1"},
		},
	})

	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListAll returned %d approvals, want 3", len(got))
	}

	ids := map[string]bool{}
	for _, a := range got {
		ids[a.ID] = true
	}
	for _, want := range []string{"a1", "a2", "a3"} {
		if !ids[want] {
			t.Errorf("missing approval %q", want)
		}
	}
}

func TestListAll_NoRunsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	// No runs/ directory at all.
	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

func TestListAll_RunWithNoApprovals(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	// Create a run directory with no approvals/ subdirectory.
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

func TestListAll_SkipsNonJsonFiles(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "a1", RunID: "run-1", NodeID: "n1"}},
	})
	// Drop a non-JSON file in the approvals directory.
	approvalDir := filepath.Join(root, "runs", "run-1", "approvals")
	if err := os.WriteFile(filepath.Join(approvalDir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(got))
	}
}

func TestListAll_SkipsSubdirectories(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "a1", RunID: "run-1", NodeID: "n1"}},
	})
	// Create a subdirectory in approvals/.
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-1", "approvals", "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(got))
	}
}

func TestListAll_SkipsNonDirEntriesInRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runsDir := filepath.Join(root, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a plain file (not a directory) in runs/.
	if err := os.WriteFile(filepath.Join(runsDir, "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

func TestFind_LocatesAcrossRuns(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "target", RunID: "run-1", NodeID: "n1"}},
		"run-2": {{ID: "other", RunID: "run-2", NodeID: "n1"}},
	})

	approval, runDir, err := Find(root, "target")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if approval.ID != "target" {
		t.Fatalf("ID = %q, want %q", approval.ID, "target")
	}
	if approval.RunID != "run-1" {
		t.Fatalf("RunID = %q, want %q", approval.RunID, "run-1")
	}
	wantRunDir := filepath.Join(root, "runs", "run-1")
	if runDir != wantRunDir {
		t.Fatalf("runDir = %q, want %q", runDir, wantRunDir)
	}
}

func TestFind_NotFound(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "exists", RunID: "run-1", NodeID: "n1"}},
	})

	_, _, err := Find(root, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing approval")
	}
}

func TestFind_NoRunsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	_, _, err := Find(root, "any")
	if err == nil {
		t.Fatal("expected error when runs/ doesn't exist")
	}
}

func TestFind_MalformedJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOIL_RUNS_DIR", filepath.Join(root, "runs"))
	runDir := filepath.Join(root, "runs", "run-1")
	approvalDir := filepath.Join(runDir, "approvals")
	if err := os.MkdirAll(approvalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(approvalDir, "bad-approval.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Find(root, "bad-approval")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestListAll_MalformedApproval(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "good", RunID: "run-1", NodeID: "n1"}},
	})
	// Inject a malformed approval file.
	approvalDir := filepath.Join(root, "runs", "run-1", "approvals")
	if err := os.WriteFile(filepath.Join(approvalDir, "bad.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ListAll(root)
	if err == nil {
		t.Fatal("expected error for malformed approval JSON in ListAll")
	}
}

func TestCreate_MkdirAllError(t *testing.T) {
	// Place a file where the approvals directory would go, so MkdirAll fails.
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "approvals"), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Create(runDir, &Approval{ID: "test"})
	if err == nil {
		t.Fatal("expected error when approvals path is blocked by a file")
	}
}

func TestSave_WriteError(t *testing.T) {
	runDir := t.TempDir()
	approvalDir := filepath.Join(runDir, "approvals")
	if err := os.MkdirAll(approvalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Make approvals dir read-only so write fails.
	if err := os.Chmod(approvalDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(approvalDir, 0o755); err != nil {
			t.Logf("cleanup chmod: %v", err)
		}
	})

	err := Save(runDir, &Approval{ID: "test"})
	if err == nil {
		t.Fatal("expected error writing to read-only directory")
	}
}

func TestListAll_ReadDirPermissionError(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "a1", RunID: "run-1", NodeID: "n1"}},
	})
	// Make the approvals dir unreadable.
	approvalDir := filepath.Join(root, "runs", "run-1", "approvals")
	if err := os.Chmod(approvalDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(approvalDir, 0o755); err != nil {
			t.Logf("cleanup chmod: %v", err)
		}
	})

	_, err := ListAll(root)
	if err == nil {
		t.Fatal("expected permission error from ListAll")
	}
}

func TestFind_ReadFilePermissionError(t *testing.T) {
	root := setupRuns(t, map[string][]*Approval{
		"run-1": {{ID: "perm-test", RunID: "run-1", NodeID: "n1"}},
	})
	// Make the approval file unreadable.
	path := filepath.Join(root, "runs", "run-1", "approvals", "perm-test.json")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Logf("cleanup chmod: %v", err)
		}
	})

	_, _, err := Find(root, "perm-test")
	if err == nil {
		t.Fatal("expected permission error from Find")
	}
}

func TestCreate_WritesValidJSON(t *testing.T) {
	runDir := t.TempDir()
	approval := &Approval{
		ID:       "json-1",
		RunID:    "run-1",
		NodeID:   "review",
		Question: "Approve this?",
		Choices:  []string{"approved", "rejected"},
	}
	if err := Create(runDir, approval); err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := os.ReadFile(ApprovalPath(runDir, "json-1"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Must be valid, pretty-printed JSON.
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}

	// Verify we can round-trip it.
	var loaded Approval
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.Question != "Approve this?" {
		t.Fatalf("Question = %q, want %q", loaded.Question, "Approve this?")
	}
}
