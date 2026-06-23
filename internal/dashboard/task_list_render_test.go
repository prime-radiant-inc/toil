package dashboard

import (
	"strings"
	"testing"
	"time"
)

// Real payloads taken from run kestrel-winter-lagoon (write_tests).

func TestRenderTaskList_Append_ShowsTasksWithDescriptionAndPrompt(t *testing.T) {
	input := map[string]any{
		"action": "append",
		"tasks": []any{
			map[string]any{
				"depends_on":       []any{},
				"description":      "Map criteria coverage",
				"prompt":           "Review the task acceptance criteria and existing tests.",
				"reasoning_effort": "medium",
				"type":             "research",
			},
			map[string]any{
				"depends_on":       []any{float64(1)},
				"description":      "Add model tests",
				"prompt":           "Create or update tests so every acceptance criterion has coverage.",
				"reasoning_effort": "high",
				"type":             "implement",
			},
		},
		"updates": []any{},
	}
	r := RenderTool("task_list", input, "Added 2 task(s). Progress: 0/2 tasks complete.", false)

	if !strings.Contains(strings.ToLower(r.Summary), "append") && !strings.Contains(r.Summary, "2") {
		t.Errorf("expected summary to mention action or count, got %q", r.Summary)
	}

	tasks := findSection(t, r, "New (2)")
	body := string(tasks.Body)
	for _, want := range []string{
		"Map criteria coverage",
		"Add model tests",
		"Review the task acceptance criteria",
		"Create or update tests",
		"research",
		"implement",
		"medium",
		"high",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Tasks body missing %q;\n---body---\n%s", want, body)
		}
	}
	if !strings.Contains(body, "1") {
		t.Errorf("expected depends_on=1 marker to appear in body;\n---body---\n%s", body)
	}
	// Each append row must render an unchecked checkbox — new tasks are
	// implicitly undone.
	if strings.Count(body, "☐") < len(input["tasks"].([]any)) {
		t.Errorf("expected one ☐ checkbox per appended task; body:\n%s", body)
	}
	// There must be a clear "new" visual affordance: a green "+" marker
	// distinguishes these rows from other list rendering.
	if !strings.Contains(body, "+") {
		t.Errorf("expected a + 'new' marker in append rendering; body:\n%s", body)
	}
}

func TestRenderTaskList_Update_ShowsStatusTransitions(t *testing.T) {
	input := map[string]any{
		"action": "update",
		"tasks":  []any{},
		"updates": []any{
			map[string]any{
				"id":               float64(1),
				"status":           "done",
				"notes":            "Reviewed acceptance criteria.",
				"reasoning_effort": "medium",
			},
			map[string]any{
				"id":     float64(2),
				"status": "in_progress",
			},
		},
	}
	r := RenderTool("task_list", input, "Updated 2 task(s).", false)

	changes := findSection(t, r, "Changes")
	body := string(changes.Body)
	for _, want := range []string{
		"#1",
		"done",
		"Reviewed acceptance criteria",
		"#2",
		"in_progress",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Changes body missing %q;\n---body---\n%s", want, body)
		}
	}
}

func TestRenderTaskList_Update_UsesToolStateForDescriptions(t *testing.T) {
	// With a post-mutation tool_state snapshot attached to the item, the
	// update rows must include the task title from state without any
	// cross-event plumbing.
	input := map[string]any{
		"action": "update",
		"tasks":  []any{},
		"updates": []any{
			map[string]any{"id": float64(1), "status": "done"},
		},
	}
	state := []any{
		map[string]any{"id": float64(1), "description": "Map criteria coverage", "status": "done"},
		map[string]any{"id": float64(2), "description": "Add model tests", "status": "open"},
	}
	r := RenderTaskListWithState(input, "", false, state)

	changes := findSection(t, r, "Changes")
	body := string(changes.Body)
	if !strings.Contains(body, "Map criteria coverage") {
		t.Errorf("expected description from tool_state in update body;\n---body---\n%s", body)
	}
}

