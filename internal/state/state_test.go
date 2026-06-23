package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRunState(t *testing.T) {
	before := time.Now().UTC()
	rs := NewRunState("run-1", "my-workflow", map[string]any{"key": "val"})
	after := time.Now().UTC()

	if rs.ID != "run-1" {
		t.Fatalf("ID = %q, want run-1", rs.ID)
	}
	if rs.WorkflowID != "my-workflow" {
		t.Fatalf("WorkflowID = %q, want my-workflow", rs.WorkflowID)
	}
	if rs.Status != "running" {
		t.Fatalf("Status = %q, want running", rs.Status)
	}
	if rs.StartedAt.Before(before) || rs.StartedAt.After(after) {
		t.Fatalf("StartedAt %v not in [%v, %v]", rs.StartedAt, before, after)
	}
	if rs.Inputs["key"] != "val" {
		t.Fatalf("Inputs[key] = %v, want val", rs.Inputs["key"])
	}
	if rs.Nodes == nil {
		t.Fatal("Nodes map should be initialized, got nil")
	}
}

func TestNode_AutoCreates(t *testing.T) {
	rs := NewRunState("r", "wf", nil)

	node := rs.Node("new-node")
	if node.ID != "new-node" {
		t.Fatalf("ID = %q, want new-node", node.ID)
	}
	if node.Status != "pending" {
		t.Fatalf("Status = %q, want pending", node.Status)
	}

	// Calling again returns the same node, not a new one.
	same := rs.Node("new-node")
	if same != node {
		t.Fatal("Node() returned a different pointer for the same ID")
	}
}

func TestWithNode_MutationPersists(t *testing.T) {
	rs := NewRunState("r", "wf", nil)

	rs.WithNode("n1", func(n *NodeState) {
		n.Status = statusCompleted
		n.Message = "done"
		n.Attempts = 3
	})

	got := rs.Node("n1")
	if got.Status != statusCompleted || got.Message != "done" || got.Attempts != 3 {
		t.Fatalf("WithNode mutation didn't persist: %+v", got)
	}
}

func TestWithNodes_ReadsAllNodes(t *testing.T) {
	rs := NewRunState("r", "wf", nil)
	rs.Node("a")
	rs.Node("b")

	var count int
	rs.WithNodes(func(nodes map[string]*NodeState) {
		count = len(nodes)
	})
	if count != 2 {
		t.Fatalf("WithNodes saw %d nodes, want 2", count)
	}
}

func TestNodeStatus_ExistingAndMissing(t *testing.T) {
	rs := NewRunState("r", "wf", nil)
	rs.WithNode("exists", func(n *NodeState) {
		n.Status = "failed"
	})

	status, ok := rs.NodeStatus("exists")
	if !ok || status != "failed" {
		t.Fatalf("NodeStatus(exists) = (%q, %v), want (failed, true)", status, ok)
	}

	status, ok = rs.NodeStatus("missing")
	if ok || status != "" {
		t.Fatalf("NodeStatus(missing) = (%q, %v), want ('', false)", status, ok)
	}
}

