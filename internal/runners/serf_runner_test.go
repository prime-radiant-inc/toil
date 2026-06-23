package runners

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSerfBuildCommandPassesArgsAndWorkspace(t *testing.T) {
	runner := NewSerfRunner(Config{Command: "serf", Args: []string{"--verbose"}})

	cmd, prompt, err := runner.buildCommand(context.Background(), Request{
		Workspace: "/tmp/work",
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--verbose") {
		t.Fatalf("expected --verbose in args: %v", cmd.Args)
	}
	if cmd.Dir != "/tmp/work" {
		t.Fatalf("unexpected dir: %q", cmd.Dir)
	}
	if prompt != "hello" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}

func TestSerfBuildCommandResumeRequiresSessionID(t *testing.T) {
	runner := NewSerfRunner(Config{Command: "serf"})

	_, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: "/tmp/work",
		Prompt:    "hello",
		Resume:    true,
	})
	if err == nil {
		t.Fatal("expected error when resume is true without session id")
	}
}

func TestSerfBuildCommandResumeIncludesSessionID(t *testing.T) {
	runner := NewSerfRunner(Config{Command: "serf"})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: "/tmp/work",
		Prompt:    "hello",
		Resume:    true,
		SessionID: "01HABCDE",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--resume 01HABCDE") {
		t.Fatalf("expected resume args, got: %v", cmd.Args)
	}
}

func TestParseSerfSessionID(t *testing.T) {
	line := `{"kind":"SESSION_START","session_id":"01HSESSION"}`
	if got := parseSerfSessionID(line); got != "01HSESSION" {
		t.Fatalf("unexpected session id: %q", got)
	}
	if got := parseSerfSessionID(`{"kind":"WARNING","data":{"message":"x"}}`); got != "" {
		t.Fatalf("expected empty session id, got: %q", got)
	}
	if got := parseSerfSessionID("not-json"); got != "" {
		t.Fatalf("expected empty session id for invalid json, got: %q", got)
	}
}

func TestParseSerfSessionID_EmptyKind(t *testing.T) {
	// session_id present but kind is empty — should return empty.
	if got := parseSerfSessionID(`{"kind":"","session_id":"01HSESSION"}`); got != "" {
		t.Fatalf("expected empty for empty kind, got: %q", got)
	}
}

