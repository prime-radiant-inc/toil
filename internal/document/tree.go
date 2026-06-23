package document

import (
	"encoding/json"
	"time"

	"primeradiant.com/toil/internal/visualize"
)

// RunNode is one workflow run (root or sub). It carries metadata about the
// run plus a chronological list of child events (rows, sub-runs, parallel
// groups). Replaces the flat []Item model.
type RunNode struct {
	RunID          string                   `json:"run_id"`
	WorkflowID     string                   `json:"workflow_id"`
	WorkflowName   string                   `json:"workflow_name,omitempty"`
	Title          string                   `json:"title,omitempty"`
	Status         string                   `json:"status,omitempty"`
	Decision       string                   `json:"decision,omitempty"`
	DecisionFamily string                   `json:"decision_family,omitempty"`
	DurationMs     int64                    `json:"duration_ms,omitempty"`
	Compact        bool                     `json:"compact,omitempty"`
	Summary        string                   `json:"summary,omitempty"`
	Topology       *visualize.TopologyGraph `json:"topology,omitempty"`
	Children       []NodeChild              `json:"children,omitempty"`
}

// NodeChild is one of RowChild, SubRunChild, ParallelChild.
type NodeChild interface{ nodeChild() }

// RowChild is one node execution within a run.
type RowChild struct {
	NodeID         string `json:"node_id"`
	RunID          string `json:"run_id"`
	WorkflowID     string `json:"workflow_id,omitempty"`
	Role           string `json:"role"`
	Runner         string `json:"runner,omitempty"`
	AttemptOrdinal int    `json:"attempt_ordinal,omitempty"`
	AttemptTotal   int    `json:"attempt_total,omitempty"`
	// Dispatches mirrors NodeState.Dispatches — number of logical
	// LLM/human dispatches this node has taken. 0 for shell-role nodes
	// (they are never dispatched via the LLM path). Omitted from JSON
	// when zero so shell nodes don't surface noise in the dashboard.
	Dispatches          int           `json:"dispatches,omitempty"`
	SessionID           string        `json:"session_id,omitempty"`
	IsResume            bool          `json:"is_resume,omitempty"`
	Decision            string        `json:"decision,omitempty"`
	DecisionFamily      string        `json:"decision_family,omitempty"`
	DecisionDescription string        `json:"decision_description,omitempty"`
	DecisionMessage     string        `json:"decision_message,omitempty"`
	NextTarget          string        `json:"next_target,omitempty"`
	Prompt              string        `json:"prompt,omitempty"`
	BoilerplateLen      int           `json:"boilerplate_len,omitempty"`
	Result              string        `json:"result,omitempty"`
	Running             bool          `json:"running,omitempty"`
	Status              string        `json:"status,omitempty"`
	StartedAt           time.Time     `json:"started_at,omitempty"`
	EndedAt             time.Time     `json:"ended_at,omitempty"`
	DurationMs          int64         `json:"duration_ms,omitempty"`
	CostUSD             *float64      `json:"cost_usd,omitempty"`
	Artifacts           []ArtifactRef `json:"artifacts,omitempty"`
	DisclosureHint      string        `json:"disclosure_hint,omitempty"`
	Transcript          *Transcript   `json:"transcript,omitempty"`
}

// SubRunChild is a single dispatched sub-workflow (non-parallel).
type SubRunChild struct {
	Run *RunNode `json:"run"`
}

// ParallelChild groups N sub-runs spawned by one ForEach iteration of a base
// node. Multi-iteration ForEach (re-fan-out after a retry) produces multiple
// ParallelChild siblings, each with its own iteration's runs. Index/Total
// disambiguate them in the rendering (so the operator sees "iteration 1 of 2"
// vs "iteration 2 of 2"). Outcome carries the base node's decision for the
// iteration ("some_failed" / "all_succeeded") when known.
type ParallelChild struct {
	ParentNode string     `json:"parent_node"`
	Index      int        `json:"index"` // 1-based ordinal within this base's iterations
	Total      int        `json:"total"` // total iterations of this base in this run
	Outcome    string     `json:"outcome,omitempty"`
	Runs       []*RunNode `json:"runs"`
}

func (RowChild) nodeChild()      {}
func (SubRunChild) nodeChild()   {}
func (ParallelChild) nodeChild() {}

// Each NodeChild variant marshals with a "kind" discriminator so templates
// (with {{if isRow .}}) and external JSON consumers can branch on the variant.

type rowChildAlias RowChild

func (r RowChild) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind string `json:"kind"`
		rowChildAlias
	}{Kind: "row", rowChildAlias: rowChildAlias(r)})
}

type subRunChildAlias SubRunChild

func (s SubRunChild) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind string `json:"kind"`
		subRunChildAlias
	}{Kind: "subrun", subRunChildAlias: subRunChildAlias(s)})
}

type parallelChildAlias ParallelChild

func (p ParallelChild) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind string `json:"kind"`
		parallelChildAlias
	}{Kind: "parallel", parallelChildAlias: parallelChildAlias(p)})
}
