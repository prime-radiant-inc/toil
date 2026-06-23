package document

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// Event types emitted to the run event log, matched while walking events.
const (
	eventNodeStarted        = "node_started"
	eventNodeCompleted      = "node_completed"
	eventNodeFailed         = "node_failed"
	eventNodePrompt         = "node_prompt"
	eventNodeAttemptStarted = "node_attempt_started"
	eventNodeOutput         = "node_output"
	eventSubworkflowStarted = "subworkflow_started"
)

// Decision ids and the display families they classify into.
const (
	decisionAllSucceeded     = "all_succeeded"
	decisionCorrectFailure   = "correct_failure"
	decisionCompleted        = "completed"
	decisionChangesRequested = "changes_requested"
	decisionReadyForReview   = "ready_for_review"
	decisionApproved         = "approved"
	decisionDefault          = "default"
	decisionSkip             = "skip"
	decisionRejected         = "rejected"
	decisionTestsPass        = "tests_pass"
	decisionTestsFail        = "tests_fail"
	familyPass               = "pass"
	familyFail               = "fail"
	familyNeutral            = "neutral"
)

// Attempt outcomes recorded on a transcript attempt.
const (
	outcomeFailed    = "failed"
	outcomeSucceeded = "succeeded"
)

// Message kinds in a transcript stream. kindDecision doubles as the
// node_completed event-data key carrying the decision id.
const (
	kindAssistant  = "assistant"
	kindUserPrompt = "user_prompt"
	kindToolCall   = "tool_call"
	kindDecision   = "decision"
)

// Artifact names / NodeState.Data keys surfaced on rows.
const (
	artifactChildRun = "child_run"
	artifactPlan     = "plan"
	artifactItems    = "items"
	artifactKindList = "list"
	inputKeyTask     = "task"
)

// EventLoader is an optional interface that a Loader may implement to supply
// the raw event slice for a run. BuildDocument uses this to populate
// per-row transcripts without requiring callers to add runsDir to every
// signature.
type EventLoader interface {
	LoadEvents(runID string) []state.Event
}

// WorkflowSnapshotLoader is an optional interface a Loader may implement to
// supply the per-run workflow.yaml snapshot. buildRunNode uses it to attach
// a per-run RunTopologyWithMetrics on every RunNode for inline graph
// rendering. Returning nil is fine — the topology field stays unset.
type WorkflowSnapshotLoader interface {
	LoadWorkflowSnapshot(runID string) *definitions.Workflow
}

// DecisionMeta is the subset of decision metadata the document builder needs.
type DecisionMeta struct {
	Description string
	Tags        []string
}

// DecisionFinder is an optional interface that a Registry may implement to
// supply full decision metadata for a (workflowID, decisionID) pair.
// WorkflowRegistry satisfies this; minimal fakes may omit it.
type DecisionFinder interface {
	FindDecisionMeta(workflowID, decisionID string) *DecisionMeta
}

// ErrRunNotFound is returned by Loader.LoadRun when the run id is unknown.
var ErrRunNotFound = errors.New("run not found")

// sanitizeTitle strips trailing Go map literals (`: map[…]`) and other
// `fmt.Sprintf("%v", structured)` leakage that older runs may have
// persisted. The structured fields are unrecoverable from the string,
// but we can stop showing the raw Go syntax. New runs avoid this at the
// source (see engine.summarizeSubjectValue).
func sanitizeTitle(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, ": map["); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	if idx := strings.Index(s, ": [map["); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// Loader abstracts run-state access so BuildDocument can be tested without
// hitting disk. The real implementation lives in internal/api or
// internal/dashboard and wraps the on-disk store.
type Loader interface {
	LoadRun(id string) (*state.RunState, error)
	ChildRuns(parentID string) []string
}

// DecisionFamily returns the semantic family for a decision ID.
// Used by server.go to enrich decision messages in the API response.
func DecisionFamily(id string) string { return decisionFamily(id) }

// decisionFamily maps a decision id to one of the canonical display families.
// Used when the definitions.Decision type does not carry a Family field, so we
// derive it from id conventions. Returns "" for unknown ids (renderer uses a
// neutral default).
func decisionFamily(id string) string {
	switch id {
	case familyPass, "passed", decisionApproved, "success", decisionTestsPass, decisionAllSucceeded,
		decisionCorrectFailure, decisionDefault, outcomeSucceeded, "resolved", "prepared",
		decisionSkip, decisionCompleted, "force_approve", "ready", "merged":
		return familyPass
	case familyFail, outcomeFailed, decisionRejected, decisionTestsFail, "fix_failed", "failed_handled",
		"give_up", "cancelled":
		return familyFail
	case "escalate", "escalated":
		return "escalate"
	case "skipped":
		return decisionSkip
	}
	return ""
}

