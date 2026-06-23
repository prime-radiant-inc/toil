package definitions

import (
	"strings"
	"testing"
)

func bundleWithRunners() *Bundle {
	return &Bundle{
		Workflows: map[string]*Workflow{},
		Runners: map[string]*Runner{
			"serf":   {ID: "serf", Type: "serf"},
			"claude": {ID: "claude", Type: "claude"},
			"shell":  {ID: "shell", Type: "shell"},
			"human":  {ID: "human", Type: "human"},
		},
	}
}

func TestValidateResumeIntegrity_HappyPathWithExpression(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:             "judge",
				Kind:           "role",
				Runner:         "serf",
				SessionID:      "${node.src.session_id}",
				PromptOnResume: true,
				Prompt:         "adjudicate",
			},
		},
	}
	r := ValidateBundle(b)
	if r.HasErrors() {
		t.Fatalf("happy path should not error: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_RejectsShellRunner(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{
				ID:        "bad",
				Kind:      "role",
				Runner:    "shell",
				SessionID: "${node.foo.session_id}",
			},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for shell runner with session_id")
	}
	if !strings.Contains(r.Error(), "does not support resume") {
		t.Fatalf("expected 'does not support resume' error, got: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_RejectsHumanRunner(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{
				ID:        "bad",
				Kind:      "role",
				Runner:    "human",
				SessionID: "${input.x}",
			},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for human runner with session_id")
	}
}

func TestValidateResumeIntegrity_RejectsModelIdentityOverride(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:             "bad",
				Kind:           "role",
				Runner:         "serf",
				SessionID:      "${node.src.session_id}",
				PromptOnResume: true,
				RunnerEnv:      map[string]string{"SERF_MODEL": "gpt-5.4-mini"},
			},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for SERF_MODEL override on resume")
	}
	if !strings.Contains(r.Error(), "SERF_MODEL") {
		t.Fatalf("expected SERF_MODEL in error, got: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_AllowsNonIdentityEnvOverride(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:             "judge",
				Kind:           "role",
				Runner:         "serf",
				SessionID:      "${node.src.session_id}",
				PromptOnResume: true,
				RunnerEnv:      map[string]string{"SERF_REASONING_EFFORT": "high"},
			},
		},
	}
	r := ValidateBundle(b)
	if r.HasErrors() {
		t.Fatalf("non-identity env var should be allowed: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_RejectsRunnerMismatch(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:             "bad",
				Kind:           "role",
				Runner:         "claude",
				SessionID:      "${node.src.session_id}",
				PromptOnResume: true,
			},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for runner mismatch on resume")
	}
	if !strings.Contains(r.Error(), "runner") {
		t.Fatalf("expected 'runner' in error, got: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_RejectsContextFreshWithSessionID(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:             "bad",
				Kind:           "role",
				Runner:         "serf",
				SessionID:      "${node.src.session_id}",
				Context:        "fresh",
				PromptOnResume: true,
			},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for session_id with context: fresh")
	}
	if !strings.Contains(strings.ToLower(r.Error()), "fresh") {
		t.Fatalf("expected 'fresh' in error, got: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_RejectsMissingPromptOnResumeNoEdgePrompts(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:        "judge",
				Kind:      "role",
				Runner:    "serf",
				SessionID: "${node.src.session_id}",
				Prompt:    "adjudicate",
			},
		},
		Edges: []Edge{
			{From: "src", To: "judge"},
		},
	}
	r := ValidateBundle(b)
	if !r.HasErrors() {
		t.Fatal("expected error for missing prompt_on_resume with no edge prompt")
	}
	if !strings.Contains(r.Error(), "prompt_on_resume") {
		t.Fatalf("expected 'prompt_on_resume' in error, got: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_AllowsEdgePromptInsteadOfPromptOnResume(t *testing.T) {
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Nodes: []Node{
			{ID: "src", Kind: "role", Runner: "serf"},
			{
				ID:        "judge",
				Kind:      "role",
				Runner:    "serf",
				SessionID: "${node.src.session_id}",
				Prompt:    "adjudicate",
			},
		},
		Edges: []Edge{
			{From: "src", To: "judge", Prompt: "follow up question"},
		},
	}
	r := ValidateBundle(b)
	if r.HasErrors() {
		t.Fatalf("edge prompt should satisfy resume-prompt requirement: %s", r.Error())
	}
}

func TestValidateResumeIntegrity_SkipsInputExpressionRunnerMatchCheck(t *testing.T) {
	// ${input.X} cannot be resolved at load time; validator should skip the
	// runner-mismatch check and let runtime surface mismatches.
	b := bundleWithRunners()
	b.Workflows["w"] = &Workflow{
		ID: "w",
		Inputs: map[string]string{
			"upstream_session": "",
		},
		Nodes: []Node{
			{
				ID:             "judge",
				Kind:           "role",
				Runner:         "claude",
				SessionID:      "${input.upstream_session}",
				PromptOnResume: true,
			},
		},
	}
	r := ValidateBundle(b)
	if r.HasErrors() {
		t.Fatalf("input-form session_id should skip runner-match check: %s", r.Error())
	}
}
