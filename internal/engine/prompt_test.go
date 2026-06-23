package engine

import (
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestExpandInputRefs(t *testing.T) {
	inputs := map[string]any{
		"project_dir":  "/path/to/project",
		"upstream_dir": "/tmp/upstream",
		"goal":         "port the SDK",
	}

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "single ref",
			text: "Clone ${input.upstream_dir} and check out",
			want: "Clone /tmp/upstream and check out",
		},
		{
			name: "multiple refs",
			text: "Port from ${input.upstream_dir} to ${input.project_dir}",
			want: "Port from /tmp/upstream to /path/to/project",
		},
		{
			name: "no refs",
			text: "No references here",
			want: "No references here",
		},
		{
			name: "unresolved ref left as is",
			text: "Unknown ${input.nonexistent} stays",
			want: "Unknown ${input.nonexistent} stays",
		},
		{
			name: "empty text",
			text: "",
			want: "",
		},
		{
			name: "nil inputs",
			text: "${input.goal}",
			want: "${input.goal}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := inputs
			if tt.name == "nil inputs" {
				in = nil
			}
			got := expandInputRefs(tt.text, in)
			if got != tt.want {
				t.Errorf("expandInputRefs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComposeQuestionExpandsInputs(t *testing.T) {
	inputs := map[string]any{
		"upstream_dir": "/tmp/upstream",
	}
	role := "Check ${input.upstream_dir} for changes."
	node := "Use ${input.upstream_dir}/src as source."

	q, err := ComposeQuestion(role, node, inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := "/tmp/upstream"; !contains(q, got) {
		t.Errorf("expected prompt to contain %q, got:\n%s", got, q)
	}
	if contains(q, "${input.upstream_dir}") {
		t.Error("expected ${input.upstream_dir} to be expanded")
	}
}

func TestComposeQuestionWithInputViews_ExpansionWithoutDisplayInputs(t *testing.T) {
	interpolationInputs := map[string]any{
		"project_dir": "/workspace/demo",
	}
	displayInputs := map[string]any{}

	question, err := ComposeQuestionWithInputViews(
		"Use ${input.project_dir}",
		"Read files in ${input.project_dir}",
		interpolationInputs,
		displayInputs,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(question, "/workspace/demo") {
		t.Fatalf("expected expanded project path in prompt, got:\n%s", question)
	}
	if contains(question, "Inputs:\n```json") {
		t.Fatalf("expected no rendered Inputs block when display inputs are empty, got:\n%s", question)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestComposePrompt_NoStaleCommunicateReferences(t *testing.T) {
	prompt, err := ComposePrompt("role", "do the thing", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// communicate(result) and communicate(status) are stale references to a
	// removed "action" parameter. Prompts should reference just "communicate".
	if searchString(prompt, "communicate(result)") {
		t.Fatalf("prompt contains stale communicate(result) reference:\n%s", prompt)
	}
	if searchString(prompt, "communicate(status)") {
		t.Fatalf("prompt contains stale communicate(status) reference:\n%s", prompt)
	}
}

func TestComposeQuestion_DecisionDescriptions(t *testing.T) {
	decisions := definitions.DecisionList{
		{ID: "pass", Description: "All tests pass with zero failures"},
		{ID: "fail", Description: "One or more tests fail"},
	}
	q, err := ComposeQuestion("role", "verify", nil, decisions)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(q, "pass") || !contains(q, "fail") {
		t.Fatalf("expected decision IDs in prompt, got:\n%s", q)
	}
	if !contains(q, "All tests pass with zero failures") {
		t.Fatalf("expected decision description in prompt, got:\n%s", q)
	}
	if !contains(q, "One or more tests fail") {
		t.Fatalf("expected decision description in prompt, got:\n%s", q)
	}
}

func TestComposeQuestion_DecisionsWithoutDescriptions(t *testing.T) {
	decisions := definitions.DecisionList{
		{ID: "pass"},
		{ID: "fail"},
	}
	q, err := ComposeQuestion("role", "verify", nil, decisions)
	if err != nil {
		t.Fatal(err)
	}
	// Should render the compact format when no descriptions present
	if !contains(q, "Allowed decisions: pass, fail") {
		t.Fatalf("expected compact decision list, got:\n%s", q)
	}
}

func TestSelectPrompts(t *testing.T) {
	tests := []struct {
		name                    string
		firstRun                bool
		rolePrompt              string
		nodePrompt              string
		edgePrompt              string
		allowNodePromptOnResume bool
		wantRole                string
		wantNode                string
	}{
		{
			name:                    "first run uses role and node prompts",
			firstRun:                true,
			rolePrompt:              "role",
			nodePrompt:              "node",
			edgePrompt:              "edge",
			allowNodePromptOnResume: false,
			wantRole:                "role",
			wantNode:                "node",
		},
		{
			name:                    "resume with edge prompt keeps role prompt",
			firstRun:                false,
			rolePrompt:              "role",
			nodePrompt:              "node",
			edgePrompt:              "edge",
			allowNodePromptOnResume: false,
			wantRole:                "role",
			wantNode:                "edge",
		},
		{
			name:                    "resume with node prompt when allowed",
			firstRun:                false,
			rolePrompt:              "role",
			nodePrompt:              "node",
			edgePrompt:              "",
			allowNodePromptOnResume: true,
			wantRole:                "",
			wantNode:                "node",
		},
		{
			name:                    "resume without edge or node fallback",
			firstRun:                false,
			rolePrompt:              "role",
			nodePrompt:              "node",
			edgePrompt:              "",
			allowNodePromptOnResume: false,
			wantRole:                "",
			wantNode:                "",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			role, node := selectPrompts(
				testCase.firstRun,
				testCase.rolePrompt,
				testCase.nodePrompt,
				testCase.edgePrompt,
				testCase.allowNodePromptOnResume,
			)
			if role != testCase.wantRole || node != testCase.wantNode {
				t.Fatalf("selectPrompts() = (%q, %q), want (%q, %q)", role, node, testCase.wantRole, testCase.wantNode)
			}
		})
	}
}

func TestSelectPrompts_PromptOnResumeWiring(t *testing.T) {
	// Verify that a role node with PromptOnResume=true gets its node prompt
	// on re-dispatch when no edge prompt is present.
	// This tests the wiring, not selectPrompts itself (already covered).
	node := &definitions.Node{
		ID:             "tester",
		Kind:           "role",
		Prompt:         "Run all tests",
		PromptOnResume: true,
	}

	// Simulate re-dispatch: firstRun=false, no edge prompt
	rolePrompt, nodePrompt := selectPrompts(false, "role instructions", node.Prompt, "", node.PromptOnResume)

	if nodePrompt != "Run all tests" {
		t.Fatalf("expected node prompt on resume, got %q", nodePrompt)
	}
	if rolePrompt != "" {
		t.Fatalf("expected empty role prompt on resume without edge, got %q", rolePrompt)
	}

	// With edge prompt, edge wins regardless of PromptOnResume
	rolePrompt, nodePrompt = selectPrompts(false, "role instructions", node.Prompt, "edge context", node.PromptOnResume)
	if nodePrompt != "edge context" {
		t.Fatalf("expected edge prompt to win, got %q", nodePrompt)
	}
	if rolePrompt != "role instructions" {
		t.Fatalf("expected role prompt preserved with edge, got %q", rolePrompt)
	}
}
