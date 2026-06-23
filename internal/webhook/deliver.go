package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"primeradiant.com/toil/internal/state"
)

const (
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	eventRunFailed  = "run_failed"
)

// Payload is the JSON body Toil POSTs to a run's configured webhook callback URL.
type Payload struct {
	Event      string                 `json:"event"`
	RunID      string                 `json:"run_id"`
	WorkflowID string                 `json:"workflow_id"`
	Status     string                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
	Title      string                 `json:"title,omitempty"`
	Summary    string                 `json:"summary,omitempty"`
	StartedAt  string                 `json:"started_at,omitempty"`
	FinishedAt string                 `json:"finished_at,omitempty"`
	Nodes      map[string]NodePayload `json:"nodes,omitempty"`

	// Delivery fields — only populated when delivery nodes ran.
	RepoURL        string `json:"repo_url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	MergeCommitSHA string `json:"merge_commit_sha,omitempty"`
	PRURL          string `json:"pr_url,omitempty"`
	DeliveryError  string `json:"delivery_error,omitempty"`

	// HasUnresolvedFailure indicates the run completed with unresolved failures.
	// When true, external consumers should treat this as a failed run despite Status="completed".
	HasUnresolvedFailure bool `json:"has_unresolved_failure,omitempty"`
}

// NodePayload carries per-node status in the webhook.
type NodePayload struct {
	Status   string `json:"status"`
	Decision string `json:"decision,omitempty"`
}

// PayloadFromRunState builds a webhook Payload from a terminal RunState.
func PayloadFromRunState(rs *state.RunState) Payload {
	event := "run_completed"
	status := rs.Status
	switch rs.Status {
	case statusFailed:
		event = eventRunFailed
	case statusCancelled:
		event = "run_cancelled"
	}

	// Treat unresolved-failure runs as failed for external consumers.
	if rs.Status == statusCompleted && rs.HasUnresolvedFailure {
		event = eventRunFailed
		status = statusFailed
	}

	finishedAt := ""
	if rs.FinishedAt != nil {
		finishedAt = rs.FinishedAt.Format(time.RFC3339)
	}

	nodes := map[string]NodePayload{}
	rs.WithNodes(func(nodeMap map[string]*state.NodeState) {
		for id, n := range nodeMap {
			nodes[id] = NodePayload{
				Status:   n.Status,
				Decision: n.Decision,
			}
		}
	})

	p := Payload{
		Event:                event,
		RunID:                rs.ID,
		WorkflowID:           rs.WorkflowID,
		Status:               status,
		Error:                rs.Error,
		Title:                rs.Title,
		Summary:              rs.Summary,
		StartedAt:            rs.StartedAt.Format(time.RFC3339),
		FinishedAt:           finishedAt,
		Nodes:                nodes,
		HasUnresolvedFailure: rs.HasUnresolvedFailure,
	}

	// Extract delivery data (only present when delivery nodes ran).
	if repoURL, ok := rs.Inputs["repo_url"].(string); ok {
		p.RepoURL = repoURL
	}

	extractNodeData := func(nodeID, field string) string {
		var val string
		rs.WithNodes(func(nodes map[string]*state.NodeState) {
			n, ok := nodes[nodeID]
			if !ok || n.Data == nil || n.Status != statusCompleted {
				return
			}
			if s, ok := n.Data[field].(string); ok {
				val = s
			}
		})
		return val
	}

	extractNodeError := func(nodeIDs ...string) string {
		var errStr string
		rs.WithNodes(func(nodes map[string]*state.NodeState) {
			for _, id := range nodeIDs {
				n, ok := nodes[id]
				if !ok || n.Status != statusFailed {
					continue
				}
				if n.Error != "" {
					errStr = n.Error
					return
				}
			}
		})
		return errStr
	}

	p.Branch = extractNodeData("push_to_remote", "branch")
	p.MergeCommitSHA = extractNodeData("finalize_remote_branch", "merge_commit_sha")
	p.PRURL = extractNodeData("finalize_remote_branch", "pr_url")
	p.DeliveryError = extractNodeError("finalize_remote_branch", "push_to_remote")

	return p
}

// deliverTimeout is the HTTP client timeout for webhook delivery.
var deliverTimeout = 10 * time.Second

// Deliver POSTs the payload as JSON to the callback URL.
// Returns an error on failure; callers should log but not propagate.
func Deliver(callbackURL string, payload Payload) error {
	if callbackURL == "" {
		return fmt.Errorf("webhook: empty callback URL")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}

	client := &http.Client{Timeout: deliverTimeout}
	resp, err := client.Post(callbackURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: POST %s: %w", callbackURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: POST %s returned %d", callbackURL, resp.StatusCode)
	}

	return nil
}
