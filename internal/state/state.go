package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusRunning   = "running"
	statusPending   = "pending"
)

const (
	decisionForceApprove = "force_approve"
	decisionSkipTask     = "skip_task"
)

const tagOverride = "override"

type RunState struct {
	ID                   string                    `json:"id"`
	Title                string                    `json:"title,omitempty"`
	Description          string                    `json:"description,omitempty"`
	Summary              string                    `json:"summary,omitempty"`
	WorkflowID           string                    `json:"workflow_id"`
	Status               string                    `json:"status"`
	Error                string                    `json:"error,omitempty"`
	HasUnresolvedFailure bool                      `json:"has_unresolved_failure,omitempty"`
	StartedAt            time.Time                 `json:"started_at"`
	FinishedAt           *time.Time                `json:"finished_at,omitempty"`
	Totals               *NodeTotals               `json:"totals,omitempty"`
	Inputs               map[string]any            `json:"inputs"`
	Env                  map[string]string         `json:"env,omitempty"`
	Secrets              map[string]string         `json:"-"`
	Nodes                map[string]*NodeState     `json:"nodes"`
	JoinState            map[string]*JoinNodeState `json:"join_state,omitempty"`
	ParentRun            string                    `json:"parent_run,omitempty"`
	CallbackURL          string                    `json:"callback_url,omitempty"`
	mu                   sync.Mutex                `json:"-"`
}

// JoinNodeState tracks arrivals and per-edge passes payloads at a
// join: all node. Arrived preserves the historical "who arrived" list
// (used by some consumers); Passes is the new evaluated-passes map
// keyed by edge index (workflow.Edges position) so two edges from the
// same source with different decisions are distinguished
// unambiguously.
type JoinNodeState struct {
	Arrived []string               `json:"arrived,omitempty"`
	Passes  map[int]map[string]any `json:"passes,omitempty"`
}

