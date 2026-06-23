package dashboard

import (
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// TestLoadRunTotal_TotalsHitSkipsFileRead proves that when rs.Totals
// is already populated, loadRunTotal returns it without reading
// events.jsonl. We test this by making the events.jsonl unreadable
// (chmod 0o000) — os.Stat still succeeds (only needs search permission
// on the parent directory), but os.ReadFile fails with EACCES. A
// correct implementation reads rs.Totals and never touches the file.
func TestLoadRunTotal_TotalsHitSkipsFileRead(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"type":"node_completed","run_id":"r","node_id":"n","data":{}}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cost := 0.5
	persisted := &state.NodeTotals{
		DurationMs: 1234,
		Tokens:     state.TokenBreakdown{Input: 100, Output: 50, Total: 150},
		CostUSD:    &cost,
	}
	rs := &state.RunState{ID: "run-1", Status: statusCompleted, Totals: persisted}

	if err := os.Chmod(eventsPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsPath, 0o644) })

	got := loadRunTotal(runDir, rs)
	if got == nil {
		t.Fatal("loadRunTotal returned nil — implementation read events.jsonl instead of using rs.Totals")
	}
	if got.DurationMs != persisted.DurationMs || got.Tokens.Total != persisted.Tokens.Total {
		t.Fatalf("loadRunTotal returned a different value than rs.Totals:\n  got       = %+v\n  persisted = %+v", *got, *persisted)
	}
}

// TestLoadRunTotal_BackfillPersists confirms that for a terminal run
// without persisted Totals, loadRunTotal computes from events AND
// writes the result back to state.json so subsequent reads take the
// fast path.
func TestLoadRunTotal_BackfillPersists(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(runDir, "state.json")
	stateContent := `{"id":"run-1","workflow_id":"w","status":"completed","started_at":"2024-01-01T00:00:00Z","inputs":{},"nodes":{}}`
	if err := os.WriteFile(statePath, []byte(stateContent), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	// Same shape as the engine's run_finalize_test — node_output event
	// with ASSISTANT_TEXT_END payload in ev.Text.
	content := `{"timestamp":"2024-01-01T00:00:00Z","type":"node_started","run_id":"run-1","node_id":"n"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_output","run_id":"run-1","node_id":"n","text":"{\"kind\":\"ASSISTANT_TEXT_END\",\"data\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"reasoning_tokens\":0},\"model\":\"claude-sonnet-4-5\"}}"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_completed","run_id":"run-1","node_id":"n","duration_ms":1000}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Totals != nil {
		t.Fatal("test setup: legacy state.json should have Totals == nil")
	}

	got := loadRunTotal(runDir, rs)
	if got == nil {
		t.Fatal("loadRunTotal returned nil")
	}
	if got.Tokens.Input != 100 {
		t.Errorf("computed Tokens.Input = %d, want 100", got.Tokens.Input)
	}

	// Reload state.json to confirm backfill was persisted.
	reloaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Totals == nil {
		t.Fatal("after backfill, state.json should have Totals persisted but it is nil")
	}
	if reloaded.Totals.Tokens.Input != 100 {
		t.Errorf("persisted Totals.Tokens.Input = %d, want 100", reloaded.Totals.Tokens.Input)
	}
}

// TestLoadRunTotal_StaleTotalsOnNonTerminalIgnored locks the defensive
// guard: even when rs.Totals is populated, a non-terminal run must
// recompute from events. Otherwise a resumed run could serve stale
// totals between the resume start and the next terminal save.
func TestLoadRunTotal_StaleTotalsOnNonTerminalIgnored(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"timestamp":"2024-01-01T00:00:00Z","type":"node_started","run_id":"r","node_id":"n"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_output","run_id":"r","node_id":"n","text":"{\"kind\":\"ASSISTANT_TEXT_END\",\"data\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":7,\"reasoning_tokens\":0},\"model\":\"claude-sonnet-4-5\"}}"}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-populate rs.Totals with a value that doesn't match the events,
	// then mark the run as running. The defensive guard should bypass
	// the fast path and recompute fresh from events.
	stale := &state.NodeTotals{Tokens: state.TokenBreakdown{Input: 999, Total: 999}}
	rs := &state.RunState{ID: "run-1", Status: "running", Totals: stale}

	got := loadRunTotal(runDir, rs)
	if got == nil {
		t.Fatal("loadRunTotal returned nil for running run with stale Totals")
	}
	if got.Tokens.Input != 42 {
		t.Fatalf("guard bypassed: expected fresh tokens (Input=42 from events), got Input=%d. The fast path served stale Totals.", got.Tokens.Input)
	}
}

// TestLoadRunTotal_NonTerminalRecomputes confirms that running runs
// always recompute from events.jsonl and do NOT persist Totals to
// state.json (the file is still being appended to).
func TestLoadRunTotal_NonTerminalRecomputes(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"type":"node_completed","run_id":"r","node_id":"n","data":{}}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := &state.RunState{ID: "run-1", Status: "running"}
	got := loadRunTotal(runDir, rs)
	if got == nil {
		t.Fatal("loadRunTotal returned nil for running run")
	}
	if rs.Totals != nil {
		t.Fatal("running run should not have rs.Totals set after read")
	}
}
