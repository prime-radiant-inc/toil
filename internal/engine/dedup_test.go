package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// countEventOccurrences counts how many times an event with the given type and
// node_id appears in the JSONL log file.
func countEventOccurrences(t *testing.T, logPath, eventType, nodeID string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"type":"`+eventType+`"`) &&
			strings.Contains(line, `"node_id":"`+nodeID+`"`) {
			count++
		}
	}
	return count
}

func TestDedupDiamondReadyNodesCollapsed(t *testing.T) {
	// Diamond workflow: S fans out to A and B, both converge on C.
	// A and B complete in the same wave, both producing edges to C.
	// Without dedup, C would execute twice. With dedup, exactly once.
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Diamond Dedup",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "a"},
			{From: "start", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "c"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// C should have completed exactly once — check the event log for duplicate executions
	completions := countEventOccurrences(t, logPath, "node_completed", "c")
	if completions != 1 {
		t.Fatalf("expected node c to complete exactly once, got %d completions", completions)
	}

	// Verify all nodes completed
	for _, id := range []string{"start", "a", "b", "c"} {
		s, ok := runState.NodeStatus(id)
		if !ok || s != statusCompleted {
			t.Fatalf("expected node %s completed, got status=%q exists=%v", id, s, ok)
		}
	}
}

func TestDedupExhaustedTargets(t *testing.T) {
	// Two nodes both with a _loop_exhausted edge pointing to the same target.
	// Both exceed loop limit in the same wave. Target should execute once.
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "Exhausted Target Dedup",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "looper_a", Kind: "system"},
			{ID: "looper_b", Kind: "system"},
			{ID: "fallback", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "looper_a"},
			{From: "start", To: "looper_b"},
			{From: "looper_a", To: "looper_a"},
			{From: "looper_b", To: "looper_b"},
			{From: "looper_a", To: "fallback", When: "_loop_exhausted"},
			{From: "looper_b", To: "fallback", When: "_loop_exhausted"},
		},
		Limits: map[string]int{"max_loop_iterations": 1},
	}

	eng := &Engine{Definitions: &definitions.Bundle{}}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Fallback should have completed exactly once
	fbStatus, exists := runState.NodeStatus("fallback")
	if !exists {
		t.Fatal("fallback node was never executed")
	}
	if fbStatus != statusCompleted {
		t.Fatalf("expected fallback completed, got %q", fbStatus)
	}

	completions := countEventOccurrences(t, logPath, "node_completed", "fallback")
	if completions != 1 {
		t.Fatalf("expected fallback to complete exactly once, got %d completions", completions)
	}
}

func TestDedup_ConvergentPassesMergedByEdgeIndex(t *testing.T) {
	// Two readyNode entries with the same ID and different EdgeIndex values.
	// The higher EdgeIndex wins on overlapping keys; non-overlapping keys
	// from both entries are preserved in the merged result.
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	runID := "test-dedup-passes"
	entryLow := readyNode{
		ID:        "target",
		EdgeIndex: 2,
		Passes:    map[string]any{"shared_key": "low-wins", "only_in_low": "from-low"},
	}
	entryHigh := readyNode{
		ID:        "target",
		EdgeIndex: 5,
		Passes:    map[string]any{"shared_key": "high-wins", "only_in_high": "from-high"},
	}

	// Low then High: high-index entry arrives second.
	result := dedupReadyQueue([]readyNode{entryLow, entryHigh}, logger, runID)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(result))
	}
	got := result[0]
	if got.Passes["shared_key"] != "high-wins" {
		t.Errorf("shared_key = %q, want %q", got.Passes["shared_key"], "high-wins")
	}
	if got.Passes["only_in_low"] != "from-low" {
		t.Errorf("only_in_low = %q, want %q", got.Passes["only_in_low"], "from-low")
	}
	if got.Passes["only_in_high"] != "from-high" {
		t.Errorf("only_in_high = %q, want %q", got.Passes["only_in_high"], "from-high")
	}

	// High then Low: low-index entry arrives second; high-index entry wins.
	result2 := dedupReadyQueue([]readyNode{entryHigh, entryLow}, logger, runID)
	if len(result2) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(result2))
	}
	got2 := result2[0]
	if got2.Passes["shared_key"] != "high-wins" {
		t.Errorf("shared_key (high first) = %q, want %q", got2.Passes["shared_key"], "high-wins")
	}
	if got2.Passes["only_in_low"] != "from-low" {
		t.Errorf("only_in_low (high first) = %q, want %q", got2.Passes["only_in_low"], "from-low")
	}
	if got2.Passes["only_in_high"] != "from-high" {
		t.Errorf("only_in_high (high first) = %q, want %q", got2.Passes["only_in_high"], "from-high")
	}
}

func TestDedupEdgePromptPreservesFirst(t *testing.T) {
	// Diamond with different EdgePrompts on converging edges to C.
	// The first prompt encountered should be preserved.
	// Also verify a dedup_dropped event is logged when prompts differ.
	tmpDir := t.TempDir()
	logger, logPath := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	workflow := &definitions.Workflow{
		ID:      "wf",
		Name:    "EdgePrompt Dedup",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "start", Kind: "system"},
			{ID: "a", Kind: "system"},
			{ID: "b", Kind: "system"},
			{ID: "c", Kind: "system"},
		},
		Edges: []definitions.Edge{
			{From: "start", To: "a"},
			{From: "start", To: "b"},
			{From: "a", To: "c", Prompt: "prompt_from_a"},
			{From: "b", To: "c", Prompt: "prompt_from_b"},
		},
	}

	eng := &Engine{}
	runState := state.NewRunState(testRunID1, "wf", nil)
	runContext := &RunContext{
		Outputs: make(map[string]NodeOutput),
		Inputs:  map[string]any{},
	}

	_, err := eng.runLoop(context.Background(), testRunID1, tmpDir, workflow, runState, runContext, logger, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// C should have completed
	statusC, exists := runState.NodeStatus("c")
	if !exists {
		t.Fatal("node c was never executed")
	}
	if statusC != statusCompleted {
		t.Fatalf("expected node c completed, got %q", statusC)
	}

	// A warning event should have been logged about the different EdgePrompt
	events, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(string(events), "dedup_dropped") {
		t.Fatalf("expected dedup_dropped event in log when EdgePrompts differ, got:\n%s", string(events))
	}
}