func TestParseSerfSessionID_EmptySessionID(t *testing.T) {
	if got := parseSerfSessionID(`{"kind":"SESSION_START","session_id":""}`); got != "" {
		t.Fatalf("expected empty for empty session_id, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_NonToolCallEvent(t *testing.T) {
	if got := parseSerfCommunicateResultOutput(`{"kind":"SESSION_START"}`); got != "" {
		t.Fatalf("expected empty for non-TOOL_CALL_START, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_NonCommunicateTool(t *testing.T) {
	evt := `{"kind":"TOOL_CALL_START","data":{"tool_name":"bash","arguments_json":"{}"}}`
	if got := parseSerfCommunicateResultOutput(evt); got != "" {
		t.Fatalf("expected empty for non-communicate tool, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_EmptyArgumentsJSON(t *testing.T) {
	evt := `{"kind":"TOOL_CALL_START","data":{"tool_name":"communicate","arguments_json":""}}`
	if got := parseSerfCommunicateResultOutput(evt); got != "" {
		t.Fatalf("expected empty for empty arguments, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_NullOutput(t *testing.T) {
	evt := `{"kind":"TOOL_CALL_START","data":{"tool_name":"communicate","arguments_json":"{\"message\":\"hi\",\"output\":null}"}}`
	if got := parseSerfCommunicateResultOutput(evt); got != "" {
		t.Fatalf("expected empty for null output, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_InvalidJSON(t *testing.T) {
	if got := parseSerfCommunicateResultOutput("not json"); got != "" {
		t.Fatalf("expected empty for invalid JSON, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_InvalidDataJSON(t *testing.T) {
	evt := `{"kind":"TOOL_CALL_START","data":"not an object"}`
	if got := parseSerfCommunicateResultOutput(evt); got != "" {
		t.Fatalf("expected empty for invalid data, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_InvalidArgumentsJSON(t *testing.T) {
	evt := `{"kind":"TOOL_CALL_START","data":{"tool_name":"communicate","arguments_json":"not json"}}`
	if got := parseSerfCommunicateResultOutput(evt); got != "" {
		t.Fatalf("expected empty for invalid arguments JSON, got: %q", got)
	}
}

func TestParseSerfCommunicateResultOutput_PrefersMessageJSON(t *testing.T) {
	messageOutput := `{"decision":"components_defined","message":"ok","data":{"components":[{"id":"c1"}]},"artifacts":[]}`

	args := map[string]any{
		"action":  "result",
		"message": messageOutput,
		"output": map[string]any{
			"decision":      "components_defined",
			"message":       "wrong place",
			"data":          map[string]any{},
			"artifacts":     []any{},
			"extra_ignored": "x",
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}

	evt := map[string]any{
		"kind":       "TOOL_CALL_START",
		"session_id": "01HSESSION",
		"data": map[string]any{
			"tool_name":      "communicate",
			"call_id":        "call_1",
			"arguments_json": string(argsJSON),
		},
	}
	lineJSON, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	got := parseSerfCommunicateResultOutput(string(lineJSON))
	if got != messageOutput {
		t.Fatalf("parseSerfCommunicateResultOutput()=%q, want %q", got, messageOutput)
	}
}

func TestParseSerfCommunicateResultOutput_NoActionField(t *testing.T) {
	// Serf removed the "action" parameter from communicate on Feb 26 2026.
	// Models now send communicate with just {message, output} and no action field.
	args := map[string]any{
		"message": "Returning required JSON output.",
		"output": map[string]any{
			"message":   "Implementation complete",
			"data":      map[string]any{},
			"artifacts": []any{},
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	evt := map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"arguments_json": string(argsJSON),
		},
	}
	lineJSON, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	got := parseSerfCommunicateResultOutput(string(lineJSON))
	if got == "" {
		t.Fatalf("expected non-empty output for communicate without action field")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", got, err)
	}
	if parsed["message"] != "Implementation complete" {
		t.Fatalf("message=%v, want %q", parsed["message"], "Implementation complete")
	}
}

func TestSerfBuildCommandExpandsArgsFromEnv(t *testing.T) {
	runner := NewSerfRunner(Config{
		Command: "serf",
		Args:    []string{"--agent", "${SERF_AGENT:-worker}", "--reasoning-effort", "${SERF_REASONING_EFFORT:-low}"},
	})

	cmd, _, err := runner.buildCommand(context.Background(), Request{
		Workspace: "/tmp/work",
		Prompt:    "hello",
		Env:       map[string]string{"SERF_REASONING_EFFORT": "low"},
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--reasoning-effort low") {
		t.Fatalf("expected --reasoning-effort low from env override, got: %v", cmd.Args)
	}
	if !strings.Contains(args, "--agent worker") {
		t.Fatalf("expected --agent worker from default, got: %v", cmd.Args)
	}
}

func TestParseSerfCommunicateResultOutput_FallsBackToOutputObject(t *testing.T) {
	args := map[string]any{
		"action":  "result",
		"message": "human summary",
		"output": map[string]any{
			"decision":  "components_defined",
			"message":   "ok",
			"data":      map[string]any{"components": []any{map[string]any{"id": "c1"}}},
			"artifacts": []any{},
		},
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	evt := map[string]any{
		"kind": "TOOL_CALL_START",
		"data": map[string]any{
			"tool_name":      "communicate",
			"arguments_json": string(argsJSON),
		},
	}
	lineJSON, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	got := parseSerfCommunicateResultOutput(string(lineJSON))
	if got == "" {
		t.Fatalf("expected non-empty output")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", got, err)
	}
	if parsed["decision"] != "components_defined" {
		t.Fatalf("decision=%v, want %q", parsed["decision"], "components_defined")
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not object: %#v", parsed["data"])
	}
	if _, ok := data["components"]; !ok {
		t.Fatalf("missing data.components: %#v", data)
	}
}
