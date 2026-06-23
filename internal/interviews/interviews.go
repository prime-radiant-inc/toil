package interviews

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
	StatusDegraded   = "degraded"
)

type Interview struct {
	ID                 string         `json:"id"`
	RunID              string         `json:"run_id"`
	NodeID             string         `json:"node_id"`
	RoleID             string         `json:"role_id"`
	WorkflowID         string         `json:"workflow_id"`
	OriginalSessionID  string         `json:"original_session_id,omitempty"`
	InterviewSessionID string         `json:"interview_session_id,omitempty"`
	Status             string         `json:"status"`
	OriginalOutcome    string         `json:"original_outcome,omitempty"`
	OriginalAttempts   int            `json:"original_attempts,omitempty"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	Responses          map[string]any `json:"responses,omitempty"`
	Error              string         `json:"error,omitempty"`
}

// BuildID constructs an interview ID from a run ID and node ID.
func BuildID(runID, nodeID string) string {
	return fmt.Sprintf("interview-%s-%s", runID, nodeID)
}

// InterviewPath returns the filesystem path for an interview JSON file.
func InterviewPath(runDir, nodeID string) string {
	return filepath.Join(runDir, "interviews", nodeID+".json")
}

// Create writes a new interview JSON file, defaulting status to pending.
func Create(runDir string, interview *Interview) error {
	if err := normalize(interview); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(runDir, "interviews"), 0o755); err != nil {
		return err
	}
	return save(runDir, interview)
}

// Save writes an existing interview back to disk.
func Save(runDir string, interview *Interview) error {
	if err := normalize(interview); err != nil {
		return err
	}
	return save(runDir, interview)
}

func normalize(interview *Interview) error {
	if interview.ID == "" {
		return fmt.Errorf("interview id required")
	}
	if interview.Status == "" {
		interview.Status = StatusPending
	}
	return nil
}

func save(runDir string, interview *Interview) error {
	data, err := json.MarshalIndent(interview, "", "  ")
	if err != nil {
		return err
	}
	path := InterviewPath(runDir, interview.NodeID)
	return os.WriteFile(path, data, 0o644)
}

// Load reads a single interview by node ID from a run directory.
func Load(runDir, nodeID string) (*Interview, error) {
	data, err := os.ReadFile(InterviewPath(runDir, nodeID))
	if err != nil {
		return nil, err
	}
	var interview Interview
	if err := json.Unmarshal(data, &interview); err != nil {
		return nil, err
	}
	return &interview, nil
}

// ListForRun returns all interviews stored in a run directory.
func ListForRun(runDir string) ([]*Interview, error) {
	dir := filepath.Join(runDir, "interviews")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Interview{}, nil
		}
		return nil, err
	}

	var interviews []*Interview
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		nodeID := strings.TrimSuffix(entry.Name(), ".json")
		iv, err := Load(runDir, nodeID)
		if err != nil {
			return nil, err
		}
		interviews = append(interviews, iv)
	}
	return interviews, nil
}
