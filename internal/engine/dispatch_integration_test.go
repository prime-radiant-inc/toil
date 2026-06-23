package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

// TestExecuteRole_FirstDispatchWritesDirAndRendersFullInputs drives a
// minimal one-role-node workflow and asserts the dispatch dir was
// created and the prompt contains "## Inputs".
func TestExecuteRole_FirstDispatchWritesDirAndRendersFullInputs(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf-first-dispatch",
		Name:             "First Dispatch",
		Version:          1,
		PromptInputsMode: "all",
		Nodes: []definitions.Node{
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
	}
	runInputs := map[string]any{"spec": "spec content"}
	setupRunForResume(t, runsDir, "run-first-dispatch", workflow, runInputs)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"done","data":{},"artifacts":[]}`, SessionID: "sess-1"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := engine.ResumeRun(context.Background(), "run-first-dispatch"); err != nil {
		t.Fatal(err)
	}

	dispatchDir := filepath.Join(runsDir, "run-first-dispatch", "dispatches", "agent", "1", "inputs")
	if _, err := os.Stat(dispatchDir); err != nil {
		t.Fatalf("expected dispatch dir at %s: %v", dispatchDir, err)
	}
	specBytes, err := os.ReadFile(filepath.Join(dispatchDir, "spec.md"))
	if err != nil {
		t.Fatalf("expected spec.md in dispatch dir: %v", err)
	}
	if string(specBytes) != "spec content" {
		t.Fatalf("expected spec.md to contain 'spec content', got: %s", specBytes)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected 1 runner request, got %d", len(runner.requests))
	}
	if !strings.Contains(runner.requests[0].Prompt, "## Inputs") {
		t.Fatalf("expected '## Inputs' in prompt, got:\n%s", runner.requests[0].Prompt)
	}

	// Verify the Dispatches counter itself, not just the path it derived.
	statePath := filepath.Join(runsDir, "run-first-dispatch", "state.json")
	rs2, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var dispatches int
	rs2.WithNode("agent", func(n *state.NodeState) { dispatches = n.Dispatches })
	if dispatches != 1 {
		t.Fatalf("expected NodeState.Dispatches=1 after first dispatch, got %d", dispatches)
	}
}

// TestExecuteRole_RetryWithinDispatchRendersFullInputs verifies that
// internal retries (within executeSingle's retry loop) reuse the same
// Dispatches counter and render full inputs (not deltas) on the retry.
func TestExecuteRole_RetryWithinDispatchRendersFullInputs(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf-retry",
		Name:             "Retry Within Dispatch",
		Version:          1,
		PromptInputsMode: "all",
		Nodes: []definitions.Node{
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Retry:     &definitions.RetryPolicy{Max: 1},
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
	}
	runInputs := map[string]any{"spec": "spec content"}
	runID := "run-retry"
	setupRunForResume(t, runsDir, runID, workflow, runInputs)

	runner := &sequentialRunner{
		results: []runners.Result{
			{SessionID: "sess-x"},
			{Output: `{"decision":"done","message":"done","data":{},"artifacts":[]}`, SessionID: "sess-x"},
		},
		errs: []error{
			fmt.Errorf("simulated transient runner failure"),
			nil,
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	if _, err := engine.ResumeRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(runsDir, runID, "state.json")
	rs2, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	var dispatches int
	rs2.WithNode("agent", func(n *state.NodeState) { dispatches = n.Dispatches })
	if dispatches != 1 {
		// Internal retries reuse the same dispatch number; Dispatches stays at 1.
		t.Fatalf("expected Dispatches=1 after retry (same logical dispatch), got %d", dispatches)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("expected 2 runner requests, got %d", len(runner.requests))
	}
	// Both requests should carry the full ## Inputs section (no deltas on
	// internal retry — attempt > 1 renders full because resume=false for
	// a fresh-attempt role node with no prior session).
	if !strings.Contains(runner.requests[1].Prompt, "## Inputs") {
		t.Fatalf("expected '## Inputs' in retry prompt, got:\n%s", runner.requests[1].Prompt)
	}
	if strings.Contains(runner.requests[1].Prompt, "## New or updated for this turn") {
		t.Fatal("expected NO deltas block in retry prompt (same dispatch, no prior session)")
	}
}

// TestRunWithResumeFallback_RebuildsPromptWithEdgeContext drives the
// runWithResumeFallback's tool_use/tool_result mismatch path and asserts
// that the rebuild closure incorporates the edge prompt from the incoming
// edge. The workflow has a producer → agent edge carrying a distinctive
// sentinel so we can verify the `if edgePrompt != ""` branch in the rebuild
// closure (execute.go). Without an edgePrompt the assertion
// strings.Contains(fallbackReq.Prompt, "## Inputs") would pass even with a
// broken rebuild closure, because both the original and rebuilt prompts
// contain "## Inputs".
func TestRunWithResumeFallback_RebuildsPromptWithEdgeContext(t *testing.T) {
	const edgeSentinel = "SENTINEL_EDGE_TEXT_12345"

	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf-rebuild",
		Name:             "Rebuild on Fallback",
		Version:          1,
		PromptInputsMode: "all",
		Nodes: []definitions.Node{
			// producer runs first; its completion fires the edge to agent.
			{
				ID: "producer", Kind: "role", Runner: "test-runner",
				Prompt:    "Produce output.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
			// agent is reached via an edge carrying the sentinel prompt.
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
		Edges: []definitions.Edge{
			{From: "producer", To: "agent", When: testDecisionDone, Prompt: edgeSentinel},
		},
	}
	runInputs := map[string]any{"spec": "spec content"}
	runID := "run-rebuild"
	setupRunForResume(t, runsDir, runID, workflow, runInputs)

	// Pre-populate agent's NodeState with a session ID so that
	// runWithResumeFallback attempts a resume on the first call for agent.
	statePath := filepath.Join(runsDir, runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	rs.WithNode("agent", func(n *state.NodeState) {
		n.SessionID = "sess-x"
	})
	if err := state.SaveState(statePath, rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Sequential requests:
	//   [0] producer first call → success
	//   [1] agent resume call   → tool_use/tool_result mismatch error
	//   [2] agent rebuild call  → success (fresh session, full prompt with edgePrompt)
	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"done","data":{},"artifacts":[]}`, SessionID: "sess-p"}, // producer
			{SessionID: "sess-x"}, // agent resume (result unused because error takes over)
			{Output: `{"decision":"done","message":"done","data":{},"artifacts":[]}`, SessionID: ""}, // agent fallback
		},
		errs: []error{
			nil, // producer succeeds
			fmt.Errorf("API Error: tool_use ids did not match tool_result ids"), // agent resume fails
			nil, // agent fallback succeeds
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	if _, err := eng.ResumeRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}

	// 3 total runner requests: producer, agent-resume, agent-fallback.
	if len(runner.requests) != 3 {
		t.Fatalf("expected 3 runner requests (producer + agent-resume + agent-fallback), got %d", len(runner.requests))
	}

	// The fallback request is the third (index 2) — not the resume attempt.
	fallbackReq := runner.requests[2]
	if fallbackReq.Resume {
		t.Fatal("expected fallback request to have Resume=false")
	}
	if fallbackReq.SessionID != "" {
		t.Fatalf("expected fallback request to have empty SessionID, got %q", fallbackReq.SessionID)
	}
	// The rebuild closure appends edgePrompt with a "---" separator, so both
	// must appear in the rebuilt prompt.
	if !strings.Contains(fallbackReq.Prompt, edgeSentinel) {
		t.Fatalf("expected fallback prompt to contain edge sentinel %q (edgePrompt incorporated by rebuild closure), got:\n%s", edgeSentinel, fallbackReq.Prompt)
	}
	if !strings.Contains(fallbackReq.Prompt, "---") {
		t.Fatalf("expected fallback prompt to contain '---' separator joining node.Prompt and edgePrompt, got:\n%s", fallbackReq.Prompt)
	}
}

