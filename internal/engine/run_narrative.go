package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

const runIntentSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "title": { "type": "string", "minLength": 1, "maxLength": 160 },
    "description": { "type": "string", "minLength": 1, "maxLength": 8000 }
  },
  "required": ["title", "description"]
}`

const runSummarySchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "summary": { "type": "string", "minLength": 1, "maxLength": 20000 }
  },
  "required": ["summary"]
}`

// RunIntentNarrative is the LLM-generated intent metadata shown in the UI.
// It intentionally has no strict "UI length" constraints; prompts should
// request concision while the schema stays permissive.
type RunIntentNarrative struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// RunSummaryNarrative is the LLM-generated outcome summary shown in the UI.
type RunSummaryNarrative struct {
	Summary string `json:"summary"`
}

func maybeGenerateRunIntent(_ context.Context, runDir string, runState *state.RunState, workflow *definitions.Workflow, _ *state.Logger) {
	if strings.TrimSpace(runState.Description) != "" {
		return
	}

	// Apply deterministic fallback synchronously so the UI has something immediately.
	runState.Description = compactText(firstNonEmptyLine(workflow.Description), 220)
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)

	if !llmcallAvailable() {
		return
	}

	// Upgrade asynchronously via LLM. The run loop's periodic SaveState calls
	// will persist the improved narrative to disk when it arrives.
	go func() {
		callCtx, cancel := context.WithTimeout(context.Background(), narrativeTimeout(15*time.Second))
		defer cancel()

		out, _, err := GenerateRunIntentNarrative(callCtx, workflow, runState)
		if err != nil {
			return
		}
		runState.SetNarrative(out.Title, out.Description)
	}()
}

func maybeGenerateRunSummary(_ context.Context, runDir string, runState *state.RunState, workflow *definitions.Workflow, _ *state.Logger) {
	if strings.TrimSpace(runState.Summary) != "" {
		return
	}
	status := strings.TrimSpace(runState.Status)
	if status != statusCompleted && status != statusFailed && status != statusCancelled {
		return
	}

	// Apply deterministic fallback synchronously so the UI has something immediately.
	switch status {
	case statusCompleted:
		if runState.HasUnresolvedFailure {
			runState.Summary = "Terminated with unresolved failure routing."
		} else {
			runState.Summary = "Completed."
		}
	case statusCancelled:
		runState.Summary = "Cancelled."
	case statusFailed:
		if strings.TrimSpace(runState.Error) != "" {
			runState.Summary = compactText("Failed: "+runState.Error, 600)
		} else {
			runState.Summary = "Failed."
		}
	}
	_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)

	if !llmcallAvailable() {
		return
	}

	// Upgrade asynchronously via LLM. The run is finished so no more run-loop
	// saves will happen — the goroutine persists the upgrade to disk itself.
	go func() {
		callCtx, cancel := context.WithTimeout(context.Background(), narrativeTimeout(20*time.Second))
		defer cancel()

		out, _, err := GenerateRunSummaryNarrative(callCtx, workflow, runState)
		if err != nil {
			return
		}
		runState.SetSummary(out.Summary)
		_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)
	}()
}

// BuildRunIntentPrompt builds the prompt used to generate a run title/description.
// This is exported for offline/preview tooling; production generation uses the
// same underlying prompt.
func BuildRunIntentPrompt(workflow *definitions.Workflow, runState *state.RunState) string {
	return buildRunIntentPrompt(workflow, runState)
}

// BuildRunSummaryPrompt builds the prompt used to generate a run outcome summary.
// This is exported for offline/preview tooling; production generation uses the
// same underlying prompt.
func BuildRunSummaryPrompt(workflow *definitions.Workflow, runState *state.RunState) string {
	return buildRunSummaryPrompt(workflow, runState)
}

