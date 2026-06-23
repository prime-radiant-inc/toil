package engine

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

// FinalizeRunTotals computes a run's NodeTotals from events.jsonl and
// stores it on runState.Totals. Called by terminal-save paths in the
// engine and orchestrator so terminal runs persist their totals
// alongside their other state.
//
// Tolerates a missing events.jsonl (returns nil error, leaves Totals
// untouched) — a run whose log was lost is broken in other ways and
// shouldn't be made un-saveable by this code path.
func FinalizeRunTotals(runState *state.RunState, runDir string) error {
	if runState == nil {
		return nil
	}
	eventsPath := filepath.Join(runDir, "events.jsonl")
	events, _, err := state.ReadEventsWithOffset(eventsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	c := metrics.NewCollector()
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id := range nodes {
			if idx := strings.Index(id, "::"); idx > 0 {
				c.SetParent(id, id[:idx])
			}
		}
	})
	for _, ev := range events {
		c.ProcessEvent(ev)
	}
	total := c.RunTotal()
	runState.Totals = &total
	return nil
}

// metaDecisions is the set of synthesized routing decisions the engine can
// emit in place of a real LLM decision. Only these values trigger the
// failed:true edge scan in ComputeUnresolvedFailure.
var metaDecisions = map[string]bool{
	MetaDecisionLoopExhausted:  true,
	MetaDecisionTimeout:        true,
	MetaDecisionRetryExhausted: true,
}

// ComputeUnresolvedFailure walks the run's terminal state and determines
// whether any failed:true meta-decision edge fired (directly in this run
// or transitively in any subworkflow child).
//
// Resets runState.HasUnresolvedFailure to false at the start of the walk,
// then sets it true if any indicator is found. Single-threaded; called only
// at run finalization.
//
// Tolerates missing/corrupt child state.json (returns false for that subtree,
// matching FinalizeRunTotals's existing tolerance).
func ComputeUnresolvedFailure(runState *state.RunState, workflow *definitions.Workflow, runDir string) bool {
	// Build O(1) lookup from node id to definition.
	nodeByID := make(map[string]*definitions.Node, len(workflow.Nodes))
	for i := range workflow.Nodes {
		nodeByID[workflow.Nodes[i].ID] = &workflow.Nodes[i]
	}

	var failed bool
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for nodeID, ns := range nodes {
			if failed {
				break
			}
			wfNode := nodeByID[strippedNodeID(nodeID)]
			if wfNode == nil {
				continue
			}
			// (a) Direct: this node fired a meta-decision routing whose edge is failed:true
			if ns.LastRoutingDecision != "" && metaDecisions[ns.LastRoutingDecision] {
				for _, e := range workflow.Edges {
					if e.From != wfNode.ID || e.When != ns.LastRoutingDecision {
						continue
					}
					if e.Failed != nil && *e.Failed {
						failed = true
						break
					}
				}
			}
			// (b) Transitive: this node is a subworkflow whose child has the flag
			if !failed && wfNode.Kind == kindSubworkflow {
				if childRunID, ok := ns.Data[dataKeyChildRun].(string); ok && childRunID != "" {
					childPath := filepath.Join(runDir, "..", childRunID, "state.json")
					if childState, err := state.LoadState(childPath); err == nil && childState.HasUnresolvedFailure {
						failed = true
					}
				}
			}
		}
		runState.HasUnresolvedFailure = failed
	})

	return failed
}

// strippedNodeID strips the ForEach item suffix from a node id.
// "foo::0" -> "foo"; "foo" -> "foo".
func strippedNodeID(id string) string {
	if idx := strings.Index(id, "::"); idx > 0 {
		return id[:idx]
	}
	return id
}