// TestExecuteRole_BackEdgeLoopRendersIterationDeltas is the end-to-end
// integration test for the headline feature of the per-dispatch inputs dir
// refactor: when a role node is re-dispatched via a back-edge loop, its
// second-dispatch prompt must render only the *changed* inputs under
// "## New or updated for this turn (iteration N)" rather than repeating
// the full "## Inputs" block.
//
// Workflow shape:
//
//	producer --[done]--> consumer --[retry]--> producer (back-edge)
//	                           \--[done]--> (end)
//
// producer declares a `feedback` input from consumer's message (optional).
// On the first dispatch, consumer hasn't run yet → feedback=nil.
// On the second dispatch (after consumer returns retry), feedback is
// consumer's message → different from nil → non-empty delta.
func TestExecuteRole_BackEdgeLoopRendersIterationDeltas(t *testing.T) {
	t.Skip("legacy InputRef optional semantics removed; see Task 30b (required-reference satisfiability) for replacement")
	runsDir := t.TempDir()

	const (
		runID            = "run-loop-deltas"
		producerDecDone  = "done"
		consumerDecRetry = "retry"
		consumerDecDone  = "done"
	)

	workflow := &definitions.Workflow{
		ID:               "wf-loop-deltas",
		Name:             "Loop Deltas",
		Version:          1,
		PromptInputsMode: "declared",
		Nodes: []definitions.Node{
			{
				ID:        "producer",
				Kind:      "role",
				Runner:    "test-runner",
				Prompt:    "Produce output.",
				Decisions: definitions.StringDecisions(producerDecDone),
				Inputs: map[string]any{
					"spec":     "input.spec",
					"feedback": "node.consumer.message",
				},
			},
			{
				ID:        "consumer",
				Kind:      "role",
				Runner:    "test-runner",
				Prompt:    "Review the output.",
				Decisions: definitions.StringDecisions(consumerDecRetry, consumerDecDone),
				Inputs: map[string]any{
					"producer": "node.producer",
				},
			},
		},
		Edges: []definitions.Edge{
			{From: "producer", To: "consumer", When: producerDecDone},
			{From: "consumer", To: "producer", When: consumerDecRetry},
		},
		// Allow up to 3 producer executions so the loop isn't exhausted after 2.
		Limits: map[string]int{"max_loop_iterations": 5},
	}

	runInputs := map[string]any{"spec": "spec-content"}
	setupRunForResume(t, runsDir, runID, workflow, runInputs)

	// Sequential runner requests — requests arrive in execution order:
	//   [0] producer dispatch 1  → done, session "sess-prod"
	//   [1] consumer dispatch 1  → retry, message "consumer-v1"
	//   [2] producer dispatch 2  → done, session "sess-prod"
	//   [3] consumer dispatch 2  → done
	runner := &sequentialRunner{
		results: []runners.Result{
			// producer dispatch 1
			{
				Output:    `{"decision":"done","message":"prod-v1","data":{},"artifacts":[]}`,
				SessionID: "sess-prod",
			},
			// consumer dispatch 1 — returns retry so the back-edge fires
			{
				Output:    `{"decision":"retry","message":"consumer-v1","data":{},"artifacts":[]}`,
				SessionID: "",
			},
			// producer dispatch 2 — session "sess-prod" present → resume → deltas
			{
				Output:    `{"decision":"done","message":"prod-v2","data":{},"artifacts":[]}`,
				SessionID: "sess-prod",
			},
			// consumer dispatch 2 — returns done, run completes
			{
				Output:    `{"decision":"done","message":"consumer-done","data":{},"artifacts":[]}`,
				SessionID: "",
			},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	if _, err := eng.ResumeRun(context.Background(), runID); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	if len(runner.requests) != 4 {
		t.Fatalf("expected 4 runner requests (2 producer + 2 consumer), got %d", len(runner.requests))
	}

	// Verify the Dispatches counter for producer.
	statePath := filepath.Join(runsDir, runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	var producerDispatches int
	rs.WithNode("producer", func(n *state.NodeState) { producerDispatches = n.Dispatches })
	if producerDispatches != 2 {
		t.Fatalf("expected producer NodeState.Dispatches=2 after two loop iterations, got %d", producerDispatches)
	}

	// Verify dispatch directories exist for both producer dispatches.
	dispatch1Dir := filepath.Join(runsDir, runID, "dispatches", "producer", "1", "inputs")
	if _, err := os.Stat(dispatch1Dir); err != nil {
		t.Fatalf("expected dispatch-1 dir at %s: %v", dispatch1Dir, err)
	}
	dispatch2Dir := filepath.Join(runsDir, runID, "dispatches", "producer", "2", "inputs")
	if _, err := os.Stat(dispatch2Dir); err != nil {
		t.Fatalf("expected dispatch-2 dir at %s: %v", dispatch2Dir, err)
	}

	// producer's second dispatch prompt (requests[2]) must render the delta
	// block, not the full-inputs block.
	//
	// requests[0] = producer dispatch 1 (full inputs)
	// requests[1] = consumer dispatch 1
	// requests[2] = producer dispatch 2 (delta — the key assertion)
	// requests[3] = consumer dispatch 2
	producerDispatch2Prompt := runner.requests[2].Prompt

	// Deltas block heading must be present.
	if !strings.Contains(producerDispatch2Prompt, "## New or updated for this turn") {
		t.Fatalf("expected '## New or updated for this turn' deltas heading in producer's second dispatch prompt, got:\n%s", producerDispatch2Prompt)
	}
	// The changed input (feedback) must be annotated with the dispatch iteration.
	if !strings.Contains(producerDispatch2Prompt, "(iteration 2)") {
		t.Fatalf("expected '(iteration 2)' tag in producer's second dispatch prompt, got:\n%s", producerDispatch2Prompt)
	}
	// The full-inputs block must NOT appear in the delta rendering.
	if strings.Contains(producerDispatch2Prompt, "## Inputs") {
		t.Fatalf("expected NO '## Inputs' block in producer's second dispatch prompt (delta mode), got:\n%s", producerDispatch2Prompt)
	}

	// Sanity check: producer's first dispatch used the full-inputs block.
	if !strings.Contains(runner.requests[0].Prompt, "## Inputs") {
		t.Fatalf("expected '## Inputs' block in producer's first dispatch prompt, got:\n%s", runner.requests[0].Prompt)
	}
}

// TestParseNodeOutputWithRepair_EmptyOutputUsesIncompleteWorkPrompt verifies
// the bb03 fix: when the agent returns empty output (interrupted), the
// engine uses the softer incomplete-work prompt rather than the harsh
// "respond with JSON only, no tool calls" prompt.
func TestParseNodeOutputWithRepair_EmptyOutputUsesIncompleteWorkPrompt(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-bb03",
		Name:    "BB03 Empty Output Repair",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
	}
	runID := "run-bb03-empty"
	setupRunForResume(t, runsDir, runID, workflow, map[string]any{"spec": "spec content"})

	// First runner result: empty Output (simulating interrupted agent).
	// Second runner result: valid JSON (the repair succeeded).
	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: "", SessionID: "sess-x"},
			{Output: `{"decision":"done","message":"ok","data":{},"artifacts":[]}`, SessionID: "sess-x"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	if _, err := engine.ResumeRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}

	if len(runner.requests) < 2 {
		t.Fatalf("expected at least 2 runner requests (initial + repair), got %d", len(runner.requests))
	}
	// The repair request (index 1) should use the soft prompt.
	repairPrompt := runner.requests[1].Prompt
	if !strings.Contains(repairPrompt, "Your previous turn ended without producing") {
		t.Fatalf("expected soft repair prompt for empty output, got:\n%s", repairPrompt)
	}
	if strings.Contains(repairPrompt, "Do NOT call any tools") {
		t.Fatalf("soft repair prompt should NOT forbid tool calls, got:\n%s", repairPrompt)
	}
}

// TestParseNodeOutputWithRepair_NonEmptyOutputUsesHarshPrompt verifies
// the existing path: when the agent emits content that's not parseable
// as JSON, the engine sends the harsh "respond with JSON only" prompt
// to force immediate classification.
func TestParseNodeOutputWithRepair_NonEmptyOutputUsesHarshPrompt(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-bb03-harsh",
		Name:    "BB03 Non-Empty Output Repair",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
	}
	runID := "run-bb03-harsh"
	setupRunForResume(t, runsDir, runID, workflow, map[string]any{"spec": "spec content"})

	// First runner result: garbage text (not JSON). Second: valid JSON.
	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: "Here is my plan: I will do A, B, C. Done.", SessionID: "sess-x"},
			{Output: `{"decision":"done","message":"ok","data":{},"artifacts":[]}`, SessionID: "sess-x"},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	if _, err := engine.ResumeRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}

	if len(runner.requests) < 2 {
		t.Fatalf("expected at least 2 runner requests, got %d", len(runner.requests))
	}
	repairPrompt := runner.requests[1].Prompt
	if !strings.Contains(repairPrompt, "Do NOT call any tools") {
		t.Fatalf("expected HARSH repair prompt for non-empty output, got:\n%s", repairPrompt)
	}
}

