package definitions

import (
	"fmt"
	"regexp"
	"strings"
)

// Runner type names — the `type:` values declared on runner definitions.
const (
	runnerSerf   = "serf"
	runnerClaude = "claude"
	runnerCodex  = "codex"
)

// modelIdentityEnvByRunnerType lists env var names that change the LLM
// model identity for a given runner type. Setting any of these via
// runner_env on a node that resumes a session would pair the resumed
// transcript with a different model than originally wrote it.
var modelIdentityEnvByRunnerType = map[string]map[string]bool{
	runnerSerf:   {"SERF_MODEL": true, "SERF_PROVIDER": true},
	runnerClaude: {"ANTHROPIC_MODEL": true, "CLAUDE_MODEL": true},
	runnerCodex:  {"CODEX_MODEL": true, "OPENAI_MODEL": true},
}

// resumableRunnerTypes is the set of runner types whose subprocesses can
// resume a prior session. Shell scripts and human prompts have no session
// concept.
var resumableRunnerTypes = map[string]bool{
	runnerSerf:   true,
	runnerClaude: true,
	runnerCodex:  true,
}

// nodeSessionIDExpr matches "${node.<id>.session_id}" and captures the
// referenced node id.
var nodeSessionIDExpr = regexp.MustCompile(`^\s*\$\{\s*node\.([A-Za-z0-9_-]+)\.session_id\s*\}\s*$`)

// validateResumeIntegrity rejects YAML where a node sets session_id in
// ways incoherent with the underlying runner behavior. See
// docs/superpowers/specs/2026-05-12-surgeon-as-judge-design.md.
func validateResumeIntegrity(r *ValidationResult, b *Bundle) {
	for wfID, wf := range b.Workflows {
		nodeByID := make(map[string]*Node, len(wf.Nodes))
		for j := range wf.Nodes {
			nodeByID[wf.Nodes[j].ID] = &wf.Nodes[j]
		}
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if strings.TrimSpace(n.SessionID) == "" {
				continue
			}

			runner, runnerOK := b.Runners[n.Runner]
			runnerType := ""
			if runnerOK && runner != nil {
				runnerType = runner.Type
			}

			// Rule 1: runner must support resume.
			if runnerOK && !resumableRunnerTypes[runnerType] {
				r.add(SeverityError, n.ID, -1, fmt.Sprintf(
					"workflow %q: node %q sets session_id but runner %q (type %q) does not support resume",
					wfID, n.ID, n.Runner, runnerType))
				continue
			}

			// Rule 4: context: fresh is incoherent with session_id.
			if strings.EqualFold(strings.TrimSpace(n.Context), "fresh") {
				r.add(SeverityError, n.ID, -1, fmt.Sprintf(
					"workflow %q: node %q sets session_id and context: fresh together; context: fresh discards session_id",
					wfID, n.ID))
			}

			// Rule 2: no model-identity env override.
			if len(n.RunnerEnv) > 0 && runnerType != "" {
				banned := modelIdentityEnvByRunnerType[runnerType]
				for envName := range n.RunnerEnv {
					if banned[envName] {
						r.add(SeverityError, n.ID, -1, fmt.Sprintf(
							"workflow %q: node %q resumes a session but overrides %s; resume must use the model that created the session",
							wfID, n.ID, envName))
					}
				}
			}

			// Rule 3: same-runner check when session_id references a sibling node.
			if m := nodeSessionIDExpr.FindStringSubmatch(n.SessionID); m != nil {
				sourceID := m[1]
				source := nodeByID[sourceID]
				if source != nil && source.Runner != n.Runner {
					r.add(SeverityError, n.ID, -1, fmt.Sprintf(
						"workflow %q: node %q resumes session of node %q but uses runner %q while %q uses %q; runner must match",
						wfID, n.ID, sourceID, n.Runner, sourceID, source.Runner))
				}
			}

			// Rule 5: prompt_on_resume guard.
			if !n.PromptOnResume && strings.TrimSpace(n.Prompt) != "" {
				hasEdgePrompt := false
				for _, e := range wf.Edges {
					if e.To == n.ID && strings.TrimSpace(e.Prompt) != "" {
						hasEdgePrompt = true
						break
					}
				}
				if !hasEdgePrompt {
					r.add(SeverityError, n.ID, -1, fmt.Sprintf(
						"workflow %q: node %q sets session_id with a prompt but neither prompt_on_resume: true nor an incoming edge prompt; prompt would be silently dropped on resume",
						wfID, n.ID))
				}
			}
		}
	}
}