// SliceExecutionEvents returns the subset of events belonging to the ordinal-th
// execution of nodeID (1-based). It finds the ordinal-th node_started for that
// node, then collects all events from that point up to and including the
// matching node_completed (or node_failed). Events from other nodes that fall
// within this window are included because BuildTranscript already filters by
// nodeID, so they are harmless.
//
// When ordinal is 0 (old rows without AttemptOrdinal), we return all events
// so BuildTranscript can handle them with its own fallback logic.
func SliceExecutionEvents(events []state.Event, nodeID string, ordinal int) []state.Event {
	if len(events) == 0 {
		return nil
	}
	// ordinal 0 means no execution-order info (fallback path); return all events.
	if ordinal == 0 {
		return events
	}

	// Find the index of the ordinal-th node_started for this node.
	startCount := 0
	startIdx := -1
	for i, ev := range events {
		if ev.NodeID == nodeID && ev.Type == eventNodeStarted {
			startCount++
			if startCount == ordinal {
				startIdx = i
				break
			}
		}
	}
	if startIdx < 0 {
		// No matching node_started found — return all events (BuildTranscript
		// will do its best with what it has).
		return events
	}

	// Find the matching node_completed (or node_failed): the ordinal-th
	// completion event for this node that appears after startIdx.
	completionCount := 0
	endIdx := len(events) - 1
	for i := startIdx + 1; i < len(events); i++ {
		ev := events[i]
		if ev.NodeID != nodeID {
			continue
		}
		if ev.Type == eventNodeCompleted || ev.Type == eventNodeFailed {
			completionCount++
			if completionCount == 1 {
				endIdx = i
				break
			}
		}
	}

	return events[startIdx : endIdx+1]
}

// buildBrief computes the brief block fields for a run: the BriefText (from
// inputs.spec when present), BriefSource label, and BriefFields list. The reg
// parameter is currently unused but kept in the signature so we can route
// registry-aware brief enrichment through here in the future.
func buildBrief(rs *state.RunState, reg Registry) (text, source string, fields []BriefField) {
	_ = reg
	if rs == nil || rs.Inputs == nil {
		return "", "", nil
	}
	if spec, ok := rs.Inputs["spec"].(string); ok && spec != "" {
		return spec, "inputs.spec", nil
	}
	bf := briefFieldsFromInputs(rs.Inputs)
	if len(bf) == 0 {
		return "", "", nil
	}
	return "", "inputs", bf
}

// findPromptForExecution returns the raw text of the node_prompt event that
// belongs to the ordinal-th execution of nodeID. The engine emits node_prompt
// just before node_started, so this function locates the Nth node_started for
// nodeID and then searches backwards for the most recent node_prompt for that
// same node. Returns "" when no matching event is found.
func findPromptForExecution(events []state.Event, nodeID string, ordinal int) string {
	if ordinal == 0 || len(events) == 0 {
		return ""
	}
	// Find the index of the ordinal-th node_started for nodeID.
	startCount := 0
	startIdx := -1
	for i, ev := range events {
		if ev.NodeID == nodeID && ev.Type == eventNodeStarted {
			startCount++
			if startCount == ordinal {
				startIdx = i
				break
			}
		}
	}
	if startIdx < 0 {
		return ""
	}
	// Search backwards from startIdx for the most recent node_prompt for nodeID.
	// It typically sits one event before node_started, but we scan a small window
	// to be safe with any reordering.
	for i := startIdx - 1; i >= 0 && i >= startIdx-10; i-- {
		ev := events[i]
		if ev.NodeID == nodeID && ev.Type == eventNodePrompt && ev.Text != "" {
			return ev.Text
		}
		// Stop if we hit a prior node_started for the same node — that prompt
		// belongs to a different execution.
		if ev.NodeID == nodeID && ev.Type == eventNodeStarted {
			break
		}
	}
	return ""
}

