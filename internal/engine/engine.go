package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

var (
	ErrApprovalPending       = errors.New("approval pending")
	ErrSubworkflowInProgress = errors.New("subworkflow in progress")
	ErrUnresolvedFailure     = errors.New("run terminated with unresolved failure routing")
)

type Engine struct {
	Definitions    *definitions.Bundle
	RunnerRegistry *runners.Registry
	RunsDir        string
	ToilRoot       string
	Approver       approvals.Approver // optional; nil means file-based polling only

	// EventStdout overrides where the event logger writes its slog-compatible
	// JSON lines. When nil, os.Stdout is used. Set to io.Discard in tests to
	// suppress noise.
	EventStdout io.Writer

	// OnInterviewCandidates is called after a run completes (or fails) with
	// interview candidates. The orchestrator sets this to spawn the learn
	// workflow. It is fire-and-forget: the callback should not block.
	OnInterviewCandidates func(runID string, runDir string, nodes []InterviewableNode)

	// OnRunComplete is called when a run reaches terminal status (completed,
	// failed, or cancelled). The orchestrator sets this to fire the webhook
	// callback. Called synchronously after all internal processing.
	OnRunComplete func(runState *state.RunState, runDir string)

	// runLocks prevents concurrent ResumeRun calls on the same run ID.
	// Without this, concurrent resumes can load the same state, execute
	// the same nodes, and create duplicate child runs.
	runLocksMu sync.Mutex
	runLocks   map[string]*sync.Mutex

	// runSecrets stores in-memory secrets per run, keyed by run ID.
	// Secrets are never persisted to disk.
	runSecretsMu sync.Mutex
	runSecrets   map[string]map[string]string

	// failOnItem is a test-only hook: when non-empty, ForEach items whose per-item
	// value matches this string cause executeSingle to return an error instead of
	// running the node. Used to exercise failure-routing logic without wiring a
	// real failing runner. Always empty outside tests.
	failOnItem string

	// transientPendingOnItem is a test-only hook: when non-empty, ForEach items
	// whose per-item value matches this string cause executeSingle to return
	// ErrApprovalPending. Used to exercise transient-error paths (approval
	// waits, subworkflow-in-progress) without wiring real approval machinery.
	transientPendingOnItem string

	// blockUntilCtxDoneOnItem is a test-only hook: when non-empty, ForEach
	// items whose per-item value matches this string cause executeSingle to
	// block on ctx.Done() and then return ctx.Err(). Used to test
	// cancellation that races with in-flight item execution.
	blockUntilCtxDoneOnItem string

	// blockerEntered is incremented (atomically) when the
	// blockUntilCtxDoneOnItem hook is about to block on <-ctx.Done().
	// (The increment happens immediately before the receive, so there's
	// a tiny window where the counter has incremented but the goroutine
	// hasn't yet entered the receive — benign because <-ctx.Done() on
	// an already-closed channel still returns immediately.) Tests poll
	// this to wait for the goroutine to be blocking before calling
	// cancel(), so the test exercises the in-flight cancel branch
	// rather than racing with the early ctx.Err() check at the top of
	// executeSingle.
	blockerEntered atomic.Int32

	// beforeChildResume is a test-only hook: when non-nil, invoked by
	// executeSubworkflow immediately before the synchronous engine.ResumeRun
	// call that drives a freshly dispatched child. Used by tests to observe
	// the parent's state.json at the exact moment a crash would risk losing
	// the child_run pointer — the window between setting Data["child_run"]
	// in memory and the child's execution chain returning control.
	beforeChildResume func(parentRunID, childRunID string)
}

// eventStdout returns the configured stdout writer for the event logger,
// defaulting to os.Stdout when EventStdout is nil.
func (engine *Engine) eventStdout() io.Writer {
	if engine.EventStdout != nil {
		return engine.EventStdout
	}
	return os.Stdout
}

func NewEngine(defs *definitions.Bundle, registry *runners.Registry, runsDir, toilRoot string) *Engine {
	return &Engine{
		Definitions:    defs,
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		ToilRoot:       toilRoot,
		runLocks:       make(map[string]*sync.Mutex),
	}
}

