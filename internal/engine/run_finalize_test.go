package engine

import (
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// TestFinalizeRunTotals_PopulatesFromEvents writes a small events.jsonl,
// calls FinalizeRunTotals, and asserts that runState.Totals reflects the
// events. The node_output event carries an ASSISTANT_TEXT_END payload —
// the format the metrics.Collector actually parses for token usage.
func TestFinalizeRunTotals_PopulatesFromEvents(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	content := `{"timestamp":"2024-01-01T00:00:00Z","type":"node_started","run_id":"r","node_id":"n"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_output","run_id":"r","node_id":"n","stream":"stdout","text":"{\"kind\":\"ASSISTANT_TEXT_END\",\"data\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"reasoning_tokens\":0},\"model\":\"claude-sonnet-4-5\"}}"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_completed","run_id":"r","node_id":"n","duration_ms":1000}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := &state.RunState{ID: "r", Status: "completed", Nodes: map[string]*state.NodeState{
		"n": {ID: "n", Status: "completed"},
	}}

	if err := FinalizeRunTotals(rs, dir); err != nil {
		t.Fatalf("FinalizeRunTotals: %v", err)
	}

	if rs.Totals == nil {
		t.Fatal("rs.Totals is nil after FinalizeRunTotals")
	}
	if rs.Totals.Tokens.Input != 100 {
		t.Errorf("Tokens.Input = %d, want 100", rs.Totals.Tokens.Input)
	}
	if rs.Totals.Tokens.Output != 50 {
		t.Errorf("Tokens.Output = %d, want 50", rs.Totals.Tokens.Output)
	}
}

// TestFinalizeRunTotals_NoEventsFile is the legitimate edge case for a
// run whose events.jsonl is missing (corrupt or never written). The
// function should not error; it should leave rs.Totals as nil.
func TestFinalizeRunTotals_NoEventsFile(t *testing.T) {
	dir := t.TempDir()
	rs := &state.RunState{ID: "r", Status: "completed", Nodes: map[string]*state.NodeState{}}

	if err := FinalizeRunTotals(rs, dir); err != nil {
		t.Fatalf("FinalizeRunTotals returned error on missing events.jsonl: %v", err)
	}
	if rs.Totals != nil {
		t.Errorf("expected rs.Totals to remain nil when events.jsonl is missing, got %+v", rs.Totals)
	}
}
