package dashboard

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	for _, n := range []string{toolReadFile, toolRead} {
		toolRenderers[n] = renderReadFile
	}
	for _, n := range []string{toolWriteFile, "Write"} {
		toolRenderers[n] = renderWriteFile
	}
	for _, n := range []string{toolEditFile, "Edit"} {
		toolRenderers[n] = renderEdit
	}
	for _, n := range []string{toolBash, "Bash", "shell"} {
		toolRenderers[n] = renderBash
	}
	toolRenderers["communicate"] = renderCommunicate
	toolRenderers["spawn_agent"] = renderSpawnAgent
	toolRenderers["send_input"] = renderSendInput
	toolRenderers["wait"] = renderSubagentWait
	toolRenderers["close_agent"] = renderCloseAgent
	for _, n := range []string{toolGrep, "Grep"} {
		toolRenderers[n] = renderGrep
	}
	for _, n := range []string{toolGlob, "Glob"} {
		toolRenderers[n] = renderGlob
	}
	toolRenderers["LS"] = renderLS
	toolRenderers["TaskCreate"] = renderTaskCreate
	toolRenderers["TaskUpdate"] = renderTaskUpdate
	toolRenderers["TaskGet"] = renderTaskGet
	toolRenderers["TodoWrite"] = renderTodoWrite
}