// lockRun returns a per-run mutex, creating one if needed. The caller must
// Lock/Unlock it to prevent concurrent ResumeRun on the same run.
func (engine *Engine) lockRun(runID string) *sync.Mutex {
	engine.runLocksMu.Lock()
	defer engine.runLocksMu.Unlock()
	if engine.runLocks == nil {
		engine.runLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := engine.runLocks[runID]
	if !ok {
		mu = &sync.Mutex{}
		engine.runLocks[runID] = mu
	}
	return mu
}

func (engine *Engine) storeRunSecrets(runID string, secrets map[string]string) {
	if len(secrets) == 0 {
		return
	}
	engine.runSecretsMu.Lock()
	defer engine.runSecretsMu.Unlock()
	if engine.runSecrets == nil {
		engine.runSecrets = make(map[string]map[string]string)
	}
	engine.runSecrets[runID] = secrets
}

func (engine *Engine) loadRunSecrets(runID string) map[string]string {
	engine.runSecretsMu.Lock()
	defer engine.runSecretsMu.Unlock()
	if engine.runSecrets == nil {
		return nil
	}
	return engine.runSecrets[runID]
}

func (engine *Engine) RunWorkflow(ctx context.Context, workflowID string, inputs map[string]any) (string, NodeOutput, error) {
	return engine.runWorkflow(ctx, workflowID, inputs, "", nil)
}

func (engine *Engine) runWorkflow(ctx context.Context, workflowID string, inputs map[string]any, parentRun string, env map[string]string) (string, NodeOutput, error) {
	runID, err := engine.createRun(workflowID, inputs, parentRun, env, "")
	if err != nil {
		return "", NodeOutput{}, err
	}

	lastOutput, err := engine.ResumeRun(ctx, runID)
	return runID, lastOutput, err
}

func (engine *Engine) snapshotWorkflow(runDir string, workflow *definitions.Workflow) error {
	data, err := yaml.Marshal(workflow)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "workflow.yaml"), data, 0o644)
}

func (engine *Engine) CreateRun(workflowID string, inputs map[string]any, env map[string]string, callbackURL string) (string, error) {
	return engine.createRun(workflowID, inputs, "", env, callbackURL)
}

func (engine *Engine) createRun(workflowID string, inputs map[string]any, parentRun string, env map[string]string, callbackURL string) (string, error) {
	workflow, ok := engine.Definitions.Workflows[workflowID]
	if !ok {
		return "", fmt.Errorf("workflow not found: %s", workflowID)
	}

	if err := os.MkdirAll(engine.RunsDir, 0o755); err != nil {
		return "", err
	}
	runID := ""
	runDir := ""
	for attempt := 0; attempt < 64; attempt++ {
		candidate := newRunID()
		candidateDir := filepath.Join(engine.RunsDir, candidate)
		if err := os.Mkdir(candidateDir, 0o755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		runID = candidate
		runDir = candidateDir
		break
	}
	if runID == "" {
		return "", fmt.Errorf("failed to allocate unique run id")
	}

	if err := engine.snapshotWorkflow(runDir, workflow); err != nil {
		return "", err
	}

	logger, err := state.NewLoggerWithStdout(filepath.Join(runDir, "events.jsonl"), engine.eventStdout())
	if err != nil {
		return "", err
	}
	defer func() { _ = logger.Close() }()

	runState := state.NewRunState(runID, workflowID, inputs)
	runState.Title = buildRunTitle(workflow, inputs)
	capturedEnv, secrets := captureEnvWithSecrets(workflow, env, inputs)
	if engine.ToilRoot != "" {
		capturedEnv["TOIL_ROOT"] = engine.ToilRoot
	}
	// Always set TOIL_RUN_ID to the current run's ID (not inherited from parent).
	// Shell nodes use this to create unique resource names (e.g., git worktrees).
	capturedEnv["TOIL_RUN_ID"] = runID
	// Create a per-run workflow directory. Top-level runs always get their
	// own directory (stale values from re-runs must not leak). Subworkflows
	// inherit the parent's value (e.g., a worktree path set via
	// childEnvForSubworkflow).
	workflowDir := filepath.Join(runDir, "workflow")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		return "", fmt.Errorf("create workflow dir: %w", err)
	}
	if _, exists := capturedEnv["TOIL_CURRENT_WORKFLOW_DIR"]; !exists || parentRun == "" {
		capturedEnv["TOIL_CURRENT_WORKFLOW_DIR"] = workflowDir
	}

	// Create workflow output directory. Each run gets its own outputs dir.
	outputsDir := filepath.Join(runDir, "outputs")
	if err := os.MkdirAll(outputsDir, 0o755); err != nil {
		return "", fmt.Errorf("create outputs dir: %w", err)
	}
	capturedEnv["TOIL_WORKFLOW_OUTPUTS"] = outputsDir

	runState.Env = capturedEnv
	runState.Secrets = secrets
	engine.storeRunSecrets(runID, secrets)
	logger.SetSecrets(secrets)
	if parentRun != "" {
		runState.ParentRun = parentRun
	}
	runState.CallbackURL = callbackURL
	if err := state.SaveState(filepath.Join(runDir, "state.json"), runState); err != nil {
		return "", err
	}

	if err := logger.Append(state.Event{Type: "run_started", RunID: runID, Data: map[string]any{keyWorkflowID: workflowID}}); err != nil {
		return "", err
	}

	return runID, nil
}

