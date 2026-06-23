package dashboard

import (
	"html/template"
	"time"

	"primeradiant.com/toil/internal/state"
)

// Run and node status values used throughout the dashboard.
const (
	statusRunning          = "running"
	statusCompleted        = "completed"
	statusFailed           = "failed"
	statusFailedHandled    = "failed-handled"
	statusPaused           = "paused"
	statusPending          = "pending"
	statusCancelled        = "cancelled"
	statusSkipped          = "skipped"
	statusAwaitingApproval = "awaiting_approval"
	statusResolved         = "resolved"
)

type BasePage struct {
	Title            string
	BasePath         string
	ActiveNav        string
	WorkflowCount    int
	RunCount         int
	PendingApprovals int
}

type RunSummary struct {
	ID           string
	Title        string
	Description  string
	Summary      string
	WorkflowID   string
	WorkflowName string
	Status       string
	// HasUnresolvedFailure is true when the run completed (status=="completed")
	// but at least one node failure was not routed to a recovery decision.
	// Consumers should treat this as functionally equivalent to "failed" —
	// use EffectiveStatus to collapse the two cases.
	HasUnresolvedFailure bool `json:"has_unresolved_failure,omitempty"`
	StartedAt            time.Time
	FinishedAt           *time.Time
	Duration             string
	ParentRun            string
	RunTotal             *state.NodeTotals
	// TaggedNodes carries every completed node from this run whose
	// decision was workflow-tagged. Indexed by tag for cheap lookups
	// by the execution-group aggregator and by template rendering.
	// Typical tags today: "override" (review-escalation waivers).
	TaggedNodes map[string][]state.TaggedNode
}

// EffectiveStatus collapses Status and HasUnresolvedFailure into a single
// string for rendering and filtering. Delegates to state.EffectiveStatus.
func EffectiveStatus(status string, hasUnresolvedFailure bool) string {
	return state.EffectiveStatus(status, hasUnresolvedFailure)
}

type RunTreeRow struct {
	Run         RunSummary
	Depth       int
	IndentPx    int
	HasChildren bool
}

type RunTreeNode struct {
	Run      RunSummary
	Children []RunTreeNode
	Expanded bool // true for the current run and its ancestors; collapsed otherwise
}

type ExecutionGroupSummary struct {
	Root        RunSummary
	GroupStatus string
	TotalRuns   int
	ActiveRuns  int
	Rows        []RunTreeRow
	Tree        []RunTreeNode
	GroupTotal  *state.NodeTotals
}

// RunTaggedNode is a TaggedNode annotated with the run that recorded
// it, flattened for dashboard and CLI rendering. Run identity lets
// the UI link each entry back to its origin without re-deriving.
type RunTaggedNode struct {
	RunID        string
	RunTitle     string
	WorkflowName string
	Node         state.TaggedNode
}

// CollectTaggedNodes walks the execution group tree and returns every
// tagged-node entry whose Tags contain the given tag, annotated with
// the run that recorded each one. Generic over tag names — the
// dashboard and inspect surfaces query by "override" today, but any
// workflow-declared tag works.
//
// Returns nil when the group is nil or has no matching entries.
func CollectTaggedNodes(group *ExecutionGroupSummary, tag string) []RunTaggedNode {
	if group == nil || tag == "" {
		return nil
	}
	var out []RunTaggedNode
	var walk func(nodes []RunTreeNode)
	walk = func(nodes []RunTreeNode) {
		for _, n := range nodes {
			for _, tn := range n.Run.TaggedNodes[tag] {
				out = append(out, RunTaggedNode{
					RunID:        n.Run.ID,
					RunTitle:     n.Run.Title,
					WorkflowName: n.Run.WorkflowName,
					Node:         tn,
				})
			}
			walk(n.Children)
		}
	}
	walk(group.Tree)
	return out
}

// OverrideTag is the decision-tag identifying review-escalation
// waivers by project convention. Workflows declare `tags: [override]`
// on force_approve / skip_task-style decisions; the dashboard
// renders amber badges + a section for nodes carrying this tag.
// Mirrored in internal/inspect for CLI use; keep in sync.
const OverrideTag = "override"

type WorkflowSummary struct {
	ID          string
	Name        string
	Description string
	NodeCount   int
	Tags        string
}

type InputField struct {
	Key         string
	Label       string
	Description string
}

type KeyValue struct {
	Key    string
	Value  string
	HTML   template.HTML
	IsJSON bool // true when value is pretty-printed JSON (not prose markdown)
}

type StoryCard struct {
	ID       string
	Title    string
	Status   string
	Filename string
	Body     string        // markdown body (frontmatter stripped)
	BodyHTML template.HTML // rendered markdown
}

type NodeSummary struct {
	ID     string
	Label  string
	Status string
}

type InterviewView struct {
	ID              string
	NodeID          string
	RoleID          string
	Status          string
	OriginalOutcome string
	Attempts        int
	Responses       map[string]any
	StartedAt       string
	CompletedAt     string
}

type ApprovalView struct {
	ID              string
	RunID           string
	NodeID          string
	NodeLabel       string
	Question        string
	QuestionHTML    template.HTML
	Decision        string
	Message         string
	DecisionOptions []string
	CreatedAt       string
	ResolvedAt      string
	TimeoutSec      int
	TimeoutDefault  string
	DeadlineUnix    int64 // CreatedAt + TimeoutSec as Unix seconds; 0 if no timeout
}