// TaggedNode describes a completed node carrying one or more
// semantic tags on its decision. Tags are workflow-authored labels
// (declared on `Decision.Tags` in the YAML) that attach cross-cutting
// meaning — e.g. the convention of tagging `force_approve` with
// `override` so dashboard badges and `tree.tagged.override` queries
// can recognize review-escalation waivers without the harness
// hardcoding the decision names.
//
// Derived from NodeState at read time rather than maintained as a
// separate aggregate. Retries that land on a decision with different
// tags automatically re-tag the node — the Nodes map is always the
// source of truth.
type TaggedNode struct {
	NodeID    string         `json:"node_id"`
	Decision  string         `json:"decision"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	EmittedAt time.Time      `json:"emitted_at"`
}

// HasTag reports whether the given tag is present on t.Tags.
// Linear scan — tag lists are expected to be short (≤3 in practice).
func (t TaggedNode) HasTag(tag string) bool {
	for _, candidate := range t.Tags {
		if candidate == tag {
			return true
		}
	}
	return false
}

// NodesByTag returns every completed node with at least one tag,
// grouped by tag. A node appears under every tag it carries. The
// returned map is never nil — empty when no node has tags. Each
// tag's bucket is sorted by EmittedAt (ascending) with NodeID as
// tiebreaker for deterministic iteration.
//
// Prefer this over calling NodesTagged repeatedly when you need
// several tags, or when you don't know the tag names up front
// (e.g. dashboard aggregation across a whole run tree).
func (rs *RunState) NodesByTag() map[string][]TaggedNode {
	byTag := map[string][]TaggedNode{}
	rs.WithNodes(func(nodes map[string]*NodeState) {
		for id, n := range nodes {
			if n == nil || len(n.Tags) == 0 {
				continue
			}
			var emitted time.Time
			if n.EndedAt != nil {
				emitted = *n.EndedAt
			}
			entry := TaggedNode{
				NodeID:    id,
				Decision:  n.Decision,
				Message:   n.Message,
				Data:      n.Data,
				Tags:      append([]string(nil), n.Tags...),
				EmittedAt: emitted,
			}
			for _, tag := range n.Tags {
				byTag[tag] = append(byTag[tag], entry)
			}
		}
	})
	for tag, list := range byTag {
		sort.Slice(list, func(i, j int) bool {
			if list[i].EmittedAt.Equal(list[j].EmittedAt) {
				return list[i].NodeID < list[j].NodeID
			}
			return list[i].EmittedAt.Before(list[j].EmittedAt)
		})
		byTag[tag] = list
	}
	return byTag
}

// NodesTagged returns every completed node whose Tags contain the
// given tag. Derived from the Nodes map — the engine materializes
// tags into NodeState.Tags at emit time based on the matched
// Decision's Tags, so old-shape states (predating decision tags)
// return empty.
//
// Results are sorted by EmittedAt (ascending), falling back to
// NodeID for nodes with equal or zero timestamps, so the order is
// deterministic across calls.
func (rs *RunState) NodesTagged(tag string) []TaggedNode {
	if tag == "" {
		return nil
	}
	var out []TaggedNode
	rs.WithNodes(func(nodes map[string]*NodeState) {
		for id, n := range nodes {
			if n == nil {
				continue
			}
			if !containsString(n.Tags, tag) {
				continue
			}
			var emitted time.Time
			if n.EndedAt != nil {
				emitted = *n.EndedAt
			}
			out = append(out, TaggedNode{
				NodeID:    id,
				Decision:  n.Decision,
				Message:   n.Message,
				Data:      n.Data,
				Tags:      append([]string(nil), n.Tags...),
				EmittedAt: emitted,
			})
		}
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].EmittedAt.Equal(out[j].EmittedAt) {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].EmittedAt.Before(out[j].EmittedAt)
	})
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// LoadExecutionGroup returns every RunState in the execution group
// rooted at startRunID's top-level ancestor, loading ONLY those runs
// rather than scanning the full runs directory. Use this over
// directory scans when the caller knows a starting run — polling
// paths, tree.* expression resolvers, compound-graph builders — so
// the cost scales with tree size (typically 10-30 runs) rather than
// total historical runs.
//
// Walks ParentRun upward to find the root, then BFS downward via
// node.Data's `child_run` references (direct and inside foreach
// `items`). Unloadable runs (missing file, corrupted JSON) are
// skipped rather than erroring — a partially-damaged runs directory
// shouldn't block tree resolution for valid runs.
//
// Returns an empty map when startRunID isn't on disk (e.g. resolving
// tree.* expressions before the first SaveState).
func LoadExecutionGroup(runsDir, startRunID string) map[string]*RunState {
	loaded := map[string]*RunState{}
	if strings.TrimSpace(runsDir) == "" || strings.TrimSpace(startRunID) == "" {
		return loaded
	}

	loadOne := func(id string) *RunState {
		rs, err := LoadState(filepath.Join(runsDir, id, "state.json"))
		if err != nil {
			return nil
		}
		return rs
	}

	// Phase 1: walk up to the root via ParentRun. Record every
	// ancestor we can load so later the downward pass doesn't
	// re-read them.
	cur := startRunID
	for {
		if _, alreadyLoaded := loaded[cur]; alreadyLoaded {
			break
		}
		rs := loadOne(cur)
		if rs == nil {
			break
		}
		loaded[rs.ID] = rs
		parent := strings.TrimSpace(rs.ParentRun)
		if parent == "" {
			break
		}
		cur = parent
	}

	// Phase 2: BFS downward from every loaded run via child_run
	// pointers. The queue seed is everything we loaded above — that
	// covers the full ancestor chain, which may have dispatched
	// sibling subtrees we haven't visited yet.
	queue := make([]string, 0, len(loaded))
	for id := range loaded {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		rs := loaded[id]
		if rs == nil {
			continue
		}
		var toEnqueue []string
		rs.WithNodes(func(nodes map[string]*NodeState) {
			for _, n := range nodes {
				if n == nil {
					continue
				}
				for _, childID := range extractChildRunIDs(n.Data) {
					if _, already := loaded[childID]; already {
						continue
					}
					child := loadOne(childID)
					if child == nil {
						continue
					}
					loaded[child.ID] = child
					toEnqueue = append(toEnqueue, child.ID)
				}
			}
		})
		queue = append(queue, toEnqueue...)
	}
	return loaded
}

// extractChildRunIDs returns child_run IDs nested inside a node's
// Data map. Supports the two shapes toil emits:
//
//   - Direct subworkflow: data.child_run = "run-id"
//   - ForEach orchestrator: data.items[].data.child_run = "run-id"
//
// JSON-round-tripped state uses `[]any` for the items slice (default
// map[string]interface{} unmarshaling); in-memory state constructed
// by the engine (e.g. executeForEach's initial empty output at
// execute.go) uses `[]map[string]any` directly. extractChildRunIDs
// must accept both so callers don't need to know whether the data
// has been serialized — this matters especially for tree.* queries
// run before a state mutation has been persisted.
//
// Empty slice when no child runs are referenced. Defensive against
// unexpected shapes — misshapen data yields a smaller result, never
// a panic.
func extractChildRunIDs(data map[string]any) []string {
	var ids []string
	if data == nil {
		return ids
	}
	if cr, ok := data["child_run"].(string); ok && strings.TrimSpace(cr) != "" {
		ids = append(ids, cr)
	}
	forEachItem(data["items"], func(item map[string]any) {
		sub, ok := item["data"].(map[string]any)
		if !ok {
			return
		}
		cr, ok := sub["child_run"].(string)
		if !ok || strings.TrimSpace(cr) == "" {
			return
		}
		ids = append(ids, cr)
	})
	return ids
}

// forEachItem iterates over an arbitrary slice value, invoking fn
// for each element that can be coerced to map[string]any. Accepts
// every common slice shape of heterogeneous items:
//
//   - []any (JSON-unmarshaled default)
//   - []map[string]any (in-memory, engine-constructed)
//   - []interface{} (pre-Go-1.18 alias; same layout as []any)
//
// Falls back to reflection for other slice-of-struct shapes callers
// might introduce. Non-map entries are skipped silently.
func forEachItem(v any, fn func(map[string]any)) {
	switch items := v.(type) {
	case nil:
		return
	case []any:
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				fn(m)
			}
		}
	case []map[string]any:
		for _, m := range items {
			fn(m)
		}
	default:
		// Reflection fallback for unexpected slice types.
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice {
			return
		}
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i).Interface()
			if m, ok := elem.(map[string]any); ok {
				fn(m)
			}
		}
	}
}

type NodeState struct {
	ID               string         `json:"id"`
	Status           string         `json:"status"`
	Error            string         `json:"error,omitempty"`
	Decision         string         `json:"decision,omitempty"`
	Message          string         `json:"message,omitempty"`
	Artifacts        []string       `json:"artifacts,omitempty"`
	Data             map[string]any `json:"data,omitempty"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	EndedAt          *time.Time     `json:"ended_at,omitempty"`
	Attempts         int            `json:"attempts"`
	SessionID        string         `json:"session_id"`
	LastDispatchHash string         `json:"last_dispatch_hash,omitempty"`
	LastOutputHash   string         `json:"last_output_hash,omitempty"`
	NoProgressCount  int            `json:"no_progress_count,omitempty"`
	RetryCount       int            `json:"retry_count,omitempty"`
	// Dispatches counts logical dispatches of this LLM/human node.
	// Bumped only when executeSingle's local `attempt == 1`. Never
	// resets (not on retrigger, not on ForEach re-execution, not on
	// context:fresh). Used to key the per-dispatch inputs directory
	// and to number iteration deltas. Shell-role nodes never reach
	// the bump (executeRole short-circuits to executeShellRole at
	// execute.go:1322-1324 before the new code); for shell roles,
	// Dispatches is always 0.
	Dispatches int `json:"dispatches,omitempty"`
	// Tags are materialized from the matched Decision's Tags at
	// completion time (see engine.markNodeCompleted). Persisting them
	// here lets the dashboard, inspect CLI, and tree.* expressions
	// answer "which nodes have tag X" without needing to load each
	// run's workflow definition and re-resolve the decision. Tags
	// reflect the workflow state AT completion, so subsequent YAML
	// edits don't retroactively re-tag historical runs.
	Tags []string `json:"tags,omitempty"`

	// LoopIterations is the persisted per-dispatch loop counter, used
	// by max_loop_iterations enforcement. Incremented atomically with
	// the status-transition write that sets Status="running" (see
	// engine.run_loop). NOT reset on _loop_exhausted emission — stays
	// at the exhausted value so meta-decision edges and downstream
	// emit nodes reading ${node.X.loop_iterations} see the real count.
	// Reset happens lazily at the start of the NEXT dispatch when
	// LastRoutingDecision == "_loop_exhausted".
	LoopIterations int `json:"loop_iterations,omitempty"`

	// LastRoutingDecision is the meta-decision (_loop_exhausted or
	// _timeout) the engine synthesized for this node's last
	// transition, when applicable. Empty for nodes that completed
	// with their own real decision. Consulted by resumeReadyNodes on
	// crash-resume for outgoing-edge matching, and by the dashboard
	// for the "routing trail" display. Cleared on the node's next
	// dispatch start (lazy reset in getAndIncrementLoopIterations).
	LastRoutingDecision string `json:"last_routing_decision,omitempty"`

	// LastRoutingAt is the timestamp LastRoutingDecision was set.
	// Used by the dashboard / inspect API for ordering events.
	// Pointer so the zero value omits from JSON.
	LastRoutingAt *time.Time `json:"last_routing_at,omitempty"`
}