func newRunID() string {
	wordIndexes := map[int]struct{}{}
	parts := make([]string, 0, 3)
	for len(parts) < 3 {
		index := randomWordIndex(len(runIDWords))
		if _, seen := wordIndexes[index]; seen {
			continue
		}
		wordIndexes[index] = struct{}{}
		parts = append(parts, runIDWords[index])
	}
	return strings.Join(parts, "-")
}

func randomWordIndex(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

func buildRunTitle(workflow *definitions.Workflow, inputs map[string]any) string {
	action := strings.TrimSpace(workflow.Name)
	if action == "" {
		action = workflow.ID
	}
	subject := runSubjectWithContext(inputs)
	if subject == "" {
		return action
	}
	return action + ": " + subject
}

// runSubjectWithContext returns a short human-readable label for a run,
// derived from its inputs. The priority is:
//
//  1. Per-iteration / domain subject (task, component, story, idea, goal,
//     project_spec, plan_doc, context). These vary across sibling
//     subworkflow dispatches, so they make each descendant run's label
//     unique — the Execution Group graph needs this to distinguish, say,
//     every implement_task iteration from every other.
//  2. Root context (product_slug, optionally combined with sprint).
//     product_slug is inherited downward through every subworkflow's
//     inputs, so using it as a primary subject would collapse every
//     descendant's label to the same product name. It only makes sense as
//     a subject for the top-level run where no more specific input exists.
//  3. project_dir as a last-resort label for exotic workflows that have
//     nothing else identifying.
//  4. Any other summarizable input — defensive fallback so unusual
//     workflow shapes still get a non-empty subject.
func runSubjectWithContext(inputs map[string]any) string {
	if len(inputs) == 0 {
		return ""
	}

	domainKeys := []string{"idea", keyTask, "story", "goal", keyComponent, "project_spec", "plan_doc", "context"}
	for _, key := range domainKeys {
		if value, ok := inputs[key]; ok {
			if subject := summarizeSubjectValue(value); subject != "" {
				return subject
			}
		}
	}

	var parts []string
	if p := stringField(inputs, "product_slug"); p != "" {
		parts = append(parts, normalizeSubjectText(p))
	}
	if sprint, ok := inputs["sprint"].(map[string]any); ok {
		label := stringField(sprint, "title")
		if label == "" {
			label = stringField(sprint, "id")
		}
		if p := normalizeSubjectText(label); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		return normalizeSubjectText(strings.Join(parts, " / "))
	}

	if value, ok := inputs["project_dir"]; ok {
		if subject := summarizeSubjectValue(value); subject != "" {
			return subject
		}
	}

	for _, value := range inputs {
		if subject := summarizeSubjectValue(value); subject != "" {
			return subject
		}
	}
	return ""
}

func summarizeSubjectValue(value any) string {
	switch typed := value.(type) {
	case string:
		return normalizeSubjectText(typed)
	case map[string]any:
		// Preferred display keys.
		preferred := []string{keyName, keyTitle, contextModeSummary, keyDescription, "id"}
		for _, key := range preferred {
			if nested, ok := typed[key]; ok {
				if subject := summarizeSubjectValue(nested); subject != "" {
					return subject
				}
			}
		}
		// Secondary keys: domain-specific identifiers that produce a useful
		// human label when joined. Used for run-metadata maps (learn,
		// interview workflows) that have node_id/outcome/run_id/attempts
		// without any of the preferred keys.
		secondary := []string{keyNodeID, "role_id", "task_id", "component_id"}
		var label string
		for _, key := range secondary {
			if v, ok := typed[key].(string); ok && strings.TrimSpace(v) != "" {
				label = strings.TrimSpace(v)
				break
			}
		}
		if label != "" {
			if outcome, ok := typed["outcome"].(string); ok && strings.TrimSpace(outcome) != "" {
				label = label + " · " + strings.TrimSpace(outcome)
			}
			return normalizeSubjectText(label)
		}
		// No useful key found. Return empty rather than the Go map literal —
		// upstream callers will fall back to other inputs or to the bare
		// workflow name.
		return ""
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return summarizeSubjectValue(typed[0])
	case nil:
		return ""
	default:
		// Numeric / boolean fallthrough is fine; only `%v` of structured
		// types produces Go syntax, which we no longer reach.
		return normalizeSubjectText(fmt.Sprintf("%v", typed))
	}
}

func normalizeSubjectText(text string) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if clean == "" {
		return ""
	}
	// 220 chars: long enough to hold filesystem paths and structured-context
	// descriptions in full while still capping pathological inputs. The
	// dashboard caps visual width via CSS + a full-text title tooltip.
	if len(clean) > 220 {
		return clean[:217] + "..."
	}
	return clean
}

