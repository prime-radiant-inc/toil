package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestMaterializeDispatchInputs_WritesAllInputs(t *testing.T) {
	dir := t.TempDir()
	inputsDir := filepath.Join(dir, "dispatches", "node-a", "1", "inputs")
	inputs := map[string]any{
		"plan": map[string]any{"tasks": []any{"task-1", "task-2"}},
		"spec": "# Spec\n\nDo the thing.",
	}
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatal(err)
	}
	planBytes, err := os.ReadFile(filepath.Join(inputsDir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var planDecoded map[string]any
	if err := json.Unmarshal(planBytes, &planDecoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := planDecoded["tasks"]; !ok {
		t.Fatal("plan.json missing 'tasks' key")
	}
	specBytes, err := os.ReadFile(filepath.Join(inputsDir, "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(specBytes), "Do the thing") {
		t.Fatalf("spec.md missing content: %s", specBytes)
	}
}

func TestMaterializeDispatchInputs_Idempotent(t *testing.T) {
	dir := t.TempDir()
	inputsDir := filepath.Join(dir, "inputs")
	inputs := map[string]any{"plan": map[string]any{"tasks": []any{"a"}}}
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(inputsDir, "plan.json"))
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(inputsDir, "plan.json"))
	if !bytes.Equal(first, second) {
		t.Fatalf("expected identical bytes after second materialize, got:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMaterializeDispatchInputs_RecoversFromPartialPriorWrite(t *testing.T) {
	dir := t.TempDir()
	inputsDir := filepath.Join(dir, "inputs")
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputsDir, "plan.json"), []byte("partial garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]any{
		"plan": map[string]any{"tasks": []any{"task-1"}},
		"spec": "real spec content",
	}
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatal(err)
	}
	planBytes, _ := os.ReadFile(filepath.Join(inputsDir, "plan.json"))
	if strings.Contains(string(planBytes), "partial garbage") {
		t.Fatal("expected plan.json to be overwritten, still contains garbage")
	}
	if !strings.Contains(string(planBytes), "task-1") {
		t.Fatalf("expected plan.json to contain new content, got: %s", planBytes)
	}
	specBytes, err := os.ReadFile(filepath.Join(inputsDir, "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(specBytes) != "real spec content" {
		t.Fatalf("expected spec.md to be created with new content, got: %s", specBytes)
	}
}

func TestMaterializeDispatchInputs_HandlesNestedKey(t *testing.T) {
	dir := t.TempDir()
	inputsDir := filepath.Join(dir, "inputs")
	inputs := map[string]any{"sub/plan": map[string]any{"x": 1}}
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inputsDir, "sub", "plan.json")); err != nil {
		t.Fatalf("expected nested file to exist: %v", err)
	}
}

