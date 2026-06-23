package dashboard

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// Serf task statuses as seen in tool_state snapshots and update inputs.
const (
	taskStatusDone       = "done"
	taskStatusInProgress = "in_progress"
	taskStatusOpen       = "open"
	taskStatusUndone     = "undone"
	taskStatusCancelled  = "cancelled"
)

func init() {
	// task_list needs the tool_state snapshot, so it goes through
	// RenderToolWithState rather than the simple toolRenderers map. We
	// register a stub that forwards to renderTaskList with nil state so
	// direct callers of RenderTool (tests, legacy code paths) still get a
	// reasonable rendering.
	toolRenderers[fieldTaskList] = func(input map[string]any, output string, isError bool) ToolRender {
		return renderTaskList(input, output, isError, nil)
	}
}

// RenderToolWithState dispatches to a renderer that can consume a serf
// tool_state snapshot (currently only task_list needs this). Unknown tools
// fall through to the stateless RenderTool.
func RenderToolWithState(name string, input map[string]any, output string, isError bool, state any) ToolRender {
	if name == fieldTaskList {
		return renderTaskList(input, output, isError, state)
	}
	return RenderTool(name, input, output, isError)
}

// RenderTaskListWithState is the public entry point for tests and other
// callers that want to render a task_list event with an explicit state
// snapshot.
func RenderTaskListWithState(input map[string]any, output string, isError bool, state any) ToolRender {
	return renderTaskList(input, output, isError, state)
}

// renderTaskList dispatches on the action field (append|update|view) to a
// rich, checklist-native layout. The optional state snapshot (from
// tool_state on TOOL_CALL_END) carries the authoritative post-mutation
// task list, which the update and append renderers use to resolve ids to
// descriptions and show the full list as context.
func renderTaskList(input map[string]any, output string, isError bool, state any) ToolRender {
	action := stringField(input, "action")
	stateTasks := parseTaskStateList(state)

	var render ToolRender
	switch action {
	case taskOpAppend:
		render = renderTaskListAppend(input, output, isError, stateTasks)
	case taskOpUpdate:
		render = renderTaskListUpdate(input, output, isError, stateTasks)
	case taskOpView:
		render = renderTaskListView(output, isError, stateTasks)
	default:
		render = renderGeneric(input, output, isError)
	}
	render.Prelude = renderIntentPrelude(input)
	return render
}

// taskStateEntry is a normalized task record parsed from tool_state.
type taskStateEntry struct {
	ID          int
	Description string
	Status      string
	DependsOn   []int
}

