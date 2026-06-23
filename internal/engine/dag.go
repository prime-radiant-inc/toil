package engine

import (
	"fmt"
	"strings"
)

// dagItem is a single node in the dependency graph. index is its position
// in the original items slice; deps lists IDs of items it depends on.
type dagItem struct {
	id    string
	index int
	deps  []string
}

// dag is a directed acyclic graph for scheduling items with dependencies.
type dag struct {
	items    []dagItem
	byID     map[string]int   // item ID → index into items slice
	children map[string][]int // item ID → indices of dependent items
	inDegree []int
}

// buildDAG constructs a dag from the given items, validating for duplicate IDs,
// missing dependency references, self-dependencies, and cycles.
func buildDAG(items []dagItem) (*dag, error) {
	d := &dag{
		items:    items,
		byID:     make(map[string]int, len(items)),
		children: make(map[string][]int, len(items)),
		inDegree: make([]int, len(items)),
	}

	// Populate byID and detect duplicates.
	for i, item := range items {
		if _, exists := d.byID[item.id]; exists {
			return nil, fmt.Errorf("duplicate item ID %q", item.id)
		}
		d.byID[item.id] = i
	}

	// Build children adjacency list and compute in-degrees.
	for i, item := range items {
		for _, dep := range item.deps {
			if dep == item.id {
				return nil, fmt.Errorf("item %q depends on itself", item.id)
			}
			if _, exists := d.byID[dep]; !exists {
				return nil, fmt.Errorf("item %q depends on unknown item %q", item.id, dep)
			}
			d.children[dep] = append(d.children[dep], i)
			d.inDegree[i]++
		}
	}

	if err := d.detectCycle(); err != nil {
		return nil, err
	}

	return d, nil
}

// detectCycle uses Kahn's algorithm (topological sort) to detect cycles.
// If not all items can be sorted, the remaining items form the cycle.
func (d *dag) detectCycle() error {
	inDeg := make([]int, len(d.inDegree))
	copy(inDeg, d.inDegree)

	var queue []int
	for i, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	visited := 0
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		visited++
		for _, childIdx := range d.children[d.items[idx].id] {
			inDeg[childIdx]--
			if inDeg[childIdx] == 0 {
				queue = append(queue, childIdx)
			}
		}
	}

	if visited < len(d.items) {
		var cycleIDs []string
		for i, deg := range inDeg {
			if deg > 0 {
				cycleIDs = append(cycleIDs, d.items[i].id)
			}
		}
		return fmt.Errorf("dependency cycle detected among items: %s", strings.Join(cycleIDs, ", "))
	}
	return nil
}

// ready returns the indices of all items with no remaining dependencies.
func (d *dag) ready() []int {
	var result []int
	for i, deg := range d.inDegree {
		if deg == 0 {
			result = append(result, i)
		}
	}
	return result
}

// resolve marks the item at itemIndex complete, decrements the in-degree of
// all items that depend on it, and returns the indices of newly unblocked items.
func (d *dag) resolve(itemIndex int) []int {
	item := d.items[itemIndex]
	var unblocked []int
	for _, childIdx := range d.children[item.id] {
		d.inDegree[childIdx]--
		if d.inDegree[childIdx] == 0 {
			unblocked = append(unblocked, childIdx)
		}
	}
	// Mark resolved so it doesn't appear in ready() again.
	d.inDegree[itemIndex] = -1
	return unblocked
}

// parseDAGItems extracts dagItem structs from ForEach items using the
// specified dependency field name.
func parseDAGItems(items []any, dependsOnField string) ([]dagItem, error) {
	result := make([]dagItem, len(items))
	for i, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d: expected map, got %T", i, raw)
		}
		idVal, ok := m["id"]
		if !ok {
			return nil, fmt.Errorf("item %d: missing required 'id' field", i)
		}
		id, ok := idVal.(string)
		if !ok {
			return nil, fmt.Errorf("item %d: 'id' must be a string, got %T", i, idVal)
		}

		var deps []string
		if depsVal, exists := m[dependsOnField]; exists && depsVal != nil {
			depSlice, err := toStringSlice(depsVal)
			if err != nil {
				return nil, fmt.Errorf("item %q: %s field: %w", id, dependsOnField, err)
			}
			deps = depSlice
		}

		result[i] = dagItem{id: id, index: i, deps: deps}
	}
	return result, nil
}

// toStringSlice converts a []any of strings to []string.
func toStringSlice(val any) ([]string, error) {
	switch typed := val.(type) {
	case []any:
		result := make([]string, len(typed))
		for i, v := range typed {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("element %d: expected string, got %T", i, v)
			}
			result[i] = s
		}
		return result, nil
	case []string:
		return typed, nil
	default:
		return nil, fmt.Errorf("expected string array, got %T", val)
	}
}