// GenerateRunIntentNarrative generates run intent metadata via llmcall, returning
// both the prompt (for debugging/workshopping) and the validated result.
func GenerateRunIntentNarrative(ctx context.Context, workflow *definitions.Workflow, runState *state.RunState) (RunIntentNarrative, string, error) {
	if !llmcallAvailable() {
		return RunIntentNarrative{}, "", fmt.Errorf("llmcall not found")
	}
	prompt := buildRunIntentPrompt(workflow, runState)
	var out RunIntentNarrative
	if err := llmcallObject(ctx, runIntentSchema, prompt, &out); err != nil {
		return RunIntentNarrative{}, prompt, err
	}
	out.Title = strings.TrimSpace(out.Title)
	out.Description = strings.TrimSpace(out.Description)
	return out, prompt, nil
}

// GenerateRunSummaryNarrative generates a run outcome summary via llmcall,
// returning both the prompt (for debugging/workshopping) and the validated result.
func GenerateRunSummaryNarrative(ctx context.Context, workflow *definitions.Workflow, runState *state.RunState) (RunSummaryNarrative, string, error) {
	if !llmcallAvailable() {
		return RunSummaryNarrative{}, "", fmt.Errorf("llmcall not found")
	}
	prompt := buildRunSummaryPrompt(workflow, runState)
	var out RunSummaryNarrative
	if err := llmcallObject(ctx, runSummarySchema, prompt, &out); err != nil {
		return RunSummaryNarrative{}, prompt, err
	}
	out.Summary = strings.TrimSpace(out.Summary)
	return out, prompt, nil
}

func buildRunIntentPrompt(workflow *definitions.Workflow, runState *state.RunState) string {
	ctx := map[string]any{
		"workflow": map[string]any{
			"id":           workflow.ID,
			keyName:        strings.TrimSpace(workflow.Name),
			keyDescription: compactText(firstNonEmptyLine(workflow.Description), 400),
		},
		keyInputs: summarizeInputsForNarrative(runState.Inputs),
	}
	b, _ := json.MarshalIndent(ctx, "", "  ")

	return strings.TrimSpace(fmt.Sprintf(`
You write short UI copy for a workflow run.

Return an object with:
- title: short label for the run (aim for <= 80 chars; 4-10 words)
- description: one-sentence description of what THIS SPECIFIC run is doing, not a generic description of the workflow type (present tense; aim for <= 240 chars)

Hard rules:
- Use only facts from the Context JSON. If something is missing/unclear, omit it (do not guess).
- Do not mention internal IDs (run IDs like "voyage-onyx-crane"), filesystem paths, or local hostnames.
- No markdown, no bullets, no quotes.
- Do not start the description with boilerplate like "Runs the ... workflow ..." or "Run the ... workflow ...".
- The description must distinguish this run from other runs of the same workflow. Reference the specific inputs: which agent/role is being acted on, what component is being built, what task is being performed.

Include if available:
- Product name (from spec) and/or product slug
- Sprint title
- The specific target (role name, component name, node ID) from inputs
- The primary goal implied by workflow name/description combined with the specific inputs

Context JSON:
%s
`, string(b)))
}

