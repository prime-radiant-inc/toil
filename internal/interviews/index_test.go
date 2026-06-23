package interviews

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndReadRoleIndex(t *testing.T) {
	indexDir := t.TempDir()

	entries := []IndexEntry{
		{
			RunID:       "run-1",
			InterviewID: "run-1-write_code",
			NodeID:      "write_code",
			RoleID:      "write_code",
			WorkflowID:  "implement_task",
			Outcome:     "completed",
			Summary:     "Learned about error handling patterns",
			Timestamp:   time.Now().UTC().Truncate(time.Millisecond),
		},
		{
			RunID:       "run-2",
			InterviewID: "run-2-write_code",
			NodeID:      "write_code",
			RoleID:      "write_code",
			WorkflowID:  "fix_bug",
			Outcome:     "completed",
			Summary:     "Discussed debugging strategies",
			Timestamp:   time.Now().UTC().Truncate(time.Millisecond),
		},
	}

	for _, entry := range entries {
		if err := AppendRoleIndex(indexDir, entry); err != nil {
			t.Fatalf("AppendRoleIndex: %v", err)
		}
	}

	loaded, err := ReadRoleIndex(indexDir, "write_code")
	if err != nil {
		t.Fatalf("ReadRoleIndex: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}

	if loaded[0].RunID != "run-1" {
		t.Fatalf("entry[0].RunID: want %q, got %q", "run-1", loaded[0].RunID)
	}
	if loaded[1].RunID != "run-2" {
		t.Fatalf("entry[1].RunID: want %q, got %q", "run-2", loaded[1].RunID)
	}
	if loaded[0].Summary != "Learned about error handling patterns" {
		t.Fatalf("entry[0].Summary: want %q, got %q", "Learned about error handling patterns", loaded[0].Summary)
	}
}

func TestAppendAndReadWorkflowIndex(t *testing.T) {
	indexDir := t.TempDir()

	entry := IndexEntry{
		RunID:       "run-1",
		InterviewID: "run-1-write_tests",
		NodeID:      "write_tests",
		RoleID:      "write_tests",
		WorkflowID:  "implement_task",
		Outcome:     "completed",
		Summary:     "Testing insights",
		Timestamp:   time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := AppendWorkflowIndex(indexDir, entry); err != nil {
		t.Fatalf("AppendWorkflowIndex: %v", err)
	}

	loaded, err := ReadWorkflowIndex(indexDir, "implement_task")
	if err != nil {
		t.Fatalf("ReadWorkflowIndex: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	if loaded[0].WorkflowID != "implement_task" {
		t.Fatalf("WorkflowID: want %q, got %q", "implement_task", loaded[0].WorkflowID)
	}
}

func TestReadRoleIndex_Empty(t *testing.T) {
	indexDir := t.TempDir()
	entries, err := ReadRoleIndex(indexDir, "nonexistent_role")
	if err != nil {
		t.Fatalf("ReadRoleIndex: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestReadWorkflowIndex_Empty(t *testing.T) {
	indexDir := t.TempDir()
	entries, err := ReadWorkflowIndex(indexDir, "nonexistent_workflow")
	if err != nil {
		t.Fatalf("ReadWorkflowIndex: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestReadRoleIndex_CorruptLine(t *testing.T) {
	indexDir := t.TempDir()
	dir := filepath.Join(indexDir, "interviews", "by_role")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "{\"run_id\":\"run-1\",\"interview_id\":\"id-1\",\"node_id\":\"n\",\"role_id\":\"bad_role\",\"workflow_id\":\"w\",\"outcome\":\"ok\",\"summary\":\"s\",\"timestamp\":\"2025-01-01T00:00:00Z\"}\n{corrupt json\n{\"run_id\":\"run-2\",\"interview_id\":\"id-2\",\"node_id\":\"n\",\"role_id\":\"bad_role\",\"workflow_id\":\"w\",\"outcome\":\"ok\",\"summary\":\"s2\",\"timestamp\":\"2025-01-01T00:00:00Z\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "bad_role.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should skip corrupt lines and return the valid ones
	entries, err := ReadRoleIndex(indexDir, "bad_role")
	if err != nil {
		t.Fatalf("ReadRoleIndex: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (skipping corrupt line), got %d", len(entries))
	}
}

func TestReadWorkflowIndex_CorruptLine(t *testing.T) {
	indexDir := t.TempDir()
	dir := filepath.Join(indexDir, "interviews", "by_workflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "{corrupt\n{\"run_id\":\"run-1\",\"interview_id\":\"id-1\",\"node_id\":\"n\",\"role_id\":\"r\",\"workflow_id\":\"bad_wf\",\"outcome\":\"ok\",\"summary\":\"s\",\"timestamp\":\"2025-01-01T00:00:00Z\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "bad_wf.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadWorkflowIndex(indexDir, "bad_wf")
	if err != nil {
		t.Fatalf("ReadWorkflowIndex: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 valid entry, got %d", len(entries))
	}
}

func TestMultipleRolesInIndex(t *testing.T) {
	indexDir := t.TempDir()

	roles := []string{"write_code", "write_tests", "debugger"}
	for _, role := range roles {
		entry := IndexEntry{
			RunID:       "run-1",
			InterviewID: "run-1-" + role,
			NodeID:      role,
			RoleID:      role,
			WorkflowID:  "workflow-1",
			Outcome:     "completed",
			Summary:     "Summary for " + role,
			Timestamp:   time.Now().UTC(),
		}
		if err := AppendRoleIndex(indexDir, entry); err != nil {
			t.Fatalf("AppendRoleIndex(%s): %v", role, err)
		}
	}

	for _, role := range roles {
		entries, err := ReadRoleIndex(indexDir, role)
		if err != nil {
			t.Fatalf("ReadRoleIndex(%s): %v", role, err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry for %s, got %d", role, len(entries))
		}
		if entries[0].RoleID != role {
			t.Fatalf("entry.RoleID: want %q, got %q", role, entries[0].RoleID)
		}
	}
}

func TestAppendRoleIndex_CreatesDirectories(t *testing.T) {
	indexDir := t.TempDir()
	entry := IndexEntry{
		RunID:       "run-1",
		InterviewID: "id-1",
		NodeID:      "node",
		RoleID:      "new_role",
		WorkflowID:  "wf",
		Outcome:     "completed",
		Timestamp:   time.Now().UTC(),
	}
	if err := AppendRoleIndex(indexDir, entry); err != nil {
		t.Fatalf("AppendRoleIndex: %v", err)
	}

	// Verify directory was created
	dir := filepath.Join(indexDir, "interviews", "by_role")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected directory %q to exist: %v", dir, err)
	}
}

func TestAppendWorkflowIndex_CreatesDirectories(t *testing.T) {
	indexDir := t.TempDir()
	entry := IndexEntry{
		RunID:       "run-1",
		InterviewID: "id-1",
		NodeID:      "node",
		RoleID:      "role",
		WorkflowID:  "new_workflow",
		Outcome:     "completed",
		Timestamp:   time.Now().UTC(),
	}
	if err := AppendWorkflowIndex(indexDir, entry); err != nil {
		t.Fatalf("AppendWorkflowIndex: %v", err)
	}

	dir := filepath.Join(indexDir, "interviews", "by_workflow")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected directory %q to exist: %v", dir, err)
	}
}
