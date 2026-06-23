package engine

// Run status values.
const (
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	statusRetrying  = "retrying"
	statusPaused    = "paused"
	statusPending   = "pending"
	statusSkipped   = "skipped"

	// statusFailedHandled marks a ForEach expanded item whose runtime failure
	// was absorbed by a template failure edge. Distinct from statusFailed so
	// resume can short-circuit and aggregate decisions see the correct status.
	statusFailedHandled = "failed-handled"

	// statusAwaitingApproval is the status set while waiting for a human approval decision.
	statusAwaitingApproval = "awaiting_approval"
)

// Interview outcome values returned by classifyOutcome.
const (
	outcomeSucceeded     = "succeeded"
	outcomeRetried       = "retried"
	outcomeFailed        = "failed"
	outcomeFailedHandled = "failed-handled"
)

// Context mode values for node execution preamble generation.
const (
	contextModeCompact = "compact"
	contextModeSummary = "summary"
)

// decisionDefault is the decision value produced by system nodes and shell nodes.
const decisionDefault = "default"

// ForEach aggregate decision values emitted by orchestrator nodes.
const (
	decisionAllSucceeded = "all_succeeded"
	decisionSomeFailed   = "some_failed"
	decisionAllFailed    = "all_failed"
)

// joinAll is the value for Node.Join that triggers fan-in synchronization.
const joinAll = "all"

// reasonCancelled is the SkipReason / Message used when an item is marked
// statusSkipped because of a context cancellation (run cancel, deadline,
// or operator stop). Kept as a constant so all cancel paths agree.
//
// Distinct from statusCancelled even though the string values match:
//   - statusCancelled is a Run/NodeState.Status (terminal lifecycle state)
//   - reasonCancelled is a free-form Message/SkipReason explanation
//
// Don't substitute one for the other — the use sites are different
// fields with different semantics, and a future divergence in the
// string values (e.g. "cancelled" → "cancelled-by-operator") would
// silently corrupt one or the other.
const reasonCancelled = "cancelled"