func buildRunSummaryPrompt(workflow *definitions.Workflow, runState *state.RunState) string {
	nodes := summarizeNodesForNarrative(runState)
	ctx := map[string]any{
		"workflow": map[string]any{
			"id":    workflow.ID,
			keyName: strings.TrimSpace(workflow.Name),
		},
		"run": map[string]any{
			keyTitle:                strings.TrimSpace(runState.Title),
			keyDescription:          strings.TrimSpace(runState.Description),
			fieldStatus:             strings.TrimSpace(runState.Status),
			keyHasUnresolvedFailure: runState.HasUnresolvedFailure,
			keyError:                strings.TrimSpace(runState.Error),
			"started_at":            runState.StartedAt.Format(time.RFC3339),
			"finished_at":           formatMaybeRFC3339(runState.FinishedAt),
		},
		keyInputs: summarizeInputsForNarrative(runState.Inputs),
		"nodes":   nodes,
	}
	b, _ := json.MarshalIndent(ctx, "", "  ")

	return strings.TrimSpace(fmt.Sprintf(`
You write a concise, evidence-based outcome summary for a workflow run.

Return an object with:
- summary: 1-3 sentences describing what happened (past tense). Keep it high-signal and scan-friendly.

Rules:
- No markdown, no bullets, no quotes.
- Only use facts visible in the Context JSON. Do not invent work that isn't supported.
- Do not claim that stories were implemented or code was changed unless a node message explicitly says so.
- Treat inputs as scope/parameters, not outcomes. Do not use inputs stories fields to claim work was completed.
- Do not start with boilerplate like "Run completed successfully" or "The run completed successfully". Start directly with the most important outcomes/actions.
- If status is completed, do not include generic completion phrases like "completed successfully" or "finished successfully". Focus on concrete outcomes/actions.
- Avoid internal implementation detail words/strings like filesystem paths (e.g. ".toil/handoff"), git strategy names (e.g. "ort"), exact branch names, or component IDs. Prefer plain-English phrasing (e.g. "handoff files were written", "changes were merged").
- If the overall run status is completed and has_unresolved_failure is false, do not describe the run as waiting/paused/awaiting input even if a node message contains that wording; focus on what actually happened. If has_unresolved_failure is true, such language in node messages is intentional failure-context signal and may be preserved.
- If failed, name the failing step if obvious; include the error briefly.
- If cancelled, say it was cancelled.

Context JSON:
%s
`, string(b)))
}

func summarizeInputsForNarrative(inputs map[string]any) map[string]any {
	if inputs == nil {
		return map[string]any{}
	}
	out := map[string]any{}

	// Well-known structured fields.
	if slug, ok := inputs[keyProductSlug].(string); ok && strings.TrimSpace(slug) != "" {
		out[keyProductSlug] = strings.TrimSpace(slug)
	}

	if sprint, ok := inputs["sprint"].(map[string]any); ok && sprint != nil {
		out["sprint"] = map[string]any{
			"id":     stringField(sprint, "id"),
			keyTitle: stringField(sprint, keyTitle),
		}
	}

	if spec, ok := inputs[keySpec].(string); ok && strings.TrimSpace(spec) != "" {
		out["spec_head"] = compactText(firstNonEmptyLine(spec), 240)
	}

	out[keyStories] = summarizeStories(inputs[keyStories], 8)
	out["stories_context"] = summarizeStories(inputs["stories_context"], 6)

	if dir, ok := inputs[keyProjectDir].(string); ok && strings.TrimSpace(dir) != "" {
		out["project_dir_tail"] = tailPath(strings.TrimSpace(dir), 2)
	}

	// Include all other scalar inputs not already captured, so the LLM
	// can describe what makes this specific run unique. Skip paths,
	// long values, and complex objects.
	knownKeys := map[string]bool{
		keyProductSlug: true, "sprint": true, keySpec: true,
		keyStories: true, "stories_context": true, keyProjectDir: true,
	}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		if !knownKeys[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	extra := map[string]any{}
	for _, k := range keys {
		v := inputs[k]
		switch val := v.(type) {
		case string:
			s := strings.TrimSpace(val)
			if s == "" || strings.HasPrefix(s, "/") {
				continue
			}
			extra[k] = compactText(s, 200)
		case float64:
			extra[k] = val
		case bool:
			extra[k] = val
		case []any:
			if len(val) <= 5 {
				// Include small arrays of scalars (e.g. tag lists).
				allScalar := true
				for _, item := range val {
					if _, ok := item.(string); !ok {
						if _, ok := item.(float64); !ok {
							allScalar = false
							break
						}
					}
				}
				if allScalar {
					extra[k] = val
				}
			}
		}
	}
	if len(extra) > 0 {
		out["other_inputs"] = extra
	}

	return out
}

func summarizeStories(value any, limit int) map[string]any {
	arr, ok := value.([]any)
	if !ok {
		return map[string]any{keyCount: 0, keyItems: []any{}}
	}

	items := make([]map[string]string, 0, min(limit, len(arr)))
	for _, raw := range arr {
		if len(items) >= limit {
			break
		}
		m, ok := raw.(map[string]any)
		if !ok || m == nil {
			continue
		}
		id := strings.TrimSpace(stringField(m, "id"))
		title := strings.TrimSpace(stringField(m, keyTitle))
		if id == "" && title == "" {
			continue
		}
		entry := map[string]string{}
		if id != "" {
			entry["id"] = id
		}
		if title != "" {
			entry[keyTitle] = compactText(title, 120)
		}
		items = append(items, entry)
	}

	return map[string]any{
		keyCount: len(arr),
		keyItems: items,
	}
}

func summarizeNodesForNarrative(runState *state.RunState) []map[string]any {
	if runState == nil || runState.Nodes == nil {
		return nil
	}
	runStatus := strings.TrimSpace(runState.Status)
	ids := make([]string, 0, len(runState.Nodes))
	for id := range runState.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]map[string]any, 0, min(40, len(ids)))
	for _, id := range ids {
		if len(out) >= 40 {
			break
		}
		n := runState.Nodes[id]
		if n == nil {
			continue
		}
		// Skip noisy for_each expanded nodes unless they have a non-empty message.
		if strings.Contains(id, "::") && strings.TrimSpace(n.Message) == "" {
			continue
		}
		entry := map[string]any{
			"id":        id,
			fieldStatus: strings.TrimSpace(n.Status),
		}
		if strings.TrimSpace(n.Decision) != "" {
			entry[fieldDecision] = strings.TrimSpace(n.Decision)
		}
		if msg := sanitizeNarrativeMessage(runStatus, runState.HasUnresolvedFailure, n.Message); msg != "" {
			entry[fieldMessage] = msg
		}
		out = append(out, entry)
	}
	return out
}

