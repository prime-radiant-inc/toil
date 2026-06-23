package interviews

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// IndexEntry is a lightweight reference stored in cross-run JSONL indices.
type IndexEntry struct {
	RunID       string    `json:"run_id"`
	InterviewID string    `json:"interview_id"`
	NodeID      string    `json:"node_id"`
	RoleID      string    `json:"role_id"`
	WorkflowID  string    `json:"workflow_id"`
	Outcome     string    `json:"outcome"`
	Summary     string    `json:"summary"`
	Timestamp   time.Time `json:"timestamp"`
}

// AppendRoleIndex appends an entry to the by-role JSONL index for the entry's role.
func AppendRoleIndex(indexDir string, entry IndexEntry) error {
	dir := filepath.Join(indexDir, "interviews", "by_role")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return appendJSONL(filepath.Join(dir, entry.RoleID+".jsonl"), entry)
}

// AppendWorkflowIndex appends an entry to the by-workflow JSONL index for the entry's workflow.
func AppendWorkflowIndex(indexDir string, entry IndexEntry) error {
	dir := filepath.Join(indexDir, "interviews", "by_workflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return appendJSONL(filepath.Join(dir, entry.WorkflowID+".jsonl"), entry)
}

// ReadRoleIndex reads all entries from a role's JSONL index, skipping corrupt lines.
func ReadRoleIndex(indexDir, roleID string) ([]IndexEntry, error) {
	path := filepath.Join(indexDir, "interviews", "by_role", roleID+".jsonl")
	return readJSONL(path)
}

// ReadWorkflowIndex reads all entries from a workflow's JSONL index, skipping corrupt lines.
func ReadWorkflowIndex(indexDir, workflowID string) ([]IndexEntry, error) {
	path := filepath.Join(indexDir, "interviews", "by_workflow", workflowID+".jsonl")
	return readJSONL(path)
}

func appendJSONL(path string, entry IndexEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

func readJSONL(path string) ([]IndexEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []IndexEntry{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []IndexEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry IndexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip corrupt lines
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
