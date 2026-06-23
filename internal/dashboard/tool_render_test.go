package dashboard

import (
	"strings"
	"testing"
)

func TestRenderTool_UnknownFallsBackToGeneric(t *testing.T) {
	render := RenderTool("unknown_tool", map[string]any{"x": 1, "y": "hello"}, "some output", false)
	if render.Summary == "" {
		t.Errorf("expected a summary, got empty")
	}
	if len(render.Sections) == 0 {
		t.Fatalf("expected at least one section")
	}
	found := map[string]bool{}
	for _, s := range render.Sections {
		found[s.Label] = true
	}
	if !found["Input"] {
		t.Errorf("expected an Input section; got labels: %v", found)
	}
	if !found["Output"] {
		t.Errorf("expected an Output section; got labels: %v", found)
	}
}

func TestRenderTool_ErrorOutputForcesExpanded(t *testing.T) {
	render := RenderTool("unknown_tool", nil, "boom", true)
	var out *ToolSection
	for i := range render.Sections {
		if render.Sections[i].Label == "Output" {
			out = &render.Sections[i]
			break
		}
	}
	if out == nil {
		t.Fatalf("no Output section")
	}
	if !out.IsError {
		t.Errorf("Output section should have IsError=true")
	}
	if out.Collapsed {
		t.Errorf("Output section should be expanded (not collapsed) when error")
	}
}