func TestSetJoinState_PreservesOrder(t *testing.T) {
	// SetJoinState preserves the order of the input slice.
	// Callers (e.g. run_loop.go) are responsible for passing
	// already-sorted slices (via state.SortedKeys) when deterministic
	// JSON output is required.
	rs := NewRunState("r", "wf", nil)
	rs.SetJoinState("join", []string{"c", "a", "b"})

	got := rs.GetJoinState("join")
	if len(got) != 3 || got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("expected [c a b], got %v", got)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	now := time.Now().UTC().Truncate(time.Millisecond)
	finished := now.Add(5 * time.Minute)

	rs := NewRunState("run-42", "deploy", map[string]any{"region": "us-east-1"})
	rs.Title = "Deploy Run"
	rs.Status = statusCompleted
	rs.Error = "timeout on node X"
	rs.FinishedAt = &finished
	rs.Env = map[string]string{"TOKEN": "abc"}
	rs.ParentRun = "parent-1"
	rs.CallbackURL = "https://example.com/hook"
	rs.StartedAt = now

	rs.WithNode("build", func(n *NodeState) {
		n.Status = statusCompleted
		n.Decision = "approve"
		n.Message = "all good"
		n.Artifacts = []string{"bin/app"}
		n.Data = map[string]any{"sha": "abc123"}
		n.Attempts = 2
		n.SessionID = "sess-1"
		n.RetryCount = 1
	})
	rs.SetJoinState("merge", []string{"a", "b"})

	if err := SaveState(path, rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// RunState fields
	if loaded.ID != "run-42" {
		t.Errorf("ID = %q", loaded.ID)
	}
	if loaded.Title != "Deploy Run" {
		t.Errorf("Title = %q", loaded.Title)
	}
	if loaded.WorkflowID != "deploy" {
		t.Errorf("WorkflowID = %q", loaded.WorkflowID)
	}
	if loaded.Status != statusCompleted {
		t.Errorf("Status = %q", loaded.Status)
	}
	if loaded.Error != "timeout on node X" {
		t.Errorf("Error = %q", loaded.Error)
	}
	if !loaded.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want %v", loaded.StartedAt, now)
	}
	if loaded.FinishedAt == nil || !loaded.FinishedAt.Equal(finished) {
		t.Errorf("FinishedAt = %v, want %v", loaded.FinishedAt, finished)
	}
	if loaded.Inputs["region"] != "us-east-1" {
		t.Errorf("Inputs[region] = %v", loaded.Inputs["region"])
	}
	if loaded.Env["TOKEN"] != "abc" {
		t.Errorf("Env[TOKEN] = %v", loaded.Env["TOKEN"])
	}
	if loaded.ParentRun != "parent-1" {
		t.Errorf("ParentRun = %q", loaded.ParentRun)
	}
	if loaded.CallbackURL != "https://example.com/hook" {
		t.Errorf("CallbackURL = %q", loaded.CallbackURL)
	}

	// NodeState fields
	node := loaded.Node("build")
	if node.Status != statusCompleted {
		t.Errorf("node.Status = %q", node.Status)
	}
	if node.Decision != "approve" {
		t.Errorf("node.Decision = %q", node.Decision)
	}
	if node.Message != "all good" {
		t.Errorf("node.Message = %q", node.Message)
	}
	if len(node.Artifacts) != 1 || node.Artifacts[0] != "bin/app" {
		t.Errorf("node.Artifacts = %v", node.Artifacts)
	}
	if node.Data["sha"] != "abc123" {
		t.Errorf("node.Data[sha] = %v", node.Data["sha"])
	}
	if node.Attempts != 2 {
		t.Errorf("node.Attempts = %d", node.Attempts)
	}
	if node.SessionID != "sess-1" {
		t.Errorf("node.SessionID = %q", node.SessionID)
	}
	if node.RetryCount != 1 {
		t.Errorf("node.RetryCount = %d", node.RetryCount)
	}

	// JoinState survives round-trip
	join := loaded.GetJoinState("merge")
	if len(join) != 2 || join[0] != "a" || join[1] != "b" {
		t.Errorf("JoinState[merge] = %v", join)
	}
}

