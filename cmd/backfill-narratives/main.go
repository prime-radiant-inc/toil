// One-time backfill tool to regenerate run intent narratives using the
// improved prompt that includes all scalar inputs.
// Does NOT require the toil server — walks the filesystem directly.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/state"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: backfill-narratives <root-run-id>\n")
		fmt.Fprintf(os.Stderr, "  set RUNS_DIR to the runs directory (default: ./runs)\n")
		os.Exit(1)
	}
	rootRunID := os.Args[1]
	runsDir := os.Getenv("RUNS_DIR")
	if runsDir == "" {
		runsDir = "runs"
	}

	// Walk the execution group by following parent_run links.
	runIDs := collectExecutionGroup(runsDir, rootRunID)
	fmt.Printf("Found %d runs to backfill\n", len(runIDs))

	updated := 0
	skipped := 0
	failed := 0

	for i, runID := range runIDs {
		runDir := filepath.Join(runsDir, runID)

		rs, err := state.LoadState(filepath.Join(runDir, "state.json"))
		if err != nil {
			fmt.Printf("[%d/%d] %s: skip (no state)\n", i+1, len(runIDs), runID)
			skipped++
			continue
		}

		wf, err := definitions.LoadWorkflowSnapshot(filepath.Join(runDir, "workflow.yaml"))
		if err != nil {
			fmt.Printf("[%d/%d] %s: skip (no workflow)\n", i+1, len(runIDs), runID)
			skipped++
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, _, err := engine.GenerateRunIntentNarrative(ctx, wf, rs)
		cancel()
		if err != nil {
			fmt.Printf("[%d/%d] %s: FAILED (%v)\n", i+1, len(runIDs), runID, err)
			failed++
			continue
		}

		changed := false
		if out.Title != "" && out.Title != rs.Title {
			rs.Title = out.Title
			changed = true
		}
		if out.Description != "" && out.Description != rs.Description {
			rs.Description = out.Description
			changed = true
		}

		if !changed {
			fmt.Printf("[%d/%d] %s: unchanged\n", i+1, len(runIDs), runID)
			skipped++
			continue
		}

		if err := state.SaveState(filepath.Join(runDir, "state.json"), rs); err != nil {
			fmt.Printf("[%d/%d] %s: SAVE FAILED (%v)\n", i+1, len(runIDs), runID, err)
			failed++
			continue
		}

		fmt.Printf("[%d/%d] %s: updated\n", i+1, len(runIDs), runID)
		fmt.Printf("  title: %s\n", rs.Title)
		fmt.Printf("  desc:  %s\n", truncate(rs.Description, 120))
		updated++
	}

	fmt.Printf("\nDone: %d updated, %d skipped, %d failed\n", updated, skipped, failed)
}

// collectExecutionGroup finds all runs in an execution group by scanning
// the runs directory for runs whose parent_run chain leads to rootRunID.
func collectExecutionGroup(runsDir, rootRunID string) []string {
	// Build parent_run index from all state files.
	children := map[string][]string{} // parentID -> []childID
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read runs dir: %v\n", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(runsDir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		parent := strings.TrimSpace(rs.ParentRun)
		if parent != "" {
			children[parent] = append(children[parent], e.Name())
		}
	}

	// BFS from root.
	var result []string
	queue := []string{rootRunID}
	visited := map[string]bool{rootRunID: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		result = append(result, id)
		for _, child := range children[id] {
			if !visited[child] {
				visited[child] = true
				queue = append(queue, child)
			}
		}
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
