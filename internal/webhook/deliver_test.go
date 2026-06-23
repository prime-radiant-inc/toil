package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

const eventRunCompleted = "run_completed"

func TestPayloadFromRunState_Completed(t *testing.T) {
	started := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 3, 2, 10, 5, 0, 0, time.UTC)
	rs := &state.RunState{
		ID:         "run-1",
		WorkflowID: "build",
		Status:     statusCompleted,
		Title:      "Build: widget",
		Summary:    "Completed successfully.",
		StartedAt:  started,
		FinishedAt: &finished,
		Nodes:      map[string]*state.NodeState{},
	}
	rs.WithNode("agent", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "approve"
	})

	p := PayloadFromRunState(rs)

	if p.Event != eventRunCompleted {
		t.Fatalf("expected event 'run_completed', got %q", p.Event)
	}
	if p.RunID != "run-1" {
		t.Fatalf("expected run_id 'run-1', got %q", p.RunID)
	}
	if p.WorkflowID != "build" {
		t.Fatalf("expected workflow_id 'build', got %q", p.WorkflowID)
	}
	if p.Status != statusCompleted {
		t.Fatalf("expected status 'completed', got %q", p.Status)
	}
	if p.Title != "Build: widget" {
		t.Fatalf("expected title 'Build: widget', got %q", p.Title)
	}
	if p.Summary != "Completed successfully." {
		t.Fatalf("expected summary, got %q", p.Summary)
	}
	if p.StartedAt != started.Format(time.RFC3339) {
		t.Fatalf("expected started_at RFC3339, got %q", p.StartedAt)
	}
	if p.FinishedAt != finished.Format(time.RFC3339) {
		t.Fatalf("expected finished_at RFC3339, got %q", p.FinishedAt)
	}
	if p.Error != "" {
		t.Fatalf("expected no error, got %q", p.Error)
	}
	node, ok := p.Nodes["agent"]
	if !ok {
		t.Fatal("expected node 'agent' in payload")
	}
	if node.Status != statusCompleted || node.Decision != "approve" {
		t.Fatalf("unexpected node payload: %+v", node)
	}
}

func TestPayloadFromRunState_Failed(t *testing.T) {
	started := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 3, 2, 10, 1, 0, 0, time.UTC)
	rs := &state.RunState{
		ID:         "run-2",
		WorkflowID: "deploy",
		Status:     statusFailed,
		Error:      "timeout exceeded",
		StartedAt:  started,
		FinishedAt: &finished,
		Nodes:      map[string]*state.NodeState{},
	}

	p := PayloadFromRunState(rs)

	if p.Event != "run_failed" {
		t.Fatalf("expected event 'run_failed', got %q", p.Event)
	}
	if p.Error != "timeout exceeded" {
		t.Fatalf("expected error 'timeout exceeded', got %q", p.Error)
	}
}

func TestPayloadFromRunState_Cancelled(t *testing.T) {
	started := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 3, 2, 10, 2, 0, 0, time.UTC)
	rs := &state.RunState{
		ID:         "run-3",
		WorkflowID: "review",
		Status:     "cancelled",
		StartedAt:  started,
		FinishedAt: &finished,
		Nodes:      map[string]*state.NodeState{},
	}

	p := PayloadFromRunState(rs)

	if p.Event != "run_cancelled" {
		t.Fatalf("expected event 'run_cancelled', got %q", p.Event)
	}
}

func TestPayloadFromRunState_NilFinishedAt(t *testing.T) {
	rs := &state.RunState{
		ID:         "run-4",
		WorkflowID: "build",
		Status:     "cancelled",
		StartedAt:  time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC),
		FinishedAt: nil,
		Nodes:      map[string]*state.NodeState{},
	}

	p := PayloadFromRunState(rs)

	if p.FinishedAt != "" {
		t.Fatalf("expected empty finished_at for nil FinishedAt, got %q", p.FinishedAt)
	}
}

