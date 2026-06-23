package visualize

import (
	"strings"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

// Status and kind constants shared across the topology builders.
const (
	statusPending          = "pending"
	statusCompleted        = "completed"
	statusRunning          = "running"
	statusFailedHandled    = "failed-handled"
	kindSubworkflow        = "subworkflow"
	kindSubworkflowForeach = "subworkflow-foreach"
	kindLeafRole           = "leaf-role"
	kindLeafSystem         = "leaf-system"
	kindForeachIteration   = "foreach-iteration"
	labelLoopExhausted     = "loop_exhausted"
)

// snapshotNodeStates copies node states from a RunState into a plain map
// for safe access outside the lock.
func snapshotNodeStates(runState *state.RunState) map[string]state.NodeState {
	snapshot := map[string]state.NodeState{}
	if runState == nil {
		return snapshot
	}
	runState.WithNodes(func(nodes map[string]*state.NodeState) {
		for id, node := range nodes {
			if node != nil {
				snapshot[id] = *node
			}
		}
	})
	return snapshot
}

// buildTemplateToOrchestratorMap returns a map from ForEach body (template)
// node ID to the orchestrator node ID that references it. Body templates
// are hidden in the rendered graph (the orchestrator subsumes them), and
// expanded iteration items are parented under the orchestrator.
func buildTemplateToOrchestratorMap(workflow *definitions.Workflow) map[string]string {
	m := map[string]string{}
	if workflow == nil {
		return m
	}
	for _, node := range workflow.Nodes {
		if node.ForEach != nil && node.ForEach.Body != "" {
			m[node.ForEach.Body] = node.ID
		}
	}
	return m
}

// normalizeStatus collapses unknown statuses to "pending" and leaves known
// values (including "failed-handled") as-is.
func normalizeStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case statusRunning, statusCompleted, "failed", statusFailedHandled, "paused", "skipped", "awaiting_approval", "cancelled":
		return status
	default:
		return statusPending
	}
}

// indexedChild pairs a ForEach expansion index with the child run ID it
// spawned. Using explicit indices (parsed from the "node::N" suffix) lets
// slices accumulated during Go map iteration be sorted deterministically
// before index-paired sibling flow edges are emitted.
type indexedChild struct {
	index int
	runID string
}

// CompoundRunInput holds everything needed to render one run as a
// compound parent node in the execution-group graph.
type CompoundRunInput struct {
	RunID     string
	ParentRun string
	Title     string
	Status    string
	Workflow  *definitions.Workflow
	RunState  *state.RunState
	Collector *metrics.Collector
}

// collectSubworkflows walks subworkflow references transitively from the
// start workflow, returning the set of workflow IDs that should be
// included in a compound workflow graph. Only workflows that exist in the
// bundle are included.
func collectSubworkflows(bundle *definitions.Bundle, start string) map[string]struct{} {
	visited := map[string]struct{}{}
	if bundle == nil {
		return visited
	}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, seen := visited[current]; seen {
			continue
		}
		wf, ok := bundle.Workflows[current]
		if !ok {
			continue
		}
		visited[current] = struct{}{}
		for _, node := range wf.Nodes {
			if node.Kind != kindSubworkflow {
				continue
			}
			target := strings.TrimSpace(node.Workflow)
			if target == "" {
				continue
			}
			if _, seen := visited[target]; !seen {
				queue = append(queue, target)
			}
		}
	}
	return visited
}
