package document

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

// RunStoreLoader adapts the on-disk run store to document.Loader.
type RunStoreLoader struct {
	runsDir string
}

// NewRunStoreLoader returns a RunStoreLoader reading from runsDir.
func NewRunStoreLoader(runsDir string) *RunStoreLoader {
	return &RunStoreLoader{runsDir: runsDir}
}

func (l *RunStoreLoader) LoadRun(id string) (*state.RunState, error) {
	return state.LoadState(filepath.Join(l.runsDir, id, "state.json"))
}

// LoadEvents returns the event slice for runID by reading the run's events.jsonl.
// Returns nil on any error (file missing, parse failure) — callers produce an
// empty transcript in that case. Satisfies the document.EventLoader interface.
func (l *RunStoreLoader) LoadEvents(runID string) []state.Event {
	events, _, err := state.ReadEventsWithOffset(filepath.Join(l.runsDir, runID, "events.jsonl"))
	if err != nil {
		return nil
	}
	return events
}

// LoadWorkflowSnapshot returns the per-run workflow.yaml snapshot, or nil
// if it can't be read. Satisfies the document.WorkflowSnapshotLoader
// optional interface so buildRunNode can attach a per-run topology graph.
func (l *RunStoreLoader) LoadWorkflowSnapshot(runID string) *definitions.Workflow {
	wf, err := definitions.LoadWorkflowSnapshot(filepath.Join(l.runsDir, runID, "workflow.yaml"))
	if err != nil {
		return nil
	}
	return wf
}

func (l *RunStoreLoader) ChildRuns(parentID string) []string {
	entries, err := os.ReadDir(l.runsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(l.runsDir, e.Name(), "state.json"))
		if err != nil || rs == nil {
			continue
		}
		if rs.ParentRun == parentID {
			out = append(out, rs.ID)
		}
	}
	return out
}

// WorkflowRegistry adapts the workflow bundle to document.Registry.
type WorkflowRegistry struct {
	bundle *definitions.Bundle
	loader *RunStoreLoader
}

// NewWorkflowRegistry returns a WorkflowRegistry backed by bundle and loader.
func NewWorkflowRegistry(bundle *definitions.Bundle, loader *RunStoreLoader) *WorkflowRegistry {
	return &WorkflowRegistry{bundle: bundle, loader: loader}
}

func (r *WorkflowRegistry) WorkflowName(workflowID string) string {
	if r.bundle == nil {
		return workflowID
	}
	if wf, ok := r.bundle.Workflows[workflowID]; ok && wf != nil && wf.Name != "" {
		return wf.Name
	}
	return workflowID
}

func (r *WorkflowRegistry) RoleForNode(workflowID, nodeID string) string {
	if r.bundle == nil {
		return nodeID
	}
	if wf, ok := r.bundle.Workflows[workflowID]; ok && wf != nil {
		for _, n := range wf.Nodes {
			if n.ID == nodeID {
				if n.Role != "" {
					return n.Role
				}
				return n.ID
			}
		}
	}
	return nodeID
}

func (r *WorkflowRegistry) RunnerForNode(workflowID, nodeID string) string {
	if r.bundle == nil {
		return ""
	}
	if wf, ok := r.bundle.Workflows[workflowID]; ok && wf != nil {
		for _, n := range wf.Nodes {
			if n.ID == nodeID {
				return n.Runner
			}
		}
	}
	return ""
}

func (r *WorkflowRegistry) NextNode(workflowID, fromNodeID, decision string) string {
	if r.bundle == nil || decision == "" {
		return ""
	}
	wf, ok := r.bundle.Workflows[workflowID]
	if !ok || wf == nil {
		return ""
	}
	for _, e := range wf.Edges {
		if e.From == fromNodeID && e.When == decision {
			return e.To
		}
	}
	return ""
}