func TestDeliver_Success(t *testing.T) {
	var received Payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload := Payload{
		Event:      eventRunCompleted,
		RunID:      "run-1",
		WorkflowID: "build",
		Status:     statusCompleted,
	}

	err := Deliver(server.URL, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.RunID != "run-1" {
		t.Fatalf("expected run_id 'run-1', got %q", received.RunID)
	}
	if received.Event != eventRunCompleted {
		t.Fatalf("expected event 'run_completed', got %q", received.Event)
	}
}

func TestDeliver_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	err := Deliver(server.URL, Payload{RunID: "run-1"})
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestDeliver_Timeout(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer func() {
		close(done)
		server.Close()
	}()

	origTimeout := deliverTimeout
	deliverTimeout = 100 * time.Millisecond
	defer func() { deliverTimeout = origTimeout }()

	err := Deliver(server.URL, Payload{RunID: "run-1"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDeliver_EmptyURL(t *testing.T) {
	err := Deliver("", Payload{RunID: "run-1"})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestPayloadFromRunState_WithDeliveryData(t *testing.T) {
	now := time.Now().UTC()
	rs := state.NewRunState("run-delivery-1", "implement_spec", nil)
	rs.Status = statusCompleted
	rs.FinishedAt = &now

	// Simulate delivery inputs
	rs.Inputs = map[string]interface{}{
		"repo_url": "https://github.com/acme/my-app",
	}

	// Simulate completed delivery nodes
	rs.WithNode("push_to_remote", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]interface{}{
			"branch": "toil/my-app/run-delivery-1",
		}
	})
	rs.WithNode("finalize_remote_branch", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]interface{}{
			"merge_commit_sha": "abc123def",
			"pr_url":           "",
		}
	})

	p := PayloadFromRunState(rs)

	if p.RepoURL != "https://github.com/acme/my-app" {
		t.Fatalf("RepoURL = %q", p.RepoURL)
	}
	if p.Branch != "toil/my-app/run-delivery-1" {
		t.Fatalf("Branch = %q", p.Branch)
	}
	if p.MergeCommitSHA != "abc123def" {
		t.Fatalf("MergeCommitSHA = %q", p.MergeCommitSHA)
	}
	if p.PRURL != "" {
		t.Fatalf("PRURL = %q, expected empty", p.PRURL)
	}
	if p.DeliveryError != "" {
		t.Fatalf("DeliveryError = %q, expected empty", p.DeliveryError)
	}
}

func TestPayloadFromRunState_WithDeliveryError(t *testing.T) {
	now := time.Now().UTC()
	rs := state.NewRunState("run-err-1", "implement_spec", nil)
	rs.Status = statusFailed
	rs.FinishedAt = &now
	rs.Inputs = map[string]interface{}{
		"repo_url": "https://github.com/acme/my-app",
	}

	rs.WithNode("push_to_remote", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]interface{}{
			"branch": "toil/my-app/run-err-1",
		}
	})
	rs.WithNode("finalize_remote_branch", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Error = "Merge conflict — cannot auto-merge branch into main."
	})

	p := PayloadFromRunState(rs)

	if p.DeliveryError != "Merge conflict — cannot auto-merge branch into main." {
		t.Fatalf("DeliveryError = %q", p.DeliveryError)
	}
	if p.Branch != "toil/my-app/run-err-1" {
		t.Fatalf("Branch = %q", p.Branch)
	}
}

func TestPayloadFromRunState_WithPRURL(t *testing.T) {
	now := time.Now().UTC()
	rs := state.NewRunState("run-pr-1", "implement_spec", nil)
	rs.Status = statusCompleted
	rs.FinishedAt = &now
	rs.Inputs = map[string]interface{}{
		"repo_url": "https://github.com/acme/my-app",
	}

	rs.WithNode("push_to_remote", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]interface{}{
			"branch": "toil/my-app/run-pr-1",
		}
	})
	rs.WithNode("finalize_remote_branch", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Data = map[string]interface{}{
			"pr_url":           "https://github.com/acme/my-app/pull/42",
			"merge_commit_sha": "",
		}
	})

	p := PayloadFromRunState(rs)

	if p.PRURL != "https://github.com/acme/my-app/pull/42" {
		t.Fatalf("PRURL = %q", p.PRURL)
	}
	if p.MergeCommitSHA != "" {
		t.Fatalf("MergeCommitSHA = %q, expected empty", p.MergeCommitSHA)
	}
}

func TestPayloadFromRunState_PushFailed(t *testing.T) {
	now := time.Now().UTC()
	rs := state.NewRunState("run-push-fail-1", "implement_spec", nil)
	rs.Status = statusFailed
	rs.FinishedAt = &now
	rs.Inputs = map[string]interface{}{
		"repo_url": "https://github.com/acme/my-app",
	}

	rs.WithNode("push_to_remote", func(n *state.NodeState) {
		n.Status = statusFailed
		n.Error = "authentication failed: bad credentials"
	})

	p := PayloadFromRunState(rs)

	if p.DeliveryError != "authentication failed: bad credentials" {
		t.Fatalf("DeliveryError = %q", p.DeliveryError)
	}
	if p.Branch != "" {
		t.Fatalf("Branch = %q, expected empty (push failed)", p.Branch)
	}
}

func TestPayloadFromRunState_NoPushNodes(t *testing.T) {
	now := time.Now().UTC()
	rs := state.NewRunState("run-old-1", "implement_spec", nil)
	rs.Status = statusCompleted
	rs.FinishedAt = &now
	// No push inputs or nodes

	p := PayloadFromRunState(rs)

	if p.RepoURL != "" {
		t.Fatalf("RepoURL = %q, expected empty", p.RepoURL)
	}
	if p.Branch != "" {
		t.Fatalf("Branch = %q, expected empty", p.Branch)
	}
	if p.MergeCommitSHA != "" {
		t.Fatalf("MergeCommitSHA = %q, expected empty", p.MergeCommitSHA)
	}
}

func TestPayloadFromRunState_UnresolvedFailure(t *testing.T) {
	started := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 3, 2, 10, 5, 0, 0, time.UTC)
	rs := &state.RunState{
		ID:                   "run-unresolved-1",
		WorkflowID:           "review",
		Status:               statusCompleted,
		HasUnresolvedFailure: true,
		Title:                "Review: changes",
		Summary:              "Completed with unresolved failures.",
		StartedAt:            started,
		FinishedAt:           &finished,
		Nodes:                map[string]*state.NodeState{},
	}
	rs.WithNode("review", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = "request_changes"
	})

	p := PayloadFromRunState(rs)

	// Event and Status should reflect failure semantics despite rs.Status="completed"
	if p.Event != "run_failed" {
		t.Fatalf("expected event 'run_failed', got %q", p.Event)
	}
	if p.Status != statusFailed {
		t.Fatalf("expected status 'failed', got %q", p.Status)
	}
	if !p.HasUnresolvedFailure {
		t.Fatalf("expected HasUnresolvedFailure=true, got false")
	}
}
