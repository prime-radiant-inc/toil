package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/state"
)

// runRunsTree renders the run family rooted at the given run ID as an
// indented tree, walking parent_run pointers in the on-disk runs
// directory. Bypasses the HTTP API — runs are local files and `inspect`
// already follows this pattern.
func runRunsTree(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: toil runs tree <root_run_id>")
		os.Exit(1)
	}
	rootRunID := args[0]
	runsDir := config.RunsDir(mustRoot())
	if err := renderRunTree(os.Stdout, runsDir, rootRunID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// renderRunTree writes an indented tree of the run family rooted at
// rootRunID to w. It scans runsDir once to build a parent→children
// index, then walks downward from the root. Returns an error if the
// root run isn't on disk.
//
// Scanning the whole directory is acceptable for a CLI command: the
// cost scales with total run count, but is paid only when the user
// explicitly asks for the tree.
func renderRunTree(w io.Writer, runsDir, rootRunID string) error {
	root, families, err := indexRunFamily(runsDir, rootRunID)
	if err != nil {
		return err
	}
	// Root: no prefix, no glyph.
	writeRunRow(w, root, "", "")
	walkTree(w, root.ID, families, "")
	return nil
}

// indexRunFamily scans runsDir once and returns the root RunState plus
// a parent→children map covering every loadable run. Unloadable runs
// (corrupted JSON, permission errors) are skipped silently — a single
// damaged run shouldn't block the tree for valid ones.
func indexRunFamily(runsDir, rootRunID string) (*state.RunState, map[string][]*state.RunState, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read runs dir: %w", err)
	}

	families := map[string][]*state.RunState{}
	var root *state.RunState
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rs, err := state.LoadState(filepath.Join(runsDir, entry.Name(), "state.json"))
		if err != nil {
			continue
		}
		if rs.ID == rootRunID {
			root = rs
		}
		if rs.ParentRun != "" {
			families[rs.ParentRun] = append(families[rs.ParentRun], rs)
		}
	}
	if root == nil {
		return nil, nil, fmt.Errorf("run %q not found in %s", rootRunID, runsDir)
	}
	// Deterministic order: by start time then ID.
	for parent := range families {
		siblings := families[parent]
		sort.Slice(siblings, func(i, j int) bool {
			if siblings[i].StartedAt.Equal(siblings[j].StartedAt) {
				return siblings[i].ID < siblings[j].ID
			}
			return siblings[i].StartedAt.Before(siblings[j].StartedAt)
		})
		families[parent] = siblings
	}
	return root, families, nil
}

// walkTree recursively renders children of parentID. prefix is the
// vertical-bar indentation carried from ancestors — every still-open
// level (parent had more siblings) contributes "│  ", every closed
// level (parent was last) contributes "   ".
func walkTree(w io.Writer, parentID string, families map[string][]*state.RunState, prefix string) {
	children := families[parentID]
	for i, child := range children {
		isLast := i == len(children)-1
		glyph := "├─ "
		nextPrefix := prefix + "│  "
		if isLast {
			glyph = "└─ "
			nextPrefix = prefix + "   "
		}
		writeRunRow(w, child, prefix, glyph)
		walkTree(w, child.ID, families, nextPrefix)
	}
}

// writeRunRow writes one run line: prefix + glyph + columns.
func writeRunRow(w io.Writer, rs *state.RunState, prefix, glyph string) {
	effectiveStatus := state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure)
	fmt.Fprintf(w, "%s%s%-30s %-22s %-11s %s\n",
		prefix, glyph, rs.ID, rs.WorkflowID, effectiveStatus, runDuration(rs))
}

// runDuration formats StartedAt→FinishedAt as a compact duration.
// Returns "—" when the run is still running (no FinishedAt).
func runDuration(rs *state.RunState) string {
	if rs.FinishedAt == nil {
		return "—"
	}
	d := rs.FinishedAt.Sub(rs.StartedAt).Round(time.Second)
	return d.String()
}
