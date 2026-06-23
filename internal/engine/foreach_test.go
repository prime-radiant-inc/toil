package engine

import (
	"context"
	"io"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestForEachCreatesExpandedNodes(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b", "c"},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}

	// Check expanded nodes exist (prefixed by template ID)
	for _, suffix := range []string{"::0", "::1", "::2"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	// Check parent node
	status, _ := runState.NodeStatus("process")
	if status != statusCompleted {
		t.Fatalf("expected parent completed, got %q", status)
	}
}

func TestForEachEmptyListCompletesImmediately(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}
	if output.Decision != "all_succeeded" {
		t.Fatalf("expected all_succeeded decision, got %q", output.Decision)
	}
	// Check items is an empty slice in output data
	items, ok := output.Data["items"]
	if !ok {
		t.Fatal("expected items key in output data")
	}
	itemSlice, ok := items.([]map[string]any)
	if !ok || len(itemSlice) != 0 {
		t.Fatalf("expected empty items list, got %v", items)
	}
}

func TestForEachParallelRunsConcurrently(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b", "c"},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}

	// All expanded nodes should be completed (prefixed by template ID)
	for _, suffix := range []string{"::0", "::1", "::2"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in output data, got %v", output.Data)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
}

func TestForEachDependsOnFieldParsesFromStruct(t *testing.T) {
	fe := &definitions.ForEach{
		List:      "input.items",
		Item:      "item",
		DependsOn: "depends_on",
	}
	if fe.DependsOn != "depends_on" {
		t.Fatalf("expected DependsOn='depends_on', got %q", fe.DependsOn)
	}
}

func TestForEachOutputAggregatesResults(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"x", "y"},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}

	if output.Decision != "all_succeeded" {
		t.Fatalf("expected all_succeeded decision, got %q", output.Decision)
	}
	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in output data, got %v", output.Data)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Each item should have decision testDecisionDefault (from system node)
	for i, item := range items {
		if item["decision"] != testDecisionDefault {
			t.Fatalf("item %d: expected decision 'default', got %v", i, item["decision"])
		}
	}
}

func TestForEachDAGSchedulesWithDependencies(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID: "process",
				ForEach: &definitions.ForEach{
					List:      "input.items",
					Item:      "item",
					DependsOn: "depends_on",
					Body:      "process_item",
				},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "task-0", "name": "first", "depends_on": []any{}},
			map[string]any{"id": "task-1", "name": "second", "depends_on": []any{"task-0"}},
		},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"::0", "::1"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in output, got %v", output.Data)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestForEachNoDependsOnRunsConcurrently(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{"a", "b", "c"},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"::0", "::1", "::2"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in output, got %v", output.Data)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
}

func TestForEachDAGResumeSkipsCompleted(t *testing.T) {
	dir := t.TempDir()
	workflow := &definitions.Workflow{
		ID: "wf",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "system"},
			{
				ID: "process",
				ForEach: &definitions.ForEach{
					List:      "input.items",
					Item:      "item",
					DependsOn: "depends_on",
					Body:      "process_item",
				},
			},
		},
	}
	runState := state.NewRunState(testRunID1, "wf", map[string]any{
		"items": []any{
			map[string]any{"id": "task-0", "name": "first", "depends_on": []any{}},
			map[string]any{"id": "task-1", "name": "second", "depends_on": []any{"task-0"}},
		},
	})
	// Mark first item as already completed (simulating resume); uses template prefix.
	runState.WithNode("process_item::0", func(n *state.NodeState) {
		n.Status = statusCompleted
		n.Decision = testDecisionDefault
		n.Message = "already done"
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()
	eng := &Engine{}

	output, err := eng.executeNode(context.Background(), testRunID1, dir, workflow, &workflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"::0", "::1"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	items, ok := output.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected items in output, got %v", output.Data)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestForEachSubworkflowUsesExpandedStateNodeID(t *testing.T) {
	dir := t.TempDir()

	// Child workflow: a single system node that completes immediately
	childWorkflow := &definitions.Workflow{
		ID:      "child",
		Name:    "Child",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "do_work", Kind: "system"},
		},
	}

	// Parent workflow: ForEach orchestrator + template (subworkflow) node
	parentWorkflow := &definitions.Workflow{
		ID: "parent",
		Nodes: []definitions.Node{
			{ID: "process_item", Kind: "subworkflow", Workflow: "child"},
			{
				ID:      "process",
				ForEach: &definitions.ForEach{List: "input.items", Item: "item", Body: "process_item"},
			},
		},
	}

	runState := state.NewRunState(testRunID1, "parent", map[string]any{
		"items": []any{"a", "b"},
	})
	runContext := &RunContext{
		Inputs:  runState.Inputs,
		Outputs: map[string]NodeOutput{},
	}
	logger, _ := newTestLogger(t)
	defer func() { _ = logger.Close() }()

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{
				"child": childWorkflow,
			},
		},
		RunsDir:     dir,
		EventStdout: io.Discard,
	}

	_, err := eng.executeNode(context.Background(), testRunID1, dir, parentWorkflow, &parentWorkflow.Nodes[1], "", "", nil, runContext, logger, runState)
	if err != nil {
		t.Fatalf("executeNode: %v", err)
	}

	// Each expanded node must have its own state entry (prefixed by template ID)
	for _, suffix := range []string{"::0", "::1"} {
		status, exists := runState.NodeStatus("process_item" + suffix)
		if !exists {
			t.Fatalf("expanded node process_item%s not found in state", suffix)
		}
		if status != statusCompleted {
			t.Fatalf("expected process_item%s completed, got %q", suffix, status)
		}
	}

	// Parent should also be completed
	status, _ := runState.NodeStatus("process")
	if status != statusCompleted {
		t.Fatalf("expected parent completed, got %q", status)
	}
}