// hasNodeStartedEvents reports whether the event slice contains at least one
// node_started event. Used to decide whether to use the chronological-execution
// path or fall back to node-state ordering.
func hasNodeStartedEvents(events []state.Event) bool {
	for _, ev := range events {
		if ev.Type == eventNodeStarted {
			return true
		}
	}
	return false
}

func isForEachBase(n *state.NodeState) bool {
	if n == nil || n.Data == nil {
		return false
	}
	_, ok := n.Data[artifactItems]
	return ok
}

func isExpandedIteration(nodeID string) bool {
	// Convention: expanded ForEach items have ids like "node::N"
	for i := 0; i+1 < len(nodeID); i++ {
		if nodeID[i] == ':' && nodeID[i+1] == ':' {
			return true
		}
	}
	return false
}

func childRunOf(n *state.NodeState) string {
	if n == nil || n.Data == nil {
		return ""
	}
	if v, ok := n.Data[artifactChildRun].(string); ok {
		return v
	}
	return ""
}

// trimWorkflowNamePrefix removes a leading "<Workflow Name>:" from a
// run's description so the dashboard breadcrumb doesn't show the
// workflow type twice (once as a pill, once as a description prefix).
func trimWorkflowNamePrefix(desc, name string) string {
	if name == "" || desc == "" {
		return desc
	}
	if !strings.HasPrefix(desc, name) {
		return desc
	}
	rest := strings.TrimPrefix(desc, name)
	rest = strings.TrimLeft(rest, " \t:·-—")
	if rest == "" {
		return desc
	}
	return rest
}

// foreachIterationPrefix returns the iteration template name encoded in a
// ForEach base node's items (e.g. "implement" for items[].expanded_id =
// "implement::0"). When non-empty, it scopes which subworkflow_started events
// belong to this base — important when sibling ForEach bases run in the same
// run. Returns "" if no items or no expanded_id is present.
func foreachIterationPrefix(base *state.NodeState) string {
	if base == nil || base.Data == nil {
		return ""
	}
	raw, _ := base.Data[artifactItems].([]any)
	for _, it := range raw {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		eid, _ := m["expanded_id"].(string)
		if eid == "" {
			continue
		}
		if idx := strings.Index(eid, "::"); idx > 0 {
			return eid[:idx]
		}
	}
	return ""
}