// NarrowToNode returns a new RunState containing only the named node
// in its Nodes map (empty map if the node doesn't exist). All other
// top-level fields are shared by reference with the receiver. The
// returned RunState has a fresh mutex — callers must not rely on
// locks held on the original carrying over.
//
// Used by the inspect API to scope processors that iterate Nodes
// (outputs, timing, tokens, etc.) when a node-scoped route is
// requested. Without this, processors would return run-wide data
// despite the node-scoped route.
func (rs *RunState) NarrowToNode(nodeID string) *RunState {
	narrow := &RunState{
		ID:                   rs.ID,
		Title:                rs.Title,
		Description:          rs.Description,
		Summary:              rs.Summary,
		WorkflowID:           rs.WorkflowID,
		Status:               rs.Status,
		Error:                rs.Error,
		HasUnresolvedFailure: rs.HasUnresolvedFailure,
		StartedAt:            rs.StartedAt,
		FinishedAt:           rs.FinishedAt,
		Inputs:               rs.Inputs,
		Env:                  rs.Env,
		Secrets:              rs.Secrets,
		Nodes:                map[string]*NodeState{},
		JoinState:            rs.JoinState,
		ParentRun:            rs.ParentRun,
		CallbackURL:          rs.CallbackURL,
	}
	rs.WithNodes(func(nodes map[string]*NodeState) {
		if n, ok := nodes[nodeID]; ok {
			narrow.Nodes[nodeID] = n
		}
	})
	return narrow
}

