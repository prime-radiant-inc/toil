package engine

import (
	"strings"
	"testing"
)

func TestBuildDAG_NoDependencies(t *testing.T) {
	items := []dagItem{
		{id: "a", index: 0},
		{id: "b", index: 1},
		{id: "c", index: 2},
	}
	d, err := buildDAG(items)
	if err != nil {
		t.Fatalf("buildDAG: %v", err)
	}
	ready := d.ready()
	if len(ready) != 3 {
		t.Fatalf("expected 3 ready items, got %d: %v", len(ready), ready)
	}
}

func TestBuildDAG_LinearChain(t *testing.T) {
	// b depends on a, c depends on b
	items := []dagItem{
		{id: "a", index: 0},
		{id: "b", index: 1, deps: []string{"a"}},
		{id: "c", index: 2, deps: []string{"b"}},
	}
	d, err := buildDAG(items)
	if err != nil {
		t.Fatalf("buildDAG: %v", err)
	}

	ready := d.ready()
	if len(ready) != 1 || ready[0] != 0 {
		t.Fatalf("expected only item 0 (a) ready, got %v", ready)
	}

	newlyReady := d.resolve(0)
	if len(newlyReady) != 1 || newlyReady[0] != 1 {
		t.Fatalf("expected resolve(a) to unblock item 1 (b), got %v", newlyReady)
	}

	newlyReady = d.resolve(1)
	if len(newlyReady) != 1 || newlyReady[0] != 2 {
		t.Fatalf("expected resolve(b) to unblock item 2 (c), got %v", newlyReady)
	}
}

func TestBuildDAG_Diamond(t *testing.T) {
	// b and c both depend on a; d depends on both b and c
	items := []dagItem{
		{id: "a", index: 0},
		{id: "b", index: 1, deps: []string{"a"}},
		{id: "c", index: 2, deps: []string{"a"}},
		{id: "d", index: 3, deps: []string{"b", "c"}},
	}
	d, err := buildDAG(items)
	if err != nil {
		t.Fatalf("buildDAG: %v", err)
	}

	ready := d.ready()
	if len(ready) != 1 || ready[0] != 0 {
		t.Fatalf("expected only item 0 (a) ready, got %v", ready)
	}

	newlyReady := d.resolve(0)
	if len(newlyReady) != 2 {
		t.Fatalf("expected resolve(a) to unblock 2 items (b,c), got %v", newlyReady)
	}

	// Resolve b — d still blocked on c
	newlyReady = d.resolve(1)
	if len(newlyReady) != 0 {
		t.Fatalf("expected resolve(b) to unblock nothing (d still needs c), got %v", newlyReady)
	}

	// Resolve c — now d is unblocked
	newlyReady = d.resolve(2)
	if len(newlyReady) != 1 || newlyReady[0] != 3 {
		t.Fatalf("expected resolve(c) to unblock item 3 (d), got %v", newlyReady)
	}
}

func TestBuildDAG_CycleDetected(t *testing.T) {
	// a→b→c→a
	items := []dagItem{
		{id: "a", index: 0, deps: []string{"c"}},
		{id: "b", index: 1, deps: []string{"a"}},
		{id: "c", index: 2, deps: []string{"b"}},
	}
	_, err := buildDAG(items)
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected error to mention 'cycle', got: %v", err)
	}
}

func TestBuildDAG_InvalidReference(t *testing.T) {
	items := []dagItem{
		{id: "a", index: 0, deps: []string{"nonexistent"}},
	}
	_, err := buildDAG(items)
	if err == nil {
		t.Fatal("expected error for invalid reference, got nil")
	}
}

func TestBuildDAG_DuplicateID(t *testing.T) {
	items := []dagItem{
		{id: "a", index: 0},
		{id: "a", index: 1},
	}
	_, err := buildDAG(items)
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected error to mention 'duplicate', got: %v", err)
	}
}

func TestBuildDAG_CycleErrorNamesItems(t *testing.T) {
	items := []dagItem{
		{id: "a", index: 0, deps: []string{"c"}},
		{id: "b", index: 1, deps: []string{"a"}},
		{id: "c", index: 2, deps: []string{"b"}},
	}
	_, err := buildDAG(items)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("expected cycle error to name item %q, got: %v", id, err)
		}
	}
}

func TestParseDAGItems_MissingID(t *testing.T) {
	items := []any{
		map[string]any{"name": "no id field", "depends_on": []any{}},
	}
	_, err := parseDAGItems(items, "depends_on")
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected error to mention 'missing', got: %v", err)
	}
}

func TestParseDAGItems_WrongType(t *testing.T) {
	items := []any{"not a map"}
	_, err := parseDAGItems(items, "depends_on")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
	if !strings.Contains(err.Error(), "expected map") {
		t.Fatalf("expected error to mention 'expected map', got: %v", err)
	}
}

func TestParseDAGItems_BadDepsType(t *testing.T) {
	items := []any{
		map[string]any{"id": "a", "depends_on": "not-an-array"},
	}
	_, err := parseDAGItems(items, "depends_on")
	if err == nil {
		t.Fatal("expected error for bad depends_on type")
	}
	if !strings.Contains(err.Error(), "expected string array") {
		t.Fatalf("expected error about string array, got: %v", err)
	}
}

func TestParseDAGItems_ValidItems(t *testing.T) {
	items := []any{
		map[string]any{"id": "task-0", "name": "first", "depends_on": []any{}},
		map[string]any{"id": "task-1", "name": "second", "depends_on": []any{"task-0"}},
	}
	dagItems, err := parseDAGItems(items, "depends_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(dagItems) != 2 {
		t.Fatalf("expected 2 items, got %d", len(dagItems))
	}
	if dagItems[0].id != "task-0" || len(dagItems[0].deps) != 0 {
		t.Fatalf("item 0: got id=%q deps=%v", dagItems[0].id, dagItems[0].deps)
	}
	if dagItems[1].id != "task-1" || len(dagItems[1].deps) != 1 || dagItems[1].deps[0] != "task-0" {
		t.Fatalf("item 1: got id=%q deps=%v", dagItems[1].id, dagItems[1].deps)
	}
}