func renderBash(input map[string]any, output string, isError bool) ToolRender {
	cmd := stringField(input, "command")

	// Compose the summary: truncated command + modifiers on the header line.
	summary := truncate(cmd, 80)
	modifiers := []string{}
	// serf's shell runner sends "timeout_ms"; Claude Code's Bash tool sends
	// "timeout". Accept either.
	timeout, ok := asInt(input["timeout_ms"])
	if !ok {
		timeout, ok = asInt(input["timeout"])
	}
	if ok && timeout > 0 {
		modifiers = append(modifiers, fmt.Sprintf("timeout %dms", timeout))
	}
	if bg, ok := input["run_in_background"].(bool); ok && bg {
		modifiers = append(modifiers, "background")
	}
	if len(modifiers) > 0 {
		summary = fmt.Sprintf("%s (%s)", summary, strings.Join(modifiers, ", "))
	}

	sections := []ToolSection{}
	// Extra args beyond what the header + prelude already show.
	sections = appendIfBody(sections, extraArgsSection(input,
		"command", "timeout", "timeout_ms", "run_in_background"))
	body := renderTerminal(cmd, output, isError)
	if body != "" {
		sections = append(sections, ToolSection{Label: "Terminal", Body: body, IsError: isError})
	}
	return ToolRender{Summary: summary, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderTerminal produces a single <pre> with "$ cmd" followed by output.
func renderTerminal(cmd, output string, isError bool) template.HTML {
	if cmd == "" && output == "" {
		return ""
	}
	cls := `text-xs bg-gray-900 text-gray-100 rounded border border-edge p-2 overflow-auto whitespace-pre-wrap`
	if isError {
		cls = `text-xs bg-red-950 text-red-100 rounded border border-red-300 p-2 overflow-auto whitespace-pre-wrap`
	}
	var b strings.Builder
	b.WriteString(`<pre class="`)
	b.WriteString(cls)
	b.WriteString(`">`)
	if cmd != "" {
		b.WriteString(`<span class="text-gray-400">$ </span>`)
		b.WriteString(html.EscapeString(cmd))
		if output != "" {
			b.WriteString("\n")
		}
	}
	if output != "" {
		b.WriteString(html.EscapeString(output))
	}
	b.WriteString(`</pre>`)
	return template.HTML(b.String())
}

// renderCommunicate renders serf's communicate tool using an email-like
// mental model: the agent *sends* a message, optionally with a structured
// result envelope attached, and the tool acknowledges delivery.
//
//	(prelude: purpose / description)
//	<message prose>                      — the primary content, unlabeled
//	Result:  <decision + data + artifacts> — the attached NodeOutput envelope
//	Delivery: <accepted / inbox>          — the tool's ack
//
// Putting the message up top unlabeled keeps it reading like the body of a
// note; the structured result lives below it as a separate labeled block
// so the decision/data never gets mistaken for the tool's own output.
func renderCommunicate(input map[string]any, toolOutput string, isError bool) ToolRender {
	msg := stringField(input, transcriptKindMessage)
	awaitReply, _ := input["await_reply"].(bool)

	sections := []ToolSection{}

	// The message body is the primary content — unlabeled prose.
	if msg != "" {
		sections = append(sections, ToolSection{Body: renderProse(msg)})
	}

	// The "output" arg is the attached structured result envelope — rendered
	// separately so it doesn't read like the tool's own output.
	if rawOut, ok := input[fieldOutput]; ok && rawOut != nil {
		if body, ok := renderNodeOutputStructured(rawOut, ""); ok {
			sections = append(sections, ToolSection{
				Label:   labelResult,
				Body:    body,
				IsError: isError,
			})
		} else {
			sections = append(sections, ToolSection{
				Label:    labelResult,
				Body:     renderAnyAsJSON(rawOut),
				Language: langJSON,
				IsError:  isError,
			})
		}
	}

	sections = appendIfBody(sections, extraArgsSection(input, transcriptKindMessage, fieldOutput))

	if toolOutput != "" {
		sections = append(sections, ToolSection{
			Label:   "Delivery",
			Body:    renderCommunicateResponse(toolOutput, isError),
			IsError: isError,
		})
	}

	summary := ""
	if awaitReply {
		summary = "awaits reply"
	}
	return ToolRender{Summary: summary, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderCommunicateResponse renders the standard ack from the communicate
// tool: {accepted, await_reply, inbox}. Falls back to raw JSON if fields
// don't match the expected shape.
func renderCommunicateResponse(out string, isError bool) template.HTML {
	var r struct {
		Accepted   *bool `json:"accepted"`
		AwaitReply *bool `json:"await_reply"`
		Inbox      any   `json:"inbox"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || (r.Accepted == nil && r.AwaitReply == nil) {
		return renderJSONMaybePretty(out)
	}
	var b strings.Builder
	b.WriteString(`<div class="text-xs space-y-0.5">`)
	if r.Accepted != nil {
		mark := `✓`
		cls := `text-green-700`
		word := wordAccepted
		if !*r.Accepted {
			mark = `✗`
			cls = `text-red-700`
			word = `rejected`
		}
		b.WriteString(`<div><span class="`)
		b.WriteString(cls)
		b.WriteString(` font-semibold">`)
		b.WriteString(mark)
		b.WriteString(` `)
		b.WriteString(word)
		b.WriteString(`</span></div>`)
	}
	if r.AwaitReply != nil {
		word := wordNoReplyExpected
		if *r.AwaitReply {
			word = `awaiting reply`
		}
		b.WriteString(`<div class="text-muted">`)
		b.WriteString(word)
		b.WriteString(`</div>`)
	}
	if r.Inbox != nil {
		b.WriteString(`<div>inbox: `)
		if ib, err := json.MarshalIndent(r.Inbox, "", "  "); err == nil {
			b.WriteString(`<code class="text-[11px]">`)
			b.WriteString(html.EscapeString(string(ib)))
			b.WriteString(`</code>`)
		}
		b.WriteString(`</div>`)
	} else {
		b.WriteString(`<div class="text-muted">inbox: empty</div>`)
	}
	b.WriteString(`</div>`)
	_ = isError // structured ack handles its own visual states
	return template.HTML(b.String())
}

// renderJSONMaybePretty parses s as JSON and pretty-prints it; falls back
// to the raw string in a code block if parsing fails.
func renderJSONMaybePretty(s string) template.HTML {
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err == nil {
		if b, err := json.MarshalIndent(parsed, "", "  "); err == nil {
			return renderCodeBlock(string(b), langJSON)
		}
	}
	return renderCodeBlock(s, langJSON)
}

// renderAnyAsJSON marshals v as pretty JSON, or renders a string form if
// marshaling fails. Handles the case where a tool-call argument is already a
// parsed nested map (json.Unmarshal from the outer arguments_json).
func renderAnyAsJSON(v any) template.HTML {
	// If already a string, try to unmarshal it (might be stringified JSON).
	if s, ok := v.(string); ok {
		return renderJSONMaybePretty(s)
	}
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		return renderCodeBlock(string(b), langJSON)
	}
	return renderCodeBlock(fmt.Sprintf("%v", v), langJSON)
}

// renderNodeOutputStructured renders a value that looks like a toil
// NodeOutput ({decision, message, data, artifacts}) as a structured block.
// When `primaryMessage` is non-empty, it is used as the big caption beside
// the decision pill and the inner `message` is shown as a smaller
// secondary line (or suppressed if identical to primaryMessage).
//
// Data is rendered compactly: scalars share one KV table; long strings,
// nested maps, and multi-element arrays become small-labeled blocks.
// Returns empty HTML and false if v doesn't look like a NodeOutput.
func renderNodeOutputStructured(v any, primaryMessage string) (template.HTML, bool) {
	// Accept either a pre-parsed map (how serf delivers it) or a JSON string
	// that parses to a map (how most other pipelines deliver it).
	m, ok := v.(map[string]any)
	if !ok {
		if s, isStr := v.(string); isStr {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				m, ok = parsed.(map[string]any)
			}
		}
	}
	if !ok {
		return "", false
	}
	decision, _ := m[fieldDecision].(string)
	innerMessage, _ := m[transcriptKindMessage].(string)
	data, _ := m[fieldData].(map[string]any)
	artifactsRaw, _ := m["artifacts"].([]any)
	if decision == "" && innerMessage == "" && len(data) == 0 && len(artifactsRaw) == 0 {
		return "", false
	}

	var b strings.Builder
	b.WriteString(`<div class="space-y-2">`)

	// Header: decision pill + primary caption (outer message preferred, else inner).
	caption := primaryMessage
	if caption == "" {
		caption = innerMessage
	}
	if decision != "" || caption != "" {
		b.WriteString(`<div class="flex items-start gap-2 flex-wrap">`)
		if decision != "" {
			b.WriteString(nodeDecisionPill(decision))
		}
		if caption != "" {
			b.WriteString(`<span class="text-sm text-ink">`)
			b.WriteString(html.EscapeString(caption))
			b.WriteString(`</span>`)
		}
		b.WriteString(`</div>`)
	}
	// Secondary: inner message only when a distinct primary was shown above.
	if primaryMessage != "" && innerMessage != "" && innerMessage != primaryMessage {
		b.WriteString(`<div class="text-xs text-muted italic">`)
		b.WriteString(html.EscapeString(innerMessage))
		b.WriteString(`</div>`)
	}

	// Partition data into short scalars and long / nested values.
	scalars := map[string]any{}
	longVals := map[string]any{}
	for k, val := range data {
		if isLongValue(val) {
			longVals[k] = val
		} else {
			scalars[k] = val
		}
	}
	if len(scalars) > 0 {
		b.WriteString(string(renderKVTable(scalars)))
	}
	for _, k := range sortedStringKeys(longVals) {
		b.WriteString(`<div>`)
		b.WriteString(`<div class="text-[10px] font-mono text-muted mt-1">`)
		b.WriteString(html.EscapeString(k))
		b.WriteString(`</div>`)
		b.WriteString(string(renderNodeOutputValue(longVals[k])))
		b.WriteString(`</div>`)
	}

	if len(artifactsRaw) > 0 {
		b.WriteString(`<div>`)
		b.WriteString(`<div class="text-[10px] font-mono text-muted mt-1">artifacts</div>`)
		b.WriteString(`<ul class="text-xs font-mono">`)
		for _, a := range artifactsRaw {
			b.WriteString(`<li>`)
			b.WriteString(html.EscapeString(fmt.Sprintf("%v", a)))
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul>`)
		b.WriteString(`</div>`)
	}

	// Any other top-level keys (future-proofing)
	consumed := map[string]bool{fieldDecision: true, transcriptKindMessage: true, fieldData: true, "artifacts": true}
	others := map[string]any{}
	for k, val := range m {
		if consumed[k] {
			continue
		}
		others[k] = val
	}
	if len(others) > 0 {
		b.WriteString(string(renderKVTable(others)))
	}

	b.WriteString(`</div>`)
	return template.HTML(b.String()), true
}

// isLongValue reports whether a data-value deserves its own labeled block
// rather than a cell in a shared KV table (multi-line / long strings,
// non-empty nested maps, multi-element arrays).
func isLongValue(v any) bool {
	switch val := v.(type) {
	case string:
		return strings.Contains(val, "\n") || len(val) > 200
	case map[string]any:
		return len(val) > 0
	case []any:
		return len(val) > 3
	}
	return false
}

func sortedStringKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderNodeOutputValue renders one data-value readably. Multi-line strings
// and nested structures get <pre> blocks; short strings stay inline.
func renderNodeOutputValue(v any) template.HTML {
	switch val := v.(type) {
	case string:
		if strings.Contains(val, "\n") {
			return template.HTML(`<pre class="text-xs bg-white rounded border border-edge p-2 overflow-auto whitespace-pre-wrap">` + html.EscapeString(val) + `</pre>`)
		}
		return template.HTML(`<div class="text-xs text-ink">` + html.EscapeString(val) + `</div>`)
	case map[string]any:
		return renderKVTable(val)
	case []any:
		return renderAnyAsJSON(val)
	default:
		return template.HTML(`<div class="text-xs font-mono text-ink">` + html.EscapeString(fmt.Sprintf("%v", v)) + `</div>`)
	}
}

const (
	decisionPillGreen = `bg-green-100 text-green-700`
	decisionPillRed   = `bg-red-100 text-red-700`
	decisionPillAmber = `bg-amber-100 text-amber-700`
	decisionPillGray  = `bg-gray-100 text-gray-700`
	decisionPillBlue  = `bg-blue-100 text-blue-700`
)

const (
	toolRead      = "Read"
	toolReadFile  = "read_file"
	toolWriteFile = "write_file"
	toolEditFile  = "edit_file"
	toolBash      = "bash"
	toolGrep      = "grep"
	toolGlob      = "glob"
)

const (
	fieldStatus          = "status"
	fieldDescription     = "description"
	fieldOutput          = "output"
	fieldData            = "data"
	fieldPrompt          = "prompt"
	fieldContent         = "content"
	fieldResult          = "result"
	fieldText            = "text"
	fieldTaskID          = "taskId"
	fieldTaskList        = "task_list"
	fieldDecision        = "decision"
	fieldPurpose         = "purpose"
	fieldReasoningEffort = "reasoning_effort"
	fieldTitle           = "title"
)

const (
	labelResult    = "Result"
	labelChanges   = "Changes"
	labelMessage   = "Message"
	labelOutput    = "Output"
	labelOtherArgs = "Other Args"
)

const (
	langJSON       = "json"
	langYAML       = "yaml"
	langTypeScript = "typescript"
	langPython     = "python"
)

const (
	wordAccepted        = "accepted"
	wordNoReplyExpected = "no reply expected"
)

const (
	taskOpAppend = "append"
	taskOpUpdate = "update"
	taskOpView   = "view"
)

// nodeDecisionPill renders a small colored pill for a NodeOutput decision.
// The switch literals are an enumerated set of recognized decision/status
// values — goconst's hit on "ready"/"failed" is a false positive here.
//
//nolint:goconst
func nodeDecisionPill(decision string) string {
	cls := decisionPillGray
	switch strings.ToLower(decision) {
	case "pass", "approved", "done", "ready", "ready_for_review", "code_written", "prepared":
		cls = decisionPillGreen
	case "fail", "failed", "rejected", "changes_requested":
		cls = decisionPillRed
	case "pending", "deferred":
		cls = decisionPillAmber
	case "spec_issue":
		cls = `bg-orange-100 text-orange-700`
	}
	return `<span class="px-2 py-0.5 rounded-full text-[11px] font-semibold uppercase tracking-wide ` + cls + `">` + html.EscapeString(decision) + `</span>`
}

// renderSpawnAgent handles serf's spawn_agent tool — a rich dispatch that
// gets a full config block, a task prose arg, an optional structured
// task_list, and returns a composite result (agent_id, status, nested
// agent output, transcript path, turns_used).
func renderSpawnAgent(input map[string]any, toolOutput string, isError bool) ToolRender {
	agentType := stringField(input, "agent_type")
	task := stringField(input, "task")

	sections := []ToolSection{}
	if task != "" {
		sections = append(sections, ToolSection{
			Label: "Task",
			Body:  renderProse(task),
		})
	}
	if tl, ok := input[fieldTaskList]; ok && tl != nil {
		sections = append(sections, ToolSection{
			Label: "Task List",
			Body:  renderTaskListItems(tl),
		})
	}
	sections = appendIfBody(sections, extraArgsSection(input, "agent_type", "task", fieldTaskList))

	if toolOutput != "" {
		// toolOutput is JSON: {agent_id, output, status, success, transcript, turns_used}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(toolOutput), &parsed); err == nil {
			sections = append(sections, spawnAgentResultSections(parsed, isError)...)
		} else {
			sections = append(sections, ToolSection{
				Label:   labelResult,
				Body:    renderPlainPre(toolOutput, isError),
				IsError: isError,
			})
		}
	}
	return ToolRender{Summary: agentType, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderTaskListItems renders an array of task specs (title + prompt +
// reasoning_effort + any extras) as a numbered list with prose for the
// prompt and small inline badges for metadata. Falls back to raw JSON
// if the shape doesn't look like a task list.
func renderTaskListItems(raw any) template.HTML {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return renderAnyAsJSON(raw)
	}
	var b strings.Builder
	b.WriteString(`<ol class="space-y-3 list-none pl-0 counter-reset-tasklist">`)
	for i, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			// unknown shape — render as JSON
			b.WriteString(`<li class="p-2 border border-edge rounded bg-white">`)
			b.WriteString(string(renderAnyAsJSON(entry)))
			b.WriteString(`</li>`)
			continue
		}
		title := stringField(m, fieldTitle)
		prompt := stringField(m, fieldPrompt)
		effort := stringField(m, fieldReasoningEffort)

		b.WriteString(`<li class="p-2 border border-edge rounded bg-white">`)
		// Header: "1. Title  [effort]"
		b.WriteString(`<div class="flex items-baseline gap-2 mb-1">`)
		fmt.Fprintf(&b, `<span class="font-mono text-muted text-[11px]">%d.</span>`, i+1)
		if title != "" {
			b.WriteString(`<span class="font-medium text-sm text-ink">`)
			b.WriteString(html.EscapeString(title))
			b.WriteString(`</span>`)
		}
		if effort != "" {
			b.WriteString(`<span class="px-1.5 py-0.5 rounded text-[10px] bg-gray-100 text-gray-600">`)
			b.WriteString(html.EscapeString(effort))
			b.WriteString(`</span>`)
		}
		b.WriteString(`</div>`)
		// Prompt
		if prompt != "" {
			b.WriteString(`<div class="text-xs text-ink-light whitespace-pre-wrap">`)
			b.WriteString(html.EscapeString(prompt))
			b.WriteString(`</div>`)
		}
		// Any extras
		extras := map[string]any{}
		for k, v := range m {
			if k == fieldTitle || k == fieldPrompt || k == fieldReasoningEffort {
				continue
			}
			extras[k] = v
		}
		if len(extras) > 0 {
			b.WriteString(`<div class="mt-1">`)
			b.WriteString(string(renderKVTable(extras)))
			b.WriteString(`</div>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol>`)
	return template.HTML(b.String())
}

// spawnAgentResultSections breaks down the JSON result from spawn_agent
// into readable sections.
func spawnAgentResultSections(r map[string]any, isError bool) []ToolSection {
	sections := []ToolSection{}

	// Metadata section (status, success, agent_id, turns, transcript).
	metaKeys := []string{fieldStatus, "success", "agent_id", "turns_used", "transcript"}
	metaInput := map[string]any{}
	for _, k := range metaKeys {
		if v, ok := r[k]; ok {
			metaInput[k] = v
		}
	}
	if len(metaInput) > 0 {
		sections = append(sections, ToolSection{
			Label: "Result Metadata",
			Body:  renderKVTable(metaInput),
		})
	}

	// Nested agent output — the subagent's decision/message/data/artifacts.
	// spawn_agent embeds the result as a JSON string, so try to parse it first.
	if out, ok := r[fieldOutput]; ok && out != nil {
		parsed := out
		if s, isStr := out.(string); isStr {
			var tmp any
			if err := json.Unmarshal([]byte(s), &tmp); err == nil {
				parsed = tmp
			}
		}
		if body, ok := renderNodeOutputStructured(parsed, ""); ok {
			sections = append(sections, ToolSection{
				Label:   "Agent Output",
				Body:    body,
				IsError: isError,
			})
		} else {
			sections = append(sections, ToolSection{
				Label:    "Agent Output",
				Body:     renderAnyAsJSON(parsed),
				Language: langJSON,
				IsError:  isError,
			})
		}
	}

	// Anything else we didn't handle
	consumed := append([]string{fieldOutput}, metaKeys...)
	sections = appendIfBody(sections, extraArgsSection(r, consumed...))
	return sections
}

// renderSendInput handles serf's send_input tool (sends a message to a
// running spawned agent). Input: agent_id, message. Output: ack.
func renderSendInput(input map[string]any, toolOutput string, isError bool) ToolRender {
	agentID := stringField(input, "agent_id")
	msg := stringField(input, transcriptKindMessage)
	sections := []ToolSection{}
	if msg != "" {
		sections = append(sections, ToolSection{
			Label: labelMessage,
			Body:  renderProse(msg),
		})
	}
	sections = appendIfBody(sections, extraArgsSection(input, "agent_id", transcriptKindMessage))
	if toolOutput != "" {
		sections = append(sections, ToolSection{
			Label:    "Response",
			Body:     renderJSONMaybePretty(toolOutput),
			Language: langJSON,
			IsError:  isError,
		})
	}
	return ToolRender{Summary: agentID, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderSubagentWait handles serf's wait tool (waits for a spawned agent's
// next output). Summary = agent_id; output is the agent's streamed result.
func renderSubagentWait(input map[string]any, toolOutput string, isError bool) ToolRender {
	agentID := stringField(input, "agent_id")
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, "agent_id"))
	if toolOutput != "" {
		sections = append(sections, ToolSection{
			Label:    labelOutput,
			Body:     renderJSONMaybePretty(toolOutput),
			Language: langJSON,
			IsError:  isError,
		})
	}
	return ToolRender{Summary: agentID, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderCloseAgent handles serf's close_agent (terminates a spawned agent).
// Input: agent_id. Output: ack.
func renderCloseAgent(input map[string]any, toolOutput string, isError bool) ToolRender {
	agentID := stringField(input, "agent_id")
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, "agent_id"))
	if toolOutput != "" {
		sections = append(sections, ToolSection{
			Label:    "Response",
			Body:     renderJSONMaybePretty(toolOutput),
			Language: langJSON,
			IsError:  isError,
		})
	}
	return ToolRender{Summary: agentID, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderProse wraps text so markdown is rendered, inside the shared
// .transcript-item-content container (tamed heading sizes etc).
func renderProse(text string) template.HTML {
	return template.HTML(`<div class="transcript-item-content">` + string(renderMarkdown(text)) + `</div>`)
}

// extraArgsSection returns an "Other Args" section for any input keys not in
// the consumed set. "purpose" and "description" are always skipped — both
// are lifted to the prelude via renderIntentPrelude. Empty ToolSection
// (Body=="") when no extras.
func extraArgsSection(input map[string]any, consumed ...string) ToolSection {
	skip := map[string]bool{fieldPurpose: true, fieldDescription: true}
	for _, k := range consumed {
		skip[k] = true
	}
	remaining := map[string]any{}
	for k, v := range input {
		if skip[k] {
			continue
		}
		remaining[k] = v
	}
	if len(remaining) == 0 {
		return ToolSection{}
	}
	return ToolSection{
		Label: labelOtherArgs,
		Body:  renderKVTable(remaining),
	}
}

// renderIntentPrelude extracts the "purpose" and/or "description" args
// (either is a common serf / built-in convention describing *why* a tool
// was called) and renders them as italicized, unlabeled leader lines.
// Empty HTML when neither is set.
func renderIntentPrelude(input map[string]any) template.HTML {
	p := stringField(input, fieldPurpose)
	d := stringField(input, fieldDescription)
	switch {
	case p != "" && d != "" && p != d:
		return template.HTML(`<div class="transcript-tool-purpose"><em>` + html.EscapeString(p) + `</em><br><em>` + html.EscapeString(d) + `</em></div>`)
	case p != "":
		return template.HTML(`<div class="transcript-tool-purpose"><em>` + html.EscapeString(p) + `</em></div>`)
	case d != "":
		return template.HTML(`<div class="transcript-tool-purpose"><em>` + html.EscapeString(d) + `</em></div>`)
	}
	return ""
}

// appendIfBody appends s to sections only when s has a non-empty Body.
func appendIfBody(sections []ToolSection, s ToolSection) []ToolSection {
	if s.Body == "" {
		return sections
	}
	return append(sections, s)
}

func renderGrep(input map[string]any, output string, isError bool) ToolRender {
	pattern := stringField(input, "pattern")
	glob := stringField(input, "glob")
	path := stringField(input, "path")
	summaryParts := []string{}
	if pattern != "" {
		summaryParts = append(summaryParts, pattern)
	}
	switch {
	case glob != "":
		summaryParts = append(summaryParts, "in "+glob)
	case path != "":
		summaryParts = append(summaryParts, "in "+path)
	}
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, "pattern", "glob", "path"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   "Matches",
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{
		Summary:  strings.Join(summaryParts, " "),
		Prelude:  renderIntentPrelude(input),
		Sections: sections,
	}
}

func renderGlob(input map[string]any, output string, isError bool) ToolRender {
	pattern := stringField(input, "pattern")
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, "pattern"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   "Files",
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: pattern, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderLS(input map[string]any, output string, isError bool) ToolRender {
	path := stringField(input, "path")
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, "path"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   "Entries",
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: path, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderTaskCreate(input map[string]any, output string, isError bool) ToolRender {
	subject := stringField(input, "subject")
	desc := stringField(input, fieldDescription)
	lines := []string{"+ " + subject}
	if desc != "" {
		lines = append(lines, "  "+truncate(desc, 120))
	}
	sections := []ToolSection{
		{Label: labelChanges, Body: renderChangeLog(lines)},
	}
	sections = appendIfBody(sections, extraArgsSection(input, "subject", fieldDescription))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: subject, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderTaskUpdate(input map[string]any, output string, isError bool) ToolRender {
	id := stringField(input, fieldTaskID)
	if id == "" {
		id = stringField(input, "id")
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		if k == fieldTaskID || k == "id" || k == fieldPurpose {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", k, formatOutputValue(input[k])))
	}
	sections := []ToolSection{
		{Label: labelChanges, Body: renderChangeLog(lines)},
	}
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: id, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderTaskGet(input map[string]any, output string, isError bool) ToolRender {
	id := stringField(input, fieldTaskID)
	if id == "" {
		id = stringField(input, "id")
	}
	sections := []ToolSection{}
	sections = appendIfBody(sections, extraArgsSection(input, fieldTaskID, "id"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   "Task",
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: id, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderTodoWrite(input map[string]any, output string, isError bool) ToolRender {
	todos, _ := input["todos"].([]any)
	lines := make([]string, 0, len(todos))
	for _, raw := range todos {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(m, "id")
		content := stringField(m, fieldContent)
		if content == "" {
			content = stringField(m, "subject")
		}
		status := stringField(m, fieldStatus)
		prefix := ""
		if id != "" {
			prefix = "[" + id + "] "
		}
		lines = append(lines, fmt.Sprintf("%s%s — %s", prefix, content, status))
	}
	summary := fmt.Sprintf("%d task updates", len(todos))
	sections := []ToolSection{
		{Label: labelChanges, Body: renderChangeLog(lines)},
	}
	// Full todo payload as JSON so per-todo extra fields (description, metadata,
	// blocks, blockedBy, activeForm, …) aren't lost.
	if len(todos) > 0 {
		if b, err := json.MarshalIndent(todos, "", "  "); err == nil {
			sections = append(sections, ToolSection{
				Label:    "Raw Todos",
				Body:     renderCodeBlock(string(b), langJSON),
				Language: langJSON,
			})
		}
	}
	sections = appendIfBody(sections, extraArgsSection(input, "todos"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: summary, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderChangeLog renders a slice of "field: value" style lines as a <ul>.
func renderChangeLog(lines []string) template.HTML {
	var b strings.Builder
	b.WriteString(`<ul class="text-xs font-mono space-y-0.5">`)
	for _, line := range lines {
		b.WriteString(`<li>`)
		b.WriteString(html.EscapeString(line))
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

func renderReadFile(input map[string]any, output string, isError bool) ToolRender {
	path := stringField(input, "path")
	if path == "" {
		path = stringField(input, "file_path")
	}
	lang := guessLanguage(path)

	// Summary: "path (offset->offset+limit)" when a range is set.
	summary := path
	offset, offsetOK := asInt(input["offset"])
	limit, limitOK := asInt(input["limit"])
	switch {
	case offsetOK && limitOK:
		summary = fmt.Sprintf("%s (%d→%d)", path, offset, offset+limit)
	case limitOK:
		summary = fmt.Sprintf("%s (→%d)", path, limit)
	case offsetOK:
		summary = fmt.Sprintf("%s (%d→)", path, offset)
	}

	sections := []ToolSection{}
	// Args first (any extras beyond path/file_path/offset/limit/purpose).
	sections = appendIfBody(sections, extraArgsSection(input, "path", "file_path", "offset", "limit"))
	// Result last.
	if output != "" {
		sections = append(sections, ToolSection{
			Label:    labelOutput,
			Body:     renderCodeBlock(output, lang),
			Language: lang,
			IsError:  isError,
		})
	}
	return ToolRender{Summary: summary, Prelude: renderIntentPrelude(input), Sections: sections}
}

// asInt returns v as an int if possible (handles float64 from JSON numeric
// parse). ok=false when the value is not numeric.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

func renderWriteFile(input map[string]any, output string, isError bool) ToolRender {
	path := stringField(input, "path")
	content := stringField(input, fieldContent)
	lang := guessLanguage(path)
	lineCount := strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		lineCount++
	}
	summary := path
	if lineCount > 0 {
		summary = fmt.Sprintf("%s · %d lines", path, lineCount)
	}
	sections := []ToolSection{}
	if content != "" {
		sections = append(sections, ToolSection{
			Label:    "Content",
			Body:     renderCodeBlock(content, lang),
			Language: lang,
		})
	}
	sections = appendIfBody(sections, extraArgsSection(input, "path", fieldContent))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: summary, Prelude: renderIntentPrelude(input), Sections: sections}
}

func renderEdit(input map[string]any, output string, isError bool) ToolRender {
	path := stringField(input, "path")
	if path == "" {
		path = stringField(input, "file_path")
	}
	oldStr := stringField(input, "old_string")
	newStr := stringField(input, "new_string")
	diff := UnifiedDiff(oldStr, newStr)
	sections := []ToolSection{
		{Label: "Diff", Body: renderDiff(diff)},
	}
	sections = appendIfBody(sections, extraArgsSection(input, "path", "file_path", "old_string", "new_string"))
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelResult,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{Summary: path, Prelude: renderIntentPrelude(input), Sections: sections}
}

// renderCodeBlock wraps text in a <pre><code class="language-X">, escaped for HTML.
// lang=="" produces a <pre> with no language hint.
func renderCodeBlock(text, lang string) template.HTML {
	cls := `text-xs bg-white rounded border border-edge p-2 overflow-auto`
	if lang == "" {
		return template.HTML(`<pre class="` + cls + `">` + html.EscapeString(text) + `</pre>`)
	}
	return template.HTML(`<pre class="` + cls + `"><code class="language-` + html.EscapeString(lang) + `">` + html.EscapeString(text) + `</code></pre>`)
}

// renderDiff renders a unified diff (see UnifiedDiff) with line-level styling.
func renderDiff(diff string) template.HTML {
	var b strings.Builder
	b.WriteString(`<pre class="text-xs bg-white rounded border border-edge p-2 overflow-auto font-mono">`)
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		switch line[0] {
		case '+':
			b.WriteString(`<span class="bg-green-50 text-green-800 block">`)
			b.WriteString(html.EscapeString(line))
			b.WriteString(`</span>`)
		case '-':
			b.WriteString(`<span class="bg-red-50 text-red-800 block">`)
			b.WriteString(html.EscapeString(line))
			b.WriteString(`</span>`)
		default:
			b.WriteString(`<span class="block">`)
			b.WriteString(html.EscapeString(line))
			b.WriteString(`</span>`)
		}
	}
	b.WriteString(`</pre>`)
	return template.HTML(b.String())
}

// ToolRender is the rendered form of a single tool call — a short summary
// shown in the collapsed header, an optional italicized prelude (usually
// the call's "purpose" arg) shown above the sections, plus one or more
// sections rendered in order.
type ToolRender struct {
	Summary  string
	Prelude  template.HTML
	Sections []ToolSection
}

// ToolSection is one labeled block inside a rendered tool call.
type ToolSection struct {
	Label     string
	Body      template.HTML // pre-rendered, safe HTML
	Language  string        // highlight.js language hint; empty means plain text
	IsError   bool
	Collapsed bool // default fold state; errors are never collapsed
}

// ToolRenderer produces a ToolRender for one tool.
type ToolRenderer func(input map[string]any, output string, isError bool) ToolRender

var toolRenderers = map[string]ToolRenderer{}

// RenderTool dispatches to the registered renderer for a tool name, or the
// generic renderer if none is registered.
func RenderTool(name string, input map[string]any, output string, isError bool) ToolRender {
	if fn, ok := toolRenderers[name]; ok {
		return fn(input, output, isError)
	}
	return renderGeneric(input, output, isError)
}

// renderGeneric is the fallback: sorted KV table for input, plain pre for output.
func renderGeneric(input map[string]any, output string, isError bool) ToolRender {
	sections := []ToolSection{}
	// Input (args) first.
	if len(input) > 0 {
		// Skip "purpose" from the Input table since it's lifted to the prelude.
		display := map[string]any{}
		for k, v := range input {
			if k == fieldPurpose {
				continue
			}
			display[k] = v
		}
		if len(display) > 0 {
			sections = append(sections, ToolSection{
				Label: "Input",
				Body:  renderKVTable(display),
			})
		}
	}
	// Output (result) last.
	if output != "" {
		sections = append(sections, ToolSection{
			Label:   labelOutput,
			Body:    renderPlainPre(output, isError),
			IsError: isError,
		})
	}
	return ToolRender{
		Summary:  shortSummary(input),
		Prelude:  renderIntentPrelude(input),
		Sections: sections,
	}
}

// shortSummary picks a one-line hint from the input (first string-valued key,
// truncated). Used when no tool-specific renderer gives a better summary.
func shortSummary(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := input[k].(string); ok {
			return truncate(s, 80)
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// renderKVTable renders a map as a sorted HTML table.
func renderKVTable(input map[string]any) template.HTML {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<table class="w-full text-xs">`)
	for _, k := range keys {
		b.WriteString(`<tr class="border-t border-edge"><td class="py-0.5 pr-2 font-mono text-muted align-top">`)
		b.WriteString(html.EscapeString(k))
		b.WriteString(`</td><td class="py-0.5 whitespace-pre-wrap break-all">`)
		b.WriteString(html.EscapeString(formatOutputValue(input[k])))
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</table>`)
	return template.HTML(b.String())
}

// renderPlainPre wraps text in a <pre>. Error blocks get red styling.
func renderPlainPre(text string, isError bool) template.HTML {
	cls := `text-xs bg-white rounded border border-edge p-2 overflow-auto whitespace-pre-wrap break-all`
	if isError {
		cls += ` text-red-700 bg-red-50 border-red-200`
	}
	return template.HTML(`<pre class="` + cls + `">` + html.EscapeString(text) + `</pre>`)
}

// guessLanguage infers a highlight.js language name from a file path.
func guessLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".yaml", ".yml":
		return langYAML
	case ".json":
		return langJSON
	case ".ts", ".tsx":
		return langTypeScript
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return langPython
	case ".sh":
		return toolBash
	case ".md":
		return "markdown"
	case ".rs":
		return "rust"
	case ".html":
		return "html"
	case ".css":
		return "css"
	}
	return ""
}