func NewRunState(id string, workflowID string, inputs map[string]any) *RunState {
	return &RunState{
		ID:         id,
		WorkflowID: workflowID,
		Status:     statusRunning,
		StartedAt:  time.Now().UTC(),
		Inputs:     inputs,
		Nodes:      make(map[string]*NodeState),
	}
}

func (state *RunState) Node(id string) *NodeState {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.getOrCreateNode(id)
}

func (state *RunState) WithNode(id string, fn func(*NodeState)) {
	state.mu.Lock()
	defer state.mu.Unlock()
	fn(state.getOrCreateNode(id))
}

// getOrCreateNode returns the node for the given ID, creating it if absent.
// Caller must hold state.mu.
func (state *RunState) getOrCreateNode(id string) *NodeState {
	node, ok := state.Nodes[id]
	if !ok {
		node = &NodeState{ID: id, Status: statusPending}
		state.Nodes[id] = node
	}
	return node
}

func (state *RunState) NodeStatus(id string) (string, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	node, ok := state.Nodes[id]
	if !ok {
		return "", false
	}
	return node.Status, true
}

func (state *RunState) WithNodes(fn func(map[string]*NodeState)) {
	state.mu.Lock()
	defer state.mu.Unlock()
	fn(state.Nodes)
}

// SetJoinState stores the list of arrived predecessors for a join node.
// Preserves caller's input order; sort at the call site if a deterministic
// order is needed (run_loop passes state.SortedKeys).
func (state *RunState) SetJoinState(joinNodeID string, arrivedPredecessors []string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.JoinState == nil {
		state.JoinState = map[string]*JoinNodeState{}
	}
	j := state.JoinState[joinNodeID]
	if j == nil {
		j = &JoinNodeState{}
		state.JoinState[joinNodeID] = j
	}
	j.Arrived = append([]string(nil), arrivedPredecessors...)
}