// TestMaterializeFailure_MarksNodeFailed verifies that a materialization
// failure (dir creation blocked by a regular file) marks the node failed.
func TestMaterializeFailure_MarksNodeFailed(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:               "wf-mat-fail",
		Name:             "Materialize Failure",
		Version:          1,
		PromptInputsMode: "all",
		Nodes: []definitions.Node{
			{
				ID: "agent", Kind: "role", Runner: "test-runner",
				Prompt:    "Do work.",
				Decisions: definitions.StringDecisions(testDecisionDone),
			},
		},
	}
	runInputs := map[string]any{"spec": "spec content"}
	runID := "run-mat-fail"
	setupRunForResume(t, runsDir, runID, workflow, runInputs)

	// Force a write failure: pre-create a regular file where the dispatch
	// inputs dir would go, so MkdirAll fails.
	blockerPath := filepath.Join(runsDir, runID, "dispatches", "agent", "1", "inputs")
	if err := os.MkdirAll(filepath.Dir(blockerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockerPath, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &sequentialRunner{}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	engine := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}
	// Run failure expected — don't assert on the error itself; just verify node state.
	_, _ = engine.ResumeRun(context.Background(), runID)

	statePath := filepath.Join(runsDir, runID, "state.json")
	rs, err := state.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	var status string
	rs.WithNode("agent", func(n *state.NodeState) { status = n.Status })
	if status != "failed" {
		t.Fatalf("expected node status='failed', got %q", status)
	}
}