func sanitizeNarrativeMessage(runStatus string, hasUnresolvedFailure bool, message string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return ""
	}

	// For completed runs, normalize noisy internal messages so summaries stay
	// user-facing (no git strategy names, paths, or raw branch names).
	// When HasUnresolvedFailure=true we still apply the internal-detail scrubs,
	// but we do NOT suppress waiting/paused/awaiting language — those messages
	// are legitimate failure-context signal (e.g. "paused at failure gate",
	// "awaiting approval").
	if runStatus == statusCompleted {
		// Suppress waiting/paused language only for clean completions.
		if !hasUnresolvedFailure {
			lower := strings.ToLower(msg)
			if strings.Contains(lower, "awaiting") || strings.Contains(lower, "waiting for") || strings.Contains(lower, "paused") {
				return ""
			}
		}

		first := firstNonEmptyLine(msg)
		if first == "" {
			return ""
		}
		lower := strings.ToLower(msg)
		lowerFirst := strings.ToLower(first)

		// Defensive normalization for raw git merge stdout, in case any node
		// ever bypasses the structured JSON output and surfaces git's
		// "Merge made by the 'ort' strategy." chatter directly. No current
		// node produces this — merge_branch.sh and merge_task_worktree.sh
		// both emit their own decision messages and tgwm swallows git stdout
		// on success — but the normalization stays as a cheap safety net.
		if strings.HasPrefix(first, "Merge made by") {
			return "Merged changes."
		}

		// Normalize common "materialize" messages to avoid surfacing internal paths.
		if strings.Contains(lowerFirst, "materialized handoff files") {
			return "Materialized handoff files in the project workspace."
		}

		// Normalize merge+verify chatter to avoid exposing branch/worktree names.
		if strings.HasPrefix(first, "Merged ") && strings.Contains(first, " into main") {
			merged := "Merged changes into main"
			if strings.Contains(lower, "resolved conflicts") || strings.Contains(lower, "conflicts resolved") {
				merged += " (conflicts resolved)"
			}
			passed := extractIntBeforeWord(lower, "passed")
			exitCode := extractDigitsAfter(lower, "exit_code=")
			if passed != "" || exitCode != "" {
				var parts []string
				if passed != "" {
					parts = append(parts, passed+" passed")
				}
				if exitCode != "" {
					parts = append(parts, "exit_code="+exitCode)
				}
				return merged + "; tests ran (" + strings.Join(parts, ", ") + ")."
			}
			if strings.Contains(lower, "pytest") || strings.Contains(lower, "go test") || strings.Contains(lower, "verifier") {
				return merged + "; tests ran."
			}
			return merged + "."
		}

		return compactText(first, 240)
	}

	return compactText(msg, 240)
}

