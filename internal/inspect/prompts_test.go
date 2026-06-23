package inspect

import (
	"testing"

	"primeradiant.com/toil/internal/state"
)

func TestPromptsProcessor_FullPromptCollection(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "You are a helpful assistant.\n\nPlease do the task.",
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.ID != "node-a" {
		t.Errorf("ID: got %q, want %q", n.ID, "node-a")
	}
	if n.FullPrompt != "You are a helpful assistant.\n\nPlease do the task." {
		t.Errorf("FullPrompt: got %q", n.FullPrompt)
	}
}

func TestPromptsProcessor_EdgePromptCapture(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_edge_prompt",
		NodeID: "node-a",
		Text:   "Please do the task.",
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.EdgePrompt != "Please do the task." {
		t.Errorf("EdgePrompt: got %q, want %q", n.EdgePrompt, "Please do the task.")
	}
}

func TestPromptsProcessor_SystemPromptComputation(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	fullPrompt := "You are a helpful assistant.\n\nPlease do the task."
	edgePrompt := "Please do the task."

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   fullPrompt,
	})
	proc.ProcessEvent(state.Event{
		Type:   "node_edge_prompt",
		NodeID: "node-a",
		Text:   edgePrompt,
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.FullPrompt != fullPrompt {
		t.Errorf("FullPrompt: got %q, want %q", n.FullPrompt, fullPrompt)
	}
	if n.EdgePrompt != edgePrompt {
		t.Errorf("EdgePrompt: got %q, want %q", n.EdgePrompt, edgePrompt)
	}
	// System prompt = full prompt minus edge portion
	wantSystem := "You are a helpful assistant.\n\n"
	if n.SystemPrompt != wantSystem {
		t.Errorf("SystemPrompt: got %q, want %q", n.SystemPrompt, wantSystem)
	}
}

func TestPromptsProcessor_MultipleNodes(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "Prompt for A.",
	})
	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-b",
		Text:   "Prompt for B.",
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}

	nodesByID := make(map[string]NodePrompts)
	for _, n := range result.Nodes {
		nodesByID[n.ID] = n
	}

	if nodesByID["node-a"].FullPrompt != "Prompt for A." {
		t.Errorf("node-a FullPrompt: got %q", nodesByID["node-a"].FullPrompt)
	}
	if nodesByID["node-b"].FullPrompt != "Prompt for B." {
		t.Errorf("node-b FullPrompt: got %q", nodesByID["node-b"].FullPrompt)
	}
}

func TestPromptsProcessor_NoEdgePrompt_SystemEqualsFullPrompt(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	fullPrompt := "You are an assistant. Do the work."
	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   fullPrompt,
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.SystemPrompt != fullPrompt {
		t.Errorf("SystemPrompt: got %q, want %q (full prompt when no edge)", n.SystemPrompt, fullPrompt)
	}
	if n.EdgePrompt != "" {
		t.Errorf("EdgePrompt should be empty when not set, got %q", n.EdgePrompt)
	}
}

func TestPromptsProcessor_EdgeNotInFullPrompt_FallsBack(t *testing.T) {
	// If the edge text is not found in the full prompt, system_prompt = full_prompt.
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "Full prompt text.",
	})
	proc.ProcessEvent(state.Event{
		Type:   "node_edge_prompt",
		NodeID: "node-a",
		Text:   "Completely different text not in full prompt.",
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	n := result.Nodes[0]
	if n.SystemPrompt != "Full prompt text." {
		t.Errorf("SystemPrompt: got %q, want full prompt as fallback", n.SystemPrompt)
	}
}

func TestPromptsProcessor_Changed(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	if proc.Changed() {
		t.Error("Changed() should be false initially")
	}

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "Hello.",
	})

	if !proc.Changed() {
		t.Error("Changed() should be true after a prompt event")
	}

	_ = proc.Result()
	if proc.Changed() {
		t.Error("Changed() should be false after Result()")
	}
}

func TestPromptsProcessor_EmptyResult(t *testing.T) {
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	result := proc.Result().(PromptsResult)

	if result.Nodes == nil {
		t.Error("Nodes should not be nil (should be empty slice)")
	}
}

func TestPromptsProcessor_LastPromptWins(t *testing.T) {
	// If a node gets two node_prompt events, the last one should be used.
	rs := state.NewRunState("run-1", "wf-1", nil)
	proc := NewPromptsProcessor(rs)

	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "First prompt.",
	})
	proc.ProcessEvent(state.Event{
		Type:   "node_prompt",
		NodeID: "node-a",
		Text:   "Second prompt.",
	})

	result := proc.Result().(PromptsResult)

	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}
	if result.Nodes[0].FullPrompt != "Second prompt." {
		t.Errorf("FullPrompt: got %q, want %q", result.Nodes[0].FullPrompt, "Second prompt.")
	}
}
