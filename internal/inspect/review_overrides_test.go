package inspect

import (
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestReviewOverridesProcessor_DerivesFromNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "implement_task", nil)
	emitted := time.Now().UTC()
	rs.WithNode("resolve_review_dispute", func(n *state.NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
		n.Message = "waived scaffold smoke concern"
		n.Data = map[string]any{
			"waived_concerns": []any{
				map[string]any{"source": "write_code", "concern": "TS exec mode", "justification": "verified"},
			},
		}
		n.EndedAt = &emitted
	})

	result := NewReviewOverridesProcessor(rs).Result().(ReviewOverridesResult)
	if result.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", result.RunID)
	}
	if len(result.Overrides) != 1 {
		t.Fatalf("Overrides = %d, want 1", len(result.Overrides))
	}
	if result.Overrides[0].Decision != "force_approve" {
		t.Errorf("Decision = %q", result.Overrides[0].Decision)
	}
	if result.Overrides[0].Message != "waived scaffold smoke concern" {
		t.Errorf("Message = %q", result.Overrides[0].Message)
	}
}

func TestReviewOverridesProcessor_EmptyReturnsEmptySlice(t *testing.T) {
	// Result must return an empty slice (not nil) when there are no
	// overrides — JSON-encoded output shows `"overrides": []`, not
	// `"overrides": null`. Downstream consumers rely on this shape.
	rs := state.NewRunState("run-2", "implement_task", nil)
	result := NewReviewOverridesProcessor(rs).Result().(ReviewOverridesResult)
	if result.Overrides == nil {
		t.Fatal("Overrides should be non-nil empty slice, got nil")
	}
	if len(result.Overrides) != 0 {
		t.Fatalf("Overrides = %d, want 0", len(result.Overrides))
	}
}

func TestReviewOverridesProcessor_IgnoresEvents(t *testing.T) {
	// The aspect reads from RunState.Nodes, not events. Feeding events
	// after construction must not alter the result — the decision is
	// derived fresh each call from whatever the Nodes map currently
	// says.
	rs := state.NewRunState("run-3", "implement_task", nil)
	rs.WithNode("esc", func(n *state.NodeState) {
		n.Decision = "skip_task"
		n.Tags = []string{"override"}
		n.Message = "moved on"
	})
	proc := NewReviewOverridesProcessor(rs)

	proc.ProcessEvent(state.Event{Type: "review_override", NodeID: "other"})
	proc.ProcessEvent(state.Event{Type: "node_completed", NodeID: "whatever"})

	result := proc.Result().(ReviewOverridesResult)
	if len(result.Overrides) != 1 || result.Overrides[0].NodeID != "esc" {
		t.Fatalf("events altered result: %+v", result)
	}
	if proc.Changed() {
		t.Fatal("processor should not report changed — result is static from RunState")
	}
}

func TestReviewOverrides_AspectRegistered(t *testing.T) {
	// The init() function must register the "review_overrides" aspect
	// so `toil inspect --aspect review_overrides` resolves. Dashboards
	// and CLIs that list aspects will drop it silently otherwise.
	found := false
	for _, name := range Aspects() {
		if name == "review_overrides" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("review_overrides aspect not registered")
	}
}