func TestMaterializeDispatchInputs_ContinuesPastUnmarshallableValue(t *testing.T) {
	dir := t.TempDir()
	inputsDir := filepath.Join(dir, "inputs")
	inputs := map[string]any{
		"plan": map[string]any{"ok": true},
		"bad":  make(chan int), // json.Marshal fails on channels
	}
	if err := MaterializeDispatchInputs(inputsDir, inputs); err != nil {
		t.Fatalf("expected no error from per-key tolerance, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inputsDir, "plan.json")); err != nil {
		t.Fatalf("expected plan.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inputsDir, "bad.json")); err == nil {
		t.Fatal("expected bad.json to be absent (serialization failed)")
	}
}

func TestDiffAgainstPriorDispatch_UnchangedKeyOmitted(t *testing.T) {
	dir := t.TempDir()
	priorDir := filepath.Join(dir, "1", "inputs")
	inputs := map[string]any{"plan": map[string]any{"tasks": []any{"a"}}}
	if err := MaterializeDispatchInputs(priorDir, inputs); err != nil {
		t.Fatal(err)
	}
	deltas := DiffAgainstPriorDispatch(inputs, priorDir)
	if len(deltas) != 0 {
		t.Fatalf("expected no deltas for unchanged inputs, got: %v", deltas)
	}
}

func TestDiffAgainstPriorDispatch_ChangedKeyEmitted(t *testing.T) {
	dir := t.TempDir()
	priorDir := filepath.Join(dir, "1", "inputs")
	if err := MaterializeDispatchInputs(priorDir, map[string]any{"plan": map[string]any{"v": 1}}); err != nil {
		t.Fatal(err)
	}
	deltas := DiffAgainstPriorDispatch(map[string]any{"plan": map[string]any{"v": 2}}, priorDir)
	if len(deltas) != 1 || deltas["plan"] == nil {
		t.Fatalf("expected plan in deltas, got: %v", deltas)
	}
}

func TestDiffAgainstPriorDispatch_NewKeyEmitted(t *testing.T) {
	dir := t.TempDir()
	priorDir := filepath.Join(dir, "1", "inputs")
	if err := MaterializeDispatchInputs(priorDir, map[string]any{"plan": map[string]any{"v": 1}}); err != nil {
		t.Fatal(err)
	}
	deltas := DiffAgainstPriorDispatch(map[string]any{
		"plan":    map[string]any{"v": 1},
		"new_key": "added this turn",
	}, priorDir)
	if len(deltas) != 1 || deltas["new_key"] == nil {
		t.Fatalf("expected only new_key in deltas, got: %v", deltas)
	}
}

func TestDiffAgainstPriorDispatch_MissingPriorDirAllChanged(t *testing.T) {
	inputs := map[string]any{"plan": map[string]any{"v": 1}, "spec": "text"}
	deltas := DiffAgainstPriorDispatch(inputs, "/nonexistent/path")
	if len(deltas) != 2 {
		t.Fatalf("expected all keys as deltas when prior dir missing, got: %v", deltas)
	}
}

func TestDiffAgainstPriorDispatch_OscillationDetected(t *testing.T) {
	dir := t.TempDir()
	dispatch1 := filepath.Join(dir, "1", "inputs")
	dispatch2 := filepath.Join(dir, "2", "inputs")
	v1 := map[string]any{"plan": "v1"}
	v2 := map[string]any{"plan": "v2"}
	if err := MaterializeDispatchInputs(dispatch1, v1); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeDispatchInputs(dispatch2, v2); err != nil {
		t.Fatal(err)
	}
	// Now current value oscillates back to v1; diff against dispatch 2 (the most recent).
	deltas := DiffAgainstPriorDispatch(v1, dispatch2)
	if len(deltas) != 1 || deltas["plan"] != "v1" {
		t.Fatalf("expected plan=v1 as delta against dispatch 2 (v2), got: %v", deltas)
	}
}

func TestPreviewInput_SmallContentFullyInlined(t *testing.T) {
	content := []byte("short content")
	preview := previewInput("plan", content, "plan.md", "/tmp/inputs/plan.md")
	if !strings.Contains(preview, "short content") {
		t.Fatalf("expected full content inlined, got: %s", preview)
	}
	if strings.Contains(preview, "…truncated") {
		t.Fatalf("expected no truncation marker for small content, got: %s", preview)
	}
}

func TestPreviewInput_LargeContentTruncated(t *testing.T) {
	content := bytes.Repeat([]byte("x"), inputPreviewBytes*2)
	preview := previewInput("plan", content, "plan.md", "/tmp/inputs/plan.md")
	if !strings.Contains(preview, "…truncated; read /tmp/inputs/plan.md") {
		t.Fatalf("expected truncation marker with file path, got: %s", preview)
	}
}

func TestPreviewInput_UTF8CodepointBoundary(t *testing.T) {
	// Build a string where byte 1024 falls in the middle of a multi-byte rune.
	prefix := bytes.Repeat([]byte("a"), inputPreviewBytes-1)
	// Append a multi-byte rune (e.g., '€' = 0xE2 0x82 0xAC, 3 bytes)
	content := append([]byte{}, prefix...)
	content = append(content, []byte("€xx")...)
	preview := previewInput("plan", content, "plan.md", "/tmp/inputs/plan.md")
	// The preview should NOT contain a partial '€' — truncation backed off to a rune boundary.
	if !utf8.ValidString(preview) {
		t.Fatalf("expected valid UTF-8 in preview output, got bytes that don't form valid runes")
	}
}

func TestPickFence_HandlesBacktickContent(t *testing.T) {
	plain := []byte("no backticks here")
	if fence := pickFence(plain); fence != "```" {
		t.Fatalf("expected ``` for plain content, got: %q", fence)
	}
	triple := []byte("text with ``` triple backticks")
	if fence := pickFence(triple); fence != "````" {
		t.Fatalf("expected 4 backticks for content with 3 backticks, got: %q", fence)
	}
	five := []byte("text with ````` five backticks")
	if fence := pickFence(five); fence != "``````" {
		t.Fatalf("expected 6 backticks for content with 5 backticks, got: %q", fence)
	}
}

func TestComposePromptWithInputViews_NilDeltasRendersFullInputs(t *testing.T) {
	inputs := map[string]any{"plan": "content"}
	prompt, err := ComposePromptWithInputViews(
		"role", "node", inputs, inputs, nil,
		"", nil, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## Inputs") {
		t.Fatalf("expected '## Inputs' for nil deltas, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## New or updated for this turn") {
		t.Fatal("expected no deltas block for nil deltas")
	}
}

func TestComposePromptWithInputViews_EmptyDeltasRendersNoChangesNotice(t *testing.T) {
	inputs := map[string]any{"plan": "content"}
	prompt, err := ComposePromptWithInputViews(
		"role", "node", inputs, inputs, nil,
		"/tmp/dispatch/2/inputs", map[string]any{}, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## New or updated for this turn") {
		t.Fatalf("expected '## New or updated' for empty deltas, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "No changes since the prior turn") {
		t.Fatalf("expected 'No changes since the prior turn' notice, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## Inputs") {
		t.Fatal("expected no full-inputs block for empty deltas")
	}
}

func TestComposePromptWithInputViews_NonEmptyDeltasRendersIteration(t *testing.T) {
	inputs := map[string]any{"plan": "v3"}
	deltas := map[string]any{"plan": "v3"}
	prompt, err := ComposePromptWithInputViews(
		"role", "node", inputs, inputs, nil,
		"/tmp/dispatch/3/inputs", deltas, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "### plan (iteration 3)") {
		t.Fatalf("expected '### plan (iteration 3)' heading, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "file: /tmp/dispatch/3/inputs/plan.md") {
		t.Fatalf("expected full file path 'file: /tmp/dispatch/3/inputs/plan.md' reference, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## Inputs") {
		t.Fatal("expected no full-inputs block for non-empty deltas")
	}
}

func TestPhase1ResolvesNodeInputsWithoutInputNamespace(t *testing.T) {
	ctx := &RunContext{
		RunID:  "r1",
		Inputs: map[string]any{"task": map[string]any{"id": "T1"}},
		Outputs: map[string]NodeOutput{
			"x": {Decision: "ok", Data: map[string]any{"foo": "bar"}},
		},
	}
	nodeInputs := map[string]any{
		"task":       "${workflow_input.task}",
		"prior_data": "${node.x.data}",
		"literal":    42,
	}
	resolved, err := evaluatePhase1(ctx, nodeInputs)
	if err != nil {
		t.Fatalf("evaluatePhase1: %v", err)
	}
	if got := resolved["task"]; !reflect.DeepEqual(got, map[string]any{"id": "T1"}) {
		t.Errorf("task=%v", got)
	}
	if got := resolved["prior_data"]; !reflect.DeepEqual(got, map[string]any{"foo": "bar"}) {
		t.Errorf("prior_data=%v", got)
	}
	if got := resolved["literal"]; got != 42 {
		t.Errorf("literal=%v want 42", got)
	}
}

func TestPhase3MergeOrderEdgePassesWins(t *testing.T) {
	workflow := map[string]any{"task": "A", "shared": "from_workflow"}
	node := map[string]any{"shared": "from_node", "node_only": 1}
	edge := map[string]any{"shared": "from_edge", "edge_only": 2}
	merged := mergeDispatchInputs(workflow, node, edge)
	want := map[string]any{
		"task":      "A",
		"shared":    "from_edge",
		"node_only": 1,
		"edge_only": 2,
	}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("merged=%v want %v", merged, want)
	}
}

func TestPhase5InputReadsMergedMap(t *testing.T) {
	base := &RunContext{
		RunID:  "r1",
		Inputs: map[string]any{"task": "from_workflow"},
		Outputs: map[string]NodeOutput{
			"x": {Decision: "ok", Message: "hello"},
		},
	}
	nodeInputs := map[string]any{"local_msg": "${node.x.message}"}
	edgePasses := map[string]any{"task": "from_edge_passes"}

	resolvedNode, err := evaluatePhase1(base, nodeInputs)
	if err != nil {
		t.Fatalf("phase1: %v", err)
	}
	resolvedEdge, err := evaluatePhase2(base, edgePasses)
	if err != nil {
		t.Fatalf("phase2: %v", err)
	}
	merged := mergeDispatchInputs(base.Inputs, resolvedNode, resolvedEdge)

	dispatchCtx := dispatchContext(base, merged)
	got, err := dispatchCtx.Resolve("input.task")
	if err != nil {
		t.Fatalf("Resolve(input.task): %v", err)
	}
	if got != "from_edge_passes" {
		t.Errorf("input.task=%v want from_edge_passes", got)
	}
	got, err = dispatchCtx.Resolve("input.local_msg")
	if err != nil {
		t.Fatalf("Resolve(input.local_msg): %v", err)
	}
	if got != "hello" {
		t.Errorf("input.local_msg=%v want hello", got)
	}
}

func TestDispatchContextReplacesRunContextInputs(t *testing.T) {
	// After dispatchContext, runContext.Resolve("input.X") reads the
	// merged dispatch map, not the original workflow inputs.
	base := &RunContext{
		RunID:  "r1",
		Inputs: map[string]any{"k": "workflow_value"},
	}
	merged := map[string]any{"k": "merged_value", "extra": "more"}
	dc := dispatchContext(base, merged)

	// dispatchContext is a clone — base is unchanged.
	if got := base.Inputs["k"]; got != "workflow_value" {
		t.Errorf("base.Inputs mutated: got %v", got)
	}

	// The clone's resolves read merged values.
	got, err := dc.Resolve("input.k")
	if err != nil {
		t.Fatalf("Resolve(input.k): %v", err)
	}
	if got != "merged_value" {
		t.Errorf("input.k=%v want merged_value", got)
	}
	got, err = dc.Resolve("input.extra")
	if err != nil {
		t.Fatalf("Resolve(input.extra): %v", err)
	}
	if got != "more" {
		t.Errorf("input.extra=%v want more", got)
	}
}

func TestDispatchContextVisibleToResolveSession(t *testing.T) {
	// session_id field resolves at dispatch via runContext.Resolve.
	// After the phase pipeline, a session_id like "${input.X}" where X
	// is supplied by the merged map (e.g., a node input) should work.
	wf := &definitions.Workflow{
		ID: "tw", Name: "tw", Version: 1,
		Nodes: []definitions.Node{
			{
				ID:        "a",
				Kind:      "role",
				Runner:    "shell",
				SessionID: "${input.computed_session}",
				Inputs: map[string]any{
					"computed_session": "${workflow_input.session}",
				},
				Decisions: definitions.DecisionList{{ID: "ok"}},
			},
		},
	}
	_ = wf // workflow struct validates the test scenario; resolution uses RunContext directly
	rs := state.NewRunState("r1", "tw", map[string]any{"session": "sess-42"})
	ctx := &RunContext{
		RunID:   "r1",
		Inputs:  rs.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	ctx.PopulateEnv(rs.Env)

	// Verify the phase pipeline produces what session_id resolution needs.
	resolved, err := evaluatePhase1(ctx, wf.Nodes[0].Inputs)
	if err != nil {
		t.Fatalf("phase1: %v", err)
	}
	merged := mergeDispatchInputs(ctx.Inputs, resolved, nil)
	dispatchCtx := dispatchContext(ctx, merged)
	// Now ${input.computed_session} on dispatchCtx should resolve to "sess-42".
	got, err := dispatchCtx.Resolve("input.computed_session")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "sess-42" {
		t.Errorf("got %v want sess-42 — dispatchCtx must expose merged inputs", got)
	}
}

func TestExecuteSingle_ForEachExtrasVisibleToPhase1(t *testing.T) {
	// The ForEach orchestrator injects the iteration item as extraInputs.
	// The body node's Inputs block must be able to read it via
	// ${workflow_input.<item>} in Phase 1 — same as the legacy
	// resolveInputs behavior.
	base := &RunContext{
		RunID:  "r1",
		Inputs: map[string]any{"base": "from_workflow"},
	}
	extras := map[string]any{
		"task": map[string]any{"id": "T1", "worktree": "/tmp/wt-T1"},
	}
	nodeInputs := map[string]any{
		"task_id":  "${workflow_input.task.id}",
		"worktree": "${workflow_input.task.worktree}",
		"base":     "${workflow_input.base}",
	}

	// Simulate the executeSingle Phase 1 path: clone runContext with
	// extras merged into Inputs, then evaluatePhase1.
	phase1Ctx := base
	if len(extras) > 0 {
		cloned := *base
		mergedInputs := make(map[string]any, len(base.Inputs)+len(extras))
		for k, v := range base.Inputs {
			mergedInputs[k] = v
		}
		for k, v := range extras {
			mergedInputs[k] = v
		}
		cloned.Inputs = mergedInputs
		phase1Ctx = &cloned
	}
	resolved, err := evaluatePhase1(phase1Ctx, nodeInputs)
	if err != nil {
		t.Fatalf("evaluatePhase1: %v", err)
	}
	if resolved["task_id"] != "T1" {
		t.Errorf("task_id=%v want T1", resolved["task_id"])
	}
	if resolved["worktree"] != "/tmp/wt-T1" {
		t.Errorf("worktree=%v want /tmp/wt-T1", resolved["worktree"])
	}
	if resolved["base"] != "from_workflow" {
		t.Errorf("base=%v want from_workflow", resolved["base"])
	}
}
