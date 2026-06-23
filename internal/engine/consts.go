package engine

// Event type values written to the run event log.
const (
	eventNodeStarted   = "node_started"
	eventNodeCompleted = "node_completed"
	eventNodeFailed    = "node_failed"
	eventNodeSkipped   = "node_skipped"
	eventNodeOutput    = "node_output"
	eventNodePrompt    = "node_prompt"
	eventWaveStarted   = "wave_started"
)

// Data map key under which a node stores the run ID of a child run it spawned.
const dataKeyChildRun = "child_run"

// Node kind values (Node.Kind).
const (
	kindRole        = "role"
	kindShell       = "shell"
	kindSystem      = "system"
	kindHuman       = "human"
	kindSubworkflow = "subworkflow"
	kindEmit        = "emit"
)

// Node-output envelope field names, also used as map keys in event data,
// resolver field lookups, and the envelope JSON schema.
const (
	fieldDecision            = "decision"
	fieldMessage             = "message"
	fieldData                = "data"
	fieldArtifacts           = "artifacts"
	fieldSessionID           = "session_id"
	fieldTags                = "tags"
	fieldStatus              = "status"
	fieldAttempts            = "attempts"
	fieldLastRoutingDecision = "last_routing_decision"
	fieldLoopIterations      = "loop_iterations"
)

// Event-data / context map keys.
const (
	keyNodeID               = "node_id"
	keyWorkflowID           = "workflow_id"
	keyRunID                = "run_id"
	keyError                = "error"
	keyReason               = "reason"
	keyNodeCount            = "node_count"
	keyHasUnresolvedFailure = "has_unresolved_failure"
	keyName                 = "name"
	keyDescription          = "description"
	keyInputs               = "inputs"
	keyTitle                = "title"
	keyCount                = "count"
	keyComponent            = "component"
	keyProductSlug          = "product_slug"
	keyProjectDir           = "project_dir"
	keyApprovalID           = "approval_id"
	keyLearnings            = "learnings"
	keyAttempt              = "attempt"
	keyNode                 = "node"
	keyItems                = "items"
	keyType                 = "type"
	keyContent              = "content"
	keyText                 = "text"
	keyStdout               = "stdout"
	keyRequired             = "required"
	keyProperties           = "properties"
	keyTask                 = "task"
	keySpec                 = "spec"
	keyStories              = "stories"
)

// JSON Schema primitive type values.
const (
	jsonTypeObject = "object"
	jsonTypeString = "string"
)

// Approval status values.
const (
	approvalTimedOut = "timed_out"
)

// Misc domain values that recur as string literals.
const (
	backoffFixed         = "fixed"
	contextModeFresh     = "fresh"
	contextModeFull      = "full"
	workspaceModeShared  = "shared"
	workspaceModeProject = "project"
	runnerTypeSerf       = "serf"
	envProjectDir        = "PROJECT_DIR"
)