func (r *WorkflowRegistry) PlanTaskDescription(parentRunID, taskID string) string {
	rs, err := r.loader.LoadRun(parentRunID)
	if err != nil || rs == nil {
		return ""
	}
	planTasks, ok := rs.Nodes["plan_tasks"]
	if !ok || planTasks == nil || planTasks.Data == nil {
		return ""
	}
	plan, ok := planTasks.Data[artifactPlan].(map[string]any)
	if !ok {
		return ""
	}
	tasks, ok := plan["tasks"].([]any)
	if !ok {
		return ""
	}
	for _, t := range tasks {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if m["id"] == taskID {
			if d, ok := m["description"].(string); ok {
				return d
			}
		}
	}
	return ""
}

// FindDecision returns the decision with the given id in the given workflow,
// or nil if not found. Walks the workflow's node decisions; the first match
// wins. Decision ids are workflow-scoped in practice — the same id always has
// the same description across nodes.
func (r *WorkflowRegistry) FindDecision(workflowID, decisionID string) *definitions.Decision {
	if r.bundle == nil {
		return nil
	}
	wf, ok := r.bundle.Workflows[workflowID]
	if !ok || wf == nil {
		return nil
	}
	for _, node := range wf.Nodes {
		for i := range node.Decisions {
			if node.Decisions[i].ID == decisionID {
				return &node.Decisions[i]
			}
		}
	}
	return nil
}

// FindDecisionMeta returns the DecisionMeta for the given decision in the given
// workflow, or nil if not found. Satisfies the document.DecisionFinder interface.
func (r *WorkflowRegistry) FindDecisionMeta(workflowID, decisionID string) *DecisionMeta {
	d := r.FindDecision(workflowID, decisionID)
	if d == nil {
		return nil
	}
	return &DecisionMeta{Description: d.Description, Tags: d.Tags}
}

// WorkflowPromptResolver resolves a node's local prompt by looking up the
// workflow's prompt template, expanding ${input.*} placeholders against the
// run's inputs, and extracting the LOCAL portion.
type WorkflowPromptResolver struct {
	bundle  *definitions.Bundle
	runsDir string
}

// NewWorkflowPromptResolver constructs a resolver backed by bundle + runsDir.
func NewWorkflowPromptResolver(bundle *definitions.Bundle, runsDir string) *WorkflowPromptResolver {
	return &WorkflowPromptResolver{bundle: bundle, runsDir: runsDir}
}

var inputExprPattern = regexp.MustCompile(`\$\{input\.([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// LocalPrompt returns the attempt-specific portion of a node's prompt.
// Returns "" on any lookup failure. ${node.*.*} expressions are left
// unsubstituted; they rarely appear in local sections and resolving them
// would require full engine state.
func (r *WorkflowPromptResolver) LocalPrompt(workflowID, nodeID, runID string, _ int) string {
	if r.bundle == nil {
		return ""
	}
	wf, ok := r.bundle.Workflows[workflowID]
	if !ok || wf == nil {
		return ""
	}
	var promptTemplate string
	for _, n := range wf.Nodes {
		if n.ID == nodeID {
			promptTemplate = n.Prompt
			break
		}
	}
	if promptTemplate == "" {
		return ""
	}
	rs, err := state.LoadState(filepath.Join(r.runsDir, runID, "state.json"))
	if err != nil {
		return ""
	}
	resolved := ExpandInputExprs(promptTemplate, rs.Inputs)
	local, _ := ExtractLocalPrompt(resolved)
	return local
}

// ExpandInputExprs replaces ${input.key} references with values from the
// inputs map. Unresolved references are left as-is.
func ExpandInputExprs(text string, inputs map[string]any) string {
	if len(inputs) == 0 {
		return text
	}
	return inputExprPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := inputExprPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		if val, ok := inputs[groups[1]]; ok {
			return fmt.Sprint(val)
		}
		return match
	})
}

// Compile-time interface checks.
var (
	_ Loader                 = (*RunStoreLoader)(nil)
	_ EventLoader            = (*RunStoreLoader)(nil)
	_ WorkflowSnapshotLoader = (*RunStoreLoader)(nil)
	_ Registry               = (*WorkflowRegistry)(nil)
	_ DecisionFinder         = (*WorkflowRegistry)(nil)
	_ PromptResolver         = (*WorkflowPromptResolver)(nil)
)