func extractIntBeforeWord(s, word string) string {
	i := strings.Index(s, word)
	if i < 0 {
		return ""
	}
	j := i - 1
	for j >= 0 && s[j] == ' ' {
		j--
	}
	k := j
	for k >= 0 && s[k] >= '0' && s[k] <= '9' {
		k--
	}
	if k == j {
		return ""
	}
	return s[k+1 : j+1]
}

func extractDigitsAfter(s, prefix string) string {
	i := strings.Index(s, prefix)
	if i < 0 {
		return ""
	}
	i += len(prefix)
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return ""
	}
	return s[i:j]
}

func llmcallObject(ctx context.Context, schema string, prompt string, target any) error {
	schemaPath, cleanup, err := writeTemp(schema)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{
		"--schema", schemaPath,
		"--max-tokens", "400",
		// gpt-5-mini can otherwise spend the output budget on hidden reasoning and
		// return incomplete/no output_text, which breaks strict JSON schema parsing.
		"--reasoning-effort", "minimal",
		prompt,
	}

	cmd := exec.CommandContext(ctx, "llmcall", args...)
	cmd.Env = append(os.Environ(), narrativeLLMEnv()...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("llmcall: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, target); err != nil {
		return fmt.Errorf("parse llmcall output: %w", err)
	}
	return nil
}

func narrativeLLMEnv() []string {
	// Allow Toil-specific override without disturbing SERF_* defaults.
	// llmcall resolves provider/model from LLM_* first, then SERF_*.
	var env []string
	if v := strings.TrimSpace(os.Getenv("TOIL_RUN_NARRATIVE_PROVIDER")); v != "" {
		env = append(env, "LLM_PROVIDER="+v)
	}
	if v := strings.TrimSpace(os.Getenv("TOIL_RUN_NARRATIVE_MODEL")); v != "" {
		env = append(env, "LLM_MODEL="+v)
	}
	return env
}

// llmcallAvailable caches whether the llmcall binary is on PATH.
// The result is computed once and reused for the lifetime of the process.
var (
	llmcallOnce  sync.Once
	llmcallFound bool
)

func llmcallAvailable() bool {
	llmcallOnce.Do(func() {
		_, err := exec.LookPath("llmcall")
		llmcallFound = err == nil
	})
	return llmcallFound
}

func narrativeTimeout(defaultTimeout time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv("TOIL_RUN_NARRATIVE_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultTimeout
	}
	return d
}

func writeTemp(content string) (string, func(), error) {
	f, err := os.CreateTemp("", "toil-llmcall-schema-*.json")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

func formatMaybeRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func compactText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return ""
	}
	if max > 0 && len(s) > max {
		if max <= 3 {
			return s[:max]
		}
		return s[:max-3] + "..."
	}
	return s
}

func tailPath(path string, parts int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(segs) == 0 {
		return ""
	}
	if parts <= 0 || len(segs) <= parts {
		return segs[len(segs)-1]
	}
	return strings.Join(segs[len(segs)-parts:], "/")
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
