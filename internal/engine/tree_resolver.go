package engine

import (
	"fmt"
	"strings"

	"primeradiant.com/toil/internal/state"
)

// TreeResolver provides read-only access to the run tree containing
// the current run for `tree.*` expressions. Implementations walk
// from the current run up to its root (via RunState.ParentRun) and
// then back down to every descendant, filtering or projecting
// results as requested.
//
// A nil TreeResolver on a RunContext means the current context has
// no tree access (typical in unit tests and non-server contexts);
// `tree.*` expressions return an error in that case so the absence
// is surfaced rather than silently resolving to empty.
//
// Entries returned by FindNodesByTag are shaped as map[string]any so
// they slot directly into the expression-path system (walkMapPath
// already handles maps and slices). Keys:
//
//	run_id      — the run that emitted this decision
//	workflow_id — the workflow id of that run (for debugging context)
//	node_id     — the node within the run
//	decision    — the decision string
//	message     — the node's message
//	tags        — the node's tag list (should contain the queried tag)
//	data        — the node's full data object (may be nil)
type TreeResolver interface {
	// FindNodesByTag returns every completed node across the current
	// run's execution group whose Tags contain the given tag. Tags
	// are workflow-declared labels materialized onto NodeState at
	// emit time — the resolver itself has no knowledge of their
	// meaning, only of the query.
	FindNodesByTag(tag string) ([]map[string]any, error)
}

// filesystemTreeResolver walks the on-disk run tree rooted in
// runsDir, starting from currentRunID. The production TreeResolver
// for non-test contexts; mirrors buildCompoundGraph's traversal
// (read every run dir, build parent map, find root, collect
// descendants) but projects to tagged-node entries.
//
// This does O(N) state.json reads per query where N is the total
// runs on disk for this toil instance. Acceptable for current run
// counts; caching per root run id would be the first optimization
// if query volume grows.
type filesystemTreeResolver struct {
	runsDir      string
	currentRunID string
}

// NewFilesystemTreeResolver constructs a TreeResolver that walks
// runsDir starting from currentRunID. Exported so external callers
// (e.g. the API layer reconstructing a historical run's context)
// can build one too, though in-engine resume flow uses
// engine.NewRunContext.
func NewFilesystemTreeResolver(runsDir, currentRunID string) TreeResolver {
	return &filesystemTreeResolver{runsDir: runsDir, currentRunID: currentRunID}
}

func (r *filesystemTreeResolver) FindNodesByTag(tag string) ([]map[string]any, error) {
	if strings.TrimSpace(r.currentRunID) == "" {
		return nil, fmt.Errorf("tree resolver: currentRunID is empty")
	}
	if strings.TrimSpace(r.runsDir) == "" {
		return nil, fmt.Errorf("tree resolver: runsDir is empty")
	}
	if strings.TrimSpace(tag) == "" {
		return nil, fmt.Errorf("tree resolver: empty tag name")
	}

	// Narrow load: walk the execution group via parent/child pointers
	// rather than scanning every run dir. Cost scales with tree size
	// (~20 runs for a typical implement_spec), not total historical runs.
	group := state.LoadExecutionGroup(r.runsDir, r.currentRunID)
	if len(group) == 0 {
		// Current run isn't on disk yet (e.g. resolving expressions
		// before the first SaveState). No tree to walk — return empty.
		return []map[string]any{}, nil
	}

	matches := []map[string]any{}
	for _, rs := range group {
		rs.WithNodes(func(nodes map[string]*state.NodeState) {
			for nodeID, n := range nodes {
				if n == nil {
					continue
				}
				if !nodeHasTag(n, tag) {
					continue
				}
				matches = append(matches, map[string]any{
					keyRunID:      rs.ID,
					keyWorkflowID: rs.WorkflowID,
					keyNodeID:     nodeID,
					fieldDecision: n.Decision,
					fieldMessage:  n.Message,
					fieldTags:     append([]string(nil), n.Tags...),
					fieldData:     n.Data,
				})
			}
		})
	}
	return matches, nil
}

// nodeHasTag is the local tag-match predicate. A small helper so the
// filter intent is obvious at the call site.
func nodeHasTag(n *state.NodeState, tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