// rowArtifacts returns a short list (max 3) of artifact references to show
// inline on the row. Picks from common NodeState.Data shapes.
func rowArtifacts(n *state.NodeState) []ArtifactRef {
	if n == nil || n.Data == nil {
		return nil
	}
	var out []ArtifactRef
	// child_run: this node spawned a sub-workflow
	if cr, ok := n.Data[artifactChildRun].(string); ok && cr != "" {
		out = append(out, ArtifactRef{Name: artifactChildRun, Kind: "run", Desc: "→ " + cr})
	}
	// commit: shell/system nodes that committed
	if c, ok := n.Data["commit"].(string); ok && c != "" {
		short := c
		if len(short) > 12 {
			short = short[:12]
		}
		out = append(out, ArtifactRef{Name: "commit", Kind: "commit", Desc: short})
	}
	// plan: plan_tasks-style plans
	if plan, ok := n.Data[artifactPlan].(map[string]any); ok {
		if tasks, ok := plan["tasks"].([]any); ok {
			out = append(out, ArtifactRef{Name: artifactPlan, Kind: "object", Desc: fmt.Sprintf("%d tasks", len(tasks))})
		} else {
			out = append(out, ArtifactRef{Name: artifactPlan, Kind: "object"})
		}
	}
	// items: ForEach base node's fan-out
	if items, ok := n.Data[artifactItems].([]any); ok {
		out = append(out, ArtifactRef{Name: artifactItems, Kind: artifactKindList, Desc: fmt.Sprintf("%d", len(items))})
	}
	// proposals / learnings (from learn workflow)
	if props, ok := n.Data["proposals"].([]any); ok && len(props) > 0 {
		out = append(out, ArtifactRef{Name: "proposals", Kind: artifactKindList, Desc: fmt.Sprintf("%d", len(props))})
	}
	if lr, ok := n.Data["learnings"].([]any); ok && len(lr) > 0 {
		out = append(out, ArtifactRef{Name: "learnings", Kind: artifactKindList, Desc: fmt.Sprintf("%d", len(lr))})
	}
	// reviewed_files, summary, issues — other common review-shaped fields
	if rf, ok := n.Data["reviewed_files"].([]any); ok && len(rf) > 0 {
		out = append(out, ArtifactRef{Name: "reviewed_files", Kind: artifactKindList, Desc: fmt.Sprintf("%d", len(rf))})
	}
	if issues, ok := n.Data["issues"].([]any); ok && len(issues) > 0 {
		out = append(out, ArtifactRef{Name: "issues", Kind: artifactKindList, Desc: fmt.Sprintf("%d", len(issues))})
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// disclosureHint returns a short string for the ▸ show details link
// summarizing what's behind it. Empty string → use the generic label.
func disclosureHint(n *state.NodeState) string {
	if n == nil {
		return ""
	}
	var parts []string
	if len(n.Data) > 0 {
		keys := 0
		for _, v := range n.Data {
			if v != nil && v != "" {
				keys++
			}
		}
		if keys > 0 {
			parts = append(parts, fmt.Sprintf("%d outputs", keys))
		}
	}
	if n.SessionID != "" {
		parts = append(parts, "session "+sidPrefix(n.SessionID))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// sidPrefix returns a short prefix of a session id for compact display.
func sidPrefix(s string) string {
	if len(s) < 6 {
		return s
	}
	return s[:6] + "…"
}

// classifyDecision maps a workflow decision string to one of the five
// display families. Conservative defaults; the dashboard CSS keys off
// these family names.
func classifyDecision(d string) string {
	switch d {
	case "":
		return familyNeutral
	case decisionApproved, "ready", "merged", decisionTestsPass, decisionAllSucceeded,
		decisionCorrectFailure, decisionDefault, outcomeSucceeded, "resolved", "prepared",
		decisionSkip, decisionCompleted, "force_approve":
		return "ok"
	case decisionChangesRequested, decisionTestsFail, "fix_failed", outcomeFailed,
		"failed_handled", decisionRejected, "conflict", "conflict_unresolved",
		"give_up", "root_cause_confirmed", "cancelled":
		return "bad"
	case decisionReadyForReview, "ready_for_plan", "planned":
		return artifactPlan
	case "analysis", "dispatched":
		return familyNeutral
	default:
		return familyNeutral
	}
}

// briefFieldsFromInputs returns a small list of key/value pairs to
// render in the brief block when inputs.spec isn't present. It filters
// out noisy keys (env-style filesystem paths, long internal blobs) and
// truncates long values.
func briefFieldsFromInputs(inputs map[string]any) []BriefField {
	if len(inputs) == 0 {
		return nil
	}
	// Skip keys that are filesystem paths or environment-y noise — they're
	// forensic-detail material, not orienting context.
	skip := map[string]bool{
		"project_dir": true,
		"run_dir":     true,
		"runs_dir":    true,
	}
	// Prefer a friendly key ordering when present.
	preferred := []string{
		"product_slug", "spec", inputKeyTask, "story", "goal", "component",
		"node_id", "role_id", "outcome", "attempts", "run_id",
		"sprint", "plan_doc", "context",
	}
	seen := map[string]bool{}
	var out []BriefField
	add := func(k string, v any) {
		if skip[k] || v == nil || seen[k] {
			return
		}
		seen[k] = true
		s := briefValueString(v)
		if s == "" {
			return
		}
		out = append(out, BriefField{Key: k, Value: s})
	}
	for _, k := range preferred {
		if v, ok := inputs[k]; ok {
			add(k, v)
		}
	}
	// Remaining keys (alphabetical) for anything we didn't catch.
	var leftover []string
	for k := range inputs {
		if !seen[k] && !skip[k] {
			leftover = append(leftover, k)
		}
	}
	sort.Strings(leftover)
	for _, k := range leftover {
		add(k, inputs[k])
	}
	// Cap to ~8 fields for the brief block.
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// briefValueString formats a value for the brief block. Scalars print as-is;
// maps and slices print as a compact summary (e.g., "2 items", "5 keys").
func briefValueString(v any) string {
	const cap = 120
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(t)
		if len(s) > cap {
			s = s[:cap-1] + "…"
		}
		return s
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case []any:
		return fmt.Sprintf("list · %d items", len(t))
	case map[string]any:
		return fmt.Sprintf("object · %d keys", len(t))
	default:
		s := fmt.Sprintf("%v", t)
		if len(s) > cap {
			s = s[:cap-1] + "…"
		}
		return s
	}
}