func TestRenderTaskList_Append_WithState_ShowsFullList(t *testing.T) {
	// When tool_state is present on an append, the renderer should show
	// the authoritative post-append list (with ids) alongside the
	// "+N new" highlight.
	input := map[string]any{
		"action": "append",
		"tasks": []any{
			map[string]any{"description": "second", "prompt": "p2", "type": "implement"},
		},
		"updates": []any{},
	}
	state := []any{
		map[string]any{"id": float64(1), "description": "first", "status": "done"},
		map[string]any{"id": float64(2), "description": "second", "status": "open"},
	}
	r := RenderTaskListWithState(input, "Added 1 task.", false, state)

	body := ""
	for _, s := range r.Sections {
		body += string(s.Body)
	}
	if !strings.Contains(body, "first") {
		t.Errorf("expected pre-existing task 'first' to appear in full list;\nbody:\n%s", body)
	}
	if !strings.Contains(body, "second") {
		t.Errorf("expected new task 'second' to appear;\nbody:\n%s", body)
	}
}

func TestParseSerfEvent_ToolCallEnd_CapturesToolState(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := `{"kind":"TOOL_CALL_END","timestamp":"2026-01-01T00:00:00Z","session_id":"s1","data":{"tool_name":"task_list","call_id":"c1","output":"ok","tool_state":[{"id":1,"description":"first","status":"done"}]}}`
	items := ExtractTranscriptItems(input, ts)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ToolState == nil {
		t.Fatalf("expected ToolState to be populated on tool_list end event; item=%+v", items[0])
	}
	arr, ok := items[0].ToolState.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected ToolState to be a 1-element slice; got %T %v", items[0].ToolState, items[0].ToolState)
	}
}

func TestMergeTranscriptItems_CarriesToolStateFromEndToMerged(t *testing.T) {
	items := []TranscriptItem{
		{Kind: transcriptKindTool, ToolUseID: "c1", ToolName: "task_list", Input: map[string]any{"action": "update"}},
		{Kind: transcriptKindTool, ToolUseID: "c1", Output: "ok", ToolState: []any{
			map[string]any{"id": float64(1), "description": "first"},
		}},
	}
	merged := MergeTranscriptItems(items)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged item, got %d", len(merged))
	}
	if merged[0].ToolState == nil {
		t.Fatalf("expected merged item to inherit ToolState from end")
	}
	if merged[0].Output != "ok" {
		t.Errorf("expected Output 'ok', got %q", merged[0].Output)
	}
}

func TestRenderTaskList_View_RendersCurrentList(t *testing.T) {
	// Output for view typically contains a formatted list; the renderer
	// should expose it in an "Output" or "Tasks" section without showing
	// the empty `tasks`/`updates` arrays as generic KV table noise.
	input := map[string]any{
		"action":  "view",
		"tasks":   []any{},
		"updates": []any{},
	}
	output := "1. [x] Map criteria coverage — done\n2. [ ] Add model tests — undone\n"
	r := RenderTool("task_list", input, output, false)

	// Must not leak the empty arrays as a generic Input KV table.
	for _, s := range r.Sections {
		if s.Label == "Input" && strings.Contains(string(s.Body), "tasks") {
			t.Errorf("view action should not render an Input KV table with empty tasks/updates; got body: %s", string(s.Body))
		}
	}

	// Must expose the view output verbatim somewhere visible.
	found := false
	for _, s := range r.Sections {
		if strings.Contains(string(s.Body), "Map criteria coverage") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected view output content to appear in some section; got %d sections", len(r.Sections))
	}
}

func TestRenderTaskList_AllActionsRegistered(t *testing.T) {
	// Regression: task_list must be wired into the renderer registry.
	if _, ok := toolRenderers["task_list"]; !ok {
		t.Fatalf("expected task_list to be registered in toolRenderers")
	}
}
