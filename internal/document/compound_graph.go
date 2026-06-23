package document

import (
	"os"
	"path/filepath"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
	"primeradiant.com/toil/internal/visualize"
)

// BuildCompoundGraph constructs an ELK-renderable topology graph for the
// execution group containing rootRunID. Walks the runs dir to load every
// related run and its workflow snapshot, then delegates to
// visualize.CompoundExecutionGroupTopology.
func BuildCompoundGraph(runsDir, rootRunID string) visualize.TopologyGraph {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return visualize.CompoundExecutionGroupTopology(nil, rootRunID)
	}

	type runInfo struct {
		state    *state.RunState
		parentID string
	}
	allRuns := map[string]runInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(runsDir, entry.Name(), "state.json"))
		if err != nil {
			continue
		}
		allRuns[rs.ID] = runInfo{state: rs, parentID: strings.TrimSpace(rs.ParentRun)}
	}

	if _, ok := allRuns[rootRunID]; !ok {
		return visualize.CompoundExecutionGroupTopology(nil, rootRunID)
	}

	// Find the root of the execution group containing rootRunID.
	root := rootRunID
	visited := map[string]bool{}
	for !visited[root] {
		visited[root] = true
		info, ok := allRuns[root]
		if !ok || info.parentID == "" {
			break
		}
		if _, parentExists := allRuns[info.parentID]; !parentExists {
			break
		}
		root = info.parentID
	}

	// Collect all descendants of root.
	groupIDs := map[string]bool{root: true}
	changed := true
	for changed {
		changed = false
		for id, info := range allRuns {
			if groupIDs[info.parentID] && !groupIDs[id] {
				groupIDs[id] = true
				changed = true
			}
		}
	}

	// Build CompoundRunInput for each run in the group.
	inputs := make([]visualize.CompoundRunInput, 0, len(groupIDs))
	for id := range groupIDs {
		info := allRuns[id]
		rs := info.state
		workflow, _ := definitions.LoadWorkflowSnapshot(filepath.Join(runsDir, id, "workflow.yaml"))

		title := strings.TrimSpace(rs.Title)
		if title == "" {
			title = rs.WorkflowID
		}

		inputs = append(inputs, visualize.CompoundRunInput{
			RunID:     rs.ID,
			ParentRun: info.parentID,
			Title:     title,
			Status:    state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure),
			Workflow:  workflow,
			RunState:  rs,
			Collector: buildRunCollector(runsDir, rs),
		})
	}

	return visualize.CompoundExecutionGroupTopology(inputs, rootRunID)
}

// BuildRunTreeGraph is the simpler companion to BuildCompoundGraph: one
// node per run, edges parent→child, no expanded workflow internals.
// Suitable for the run-view's orientation strip where the operator just
// needs to see the shape of the execution group, not the inner
// workflow node graphs.
func BuildRunTreeGraph(runsDir, rootRunID string) visualize.TopologyGraph {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return visualize.RunTreeTopology(nil, rootRunID)
	}
	type runInfo struct {
		state    *state.RunState
		parentID string
	}
	allRuns := map[string]runInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(runsDir, entry.Name(), "state.json"))
		if err != nil {
			continue
		}
		allRuns[rs.ID] = runInfo{state: rs, parentID: strings.TrimSpace(rs.ParentRun)}
	}
	if _, ok := allRuns[rootRunID]; !ok {
		return visualize.RunTreeTopology(nil, rootRunID)
	}
	root := rootRunID
	visited := map[string]bool{}
	for !visited[root] {
		visited[root] = true
		info, ok := allRuns[root]
		if !ok || info.parentID == "" {
			break
		}
		if _, parentExists := allRuns[info.parentID]; !parentExists {
			break
		}
		root = info.parentID
	}
	groupIDs := map[string]bool{root: true}
	changed := true
	for changed {
		changed = false
		for id, info := range allRuns {
			if groupIDs[info.parentID] && !groupIDs[id] {
				groupIDs[id] = true
				changed = true
			}
		}
	}
	inputs := make([]visualize.CompoundRunInput, 0, len(groupIDs))
	for id := range groupIDs {
		info := allRuns[id]
		rs := info.state
		workflow, _ := definitions.LoadWorkflowSnapshot(filepath.Join(runsDir, id, "workflow.yaml"))
		title := strings.TrimSpace(rs.Title)
		if title == "" {
			title = rs.WorkflowID
		}
		inputs = append(inputs, visualize.CompoundRunInput{
			RunID:     rs.ID,
			ParentRun: info.parentID,
			Title:     title,
			Status:    state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure),
			Workflow:  workflow,
			RunState:  rs,
			Collector: buildRunCollector(runsDir, rs),
		})
	}
	return visualize.RunTreeTopology(inputs, rootRunID)
}

// buildRunCollector constructs a metrics.Collector primed with the run's
// events and ForEach parent wiring. Returns nil if the events file is
// unreadable — callers treat that as "no metrics for this run" via the
// Collector != nil guard in visualize.CompoundExecutionGroupTopology.
func buildRunCollector(runsDir string, rs *state.RunState) *metrics.Collector {
	if rs == nil {
		return nil
	}
	events, _, err := state.ReadEventsWithOffset(filepath.Join(runsDir, rs.ID, "events.jsonl"))
	if err != nil {
		return buildCollectorFromEvents(rs, nil)
	}
	return buildCollectorFromEvents(rs, events)
}

// buildCollectorFromEvents primes a collector from a run state and event slice.
func buildCollectorFromEvents(rs *state.RunState, events []state.Event) *metrics.Collector {
	if rs == nil {
		return nil
	}
	c := metrics.NewCollector()
	rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for id := range nodes {
			if idx := strings.Index(id, "::"); idx > 0 {
				c.SetParent(id, id[:idx])
			}
		}
	})
	for _, ev := range events {
		c.ProcessEvent(ev)
	}
	return c
}