// parseTaskStateList coerces a raw tool_state snapshot (produced by serf's
// task_list handler as a JSON array of Task records) into a convenient
// slice. Returns nil if the shape doesn't match.
func parseTaskStateList(state any) []taskStateEntry {
	arr, ok := state.([]any)
	if !ok {
		return nil
	}
	out := make([]taskStateEntry, 0, len(arr))
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := taskStateEntry{
			Description: stringField(m, fieldDescription),
			Status:      stringField(m, fieldStatus),
		}
		if id, ok := asInt(m["id"]); ok {
			entry.ID = id
		}
		if deps, ok := m["depends_on"].([]any); ok {
			for _, d := range deps {
				if n, ok := asInt(d); ok {
					entry.DependsOn = append(entry.DependsOn, n)
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// stateByID returns a lookup map from id to description.
func stateByID(state []taskStateEntry) map[int]string {
	m := map[int]string{}
	for _, e := range state {
		m[e.ID] = e.Description
	}
	return m
}

func renderTaskListAppend(input map[string]any, output string, isError bool, state []taskStateEntry) ToolRender {
	tasks, _ := input["tasks"].([]any)
	sections := []ToolSection{}

	if len(tasks) > 0 {
		sections = append(sections, ToolSection{
			Label: fmt.Sprintf("New (%d)", len(tasks)),
			Body:  renderTaskListNewItems(tasks),
		})
	}
	if len(state) > 0 {
		sections = append(sections, ToolSection{
			Label: "All tasks",
			Body:  renderTaskStateChecklist(state),
		})
	}
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{
		Summary:  fmt.Sprintf("append · %d new", len(tasks)),
		Sections: sections,
	}
}

func renderTaskListUpdate(input map[string]any, output string, isError bool, state []taskStateEntry) ToolRender {
	updates, _ := input["updates"].([]any)
	descriptions := stateByID(state)

	sections := []ToolSection{}
	if len(updates) > 0 {
		sections = append(sections, ToolSection{
			Label: labelChanges,
			Body:  renderTaskListUpdateRows(updates, descriptions),
		})
	}
	if len(state) > 0 {
		sections = append(sections, ToolSection{
			Label: "All tasks",
			Body:  renderTaskStateChecklist(state),
		})
	}
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	summary := fmt.Sprintf("update · %d change", len(updates))
	if len(updates) != 1 {
		summary += "s"
	}
	return ToolRender{Summary: summary, Sections: sections}
}

func renderTaskListView(output string, isError bool, state []taskStateEntry) ToolRender {
	sections := []ToolSection{}
	if len(state) > 0 {
		sections = append(sections, ToolSection{
			Label: "Current list",
			Body:  renderTaskStateChecklist(state),
		})
	} else if output != "" {
		// Pre-tool_state runs or malformed snapshots — fall back to the
		// raw output so nothing is silently dropped.
		sections = append(sections, ToolSection{
			Label:   "Current list",
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: taskOpView, Sections: sections}
}

// renderTaskListNewItems formats a serf `tasks` array (from an append input)
// as a numbered card list with a green "+" badge and unchecked checkbox.
func renderTaskListNewItems(tasks []any) template.HTML {
	var b strings.Builder
	b.WriteString(`<ol class="space-y-3 list-none pl-0">`)
	for i, entry := range tasks {
		m, ok := entry.(map[string]any)
		if !ok {
			b.WriteString(`<li class="p-2 border border-edge rounded bg-white">`)
			b.WriteString(string(renderAnyAsJSON(entry)))
			b.WriteString(`</li>`)
			continue
		}
		desc := stringField(m, fieldDescription)
		prompt := stringField(m, fieldPrompt)
		taskType := stringField(m, "type")
		effort := stringField(m, fieldReasoningEffort)
		deps, _ := m["depends_on"].([]any)

		b.WriteString(`<li class="p-2 border border-edge border-l-4 border-l-accent/50 rounded bg-accent/5">`)
		b.WriteString(`<div class="flex items-baseline gap-2 mb-1 flex-wrap">`)
		b.WriteString(`<span class="text-accent text-sm font-semibold" aria-label="new">+</span>`)
		b.WriteString(`<span class="text-muted text-sm" aria-label="unchecked">☐</span>`)
		fmt.Fprintf(&b, `<span class="font-mono text-muted text-[11px]">%d.</span>`, i+1)
		if desc != "" {
			b.WriteString(`<span class="font-medium text-sm text-ink">`)
			b.WriteString(html.EscapeString(desc))
			b.WriteString(`</span>`)
		}
		if taskType != "" {
			writeTaskBadge(&b, taskType, "bg-accent/10 text-accent")
		}
		if effort != "" {
			writeTaskBadge(&b, effort, "bg-gray-100 text-gray-600")
		}
		b.WriteString(`</div>`)

		if len(deps) > 0 {
			b.WriteString(`<div class="text-[11px] text-muted mb-1">depends on: `)
			b.WriteString(html.EscapeString(joinDeps(deps)))
			b.WriteString(`</div>`)
		}
		if prompt != "" {
			b.WriteString(`<div class="text-xs text-ink-light whitespace-pre-wrap">`)
			b.WriteString(html.EscapeString(prompt))
			b.WriteString(`</div>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol>`)
	return template.HTML(b.String())
}

// renderTaskListUpdateRows formats the updates array, resolving ids to
// descriptions from the snapshot.
func renderTaskListUpdateRows(updates []any, descriptions map[int]string) template.HTML {
	var b strings.Builder
	b.WriteString(`<ul class="space-y-2 list-none pl-0">`)
	for _, entry := range updates {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := asInt(m["id"])
		status := stringField(m, fieldStatus)
		notes := stringField(m, "notes")
		desc := descriptions[id]

		b.WriteString(`<li class="p-2 border border-edge rounded bg-white">`)
		b.WriteString(`<div class="flex items-baseline gap-2 flex-wrap">`)
		b.WriteString(taskStatusIcon(status))
		if id != 0 {
			fmt.Fprintf(&b, `<span class="font-mono text-muted text-[11px]">#%d</span>`, id)
		}
		if desc != "" {
			b.WriteString(`<span class="font-medium text-sm text-ink">`)
			b.WriteString(html.EscapeString(desc))
			b.WriteString(`</span>`)
		}
		if status != "" {
			b.WriteString(`<span class="text-muted text-xs">→</span>`)
			writeTaskBadge(&b, status, taskStatusBadgeClass(status))
		}
		b.WriteString(`</div>`)

		if notes != "" {
			b.WriteString(`<div class="text-xs text-ink-light whitespace-pre-wrap mt-1">`)
			b.WriteString(html.EscapeString(notes))
			b.WriteString(`</div>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

// renderTaskStateChecklist renders the full task list from a state snapshot
// as a checklist with status icons per row.
func renderTaskStateChecklist(state []taskStateEntry) template.HTML {
	var b strings.Builder
	b.WriteString(`<ul class="space-y-1 list-none pl-0">`)
	for _, t := range state {
		b.WriteString(`<li class="flex items-baseline gap-2 flex-wrap">`)
		b.WriteString(taskStatusIcon(t.Status))
		fmt.Fprintf(&b, `<span class="font-mono text-muted text-[11px]">#%d</span>`, t.ID)
		if t.Description != "" {
			b.WriteString(`<span class="text-sm text-ink">`)
			b.WriteString(html.EscapeString(t.Description))
			b.WriteString(`</span>`)
		}
		if t.Status != "" {
			writeTaskBadge(&b, t.Status, taskStatusBadgeClass(t.Status))
		}
		if len(t.DependsOn) > 0 {
			parts := make([]string, 0, len(t.DependsOn))
			for _, d := range t.DependsOn {
				parts = append(parts, fmt.Sprintf("#%d", d))
			}
			fmt.Fprintf(&b, `<span class="text-[10px] text-muted">deps: %s</span>`, html.EscapeString(strings.Join(parts, ", ")))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

func writeTaskBadge(b *strings.Builder, text, classes string) {
	fmt.Fprintf(b, `<span class="px-1.5 py-0.5 rounded text-[10px] %s">%s</span>`,
		classes, html.EscapeString(text))
}

func taskStatusIcon(status string) string {
	switch status {
	case taskStatusDone:
		return `<span class="text-accent text-sm">✓</span>`
	case taskStatusInProgress:
		return `<span class="text-blue-600 text-sm">▶</span>`
	case taskStatusCancelled:
		return `<span class="text-red-600 text-sm">✗</span>`
	case taskStatusOpen, taskStatusUndone, "":
		return `<span class="text-muted text-sm">☐</span>`
	default:
		return `<span class="text-muted text-sm">•</span>`
	}
}

func taskStatusBadgeClass(status string) string {
	switch status {
	case taskStatusDone:
		return "bg-accent/10 text-accent"
	case taskStatusInProgress:
		return decisionPillBlue
	case taskStatusCancelled:
		return decisionPillRed
	default:
		return "bg-gray-100 text-gray-600"
	}
}

func joinDeps(deps []any) string {
	parts := make([]string, 0, len(deps))
	for _, d := range deps {
		if n, ok := asInt(d); ok {
			parts = append(parts, fmt.Sprintf("#%d", n))
		}
	}
	return strings.Join(parts, ", ")
}
