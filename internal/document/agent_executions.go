package document

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"primeradiant.com/toil/internal/state"
)

// FindPromptForExecution is the exported wrapper around findPromptForExecution
// used by the dashboard agent-detail handler. It returns the raw prompt text
// for the ordinal-th execution of nodeID, or "" if none was logged.
func FindPromptForExecution(events []state.Event, nodeID string, ordinal int) string {
	return findPromptForExecution(events, nodeID, ordinal)
}

// RowArtifacts is the exported wrapper around rowArtifacts. Returns the
// list of artifacts referenced by the node state.
func RowArtifacts(n *state.NodeState) []ArtifactRef {
	return rowArtifacts(n)
}

// AgentExecution identifies one node execution that ran under a given agent
// session id. Multiple executions can share a session id when the runner
// resumes the agent across attempts. Ordinal counts the node_started events
// for (RunID, NodeID) up to and including this one — used to slice events
// for the specific execution via SliceExecutionEvents.
type AgentExecution struct {
	RunID      string
	NodeID     string
	Ordinal    int
	StartedAt  time.Time
	WorkflowID string
	Resume     bool
}

// FindAgentExecutions walks every run directory under runsDir, scans each
// run's events.jsonl for node_started events carrying the target session
// id in their Data, and returns the matching executions sorted by start
// time. Cheap-enough for a per-session diagnostic page; not built for
// hot-path use.
func FindAgentExecutions(runsDir, sessionID string) []AgentExecution {
	if sessionID == "" {
		return nil
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil
	}
	var out []AgentExecution
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		events, _, err := state.ReadEventsWithOffset(filepath.Join(runsDir, runID, "events.jsonl"))
		if err != nil {
			continue
		}
		var workflowID string
		if rs, err := state.LoadState(filepath.Join(runsDir, runID, "state.json")); err == nil && rs != nil {
			workflowID = rs.WorkflowID
		}
		nodeStartCount := map[string]int{}
		for _, ev := range events {
			if ev.Type != eventNodeStarted {
				continue
			}
			nodeStartCount[ev.NodeID]++
			sid, _ := ev.Data["session_id"].(string)
			if sid != sessionID {
				continue
			}
			resume, _ := ev.Data["resume"].(bool)
			out = append(out, AgentExecution{
				RunID:      runID,
				NodeID:     ev.NodeID,
				Ordinal:    nodeStartCount[ev.NodeID],
				StartedAt:  ev.Timestamp,
				WorkflowID: workflowID,
				Resume:     resume,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}