// SetJoinPasses stores the evaluated passes map for the given edge
// index at the named join node. Cross-iteration re-arrival of the
// same edge index replaces the prior value (latest fresh evaluation
// wins).
func (state *RunState) SetJoinPasses(joinID string, edgeIndex int, passes map[string]any) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.JoinState == nil {
		state.JoinState = map[string]*JoinNodeState{}
	}
	j := state.JoinState[joinID]
	if j == nil {
		j = &JoinNodeState{}
		state.JoinState[joinID] = j
	}
	if j.Passes == nil {
		j.Passes = map[int]map[string]any{}
	}
	cloned := make(map[string]any, len(passes))
	for k, v := range passes {
		cloned[k] = v
	}
	j.Passes[edgeIndex] = cloned
}

// GetJoinState returns the stored list of arrived predecessors for a join node.
func (state *RunState) GetJoinState(joinNodeID string) []string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.JoinState == nil {
		return nil
	}
	j := state.JoinState[joinNodeID]
	if j == nil {
		return nil
	}
	return j.Arrived
}

// SetNarrative updates the run's title and description under the state mutex.
// Empty values are ignored to avoid overwriting existing content.
func (state *RunState) SetNarrative(title, description string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if title != "" {
		state.Title = title
	}
	if description != "" {
		state.Description = description
	}
}

// SetSummary updates the run's summary under the state mutex.
func (state *RunState) SetSummary(summary string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if summary != "" {
		state.Summary = summary
	}
}

// SortedKeys converts a map[string]bool to a sorted []string.
func SortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ToSet converts a []string to a map[string]bool.
func ToSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func SaveState(path string, state *RunState) error {
	state.mu.Lock()
	data, err := json.Marshal(state)
	state.mu.Unlock()
	if err != nil {
		return err
	}
	// Atomic replace via temp-and-rename. os.CreateTemp uses the kernel's
	// unique-filename primitive, which is robust under high concurrency
	// (time-based naming collides on low-resolution clocks). Multiple
	// goroutines can safely call SaveState on the same path.
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// os.CreateTemp defaults to mode 0600 — state.json can contain
	// sensitive workflow inputs, outputs, and error details, so we keep
	// that restrictive default. External tooling must read run state
	// via the HTTP API, not by reading these files directly.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func LoadState(path string) (*RunState, error) {
	// Clean up tmp files that a prior SaveState may have orphaned (crash
	// between CreateTemp and Rename). Sync.Once-gated per directory so
	// the ReadDir cost is paid at most once per process per run dir,
	// keeping LoadState (called on every SSE poll, dashboard refresh,
	// and subworkflow status check) on its fast path. Also limits the
	// window where a concurrent slow SaveState could have its in-flight
	// tmp file deleted to the first LoadState of the dir.
	//
	// Run BEFORE ReadFile so the crash-recovery case — where state.json
	// is missing or unreadable and only tmp files remain in the dir —
	// still triggers cleanup. Otherwise an early ReadFile failure would
	// skip cleanup and leak tmp files indefinitely.
	cleanOrphanTmpFilesOnce(path, 30*time.Second)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state RunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Nodes == nil {
		state.Nodes = make(map[string]*NodeState)
	}
	backfillLegacyDecisionTags(&state)
	return &state, nil
}