func TestLoadState_InitializesNilNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write minimal JSON with no "nodes" key.
	data := []byte(`{"id":"r","workflow_id":"wf","status":"running","started_at":"2026-01-01T00:00:00Z"}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Nodes == nil {
		t.Fatal("Nodes should be initialized to empty map, got nil")
	}
	// Should be safe to use Node() immediately after load.
	node := loaded.Node("x")
	if node.Status != "pending" {
		t.Fatalf("auto-created node status = %q, want pending", node.Status)
	}
}

func TestLoadState_FileNotFound(t *testing.T) {
	_, err := LoadState("/nonexistent/path/state.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestJoinState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	rs := NewRunState("run-1", "wf", nil)

	// Set join state for two join nodes
	rs.SetJoinState("join_1", []string{"a", "b"})
	rs.SetJoinState("join_2", []string{"x"})

	// Verify in-memory
	got := rs.GetJoinState("join_1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}
	got2 := rs.GetJoinState("join_2")
	if len(got2) != 1 || got2[0] != "x" {
		t.Fatalf("expected [x], got %v", got2)
	}

	// Missing key returns nil
	if rs.GetJoinState("nonexistent") != nil {
		t.Fatal("expected nil for missing join state")
	}

	// Save and reload
	if err := SaveState(path, rs); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Verify round-trip
	got = loaded.GetJoinState("join_1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("after reload: expected [a b], got %v", got)
	}
	got2 = loaded.GetJoinState("join_2")
	if len(got2) != 1 || got2[0] != "x" {
		t.Fatalf("after reload: expected [x], got %v", got2)
	}
}

func TestRunState_SecretsExcludedFromJSON(t *testing.T) {
	rs := NewRunState("run-1", "test-wf", nil)
	rs.Secrets = map[string]string{
		"GITHUB_TOKEN": "ghp_superSecretValue123",
	}

	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "ghp_superSecretValue123") {
		t.Fatal("secret value leaked into JSON output")
	}
	if strings.Contains(jsonStr, "GITHUB_TOKEN") {
		t.Fatal("secret key name leaked into JSON output")
	}
}

func TestNodeState_ErrorRoundTrip(t *testing.T) {
	ns := &NodeState{
		ID:     "test-node",
		Status: "failed",
		Error:  "merge conflict on main",
	}

	data, err := json.Marshal(ns)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded NodeState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Error != "merge conflict on main" {
		t.Fatalf("Error = %q, want %q", loaded.Error, "merge conflict on main")
	}
}

func TestSetNarrative(t *testing.T) {
	rs := NewRunState("r", "wf", nil)

	rs.SetNarrative("My Title", "My Description")
	if rs.Title != "My Title" {
		t.Errorf("Title = %q, want %q", rs.Title, "My Title")
	}
	if rs.Description != "My Description" {
		t.Errorf("Description = %q, want %q", rs.Description, "My Description")
	}

	// Empty values should not overwrite.
	rs.SetNarrative("", "")
	if rs.Title != "My Title" {
		t.Errorf("Title changed to %q after empty SetNarrative", rs.Title)
	}
	if rs.Description != "My Description" {
		t.Errorf("Description changed to %q after empty SetNarrative", rs.Description)
	}

	// Partial update.
	rs.SetNarrative("New Title", "")
	if rs.Title != "New Title" {
		t.Errorf("Title = %q, want %q", rs.Title, "New Title")
	}
	if rs.Description != "My Description" {
		t.Errorf("Description = %q, want %q", rs.Description, "My Description")
	}
}

func TestSetSummary(t *testing.T) {
	rs := NewRunState("r", "wf", nil)

	rs.SetSummary("All done.")
	if rs.Summary != "All done." {
		t.Errorf("Summary = %q, want %q", rs.Summary, "All done.")
	}

	// Empty value should not overwrite.
	rs.SetSummary("")
	if rs.Summary != "All done." {
		t.Errorf("Summary changed to %q after empty SetSummary", rs.Summary)
	}
}

func TestJoinState_BackwardsCompatible(t *testing.T) {
	// State without JoinState field should load fine (omitempty)
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	rs := NewRunState("run-1", "wf", nil)
	// Don't set any JoinState
	if err := SaveState(path, rs); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.GetJoinState("anything") != nil {
		t.Fatal("expected nil for missing join state on legacy state")
	}
}

func TestSaveState_ConcurrentCallersDoNotCollide(t *testing.T) {
	// Regression guard: SaveState uses a unique tmp filename per call so
	// concurrent writers don't clobber each other. With a shared "path.tmp"
	// one goroutine's Rename could succeed while another's WriteFile
	// clobbers then its Rename fails with ENOENT. Multi-item ForEach
	// executions run many goroutines that each flush state, so this race
	// is real in practice.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	rs := NewRunState("r", "wf", nil)
	// Pre-save so the file exists.
	if err := SaveState(path, rs); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	const N = 32
	errs := make(chan error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- SaveState(path, rs)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent SaveState returned error: %v", err)
		}
	}
}

func TestLoadState_CleansOrphanTmpFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	rs := NewRunState("run-x", "wf", nil)
	if err := SaveState(path, rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Drop a stale tmp file that simulates a crashed prior SaveState.
	orphan := filepath.Join(dir, "state.json.tmp.abc123")
	if err := os.WriteFile(orphan, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	// Backdate it well past the cleanup threshold.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Recent tmp file — must NOT be deleted (a concurrent SaveState might
	// own it).
	recent := filepath.Join(dir, "state.json.tmp.xyz999")
	if err := os.WriteFile(recent, []byte("active"), 0o644); err != nil {
		t.Fatalf("write recent: %v", err)
	}

	if _, err := LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("expected stale orphan to be removed, stat err: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("expected recent tmp to survive, got err: %v", err)
	}
}

func TestLoadState_CleansOrphansEvenWhenStateMissing(t *testing.T) {
	// Regression guard: LoadState must run cleanup BEFORE reading
	// state.json. Otherwise the crash-recovery case — where only tmp
	// files remain and state.json is missing — would leak orphans
	// indefinitely because the early ReadFile error would short-circuit.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json") // deliberately absent

	// Drop a stale orphan tmp.
	orphan := filepath.Join(dir, "state.json.tmp.ghost")
	if err := os.WriteFile(orphan, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// LoadState is expected to fail since state.json is absent.
	if _, err := LoadState(path); err == nil {
		t.Fatalf("expected LoadState to error when state.json is missing")
	}

	// But cleanup should still have run for this dir.
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("expected stale orphan removed even when state.json missing, stat err: %v", err)
	}
}

func TestTaggedNode_HasTag(t *testing.T) {
	n := TaggedNode{Tags: []string{"override", "waiver"}}
	if !n.HasTag("override") {
		t.Error("HasTag(override) should be true")
	}
	if !n.HasTag("waiver") {
		t.Error("HasTag(waiver) should be true")
	}
	if n.HasTag("nope") {
		t.Error("HasTag(nope) should be false")
	}
	if n.HasTag("") {
		t.Error("HasTag(empty) should be false")
	}
}

func TestNodesTagged_DerivesFromNodeTags(t *testing.T) {
	// NodesTagged reads NodeState.Tags (materialized by the engine
	// at emit time). Decision strings alone don't qualify — workflows
	// must have declared the tag on the decision, and the engine must
	// have copied it onto the node.
	rs := NewRunState("r", "implement_task", nil)
	t1 := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	rs.WithNode("a", func(n *NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
		n.Message = "waived A"
		n.EndedAt = &t1
	})
	rs.WithNode("b", func(n *NodeState) {
		n.Decision = "approved"
		n.Message = "ok"
		n.EndedAt = &t1
	})
	rs.WithNode("c", func(n *NodeState) {
		n.Decision = "skip_task"
		n.Tags = []string{"override"}
		n.Message = "waived C"
		n.EndedAt = &t2
	})
	// Node with a different tag — must not be matched by "override" query.
	rs.WithNode("d", func(n *NodeState) {
		n.Decision = "something"
		n.Tags = []string{"other_tag"}
		n.EndedAt = &t1
	})

	got := rs.NodesTagged("override")
	if len(got) != 2 {
		t.Fatalf("expected 2 tagged nodes, got %d: %+v", len(got), got)
	}
	if got[0].NodeID != "a" || got[1].NodeID != "c" {
		t.Fatalf("wrong order: %v, %v", got[0].NodeID, got[1].NodeID)
	}
}

func TestNodesTagged_EmptyForUnknownTag(t *testing.T) {
	rs := NewRunState("r", "wf", nil)
	rs.WithNode("a", func(n *NodeState) {
		n.Decision = "approved"
		n.Tags = []string{"override"}
	})
	if got := rs.NodesTagged("nonexistent"); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
	if got := rs.NodesTagged(""); got != nil {
		t.Fatalf("empty tag query should return nil, got %+v", got)
	}
}

func TestNodesTagged_DecisionStringAloneDoesNotQualify(t *testing.T) {
	// A node with decision=force_approve but no Tags is NOT tagged.
	// This enforces the rule that workflows declare tags explicitly
	// — the harness doesn't have hardcoded knowledge that
	// "force_approve" means "override".
	rs := NewRunState("r", "wf", nil)
	rs.WithNode("a", func(n *NodeState) {
		n.Decision = "force_approve"
		// No Tags set.
	})
	if got := rs.NodesTagged("override"); len(got) != 0 {
		t.Fatalf("untagged node should not match: %+v", got)
	}
}

func TestNodeStateLoopIterationsRoundtrip(t *testing.T) {
	n := &NodeState{
		ID:             "x",
		Status:         "completed",
		LoopIterations: 5,
	}
	buf, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"loop_iterations":5`) {
		t.Fatalf("serialized JSON missing loop_iterations=5: %s", string(buf))
	}
	var back NodeState
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.LoopIterations != 5 {
		t.Errorf("LoopIterations=%d want 5", back.LoopIterations)
	}
}