var runIDWords = []string{
	"amber", "anchor", "apex", "arc", "atlas", "aurora", "banyan", "barley",
	"beacon", "birch", "blaze", "brisk", "brook", "canyon", "cedar", "cirrus",
	"clover", "cobalt", "comet", "copper", "crane", "crest", "delta", "drift",
	"dune", "eagle", "echo", "ember", "falcon", "fern", "fjord", "flint",
	"forge", "frost", "glade", "granite", "grove", "harbor", "horizon", "indigo",
	"iris", "jet", "kestrel", "lagoon", "lantern", "lattice", "meadow", "mercury",
	"mesa", "mist", "mosaic", "nebula", "north", "oak", "onyx", "orbit",
	"otter", "pacer", "pebble", "pine", "pivot", "prairie", "quartz", "quest",
	"raven", "ridge", "river", "sable", "sage", "scout", "shadow", "signal",
	"silver", "solstice", "sparrow", "spruce", "stone", "summit", "sunrise", "swift",
	"thistle", "timber", "topaz", "trail", "tundra", "valley", "velvet", "violet",
	"voyage", "wave", "willow", "winter", "zephyr",
}

func startNodes(workflow *definitions.Workflow) []readyNode {
	// Template nodes (referenced via for_each.body) are not standalone start
	// nodes — they are only executed as expanded items by their orchestrator.
	// Map from template ID → owning orchestrator so we can recognize
	// template→orchestrator failure/continuation edges and exclude them
	// from the incoming-edge count. Without this, a root orchestrator
	// that has a `status == 'failed'` failure edge from its template
	// appears to have one incoming edge and is skipped as a start node.
	bodies := map[string]string{}
	for _, n := range workflow.Nodes {
		if n.ForEach != nil && n.ForEach.Body != "" {
			bodies[n.ForEach.Body] = n.ID
		}
	}
	incoming := make(map[string]int)
	for _, edge := range workflow.Edges {
		if orchID, isTemplate := bodies[edge.From]; isTemplate && edge.To == orchID {
			// template→its-own-orchestrator edge: not a true incoming
			// edge for scheduling purposes (the orchestrator expands
			// and invokes the template, not the other way round).
			continue
		}
		incoming[edge.To]++
	}
	isTemplate := func(id string) bool { _, ok := bodies[id]; return ok }
	start := []readyNode{}
	for _, node := range workflow.Nodes {
		if isTemplate(node.ID) {
			continue
		}
		if incoming[node.ID] == 0 {
			start = append(start, readyNode{ID: node.ID, EdgeIndex: -1})
		}
	}
	if len(start) == 0 && len(workflow.Nodes) > 0 {
		// Fallback: first non-template node
		for _, node := range workflow.Nodes {
			if isTemplate(node.ID) {
				continue
			}
			start = append(start, readyNode{ID: node.ID, EdgeIndex: -1})
			break
		}
	}
	return start
}

func matchEdges(workflow *definitions.Workflow, nodeID string, decision string) []definitions.Edge {
	ctx := &EvalContext{Decision: decision, Status: statusCompleted}
	return matchEdgesExpr(workflow, nodeID, ctx)
}

// matchEdgesExplicit matches outgoing edges that have an explicit when value
// equal to decision. Unlike matchEdges, it does NOT fall back to default/empty
// edges. Used for meta-decision probing (_loop_exhausted, _timeout) where a
// default edge must NOT accidentally trigger a meta-decision routing.
func matchEdgesExplicit(workflow *definitions.Workflow, nodeID string, decision string) []definitions.Edge {
	matchID := nodeID
	if idx := strings.Index(nodeID, "::"); idx > 0 {
		matchID = nodeID[:idx]
	}
	var matched []definitions.Edge
	for _, edge := range workflow.Edges {
		if edge.From != matchID {
			continue
		}
		if edge.When == decision {
			matched = append(matched, edge)
		}
	}
	return matched
}

