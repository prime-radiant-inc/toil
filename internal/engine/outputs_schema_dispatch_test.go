package engine

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/runners"
)

// TestOutputsSchemaPropagatedToRunnerRequest verifies that a node declaring
// `outputs_schema:` results in the runner receiving a fully-built envelope
// schema in Request.OutputSchemaJSON, with the author schema nested under
// `data` and the node's decisions as `decision.enum`.
func TestOutputsSchemaPropagatedToRunnerRequest(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-outputs-schema-dispatch",
		Name:    "Outputs Schema Dispatch",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:        "agent",
				Kind:      "role",
				Runner:    "test-runner",
				Decisions: definitions.StringDecisions("done"),
				OutputsSchema: map[string]any{
					"type":     "object",
					"required": []any{"plan"},
					"properties": map[string]any{
						"plan": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	setupRunForResume(t, runsDir, "run-outputs-schema-dispatch", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"done","message":"ok","data":{"plan":"p"},"artifacts":[]}`},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := eng.ResumeRun(context.Background(), "run-outputs-schema-dispatch"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.requests) == 0 {
		t.Fatal("expected at least one dispatched request")
	}
	req := runner.requests[0]
	if len(req.OutputSchemaJSON) == 0 {
		t.Fatal("expected OutputSchemaJSON to be populated")
	}

	var envelope map[string]any
	if err := json.Unmarshal(req.OutputSchemaJSON, &envelope); err != nil {
		t.Fatalf("OutputSchemaJSON is not valid JSON: %v", err)
	}
	props, _ := envelope["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	enum, _ := decision["enum"].([]any)
	if len(enum) != 1 || enum[0] != "done" {
		t.Fatalf("expected decision.enum=[done], got %v", enum)
	}
	data, _ := props["data"].(map[string]any)
	if data["type"] != "object" {
		t.Fatalf("expected data.type=object, got %v", data["type"])
	}
	dataProps, _ := data["properties"].(map[string]any)
	if _, ok := dataProps["plan"]; !ok {
		t.Fatalf("expected data.properties.plan to be propagated, got: %v", dataProps)
	}
}

// TestOutputsSchemaNotSetWhenUnconfigured verifies that a node with Decisions
// but no explicit OutputsSchema still has a populated OutputSchemaJSON — the
// envelope describes decision.enum with a permissive data schema. This
// confirms OutputSchemaJSON is built whenever there's anything to describe,
// not only when outputs_schema is set.
func TestOutputsSchemaNotSetWhenUnconfigured(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-decisions-only",
		Name:    "Decisions Only",
		Version: 1,
		Nodes: []definitions.Node{
			{
				ID:        "agent",
				Kind:      "role",
				Runner:    "test-runner",
				Decisions: definitions.StringDecisions("pass", "fail"),
			},
		},
	}
	setupRunForResume(t, runsDir, "run-decisions-only", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"pass","message":"ok","data":{},"artifacts":[]}`},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := eng.ResumeRun(context.Background(), "run-decisions-only"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.requests) == 0 {
		t.Fatal("expected at least one dispatched request")
	}
	req := runner.requests[0]
	if len(req.OutputSchemaJSON) == 0 {
		t.Fatal("expected OutputSchemaJSON to be populated when decisions are declared")
	}

	var envelope map[string]any
	if err := json.Unmarshal(req.OutputSchemaJSON, &envelope); err != nil {
		t.Fatalf("OutputSchemaJSON is not valid JSON: %v", err)
	}
	props, _ := envelope["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	enum, _ := decision["enum"].([]any)
	if len(enum) != 2 || enum[0] != "pass" || enum[1] != "fail" {
		t.Fatalf("expected decision.enum=[pass,fail], got %v", enum)
	}
	data, _ := props["data"].(map[string]any)
	if data["type"] != "object" {
		t.Fatalf("expected permissive data.type=object, got %v", data["type"])
	}
	if _, hasProps := data["properties"]; hasProps {
		t.Fatalf("expected data to have no properties when outputs_schema is unset, got %v", data)
	}
}

// TestOutputsSchemaNilWhenNothingDeclared verifies no schema is sent when
// the node has neither decisions nor outputs_schema.
func TestOutputsSchemaNilWhenNothingDeclared(t *testing.T) {
	runsDir := t.TempDir()
	workflow := &definitions.Workflow{
		ID:      "wf-no-schema",
		Name:    "No Schema",
		Version: 1,
		Nodes: []definitions.Node{
			{ID: "agent", Kind: "human", Runner: "test-runner", Prompt: "x"},
		},
	}
	setupRunForResume(t, runsDir, "run-no-schema", workflow, nil)

	runner := &sequentialRunner{
		results: []runners.Result{
			{Output: `{"decision":"default","message":"ok","data":{},"artifacts":[]}`},
		},
	}
	registry := runners.NewRegistry()
	_ = registry.Register("test-runner", runner)

	eng := &Engine{
		Definitions: &definitions.Bundle{
			Workflows: map[string]*definitions.Workflow{workflow.ID: workflow},
			Runners:   map[string]*definitions.Runner{"test-runner": {ID: "test-runner", Type: "test"}},
		},
		RunnerRegistry: registry,
		RunsDir:        runsDir,
		EventStdout:    io.Discard,
	}

	if _, err := eng.ResumeRun(context.Background(), "run-no-schema"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.requests) == 0 {
		t.Fatal("expected at least one request")
	}
	if runner.requests[0].OutputSchemaJSON != nil {
		t.Fatalf("expected nil OutputSchemaJSON when no decisions/schema declared, got %s", runner.requests[0].OutputSchemaJSON)
	}
}
