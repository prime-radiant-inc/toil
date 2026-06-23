package api

import (
	"os"
	"path/filepath"
	"testing"

	"primeradiant.com/toil/internal/state"
)

// TestServer_LoadRunTotal_TotalsHitSkipsFileRead — same shape as the
// dashboard test. Persisted Totals on the loaded state should be
// returned without reading events.jsonl.
func TestServer_LoadRunTotal_TotalsHitSkipsFileRead(t *testing.T) {
	dir := t.TempDir()
	runID := "run-1"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	stateContent := `{"id":"run-1","workflow_id":"w","status":"completed","started_at":"2024-01-01T00:00:00Z","totals":{"duration_ms":1234,"tokens":{"input":100,"output":50,"cache_read":0,"reasoning":0,"total":150},"cost_usd":0.5}}`
	if err := os.WriteFile(filepath.Join(runDir, "state.json"), []byte(stateContent), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"type":"node_completed","run_id":"run-1","node_id":"n","data":{}}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(eventsPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsPath, 0o644) })

	server := &Server{RunsDir: dir}
	got := server.loadRunTotal(runID)
	if got == nil {
		t.Fatal("loadRunTotal returned nil — implementation read events.jsonl instead of using rs.Totals")
	}
	if got.DurationMs != 1234 || got.Tokens.Total != 150 {
		t.Fatalf("got %+v, want totals from state.json (duration=1234, tokens.total=150)", *got)
	}
}

// TestServer_LoadRunTotal_BackfillPersists — when state.json has no
// totals but the run is terminal, the slow path computes from events
// AND writes the result back to state.json.
func TestServer_LoadRunTotal_BackfillPersists(t *testing.T) {
	dir := t.TempDir()
	runID := "run-1"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(runDir, "state.json")
	stateContent := `{"id":"run-1","workflow_id":"w","status":"completed","started_at":"2024-01-01T00:00:00Z","inputs":{},"nodes":{}}`
	if err := os.WriteFile(statePath, []byte(stateContent), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"timestamp":"2024-01-01T00:00:00Z","type":"node_started","run_id":"run-1","node_id":"n"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_output","run_id":"run-1","node_id":"n","text":"{\"kind\":\"ASSISTANT_TEXT_END\",\"data\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"reasoning_tokens\":0},\"model\":\"claude-sonnet-4-5\"}}"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_completed","run_id":"run-1","node_id":"n","duration_ms":1000}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir}
	got := server.loadRunTotal(runID)
	if got == nil {
		t.Fatal("loadRunTotal returned nil")
	}
	if got.Tokens.Input != 100 {
		t.Fatalf("computed Tokens.Input = %d, want 100", got.Tokens.Input)
	}

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

// TestServer_LoadRunTotal_StaleTotalsOnNonTerminalIgnored locks the
// defensive guard for the API path. If a run's state.json has Totals
// set but Status is non-terminal, the API must recompute fresh.
func TestServer_LoadRunTotal_StaleTotalsOnNonTerminalIgnored(t *testing.T) {
	dir := t.TempDir()
	runID := "run-1"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// state.json has Status=running AND a populated Totals — the case
	// where a resumed/retriggered run hasn't had its Totals cleared.
	stateContent := `{"id":"run-1","workflow_id":"w","status":"running","started_at":"2024-01-01T00:00:00Z","inputs":{},"nodes":{},"totals":{"duration_ms":0,"tokens":{"input":999,"output":0,"cache_read":0,"reasoning":0,"total":999}}}`
	if err := os.WriteFile(filepath.Join(runDir, "state.json"), []byte(stateContent), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"timestamp":"2024-01-01T00:00:00Z","type":"node_started","run_id":"run-1","node_id":"n"}
{"timestamp":"2024-01-01T00:00:01Z","type":"node_output","run_id":"run-1","node_id":"n","text":"{\"kind\":\"ASSISTANT_TEXT_END\",\"data\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":7,\"reasoning_tokens\":0},\"model\":\"claude-sonnet-4-5\"}}"}
`
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir}
	got := server.loadRunTotal(runID)
	if got == nil {
		t.Fatal("loadRunTotal returned nil")
	}
	if got.Tokens.Input != 42 {
		t.Fatalf("guard bypassed: expected fresh tokens (Input=42 from events), got Input=%d. The fast path served stale Totals.", got.Tokens.Input)
	}
}

// TestServer_LoadRunTotal_NonTerminalDoesNotPersist — running runs
// should never persist Totals to state.json (the file is still being
// appended to).
func TestServer_LoadRunTotal_NonTerminalDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	runID := "run-1"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(runDir, "state.json")
	stateContent := `{"id":"run-1","workflow_id":"w","status":"running","started_at":"2024-01-01T00:00:00Z","inputs":{},"nodes":{}}`
	if err := os.WriteFile(statePath, []byte(stateContent), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(runDir, "events.jsonl")
	content := `{"type":"node_completed","run_id":"run-1","node_id":"n","data":{}}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{RunsDir: dir}
	got := server.loadRunTotal(runID)
	if got == nil {
		t.Fatal("loadRunTotal returned nil for running run")
	}

	reloaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Totals != nil {
		t.Fatal("running run should not persist Totals to state.json")
	}
}
