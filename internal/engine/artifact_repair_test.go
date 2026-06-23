package engine

import (
	"strings"
	"testing"
)

func TestBuildArtifactRepairPrompt_NoStaleCommunicateReferences(t *testing.T) {
	prompt := buildArtifactRepairPrompt(
		[]string{"/workspace/missing.go"},
		"components_defined",
		[]string{"components_defined"},
	)
	// communicate(result) and communicate(status) are stale references to a
	// removed "action" parameter. Prompts should reference just "communicate".
	if strings.Contains(prompt, "communicate(result)") {
		t.Fatalf("prompt contains stale communicate(result) reference:\n%s", prompt)
	}
	if strings.Contains(prompt, "communicate(status)") {
		t.Fatalf("prompt contains stale communicate(status) reference:\n%s", prompt)
	}
}
