package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRunNodeData_IncludesTimingAndArtifacts(t *testing.T) {
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"node_started","run_id":"r1","node_id":"n1","timestamp":"2026-04-20T12:00:00Z"}
{"type":"node_completed","run_id":"r1","node_id":"n1","timestamp":"2026-04-20T12:00:05Z","data":{"decision":"pass","artifacts":["out/a.json","out/b.log"]},"duration_ms":5000}
`
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	server := &Server{runsDir: tmp}

	data := server.extractRunNodeData("r1")
	if len(data) != 1 {
		t.Fatalf("expected 1 node; got %d", len(data))
	}
	nd := data[0]
	if nd.startedAt == nil || nd.finishedAt == nil {
		t.Fatalf("startedAt=%v finishedAt=%v", nd.startedAt, nd.finishedAt)
	}
	if nd.durationMs != 5000 {
		t.Errorf("durationMs = %d, want 5000", nd.durationMs)
	}
	if len(nd.artifacts) != 2 || !strings.Contains(strings.Join(nd.artifacts, ","), "out/a.json") {
		t.Errorf("artifacts = %v; want 2 including out/a.json", nd.artifacts)
	}
}

func TestBuildReportByRun_ForEachSpawnsAttachToOrchestrator(t *testing.T) {
	// When a ForEach orchestrator `orch` dispatches iterations of body `body`,
	// the resulting report should attribute the spawned child runs to the
	// orchestrator — not to a synthesized `body` entry. The body is a template,
	// not a runtime entity, and should not appear as its own row.
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "r1")
	childDir := filepath.Join(tmp, "c1")
	for _, d := range []string{parentDir, childDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	workflowYAML := `id: test_wf
name: Test
version: 1
nodes:
  - id: body
    kind: subworkflow
    workflow: child_wf
  - id: orch
    for_each:
      list: input.items
      item: it
      body: body
    decisions: [all_succeeded]
`
	if err := os.WriteFile(filepath.Join(parentDir, "workflow.yaml"), []byte(workflowYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	events := `{"type":"node_started","run_id":"r1","node_id":"orch","timestamp":"2026-04-20T12:00:00Z"}
{"type":"node_inputs_resolved","run_id":"r1","node_id":"body::0","timestamp":"2026-04-20T12:00:00.1Z"}
{"type":"subworkflow_started","run_id":"r1","node_id":"body::0","timestamp":"2026-04-20T12:00:00.2Z","data":{"child_run":"c1","child_workflow":"child_wf"}}
{"type":"subworkflow_completed","run_id":"r1","node_id":"body::0","timestamp":"2026-04-20T12:00:05Z","duration_ms":4800}
{"type":"node_completed","run_id":"r1","node_id":"orch","timestamp":"2026-04-20T12:00:05.1Z","data":{"decision":"all_succeeded"},"duration_ms":5100}
`
	if err := os.WriteFile(filepath.Join(parentDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "events.jsonl"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	server := &Server{runsDir: tmp}
	group := &ExecutionGroupSummary{
		Tree: []RunTreeNode{
			{
				Run: RunSummary{ID: "r1"},
				Children: []RunTreeNode{
					{Run: RunSummary{ID: "c1", WorkflowID: "child_wf"}},
				},
			},
		},
		Rows: []RunTreeRow{
			{Run: RunSummary{ID: "r1"}},
			{Run: RunSummary{ID: "c1", WorkflowID: "child_wf"}},
		},
	}

	report := server.BuildReportByRun(group)
	parentNodes := report["r1"].PreChildren

	var orch *ReportNode
	for i := range parentNodes {
		if parentNodes[i].Label == "orch" {
			orch = &parentNodes[i]
		}
		if parentNodes[i].Label == "body" {
			t.Errorf("report should not contain a row for ForEach body %q (template, not runtime entity); found: %+v", "body", parentNodes[i])
		}
	}
	if orch == nil {
		t.Fatalf("expected a ReportNode for orchestrator 'orch'; got labels: %v", nodeLabels(parentNodes))
	}
	if len(orch.SpawnedRuns) != 1 {
		t.Fatalf("expected orch.SpawnedRuns to contain the spawned child run (c1); got %d entries: %+v", len(orch.SpawnedRuns), orch.SpawnedRuns)
	}
	if orch.SpawnedRuns[0].Run.ID != "c1" {
		t.Errorf("expected spawned run c1; got %q", orch.SpawnedRuns[0].Run.ID)
	}
	if orch.Status != statusCompleted {
		t.Errorf("orch.Status = %q, want %q (orchestrator retains its own lifecycle events)", orch.Status, statusCompleted)
	}
}

func nodeLabels(nodes []ReportNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Label)
	}
	return out
}

func TestExtractCommunicateMessage(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "serf_format",
			text: `{"kind":"TOOL_CALL_START","data":{"tool_name":"communicate","arguments_json":"{\"message\":\"Completed the refactor\"}"}}`,
			want: "Completed the refactor",
		},
		{
			name: "serf_non_communicate",
			text: `{"kind":"TOOL_CALL_START","data":{"tool_name":"write_file","arguments_json":"{\"path\":\"/tmp/x\"}"}}`,
			want: "",
		},
		{
			name: "anthropic_format",
			text: `{"content":[{"type":"tool_use","name":"communicate","input":{"message":"Fixed the bug"}}]}`,
			want: "Fixed the bug",
		},
		{
			name: "toil_envelope",
			text: `{"message":{"content":[{"type":"tool_use","name":"communicate","input":{"message":"All tests pass"}}]}}`,
			want: "All tests pass",
		},
		{
			name: "plain_text",
			text: `not json at all`,
			want: "",
		},
		{
			name: "other_json",
			text: `{"foo":"bar"}`,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractCommunicateMessage(tc.text)
			if got != tc.want {
				t.Errorf("extractCommunicateMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}
