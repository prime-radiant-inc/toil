package state

// EffectiveStatus collapses Status and HasUnresolvedFailure into a single
// string for rendering and filtering.
//
// HasUnresolvedFailure is set at finalization time. When a failed run is
// retriggered, Status returns to "running" but the flag may still carry the
// stale value from the prior finalization. Only treat the flag as a failure
// indicator for terminal statuses — if the run is actively executing, trust
// Status directly.
func EffectiveStatus(status string, hasUnresolvedFailure bool) string {
	if status == statusFailed {
		return statusFailed
	}
	if status == statusCompleted && hasUnresolvedFailure {
		return statusFailed
	}
	return status
}
