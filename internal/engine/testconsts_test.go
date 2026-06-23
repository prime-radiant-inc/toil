package engine

// Test fixture constants shared across engine package tests.
const (
	testRunID1        = "run-1"
	testRunIDSrc      = "run-src"
	testNodeGate      = "gate"
	testNodeWriteCode = "write_code"
	testSessID1       = "sess-1"
	testSessIDStale   = "sess-stale"
	testSessExisting  = "existing-session"

	testDecisionApproved = "approved"
	testDecisionDone     = "done"
	testDecisionDefault  = "default"

	testStatusPending  = "pending"
	testStatusResolved = "resolved"

	testMessageAutoResolvedTimeout = "auto-resolved: timeout"
	testOutcomeRoleRunner          = "role-runner"
	testInputHello                 = "hello"
)
