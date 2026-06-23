package approvals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"primeradiant.com/toil/internal/config"
)

const statusPending = "pending"

type Approval struct {
	ID         string     `json:"id"`
	RunID      string     `json:"run_id"`
	NodeID     string     `json:"node_id"`
	Attempt    int        `json:"attempt"`
	Status     string     `json:"status"`
	Question   string     `json:"question,omitempty"`
	Choices    []string   `json:"choices,omitempty"`
	TimeoutSec int        `json:"timeout_sec,omitempty"`
	Default    string     `json:"default,omitempty"`
	Decision   string     `json:"decision,omitempty"`
	Message    string     `json:"message,omitempty"`
	Comment    string     `json:"comment,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

func ApprovalPath(runDir string, approvalID string) string {
	return filepath.Join(runDir, "approvals", approvalID+".json")
}

func BuildID(runID string, nodeID string, attempt int) string {
	return fmt.Sprintf("%s-%s-%d", runID, nodeID, attempt)
}

func Create(runDir string, approval *Approval) error {
	if approval.ID == "" {
		return fmt.Errorf("approval id required")
	}
	if approval.Status == "" {
		approval.Status = statusPending
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Join(runDir, "approvals"), 0o755); err != nil {
		return err
	}
	return save(runDir, approval)
}

func Load(runDir string, approvalID string) (*Approval, error) {
	data, err := os.ReadFile(ApprovalPath(runDir, approvalID))
	if err != nil {
		return nil, err
	}
	var approval Approval
	if err := json.Unmarshal(data, &approval); err != nil {
		return nil, err
	}
	return &approval, nil
}

func Save(runDir string, approval *Approval) error {
	if approval.ID == "" {
		return fmt.Errorf("approval id required")
	}
	if approval.Status == "" {
		approval.Status = statusPending
	}
	return save(runDir, approval)
}

func save(runDir string, approval *Approval) error {
	data, err := json.MarshalIndent(approval, "", "  ")
	if err != nil {
		return err
	}
	path := ApprovalPath(runDir, approval.ID)
	return os.WriteFile(path, data, 0o644)
}

func ListAll(root string) ([]*Approval, error) {
	runsDir := config.RunsDir(root)
	runs, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Approval{}, nil
		}
		return nil, err
	}
	approvals := []*Approval{}
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		approvalDir := filepath.Join(runsDir, run.Name(), "approvals")
		entries, err := os.ReadDir(approvalDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			approval, err := Load(filepath.Join(runsDir, run.Name()), strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				return nil, err
			}
			approvals = append(approvals, approval)
		}
	}
	return approvals, nil
}

func Find(root string, approvalID string) (*Approval, string, error) {
	runsDir := config.RunsDir(root)
	runs, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, "", err
	}
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		path := ApprovalPath(filepath.Join(runsDir, run.Name()), approvalID)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		var approval Approval
		if err := json.Unmarshal(data, &approval); err != nil {
			return nil, "", err
		}
		return &approval, filepath.Join(runsDir, run.Name()), nil
	}
	return nil, "", fmt.Errorf("approval not found")
}