// legacyOverrideDecisionTags maps decisions that pre-date the
// workflow-declared-tags feature to the tags they would have
// carried if they had been authored post-tags. Read at state
// load time only — a one-way migration, not a runtime hardcoding.
//
// Keep narrow. Entries exist only because we committed those
// decisions with the "override" semantic before tags existed, and
// removing override visibility from historical runs would be a
// regression. New decisions get tags declared in their workflow YAML
// (see Decision.Tags in internal/definitions/decision.go).
var legacyOverrideDecisionTags = map[string][]string{
	decisionForceApprove: {tagOverride},
	decisionSkipTask:     {tagOverride},
}

// backfillLegacyDecisionTags synthesizes NodeState.Tags for runs that
// completed before the tag-based model existed. When a node has no
// Tags but its Decision matches a historically-tagged decision, apply
// the canonical tag list in memory. The disk file is untouched until
// the next SaveState, at which point the synthesized tags become
// persistent — a lazy migration on write.
//
// This exists to preserve the "override" badge / tree.tagged.override
// / review_overrides inspect aspect for pre-existing runs. New
// workflows should declare tags in YAML; this path isn't for them.
func backfillLegacyDecisionTags(rs *RunState) {
	rs.WithNodes(func(nodes map[string]*NodeState) {
		for _, n := range nodes {
			if n == nil || len(n.Tags) > 0 {
				continue
			}
			tags, ok := legacyOverrideDecisionTags[n.Decision]
			if !ok {
				continue
			}
			n.Tags = append([]string(nil), tags...)
		}
	})
}

// cleanedDirs tracks directories whose orphan tmp files have been cleaned
// in this process, so cleanup runs at most once per directory.
//
// Trade-off: never shrinks for the life of the process — memory grows
// O(unique run dirs ever loaded). Each entry is a *sync.Once (~32 bytes
// plus the dir-path string), so a server processing 100k unique runs
// holds ~5–10 MB of these. Acceptable for typical lifetimes; unsuitable
// for processes that load millions of runs without restart. Also note:
// if a run dir is deleted and recreated at the same path during the
// process lifetime, cleanup is NOT re-run for the new dir — the Once
// has already fired and stale tmp files in the recreated dir won't be
// removed until process restart.
var cleanedDirs sync.Map // dir path → *sync.Once

// cleanOrphanTmpFilesOnce wraps cleanOrphanTmpFiles in a sync.Once keyed by
// the directory of path.
func cleanOrphanTmpFilesOnce(path string, maxAge time.Duration) {
	dir := filepath.Dir(path)
	v, _ := cleanedDirs.LoadOrStore(dir, &sync.Once{})
	v.(*sync.Once).Do(func() {
		cleanOrphanTmpFiles(path, maxAge)
	})
}

// cleanOrphanTmpFiles removes state.json.tmp.* files in the same directory
// as path that are older than maxAge. Best-effort; errors are ignored.
func cleanOrphanTmpFiles(path string, maxAge time.Duration) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + ".tmp."
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
