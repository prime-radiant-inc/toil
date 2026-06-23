package inspect

import (
	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("review_overrides", func(rs *state.RunState) Processor { return NewReviewOverridesProcessor(rs) })
}

// OverrideTag is the decision-tag that identifies review-escalation
// waivers by project convention. Workflows declare `tags: [override]`
// on decisions like force_approve and skip_task; this aspect queries
// for that tag.
//
// The convention lives here rather than in internal/state because
// state is the generic primitive — any tag works via NodesTagged().
// The dashboard uses the same constant.
const OverrideTag = "override"

// ReviewOverridesResult surfaces the waivers made during review
// escalation — decisions tagged with `override` by the workflow.
//
// Derived from RunState.Nodes at read time via NodesTagged(OverrideTag).
// The processor doesn't replay events — whatever the Nodes map
// currently says is the current set of overrides, so retries that
// land a non-override decision automatically drop the waiver.
type ReviewOverridesResult struct {
	RunID     string             `json:"run_id"`
	Overrides []state.TaggedNode `json:"overrides"`
}

type reviewOverridesProcessor struct {
	rs *state.RunState
}

// NewReviewOverridesProcessor returns a Processor that surfaces
// nodes tagged with the canonical "override" tag for the
// `toil inspect --aspect review_overrides` path.
func NewReviewOverridesProcessor(rs *state.RunState) *reviewOverridesProcessor {
	return &reviewOverridesProcessor{rs: rs}
}

// ProcessEvent is a no-op; overrides are derived from Nodes at read
// time and the processor produces a static snapshot each call.
func (p *reviewOverridesProcessor) ProcessEvent(_ state.Event) {}

// Changed returns false since the processor doesn't accumulate
// state across events — callers get a fresh derivation each call.
func (p *reviewOverridesProcessor) Changed() bool { return false }

func (p *reviewOverridesProcessor) Result() any {
	overrides := p.rs.NodesTagged(OverrideTag)
	if overrides == nil {
		overrides = []state.TaggedNode{}
	}
	return ReviewOverridesResult{
		RunID:     p.rs.ID,
		Overrides: overrides,
	}
}
