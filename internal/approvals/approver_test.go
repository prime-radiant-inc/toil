package approvals

import (
	"encoding/json"
	"testing"
)

const testDecisionApproved = "approved"

// --- AutoApprover tests ---

func TestAutoApprover_SelectsFirstChoice(t *testing.T) {
	approver := &AutoApprover{}
	approval := &Approval{
		ID:      "test-1",
		Choices: []string{testDecisionApproved, "rejected", "needs_changes"},
	}
	res, err := approver.Resolve(approval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil resolution")
	}
	if res.Decision != testDecisionApproved {
		t.Fatalf("expected decision %q, got %q", testDecisionApproved, res.Decision)
	}
	if res.Comment == "" {
		t.Fatal("expected non-empty comment")
	}
}

func TestAutoApprover_UsesDefaultWhenSet(t *testing.T) {
	approver := &AutoApprover{}
	approval := &Approval{
		ID:      "test-2",
		Choices: []string{testDecisionApproved, "rejected"},
		Default: "rejected",
	}
	res, err := approver.Resolve(approval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil resolution")
	}
	if res.Decision != "rejected" {
		t.Fatalf("expected decision %q, got %q", "rejected", res.Decision)
	}
}

func TestAutoApprover_FallsBackToApproved(t *testing.T) {
	approver := &AutoApprover{}
	approval := &Approval{
		ID: "test-3",
	}
	res, err := approver.Resolve(approval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil resolution")
	}
	if res.Decision != testDecisionApproved {
		t.Fatalf("expected decision %q, got %q", testDecisionApproved, res.Decision)
	}
}

// --- CallbackApprover tests ---

func TestCallbackApprover_CallsFunction(t *testing.T) {
	called := false
	approver := &CallbackApprover{
		Fn: func(a *Approval) (*Resolution, error) {
			called = true
			if a.ID != "test-cb" {
				t.Fatalf("expected approval ID %q, got %q", "test-cb", a.ID)
			}
			return &Resolution{
				Decision: "needs_changes",
				Message:  "custom message",
				Comment:  "custom comment",
			}, nil
		},
	}
	approval := &Approval{ID: "test-cb"}
	res, err := approver.Resolve(approval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected callback to be called")
	}
	if res.Decision != "needs_changes" {
		t.Fatalf("expected decision %q, got %q", "needs_changes", res.Decision)
	}
	if res.Message != "custom message" {
		t.Fatalf("expected message %q, got %q", "custom message", res.Message)
	}
}

// --- FileApprover tests ---

func TestFileApprover_ReturnsNil(t *testing.T) {
	approver := &FileApprover{}
	approval := &Approval{ID: "test-file"}
	res, err := approver.Resolve(approval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil resolution, got %+v", res)
	}
}

// --- JSON round-trip tests ---

func TestApproval_ChoicesJSONRoundTrip(t *testing.T) {
	original := &Approval{
		ID:         "roundtrip-1",
		RunID:      "run-1",
		NodeID:     "review",
		Status:     "pending",
		Question:   "Please review",
		Choices:    []string{"approved", "rejected", "needs_changes"},
		TimeoutSec: 300,
		Default:    testDecisionApproved,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Approval
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.Choices) != 3 {
		t.Fatalf("expected 3 choices, got %d", len(decoded.Choices))
	}
	for i, want := range []string{testDecisionApproved, "rejected", "needs_changes"} {
		if decoded.Choices[i] != want {
			t.Fatalf("choice[%d]: expected %q, got %q", i, want, decoded.Choices[i])
		}
	}
	if decoded.TimeoutSec != 300 {
		t.Fatalf("expected timeout_sec 300, got %d", decoded.TimeoutSec)
	}
	if decoded.Default != testDecisionApproved {
		t.Fatalf("expected default %q, got %q", testDecisionApproved, decoded.Default)
	}
}

func TestApproval_EmptyChoicesOmitted(t *testing.T) {
	original := &Approval{
		ID:     "no-choices",
		Status: "pending",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Verify omitempty works: choices, timeout_sec, default should not be in JSON
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if _, ok := raw["choices"]; ok {
		t.Fatal("expected choices to be omitted from JSON when empty")
	}
	if _, ok := raw["timeout_sec"]; ok {
		t.Fatal("expected timeout_sec to be omitted from JSON when zero")
	}
	if _, ok := raw["default"]; ok {
		t.Fatal("expected default to be omitted from JSON when empty")
	}
}