// matchEdgesExpr matches outgoing edges using expression-aware evaluation.
// For plain when values, behaves identically to the old matchEdges.
// For expression when values, evaluates against the EvalContext.
// Default/empty edges only match when status is not "failed".
func matchEdgesExpr(workflow *definitions.Workflow, nodeID string, ctx *EvalContext) []definitions.Edge {
	// Expanded ForEach items carry IDs like "template::N". Edges are declared
	// against the template, so resolve the prefix before matching.
	matchID := nodeID
	if idx := strings.Index(nodeID, "::"); idx > 0 {
		matchID = nodeID[:idx]
	}
	matched := []definitions.Edge{}
	for _, edge := range workflow.Edges {
		if edge.From != matchID {
			continue
		}
		if IsExpression(edge.When) {
			ok, err := EvalEdgeExpr(edge.When, ctx)
			if err == nil && ok {
				matched = append(matched, edge)
			}
		} else if edge.When == ctx.Decision {
			matched = append(matched, edge)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	// Fallback to default/empty edges — but NOT for failed nodes
	if ctx.Status == statusFailed {
		return matched
	}
	for _, edge := range workflow.Edges {
		if edge.From != matchID {
			continue
		}
		if edge.When == decisionDefault || edge.When == "" {
			matched = append(matched, edge)
		}
	}
	return matched
}

func captureEnv(workflow *definitions.Workflow, override map[string]string, inputs map[string]any) map[string]string {
	keys := map[string]struct{}{}
	for _, key := range definitions.FindEnvKeys(workflow) {
		keys[key] = struct{}{}
	}
	env := map[string]string{}
	for key, value := range override {
		env[key] = value
	}
	if value, ok := inputEnvValue("PROJECT_DIR", inputs); ok {
		if _, exists := env["PROJECT_DIR"]; !exists {
			env["PROJECT_DIR"] = value
		}
	}
	if len(keys) == 0 {
		if len(env) == 0 {
			return nil
		}
		return env
	}
	for key := range keys {
		if _, ok := env[key]; ok {
			continue
		}
		if value, ok := inputEnvValue(key, inputs); ok {
			env[key] = value
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

var secretKeyPattern = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|CREDENTIAL)`)

// captureEnvWithSecrets works like captureEnv but separates secret-looking
// values into a second map. Keys listed in inputs["secret_keys"] are always
// classified as secrets. Additionally, keys matching common secret patterns
// with values >= 8 characters are auto-classified.
func captureEnvWithSecrets(workflow *definitions.Workflow, override map[string]string, inputs map[string]any) (map[string]string, map[string]string) {
	env := captureEnv(workflow, override, inputs)
	if env == nil {
		env = map[string]string{}
	}
	secrets := map[string]string{}

	// Explicit secret_keys from inputs
	if raw, ok := inputs["secret_keys"]; ok {
		if keys, ok := raw.([]any); ok {
			for _, k := range keys {
				if key, ok := k.(string); ok {
					// Check env first, then fall back to os env
					if val, exists := env[key]; exists {
						secrets[key] = val
						delete(env, key)
					} else if val, exists := os.LookupEnv(key); exists {
						secrets[key] = val
					}
				}
			}
		}
	}

	// Defense-in-depth: auto-classify keys matching secret patterns
	for key, value := range env {
		if looksLikeSecret(key, value) {
			secrets[key] = value
			delete(env, key)
		}
	}

	return env, secrets
}

// looksLikeSecret returns true if the key matches common secret naming
// patterns and the value is long enough to plausibly be a secret.
func looksLikeSecret(key, value string) bool {
	return len(value) >= 8 && secretKeyPattern.MatchString(key)
}

func inputEnvValue(key string, inputs map[string]any) (string, bool) {
	if inputs == nil {
		return "", false
	}
	if value, ok := inputs[key]; ok {
		if text, ok := value.(string); ok && text != "" {
			return text, true
		}
	}
	if key == envProjectDir {
		if value, ok := inputs[keyProjectDir]; ok {
			if text, ok := value.(string); ok && text != "" {
				return text, true
			}
		}
	}
	return "", false
}
