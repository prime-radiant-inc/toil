package engine

import (
	"strings"
	"testing"
)

func TestBuildRepairPrompt_NoStaleCommunicateReferences(t *testing.T) {
	prompt := buildRepairPrompt(
		[]string{"components_defined"},
		[]string{`field "decision" is required`},
	)
	// communicate is a serf-specific tool. The repair prompt should be
	// runner-neutral so it works for codex and other runners too.
	if strings.Contains(prompt, "communicate") {
		t.Fatalf("prompt contains runner-specific communicate reference:\n%s", prompt)
	}
}

func TestBuildRepairPrompt_NoHardcodedToolName(t *testing.T) {
	prompt := buildRepairPrompt(
		[]string{"approved", "rejected"},
		[]string{"invalid json output"},
	)
	if strings.Contains(prompt, "communicate") {
		t.Fatalf("repair prompt should not reference runner-specific tool names:\n%s", prompt)
	}
}

func TestBuildRepairPrompt_IncludesAllowedDecisions(t *testing.T) {
	prompt := buildRepairPrompt(
		[]string{"approved", "rejected"},
		[]string{`field "decision" is required`},
	)
	if !strings.Contains(prompt, "approved, rejected") {
		t.Fatalf("expected prompt to list allowed decisions, got:\n%s", prompt)
	}
}

func TestBuildRepairPrompt_IncludesValidationErrors(t *testing.T) {
	prompt := buildRepairPrompt(
		[]string{"done"},
		[]string{`field "message" is required and must be a non-empty string`},
	)
	if !strings.Contains(prompt, `field "message" is required`) {
		t.Fatalf("expected validation error text in prompt, got:\n%s", prompt)
	}
}

func TestBuildIncompleteWorkPrompt_AllowsToolCalls(t *testing.T) {
	prompt := buildIncompleteWorkPrompt(
		[]string{"correct_failure", "wrong_failure"},
		[]string{"json output block not found"},
	)
	// The whole point of this prompt: tell the agent it MAY call tools.
	// It must NOT contain the harsh "Do NOT call any tools" instruction.
	if strings.Contains(prompt, "Do NOT call any tools") {
		t.Fatalf("incomplete-work prompt should NOT forbid tool calls, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "Do NOT do more analysis") {
		t.Fatalf("incomplete-work prompt should NOT forbid further analysis, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "may call tools") {
		t.Fatalf("expected explicit permission to call tools, got:\n%s", prompt)
	}
}

func TestBuildIncompleteWorkPrompt_IncludesAllowedDecisions(t *testing.T) {
	prompt := buildIncompleteWorkPrompt(
		[]string{"approved", "rejected"},
		nil,
	)
	if !strings.Contains(prompt, "approved, rejected") {
		t.Fatalf("expected allowed decisions listed, got:\n%s", prompt)
	}
}

func TestBuildIncompleteWorkPrompt_NoCommunicateReference(t *testing.T) {
	prompt := buildIncompleteWorkPrompt(
		[]string{"done"},
		nil,
	)
	if strings.Contains(prompt, "communicate") {
		t.Fatalf("incomplete-work prompt should be runner-neutral, got:\n%s", prompt)
	}
}