func TestNodeStateLastRoutingFieldsRoundtrip(t *testing.T) {
	when := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	n := &NodeState{
		ID:                  "x",
		Status:              "completed",
		LastRoutingDecision: "_loop_exhausted",
		LastRoutingAt:       &when,
	}
	buf, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"last_routing_decision":"_loop_exhausted"`) {
		t.Fatalf("missing last_routing_decision: %s", string(buf))
	}
	var back NodeState
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.LastRoutingDecision != "_loop_exhausted" {
		t.Errorf("LastRoutingDecision=%q want %q", back.LastRoutingDecision, "_loop_exhausted")
	}
	if back.LastRoutingAt == nil || !back.LastRoutingAt.Equal(when) {
		t.Errorf("LastRoutingAt=%v want %v", back.LastRoutingAt, when)
	}
}

func TestNodesTagged_RetryToUntaggedDecisionSupersedes(t *testing.T) {
	// The point of derivation + materialized tags: when a node's
	// decision changes from a tagged one to an untagged one, the
	// derived list drops the entry automatically.
	rs := NewRunState("r", "implement_task", nil)
	ended := time.Now().UTC()
	rs.WithNode("esc", func(n *NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
		n.EndedAt = &ended
	})
	if len(rs.NodesTagged("override")) != 1 {
		t.Fatalf("expected 1 tagged node")
	}

	// Simulate retry: overwrite decision and clear tags.
	rs.WithNode("esc", func(n *NodeState) {
		n.Decision = "send_back"
		n.Tags = nil
	})
	if got := rs.NodesTagged("override"); len(got) != 0 {
		t.Fatalf("expected 0 after retry to untagged decision, got %+v", got)
	}
}

func TestNodesTagged_DeterministicOrder(t *testing.T) {
	// Zero-timestamps all tie; fall back to NodeID for determinism.
	rs := NewRunState("r", "wf", nil)
	rs.WithNode("zebra", func(n *NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
	})
	rs.WithNode("alpha", func(n *NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
	})
	rs.WithNode("middle", func(n *NodeState) {
		n.Decision = "skip_task"
		n.Tags = []string{"override"}
	})

	got := rs.NodesTagged("override")
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	want := []string{"alpha", "middle", "zebra"}
	for i, w := range want {
		if got[i].NodeID != w {
			t.Fatalf("got[%d].NodeID = %q, want %q", i, got[i].NodeID, w)
		}
	}
}

func TestNodesByTag_GroupsMultipleTags(t *testing.T) {
	// A node with two tags appears under both keys. Nodes with no
	// tags don't appear at all.
	rs := NewRunState("r", "wf", nil)
	rs.WithNode("a", func(n *NodeState) {
		n.Tags = []string{"override", "audit"}
	})
	rs.WithNode("b", func(n *NodeState) {
		n.Tags = []string{"audit"}
	})
	rs.WithNode("c", func(n *NodeState) {
		// no tags
	})

	byTag := rs.NodesByTag()
	if len(byTag["override"]) != 1 || byTag["override"][0].NodeID != "a" {
		t.Fatalf("override bucket = %+v", byTag["override"])
	}
	if len(byTag["audit"]) != 2 {
		t.Fatalf("audit bucket should have 2, got %d", len(byTag["audit"]))
	}
	if _, present := byTag["other"]; present {
		t.Error("non-matching tag should not be in map")
	}
}

func TestTaggedNodes_SurviveRoundTripViaNodes(t *testing.T) {
	// Round-trip still works — tags are on Nodes, which persist. The
	// derived query returns the same list before and after
	// Save/Load.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	ended := time.Now().UTC().Truncate(time.Millisecond)

	rs := NewRunState("r", "implement_task", nil)
	rs.WithNode("resolve_review_dispute", func(n *NodeState) {
		n.Decision = "force_approve"
		n.Tags = []string{"override"}
		n.Message = "waived scaffold concern"
		n.Data = map[string]any{
			"waived_concerns": []any{
				map[string]any{"source": "write_code", "concern": "TS exec mode", "justification": "verified"},
			},
		}
		n.EndedAt = &ended
	})

	if err := SaveState(path, rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	overrides := loaded.NodesTagged("override")
	if len(overrides) != 1 {
		t.Fatalf("expected 1, got %d", len(overrides))
	}
	o := overrides[0]
	if o.NodeID != "resolve_review_dispute" || o.Decision != "force_approve" {
		t.Fatalf("fields lost: %+v", o)
	}
	if !o.HasTag("override") {
		t.Fatalf("tags lost in round-trip: %+v", o.Tags)
	}
	if _, has := o.Data["waived_concerns"]; !has {
		t.Fatal("data.waived_concerns lost in round-trip")
	}
}

func TestLoadState_BackfillsLegacyOverrideTags(t *testing.T) {
	// Runs that completed BEFORE the tag-based model persisted decisions
	// like force_approve with no Tags. LoadState synthesizes Tags=[override]
	// so dashboard badges, inspect, and tree.tagged.override continue to
	// surface historical waivers.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Legacy shape: no Tags on nodes.
	legacyJSON := []byte(`{
		"id": "legacy-run",
		"workflow_id": "implement_task",
		"status": "completed",
		"started_at": "2026-04-19T20:00:00Z",
		"inputs": {},
		"nodes": {
			"resolve_review_dispute": {
				"id": "resolve_review_dispute",
				"status": "completed",
				"decision": "force_approve",
				"message": "waived scaffold concern"
			},
			"write_code": {
				"id": "write_code",
				"status": "completed",
				"decision": "spec_issue",
				"message": "unresolved"
			},
			"already_tagged": {
				"id": "already_tagged",
				"status": "completed",
				"decision": "force_approve",
				"message": "has explicit tags",
				"tags": ["custom", "other"]
			}
		}
	}`)
	if err := os.WriteFile(path, legacyJSON, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// force_approve with no tags → synthesized override tag.
	reNode := loaded.Node("resolve_review_dispute")
	if len(reNode.Tags) != 1 || reNode.Tags[0] != "override" {
		t.Errorf("resolve_review_dispute tags = %v, want [override]", reNode.Tags)
	}

	// Non-legacy decision (spec_issue) → untagged, as expected.
	ceNode := loaded.Node("write_code")
	if len(ceNode.Tags) != 0 {
		t.Errorf("write_code tags = %v, want []", ceNode.Tags)
	}

	// Explicit tags are NOT overwritten. Even if the decision has a
	// legacy mapping, the workflow's own declaration wins.
	atNode := loaded.Node("already_tagged")
	if len(atNode.Tags) != 2 || atNode.Tags[0] != "custom" {
		t.Errorf("already_tagged tags = %v, want [custom other]", atNode.Tags)
	}

	// Backfilled tags must survive save+reload (lazy migration on write).
	if err := SaveState(path, loaded); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	reloaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Node("resolve_review_dispute").Tags) != 1 {
		t.Error("backfilled tags didn't persist across save+reload")
	}

	// NodesTagged should now surface the legacy run's override —
	// only resolve_review_dispute qualifies; already_tagged has tags
	// [custom, other], not [override].
	overrides := reloaded.NodesTagged("override")
	if len(overrides) != 1 {
		t.Fatalf("NodesTagged(override) = %d, want 1", len(overrides))
	}
	if overrides[0].NodeID != "resolve_review_dispute" {
		t.Errorf("wrong node surfaced: %v", overrides[0].NodeID)
	}
}

func TestLoadExecutionGroup_EmptyRunsDir(t *testing.T) {
	// Missing runs dir or unknown starting run returns an empty map
	// (not nil, not an error) — the caller handles "no tree" via map
	// size, not by error-checking.
	if got := LoadExecutionGroup("", "whatever"); len(got) != 0 {
		t.Fatalf("empty runsDir should return empty, got %v", got)
	}
	if got := LoadExecutionGroup(t.TempDir(), ""); len(got) != 0 {
		t.Fatalf("empty startRunID should return empty, got %v", got)
	}
	if got := LoadExecutionGroup(t.TempDir(), "nonexistent"); len(got) != 0 {
		t.Fatalf("missing run should return empty, got %v", got)
	}
}

// writeRunForTest creates a state.json file with the given fields.
// Small local helper so state-level tests don't depend on the engine
// package's tree_resolver_test fixtures.
func writeRunForTest(t *testing.T, runsDir, runID, parentRun string, nodes map[string]*NodeState) {
	t.Helper()
	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rs := &RunState{ID: runID, ParentRun: parentRun, Nodes: nodes}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadExecutionGroup_WalksUpToRootAndDownToChildren(t *testing.T) {
	// root -> mid -> leaf   via ParentRun walk UP, child_run walk DOWN
	runsDir := t.TempDir()
	writeRunForTest(t, runsDir, "root", "", map[string]*NodeState{
		"dispatch": {ID: "dispatch", Data: map[string]any{"child_run": "mid"}},
	})
	writeRunForTest(t, runsDir, "mid", "root", map[string]*NodeState{
		"dispatch": {ID: "dispatch", Data: map[string]any{"child_run": "leaf"}},
	})
	writeRunForTest(t, runsDir, "leaf", "mid", nil)

	// Starting from the middle, we should find root (up) and leaf
	// (down).
	got := LoadExecutionGroup(runsDir, "mid")
	if _, ok := got["root"]; !ok {
		t.Error("root missing from group")
	}
	if _, ok := got["mid"]; !ok {
		t.Error("mid missing from group")
	}
	if _, ok := got["leaf"]; !ok {
		t.Error("leaf missing from group")
	}
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 runs, got %d", len(got))
	}
}

func TestLoadExecutionGroup_IgnoresUnrelatedRuns(t *testing.T) {
	// Two unrelated execution groups in the same dir. Queries must
	// isolate to the starting run's group — that's the performance
	// guarantee the narrow loader exists to provide.
	runsDir := t.TempDir()
	writeRunForTest(t, runsDir, "a-root", "", nil)
	writeRunForTest(t, runsDir, "b-root", "", nil)
	writeRunForTest(t, runsDir, "unrelated-extra", "", nil)

	got := LoadExecutionGroup(runsDir, "a-root")
	if _, ok := got["a-root"]; !ok {
		t.Fatal("a-root missing from its own group")
	}
	if _, ok := got["b-root"]; ok {
		t.Error("b-root must not be included in a-root's group")
	}
	if _, ok := got["unrelated-extra"]; ok {
		t.Error("unrelated runs must not be included")
	}
	if len(got) != 1 {
		t.Fatalf("expected only a-root, got %d runs", len(got))
	}
}

func TestExtractChildRunIDs_AcceptsMultipleSliceShapes(t *testing.T) {
	// The items slice can arrive as []any (JSON round-trip) or as
	// []map[string]any (engine-constructed in-memory). Both must
	// yield the same child_run IDs.
	direct := map[string]any{
		"child_run": "direct-child",
	}
	asAny := map[string]any{
		"items": []any{
			map[string]any{"data": map[string]any{"child_run": "any-0"}},
			map[string]any{"data": map[string]any{"child_run": "any-1"}},
		},
	}
	asTyped := map[string]any{
		"items": []map[string]any{
			{"data": map[string]any{"child_run": "typed-0"}},
			{"data": map[string]any{"child_run": "typed-1"}},
		},
	}
	mixedShapes := map[string]any{
		"child_run": "mixed-direct",
		"items": []any{
			map[string]any{"data": map[string]any{"child_run": "mixed-item-0"}},
		},
	}

	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"direct", direct, []string{"direct-child"}},
		{"items_as_any", asAny, []string{"any-0", "any-1"}},
		{"items_as_typed", asTyped, []string{"typed-0", "typed-1"}},
		{"mixed", mixedShapes, []string{"mixed-direct", "mixed-item-0"}},
		{"empty", map[string]any{}, nil},
		{"nil_items", map[string]any{"items": nil}, nil},
		{"non_slice_items", map[string]any{"items": "not a slice"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractChildRunIDs(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestLoadExecutionGroup_FollowsForeachItems(t *testing.T) {
	// ForEach dispatchers put child_run inside data.items[].data —
	// the narrow loader must traverse that shape too.
	runsDir := t.TempDir()
	writeRunForTest(t, runsDir, "root", "", map[string]*NodeState{
		"implement_tasks": {
			ID: "implement_tasks",
			Data: map[string]any{
				"items": []any{
					map[string]any{"data": map[string]any{"child_run": "iter-0"}},
					map[string]any{"data": map[string]any{"child_run": "iter-1"}},
				},
			},
		},
	})
	writeRunForTest(t, runsDir, "iter-0", "root", nil)
	writeRunForTest(t, runsDir, "iter-1", "root", nil)

	got := LoadExecutionGroup(runsDir, "root")
	for _, id := range []string{"root", "iter-0", "iter-1"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s missing from group", id)
		}
	}
}

func TestLoadExecutionGroup_TraversesTypedForeachItems(t *testing.T) {
	// Defensive: even though disk round-trip normalizes items to
	// []any, if a caller ever feeds LoadExecutionGroup an in-memory
	// state where items is still []map[string]any (engine initial
	// shape), traversal must still follow the child_run pointers.
	// The extractChildRunIDs helper handles both — verify end-to-end.
	runsDir := t.TempDir()

	// Manually serialize to disk (simulate a run that was saved
	// immediately after engine construction). json.Marshal normalizes
	// to []any on write, so to preserve the typed-slice shape in
	// state.json we'd need to bypass json.Marshal — which isn't a
	// realistic production path. Instead, verify the helper on
	// in-memory data directly (which is what matters for future
	// callers that operate on an un-serialized RunState).
	rs := &RunState{
		ID: "root",
		Nodes: map[string]*NodeState{
			"orch": {
				ID: "orch",
				Data: map[string]any{
					"items": []map[string]any{
						{"data": map[string]any{"child_run": "iter-0"}},
						{"data": map[string]any{"child_run": "iter-1"}},
					},
				},
			},
		},
	}
	ids := extractChildRunIDs(rs.Node("orch").Data)
	if len(ids) != 2 || ids[0] != "iter-0" || ids[1] != "iter-1" {
		t.Fatalf("extractChildRunIDs on typed-slice items = %v, want [iter-0 iter-1]", ids)
	}

	// And JSON-round-tripped state still works (regression guard).
	writeRunForTest(t, runsDir, "root", "", map[string]*NodeState{
		"orch": {
			ID: "orch",
			Data: map[string]any{
				"items": []any{
					map[string]any{"data": map[string]any{"child_run": "iter-0"}},
					map[string]any{"data": map[string]any{"child_run": "iter-1"}},
				},
			},
		},
	})
	writeRunForTest(t, runsDir, "iter-0", "root", nil)
	writeRunForTest(t, runsDir, "iter-1", "root", nil)

	got := LoadExecutionGroup(runsDir, "root")
	for _, expectedID := range []string{"root", "iter-0", "iter-1"} {
		if _, ok := got[expectedID]; !ok {
			t.Errorf("%s missing from group", expectedID)
		}
	}
}

func TestLoadExecutionGroup_HandlesCycles(t *testing.T) {
	// A corrupted state where run A lists run B as its parent and
	// run B lists run A as its parent shouldn't loop forever. The
	// loader's `already loaded` check breaks the cycle.
	runsDir := t.TempDir()
	writeRunForTest(t, runsDir, "a", "b", nil)
	writeRunForTest(t, runsDir, "b", "a", nil)

	done := make(chan map[string]*RunState, 1)
	go func() {
		done <- LoadExecutionGroup(runsDir, "a")
	}()
	select {
	case got := <-done:
		// Both should be in the group; no panic, no hang.
		if _, ok := got["a"]; !ok {
			t.Error("a missing")
		}
		if _, ok := got["b"]; !ok {
			t.Error("b missing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadExecutionGroup hung on cycle")
	}
}

func TestRunState_Totals_RoundTrip(t *testing.T) {
	cost := 0.0123
	original := &RunState{
		ID:         "r",
		WorkflowID: "w",
		Status:     "completed",
		StartedAt:  time.Now().UTC(),
		Inputs:     map[string]any{},
		Nodes:      map[string]*NodeState{},
		Totals: &NodeTotals{
			DurationMs: 1234,
			Tokens:     TokenBreakdown{Input: 100, Output: 50, Total: 150},
			CostUSD:    &cost,
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := SaveState(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Totals == nil {
		t.Fatal("loaded.Totals is nil after round trip")
	}
	if loaded.Totals.DurationMs != 1234 {
		t.Errorf("DurationMs = %d, want 1234", loaded.Totals.DurationMs)
	}
	if loaded.Totals.Tokens.Total != 150 {
		t.Errorf("Tokens.Total = %d, want 150", loaded.Totals.Tokens.Total)
	}
	if loaded.Totals.CostUSD == nil || *loaded.Totals.CostUSD != cost {
		t.Errorf("CostUSD round-trip failed: %v", loaded.Totals.CostUSD)
	}
}

func TestRunState_Totals_LegacyAbsent(t *testing.T) {
	// Simulate a legacy state.json without a "totals" field.
	legacy := `{"id":"r","workflow_id":"w","status":"completed","started_at":"2024-01-01T00:00:00Z","inputs":{},"nodes":{}}`
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Totals != nil {
		t.Errorf("legacy state.json should deserialize with Totals == nil, got %+v", loaded.Totals)
	}
}

func TestNodeStateSerializesDispatches(t *testing.T) {
	original := NodeState{
		ID:         "test-node",
		Status:     "pending",
		Dispatches: 7,
	}
	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped NodeState
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if roundTripped.Dispatches != 7 {
		t.Fatalf("expected Dispatches=7 after round-trip, got %d", roundTripped.Dispatches)
	}
}

func TestJoinNodeStateRoundtrip(t *testing.T) {
	rs := &RunState{
		ID:         "r1",
		WorkflowID: "w",
		Status:     "running",
		StartedAt:  time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
		Inputs:     map[string]any{},
		Nodes:      map[string]*NodeState{},
		JoinState: map[string]*JoinNodeState{
			"j": {
				Arrived: []string{"a", "b"},
				Passes: map[int]map[string]any{
					2: {"k1": "v1"},
					5: {"k2": float64(3)},
				},
			},
		},
	}
	buf, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back RunState
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	j, ok := back.JoinState["j"]
	if !ok || j == nil {
		t.Fatalf("JoinState[j] missing after roundtrip")
	}
	if len(j.Arrived) != 2 || j.Arrived[0] != "a" {
		t.Errorf("Arrived=%v want [a b]", j.Arrived)
	}
	if got := j.Passes[2]["k1"]; got != "v1" {
		t.Errorf("Passes[2][k1]=%v want v1", got)
	}
	if got := j.Passes[5]["k2"]; got != float64(3) {
		t.Errorf("Passes[5][k2]=%v (%T) want float64(3)", got, got)
	}
}

func TestSaveState_FileMode0600(t *testing.T) {
	// Regression guard: state.json holds potentially-sensitive workflow
	// inputs/outputs and must stay restrictive. External tooling reads
	// run state via the HTTP API, not via direct filesystem access.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	rs := NewRunState("run-x", "wf", nil)
	if err := SaveState(path, rs); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Fatalf("state.json mode = %o, want 0600", mode)
	}
}

func TestRunStateHasUnresolvedFailureJSONRoundTrip(t *testing.T) {
	rs := &RunState{
		ID:                   "test-run",
		WorkflowID:           "wf",
		Status:               "completed",
		Inputs:               map[string]any{},
		Nodes:                map[string]*NodeState{},
		HasUnresolvedFailure: true,
	}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RunState
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.HasUnresolvedFailure {
		t.Fatalf("HasUnresolvedFailure lost; got %v, want true; raw json: %s", back.HasUnresolvedFailure, data)
	}
}

func TestRunStateHasUnresolvedFailure_OmitemptyAbsentWhenFalse(t *testing.T) {
	rs := &RunState{
		ID:         "test-run",
		WorkflowID: "wf",
		Status:     "completed",
		Inputs:     map[string]any{},
		Nodes:      map[string]*NodeState{},
	}
	data, _ := json.Marshal(rs)
	if bytes.Contains(data, []byte("has_unresolved_failure")) {
		t.Fatalf("has_unresolved_failure should be omitted when false; got: %s", data)
	}
}

func TestNarrowToNode_CopiesHasUnresolvedFailure(t *testing.T) {
	rs := NewRunState("r", "wf", nil)
	rs.HasUnresolvedFailure = true
	rs.WithNode("n1", func(n *NodeState) {
		n.Status = statusCompleted
		n.Decision = "pass"
	})

	narrow := rs.NarrowToNode("n1")

	if !narrow.HasUnresolvedFailure {
		t.Fatalf("NarrowToNode did not copy HasUnresolvedFailure: got false, want true")
	}
	// Verify that the narrowed run only contains the requested node.
	if _, ok := narrow.Nodes["n1"]; !ok {
		t.Errorf("narrow.Nodes missing n1")
	}

	// Also verify the false case: a run without the flag must not gain it.
	rs2 := NewRunState("r2", "wf", nil)
	rs2.WithNode("n2", func(n *NodeState) { n.Status = statusCompleted })
	narrow2 := rs2.NarrowToNode("n2")
	if narrow2.HasUnresolvedFailure {
		t.Fatalf("NarrowToNode should not set HasUnresolvedFailure when source is false")
	}
}