func TestGuessLanguage_ByExtension(t *testing.T) {
	cases := map[string]string{
		"foo.go":     "go",
		"foo.yaml":   "yaml",
		"foo.yml":    "yaml",
		"foo.json":   "json",
		"foo.ts":     "typescript",
		"foo.tsx":    "typescript",
		"foo.js":     "javascript",
		"foo.py":     "python",
		"foo.sh":     "bash",
		"foo.md":     "markdown",
		"foo.rs":     "rust",
		"foo":        "",
		"":           "",
		"dir/a.go":   "go",
		"a/b/c.yaml": "yaml",
	}
	for path, want := range cases {
		if got := guessLanguage(path); got != want {
			t.Errorf("guessLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

// findSection asserts a section with the given label is present.
func findSection(t *testing.T, r ToolRender, label string) ToolSection {
	t.Helper()
	for _, s := range r.Sections {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("no section %q; got %d sections", label, len(r.Sections))
	return ToolSection{}
}

func bodyContains(s ToolSection, sub string) bool {
	return strings.Contains(string(s.Body), sub)
}

func TestRenderReadFile_PathAndLanguageInference(t *testing.T) {
	for _, name := range []string{"read_file", "Read"} {
		r := RenderTool(name, map[string]any{"path": "internal/foo.go"}, "package foo\n\nfunc Bar() {}\n", false)
		if !strings.Contains(r.Summary, "internal/foo.go") {
			t.Errorf("[%s] expected path in summary, got %q", name, r.Summary)
		}
		out := findSection(t, r, "Output")
		if out.Language != "go" {
			t.Errorf("[%s] expected language=go, got %q", name, out.Language)
		}
		if !bodyContains(out, "package foo") {
			t.Errorf("[%s] expected file content in body; got %q", name, out.Body)
		}
	}
}

func TestRenderWriteFile_SummaryIncludesPathAndLineCount(t *testing.T) {
	content := "line 1\nline 2\nline 3\n"
	for _, name := range []string{"write_file", "Write"} {
		r := RenderTool(name, map[string]any{"path": "a/b.py", "content": content}, "", false)
		if !strings.Contains(r.Summary, "a/b.py") {
			t.Errorf("[%s] expected path in summary, got %q", name, r.Summary)
		}
		if !strings.Contains(r.Summary, "3 lines") {
			t.Errorf("[%s] expected '3 lines' in summary, got %q", name, r.Summary)
		}
		body := findSection(t, r, "Content")
		if body.Language != "python" {
			t.Errorf("[%s] expected language=python, got %q", name, body.Language)
		}
	}
}

func TestRenderBash_UnifiedTerminalBlock(t *testing.T) {
	for _, name := range []string{"bash", "Bash", "shell"} {
		r := RenderTool(name, map[string]any{"command": "ls -la /tmp"}, "total 4\ndrwxr-xr-x  3 root", false)
		if !strings.Contains(r.Summary, "ls -la /tmp") {
			t.Errorf("[%s] expected command in summary, got %q", name, r.Summary)
		}
		if len(r.Sections) != 1 {
			t.Fatalf("[%s] expected 1 section, got %d", name, len(r.Sections))
		}
		s := r.Sections[0]
		if !bodyContains(s, "$ ") || !bodyContains(s, "ls -la /tmp") {
			t.Errorf("[%s] expected '$' prompt + command in body; got %q", name, s.Body)
		}
		if !bodyContains(s, "drwxr-xr-x") {
			t.Errorf("[%s] expected output text in body; got %q", name, s.Body)
		}
	}
}

// TestRenderers_PreserveAllInputValues verifies that every registered renderer
// surfaces every input value somewhere in the rendered output (summary or a
// section body). This guards against silently dropping arguments.
func TestRenderers_PreserveAllInputValues(t *testing.T) {
	cases := []struct {
		tool        string
		input       map[string]any
		valueChecks []string // substrings that must appear somewhere in render
	}{
		{
			"read_file",
			map[string]any{"path": "a.go", "limit": 100, "offset": 5},
			// offset + limit appear in summary as "5→105" (offset→offset+limit)
			[]string{"a.go", "5", "105"},
		},
		{
			"Read",
			map[string]any{"path": "a.go", "pages": "1-5"},
			[]string{"a.go", "1-5"},
		},
		{
			"write_file",
			map[string]any{"path": "a.go", "content": "x", "mode": "overwrite"},
			[]string{"a.go", "overwrite"},
		},
		{
			"edit_file",
			map[string]any{"path": "a.go", "old_string": "OLD", "new_string": "NEW", "replace_all": true},
			[]string{"a.go", "OLD", "NEW", "true"},
		},
		{
			"bash",
			map[string]any{"command": "ls", "timeout": 5000, "run_in_background": true, "description": "list"},
			// run_in_background true -> "background" in summary; description lifted to prelude.
			[]string{"ls", "5000", "background", "list"},
		},
		{
			"grep",
			map[string]any{"pattern": "TODO", "output_mode": "count", "-A": 2, "multiline": true},
			[]string{"TODO", "count", "2", "true"},
		},
		{
			"glob",
			map[string]any{"pattern": "*.go", "path": "/tmp/x"},
			[]string{"*.go", "/tmp/x"},
		},
		{
			"LS",
			map[string]any{"path": "/tmp", "ignore": []any{"*.tmp"}},
			[]string{"/tmp", "*.tmp"},
		},
		{
			"TaskCreate",
			map[string]any{"subject": "SUBJ", "description": "DESC", "activeForm": "Doing"},
			[]string{"SUBJ", "DESC", "Doing"},
		},
		{
			"TaskGet",
			map[string]any{"taskId": "1", "custom_field": "VAL"},
			[]string{"1", "VAL"},
		},
		{"TodoWrite", map[string]any{
			"todos":    []any{map[string]any{"id": "1", "content": "TODO-CONTENT", "status": "pending", "extra": "EXTRA"}},
			"strategy": "REPLACE_STRATEGY",
		}, []string{"TODO-CONTENT", "EXTRA", "REPLACE_STRATEGY"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			r := RenderTool(tc.tool, tc.input, "", false)
			all := r.Summary + " " + string(r.Prelude)
			for _, s := range r.Sections {
				all += " " + s.Label + " " + string(s.Body)
			}
			for _, want := range tc.valueChecks {
				if !strings.Contains(all, want) {
					t.Errorf("tool=%s: value %q missing from render (summary=%q prelude=%q labels=%v)",
						tc.tool, want, r.Summary, r.Prelude, sectionLabels(r))
				}
			}
		})
	}
}

func sectionLabels(r ToolRender) []string {
	out := make([]string, 0, len(r.Sections))
	for _, s := range r.Sections {
		out = append(out, s.Label)
	}
	return out
}

func TestRenderReadFile_SummaryIncludesRangeAndPreludeHoldsPurpose(t *testing.T) {
	r := RenderTool("read_file", map[string]any{
		"path":    "/tmp/x.json",
		"offset":  0,
		"limit":   4000,
		"purpose": "Review the plan.",
	}, `{"k":"v"}`, false)
	if !strings.Contains(r.Summary, "/tmp/x.json") {
		t.Errorf("summary missing path: %q", r.Summary)
	}
	if !strings.Contains(r.Summary, "0") || !strings.Contains(r.Summary, "4000") {
		t.Errorf("summary missing range: %q", r.Summary)
	}
	prelude := string(r.Prelude)
	if !strings.Contains(prelude, "<em>") || !strings.Contains(prelude, "Review the plan.") {
		t.Errorf("prelude missing italicized purpose: %q", prelude)
	}
	// Purpose must NOT appear in any labeled Other Args section
	for _, s := range r.Sections {
		if s.Label == "Other Args" && strings.Contains(string(s.Body), "purpose") {
			t.Errorf("purpose should be lifted to prelude, not in Other Args: %q", s.Body)
		}
	}
}

func TestRenderers_ArgsBeforeResults(t *testing.T) {
	// For tools that emit a "Result" or "Output" section, all non-result
	// sections should render before it.
	cases := []struct {
		tool    string
		input   map[string]any
		output  string
		lastSet []string // set of acceptable last-section labels
	}{
		{"read_file", map[string]any{"path": "a.go", "limit": 100}, "content", []string{"Output"}},
		{"bash", map[string]any{"command": "ls", "description": "list"}, "x", []string{"Terminal", "Output"}},
		{"write_file", map[string]any{"path": "a.py", "content": "x", "mode": "overwrite"}, "written", []string{"Result"}},
		{"edit_file", map[string]any{"path": "a.go", "old_string": "x", "new_string": "y", "replace_all": true}, "ok", []string{"Result"}},
		{"grep", map[string]any{"pattern": "x", "-A": 3}, "match", []string{"Matches"}},
		{"TaskCreate", map[string]any{"subject": "X", "activeForm": "Doing"}, "created", []string{"Result"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			r := RenderTool(tc.tool, tc.input, tc.output, false)
			if len(r.Sections) == 0 {
				t.Fatalf("no sections")
			}
			last := r.Sections[len(r.Sections)-1].Label
			found := false
			for _, w := range tc.lastSet {
				if last == w {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected last section to be one of %v, got %q; all labels=%v",
					tc.lastSet, last, sectionLabels(r))
			}
		})
	}
}

func TestRenderSpawnAgent_TaskListRendersAsList(t *testing.T) {
	input := map[string]any{
		"agent_type": "explorer",
		"task_list": []any{
			map[string]any{"title": "Step One", "prompt": "Do thing", "reasoning_effort": "low"},
			map[string]any{"title": "Step Two", "prompt": "Do other"},
		},
	}
	r := RenderTool("spawn_agent", input, "", false)
	tl := findSection(t, r, "Task List")
	for _, want := range []string{"Step One", "Do thing", "low", "Step Two", "Do other"} {
		if !bodyContains(tl, want) {
			t.Errorf("Task List body missing %q; got %q", want, tl.Body)
		}
	}
	// Must render as a list (ol/li), not as JSON
	if !bodyContains(tl, "<ol") || !bodyContains(tl, "<li") {
		t.Errorf("expected <ol>/<li> task list markup, got %q", tl.Body)
	}
}

func TestRenderCommunicate_EmailLikeSections(t *testing.T) {
	// communicate should read like: <message body>  +  Result: <envelope>  +  Delivery: <ack>
	r := RenderTool("communicate", map[string]any{
		"message":     "Ran the full test suite. It passes.",
		"await_reply": false,
		"output": map[string]any{
			"decision":  "pass",
			"message":   "Full test suite passed via npm test with exit code 0.",
			"artifacts": []any{"path/to/log.txt"},
			"data": map[string]any{
				"command":   "npm test",
				"exit_code": 0,
				"evidence":  "line 1\nline 2\nline 3",
				"counts":    map[string]any{"pass": 30, "fail": 0},
			},
		},
	}, `{"accepted":true,"await_reply":false,"inbox":null}`, false)

	labels := sectionLabels(r)
	// Expect: (unlabeled message) -> "Result" -> optionally "Other Args" -> "Delivery"
	// The test input above has await_reply which becomes an Other Args entry.
	// Verify presence & order of the semantic sections.
	idx := func(label string) int {
		for i, l := range labels {
			if l == label {
				return i
			}
		}
		return -1
	}
	if labels[0] != "" {
		t.Errorf("first section should be unlabeled message, got %q", labels[0])
	}
	if idx("Result") < 0 {
		t.Errorf("missing Result section; got %v", labels)
	}
	if idx("Delivery") < 0 {
		t.Errorf("missing Delivery section; got %v", labels)
	}
	if idx("Result") > idx("Delivery") {
		t.Errorf("Result should come before Delivery; got %v", labels)
	}

	// Message section: prose only, no decision pill here
	msgSection := r.Sections[0]
	if !bodyContains(msgSection, "Ran the full test suite") {
		t.Errorf("expected message prose in unlabeled section; got %q", msgSection.Body)
	}
	if strings.Contains(string(msgSection.Body), "transcript-chip") || strings.Contains(string(msgSection.Body), ">pass<") {
		t.Errorf("message section should not contain decision pill; got %q", msgSection.Body)
	}

	// Result section: decision pill + inner message + data/artifacts
	result := findSection(t, r, "Result")
	if !bodyContains(result, ">pass<") {
		t.Errorf("Result missing pass pill; got %q", result.Body)
	}
	if !bodyContains(result, "Full test suite passed") {
		t.Errorf("Result missing inner NodeOutput message; got %q", result.Body)
	}
	for _, want := range []string{"npm test", "line 1", "log.txt"} {
		if !bodyContains(result, want) {
			t.Errorf("Result missing %q; got %q", want, result.Body)
		}
	}

	// Delivery section: structured ack
	delivery := findSection(t, r, "Delivery")
	for _, want := range []string{"accepted", "no reply expected", "inbox"} {
		if !bodyContains(delivery, want) {
			t.Errorf("Delivery missing %q; got %q", want, delivery.Body)
		}
	}
}

func TestRenderBash_TimeoutInSummary(t *testing.T) {
	// Accept both "timeout" (Claude Code Bash) and "timeout_ms" (serf shell runner)
	for _, key := range []string{"timeout", "timeout_ms"} {
		t.Run(key, func(t *testing.T) {
			r := RenderTool("bash", map[string]any{
				"command":     "ls",
				key:           5000,
				"description": "list files",
			}, "", false)
			if !strings.Contains(r.Summary, "ls") {
				t.Errorf("expected command in summary, got %q", r.Summary)
			}
			if !strings.Contains(r.Summary, "5000") {
				t.Errorf("[%s] expected timeout in summary, got %q", key, r.Summary)
			}
			// description should be lifted to the prelude (italic), not in Other Args.
			prelude := string(r.Prelude)
			if !strings.Contains(prelude, "list files") {
				t.Errorf("expected description in prelude; got %q", prelude)
			}
			for _, s := range r.Sections {
				if s.Label == "Other Args" {
					b := string(s.Body)
					if strings.Contains(b, "description") {
						t.Errorf("description should be lifted; got Other Args=%q", b)
					}
					if strings.Contains(b, key) {
						t.Errorf("[%s] timeout should be in summary, not Other Args=%q", key, b)
					}
				}
			}
		})
	}
}

func TestRenderIntentPrelude_DescriptionLikePurpose(t *testing.T) {
	// Description-only
	r := RenderTool("Glob", map[string]any{
		"pattern":     "*.go",
		"description": "find go files",
	}, "", false)
	if !strings.Contains(string(r.Prelude), "find go files") {
		t.Errorf("expected description in prelude; got %q", r.Prelude)
	}
	// Purpose + description both set; both render.
	r2 := RenderTool("Glob", map[string]any{
		"pattern":     "*.go",
		"purpose":     "Audit module.",
		"description": "find go files",
	}, "", false)
	for _, want := range []string{"Audit module.", "find go files"} {
		if !strings.Contains(string(r2.Prelude), want) {
			t.Errorf("expected %q in prelude; got %q", want, r2.Prelude)
		}
	}
}

func TestRenderCommunicate_DeliveryStandardStructure(t *testing.T) {
	r := RenderTool("communicate",
		map[string]any{"message": "hi", "await_reply": false},
		`{"accepted":true,"await_reply":false,"inbox":null}`,
		false,
	)
	resp := findSection(t, r, "Delivery")
	for _, want := range []string{"accepted", "no reply expected", "inbox"} {
		if !bodyContains(resp, want) {
			t.Errorf("Delivery missing %q; got %q", want, resp.Body)
		}
	}
	if bodyContains(resp, `"accepted":`) {
		t.Errorf("expected structured Delivery, got raw JSON: %q", resp.Body)
	}
}

func TestRenderCommunicate_SummaryDoesNotDuplicateMessage(t *testing.T) {
	r := RenderTool("communicate",
		map[string]any{"message": "This is the message body that would duplicate."},
		"", false,
	)
	if strings.Contains(r.Summary, "message body") {
		t.Errorf("summary should not echo the message; got %q", r.Summary)
	}
}

func TestRenderSpawnAgent_TaskAndResultSections(t *testing.T) {
	input := map[string]any{
		"agent_type": "explorer",
		"task":       "Explore the workspace",
		"task_list":  []any{map[string]any{"prompt": "list files"}},
		"max_turns":  200,
		"model":      "gpt-5.4",
	}
	output := `{"agent_id":"A1","output":"{\"decision\":\"pass\"}","status":"completed","success":true,"transcript":"/tmp/t.jsonl","turns_used":3}`
	r := RenderTool("spawn_agent", input, output, false)
	if r.Summary != "explorer" {
		t.Errorf("expected summary=explorer, got %q", r.Summary)
	}
	task := findSection(t, r, "Task")
	if !bodyContains(task, "Explore") {
		t.Errorf("Task section missing prose: %q", task.Body)
	}
	tl := findSection(t, r, "Task List")
	if !bodyContains(tl, "list files") {
		t.Errorf("Task List missing content: %q", tl.Body)
	}
	other := findSection(t, r, "Other Args")
	if !bodyContains(other, "max_turns") || !bodyContains(other, "gpt-5.4") {
		t.Errorf("Other Args missing config: %q", other.Body)
	}
	meta := findSection(t, r, "Result Metadata")
	for _, want := range []string{"A1", "completed", "true", "3"} {
		if !bodyContains(meta, want) {
			t.Errorf("Result Metadata missing %q: %q", want, meta.Body)
		}
	}
	ao := findSection(t, r, "Agent Output")
	// The structured renderer produces a decision pill with the value, not
	// the literal string "decision".
	if !bodyContains(ao, ">pass<") {
		t.Errorf("Agent Output missing pass pill: %q", ao.Body)
	}
}

func TestRenderCommunicate_OutputAsNestedMap(t *testing.T) {
	// The serf runner parses arguments_json, so "output" arrives as a nested
	// map[string]any. The Result section should render it structurally.
	r := RenderTool("communicate", map[string]any{
		"await_reply": false,
		"message":     "done",
		"output":      map[string]any{"decision": "pass", "count": 5},
	}, "", false)
	out := findSection(t, r, "Result")
	if !bodyContains(out, ">pass<") {
		t.Errorf("expected pass decision pill; got %q", out.Body)
	}
	if !strings.Contains(string(out.Body), "5") {
		t.Errorf("expected count=5 preserved; got %q", out.Body)
	}
	if bodyContains(out, "map[") {
		t.Errorf("map was stringified as Go-fmt not JSON; got %q", out.Body)
	}
}

func TestRenderCommunicate_OutputNoNodeOutputShapeFallsBackToJSON(t *testing.T) {
	// If the output doesn't have decision/message/data/artifacts, still
	// rendered under "Result" but as raw JSON rather than the structured form.
	r := RenderTool("communicate", map[string]any{
		"message": "done",
		"output":  map[string]any{"arbitrary": "payload"},
	}, "", false)
	out := findSection(t, r, "Result")
	if !bodyContains(out, "arbitrary") || !bodyContains(out, "payload") {
		t.Errorf("expected arbitrary payload in Result; got %q", out.Body)
	}
}

func TestRenderCommunicate_StringOutputStillGetsStructured(t *testing.T) {
	// String "output" with NodeOutput JSON is parsed and rendered structurally.
	r := RenderTool("communicate", map[string]any{
		"await_reply": false,
		"message":     "# Result\n\nAll tests pass.",
		"output":      `{"decision":"pass","data":{"count":5}}`,
	}, "", false)
	result := findSection(t, r, "Result")
	if !bodyContains(result, ">pass<") {
		t.Errorf("expected pass decision pill in Result; got %q", result.Body)
	}
}

func TestRenderBash_LongCommandTruncatedInSummary(t *testing.T) {
	cmd := strings.Repeat("a ", 200)
	r := RenderTool("bash", map[string]any{"command": cmd}, "", false)
	if len(r.Summary) > 100 {
		t.Errorf("expected truncated summary, got len=%d", len(r.Summary))
	}
}

func TestRenderGrep_PatternAndPath(t *testing.T) {
	input := map[string]any{"pattern": "TODO", "path": "src/", "glob": "*.go"}
	for _, name := range []string{"grep", "Grep"} {
		r := RenderTool(name, input, "src/a.go:42:// TODO thing\n", false)
		if !strings.Contains(r.Summary, "TODO") {
			t.Errorf("[%s] expected pattern in summary, got %q", name, r.Summary)
		}
		if !strings.Contains(r.Summary, "*.go") {
			t.Errorf("[%s] expected glob in summary, got %q", name, r.Summary)
		}
		out := findSection(t, r, "Matches")
		if !bodyContains(out, "TODO thing") {
			t.Errorf("[%s] expected matches in body; got %q", name, out.Body)
		}
	}
}

func TestRenderGlob_PatternInSummary(t *testing.T) {
	for _, name := range []string{"glob", "Glob"} {
		r := RenderTool(name, map[string]any{"pattern": "**/*.go"}, "a.go\nb.go\n", false)
		if !strings.Contains(r.Summary, "**/*.go") {
			t.Errorf("[%s] expected pattern in summary, got %q", name, r.Summary)
		}
		out := findSection(t, r, "Files")
		if !bodyContains(out, "a.go") {
			t.Errorf("[%s] expected file list in body; got %q", name, out.Body)
		}
	}
}

func TestRenderLS_PathInSummary(t *testing.T) {
	r := RenderTool("LS", map[string]any{"path": "/tmp/x"}, "a.txt\nb.txt\n", false)
	if !strings.Contains(r.Summary, "/tmp/x") {
		t.Errorf("expected path in summary, got %q", r.Summary)
	}
	out := findSection(t, r, "Entries")
	if !bodyContains(out, "a.txt") {
		t.Errorf("expected entries in body; got %q", out.Body)
	}
}

func TestRenderTaskCreate_SubjectInSummary(t *testing.T) {
	r := RenderTool("TaskCreate", map[string]any{
		"subject":     "Add caching layer",
		"description": "Detailed ask.",
	}, "", false)
	if !strings.Contains(r.Summary, "Add caching layer") {
		t.Errorf("expected subject in summary, got %q", r.Summary)
	}
	s := findSection(t, r, "Changes")
	if !bodyContains(s, "Add caching layer") {
		t.Errorf("expected subject in Changes; got %q", s.Body)
	}
}

func TestRenderTaskUpdate_ShowsChangedFields(t *testing.T) {
	r := RenderTool("TaskUpdate", map[string]any{
		"taskId":  "1",
		"status":  "in_progress",
		"owner":   "alice",
		"subject": "Updated title",
	}, "", false)
	if !strings.Contains(r.Summary, "1") {
		t.Errorf("expected task id in summary, got %q", r.Summary)
	}
	s := findSection(t, r, "Changes")
	for _, sub := range []string{"status", "in_progress", "owner", "alice", "subject", "Updated title"} {
		if !bodyContains(s, sub) {
			t.Errorf("expected %q in Changes body; got %q", sub, s.Body)
		}
	}
}

func TestRenderTodoWrite_ListsTasks(t *testing.T) {
	input := map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "do a thing", "status": "pending"},
			map[string]any{"id": "2", "content": "do another", "status": "in_progress"},
		},
	}
	r := RenderTool("TodoWrite", input, "", false)
	if !strings.Contains(r.Summary, "2") {
		t.Errorf("expected task count in summary, got %q", r.Summary)
	}
	s := findSection(t, r, "Changes")
	for _, sub := range []string{"do a thing", "pending", "do another", "in_progress"} {
		if !bodyContains(s, sub) {
			t.Errorf("expected %q in Changes body; got %q", sub, s.Body)
		}
	}
}

func TestRenderTaskGet_IdInSummary(t *testing.T) {
	r := RenderTool("TaskGet", map[string]any{"taskId": "42"}, "", false)
	if !strings.Contains(r.Summary, "42") {
		t.Errorf("expected task id in summary, got %q", r.Summary)
	}
}

func TestRenderEdit_ProducesUnifiedDiff(t *testing.T) {
	input := map[string]any{
		"path":       "file.go",
		"old_string": "hello\nworld\n",
		"new_string": "hello\nWORLD\n",
	}
	for _, name := range []string{"edit_file", "Edit"} {
		r := RenderTool(name, input, "", false)
		if !strings.Contains(r.Summary, "file.go") {
			t.Errorf("[%s] expected path in summary, got %q", name, r.Summary)
		}
		diff := findSection(t, r, "Diff")
		if !bodyContains(diff, "-world") {
			t.Errorf("[%s] expected -world in diff; got %q", name, diff.Body)
		}
		if !bodyContains(diff, "+WORLD") {
			t.Errorf("[%s] expected +WORLD in diff; got %q", name, diff.Body)
		}
	}
}
