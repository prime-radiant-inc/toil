package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/approvals"
	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/state"
)

const (
	decisionApproved         = "approved"
	decisionClarified        = "clarified"
	decisionReadyForReview   = "ready_for_review"
	decisionChangesRequested = "changes_requested"
	decisionNeedsChanges     = "needs_changes"
	statusFailed             = "failed"
	statusPassed             = "passed"
	statusCompleted          = "completed"
	commentAutoApproval      = "eval auto-approval"
	edgeWhenDefault          = "default"
)

type Spec struct {
	ID          string                  `yaml:"id"`
	Name        string                  `yaml:"name"`
	WorkflowID  string                  `yaml:"workflow_id"`
	ProjectDir  string                  `yaml:"project_dir"`
	Inputs      map[string]any          `yaml:"inputs"`
	Verify      VerifySpec              `yaml:"verify"`
	AutoApprove bool                    `yaml:"auto_approve,omitempty"`
	Approvals   map[string]ApprovalSpec `yaml:"approvals,omitempty"`
}

type VerifySpec struct {
	Command string        `yaml:"command"`
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

const defaultVerifyTimeout = 5 * time.Minute

type ApprovalSpec struct {
	Decision string `yaml:"decision"`
	Message  string `yaml:"message,omitempty"`
	Comment  string `yaml:"comment,omitempty"`
}

type Result struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RunID        string    `json:"run_id"`
	Status       string    `json:"status"`
	VerifyOutput string    `json:"verify_output,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec Spec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	if spec.ID == "" || spec.WorkflowID == "" {
		return nil, fmt.Errorf("eval spec missing id or workflow_id")
	}
	if spec.Inputs == nil {
		spec.Inputs = map[string]any{}
	}
	return &spec, nil
}

// prepareEvalEnv resolves the project directory and configures environment
// variables needed by eval runs: PROJECT_DIR, PATH (with bin/), and LEDGER_PATH.
// It also expands env var references in spec.Inputs and sets the project_dir input.
func prepareEvalEnv(root string, spec *Spec) (string, error) {
	projectDir := os.ExpandEnv(spec.ProjectDir)
	if !filepath.IsAbs(projectDir) {
		projectDir = filepath.Join(root, projectDir)
	}
	if err := os.Setenv("PROJECT_DIR", projectDir); err != nil {
		return "", err
	}

	// Add toil bin/ to PATH so workflow-specific tools (e.g., semantic_port) are found.
	binDir := filepath.Join(root, "bin")
	if _, err := os.Stat(binDir); err == nil {
		if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return "", err
		}
	}

	if spec.Inputs == nil {
		spec.Inputs = map[string]any{}
	}
	// Expand environment variable references in string input values.
	for k, v := range spec.Inputs {
		if s, ok := v.(string); ok {
			spec.Inputs[k] = os.ExpandEnv(s)
		}
	}
	if _, ok := spec.Inputs["project_dir"]; !ok {
		spec.Inputs["project_dir"] = projectDir
	}

	// Wire LEDGER_PATH from inputs if present.
	if lp, ok := spec.Inputs["ledger_path"]; ok {
		if lpStr, ok := lp.(string); ok && lpStr != "" {
			if !filepath.IsAbs(lpStr) {
				lpStr = filepath.Join(projectDir, lpStr)
			}
			if err := os.Setenv("LEDGER_PATH", lpStr); err != nil {
				return "", err
			}
		}
	}

	return projectDir, nil
}

func Run(ctx context.Context, root string, spec *Spec) (*Result, error) {
	if spec.ProjectDir == "" {
		return nil, fmt.Errorf("project_dir is required for eval runs")
	}
	projectDir, err := prepareEvalEnv(root, spec)
	if err != nil {
		return nil, err
	}

	// Clean up stale artifacts from previous eval runs to prevent contamination.
	cleanEvalArtifacts(root, projectDir)

	application, err := app.Load(root)
	if err != nil {
		return nil, err
	}

	autoApprove := spec.AutoApprove || len(spec.Approvals) > 0
	if autoApprove {
		application.Engine.Approver = &approvals.CallbackApprover{
			Fn: func(a *approvals.Approval) (*approvals.Resolution, error) {
				decision, message, comment := decisionForApproval(root, spec, a)
				if decision == "" {
					decision = decisionApproved
				}
				if message == "" {
					message = fmt.Sprintf("Auto-approved by eval (%s).", decision)
				}
				if comment == "" {
					comment = commentAutoApproval
				}
				return &approvals.Resolution{
					Decision: decision,
					Message:  message,
					Comment:  comment,
				}, nil
			},
		}
	}

	result := &Result{ID: spec.ID, Name: spec.Name, StartedAt: time.Now().UTC()}
	runID, _, err := application.Engine.RunWorkflow(ctx, spec.WorkflowID, spec.Inputs)
	if err != nil && !errors.Is(err, engine.ErrApprovalPending) {
		result.Status = statusFailed
		result.RunID = runID
		result.FinishedAt = time.Now().UTC()
		if runID != "" {
			_ = saveResult(root, runID, result)
		}
		return result, err
	}
	result.RunID = runID

	if errors.Is(err, engine.ErrApprovalPending) && !autoApprove {
		result.Status = "paused"
		result.FinishedAt = time.Now().UTC()
		_ = saveResult(root, runID, result)
		return result, nil
	}
	for errors.Is(err, engine.ErrApprovalPending) {
		_, err = application.Engine.ResumeRun(ctx, runID)
	}
	if err != nil {
		result.Status = statusFailed
		result.FinishedAt = time.Now().UTC()
		_ = saveResult(root, runID, result)
		return result, err
	}

	if spec.Verify.Command != "" {
		verifyTimeout := spec.Verify.Timeout
		if verifyTimeout == 0 {
			verifyTimeout = defaultVerifyTimeout
		}
		verifyCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(verifyCtx, "bash", "-lc", spec.Verify.Command)
		cmd.Dir = projectDir
		out, verifyErr := cmd.CombinedOutput()
		result.VerifyOutput = string(out)
		if verifyErr != nil {
			result.Status = statusFailed
			result.FinishedAt = time.Now().UTC()
			_ = saveResult(root, runID, result)
			return result, fmt.Errorf("verify failed: %w\n%s", verifyErr, result.VerifyOutput)
		}
	}

	result.Status = statusPassed
	result.FinishedAt = time.Now().UTC()
	if err := saveResult(root, runID, result); err != nil {
		return result, err
	}

	return result, nil
}

func saveResult(root string, runID string, result *Result) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(config.RunsDir(root), runID, "eval.json")
	return os.WriteFile(path, data, 0o644)
}

func decisionForApproval(root string, spec *Spec, approval *approvals.Approval) (string, string, string) {
	var configured *ApprovalSpec
	if spec.Approvals != nil {
		if entry, ok := spec.Approvals[approval.NodeID]; ok {
			if entry.Decision != "" {
				return entry.Decision, entry.Message, entry.Comment
			}
			configured = &entry
		}
	}

	decision := ""
	if inferred, ok := inferDecision(root, approval); ok {
		decision = inferred
	}
	if decision == "" {
		decision = decisionApproved
	}

	message := ""
	comment := ""
	if configured != nil {
		message = configured.Message
		comment = configured.Comment
	}
	if message == "" {
		message = defaultApprovalMessage(root, approval, decision)
	}
	if comment == "" {
		comment = commentAutoApproval
	}
	return decision, message, comment
}

func inferDecision(root string, approval *approvals.Approval) (string, bool) {
	runDir := filepath.Join(config.RunsDir(root), approval.RunID)
	workflow, err := definitions.LoadWorkflowSnapshot(filepath.Join(runDir, "workflow.yaml"))
	if err != nil {
		return "", false
	}
	node := definitions.FindNode(workflow, approval.NodeID)
	if node == nil || len(node.Decisions) == 0 {
		return "", false
	}
	runState, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return "", false
	}

	incomingDecision := latestIncomingDecision(workflow, runState, approval.NodeID)
	allowed := map[string]struct{}{}
	for _, decision := range node.Decisions {
		allowed[decision.ID] = struct{}{}
	}
	if incomingDecision == "needs_more_info" {
		if _, ok := allowed[decisionClarified]; ok {
			return decisionClarified, true
		}
	}
	if incomingDecision == decisionReadyForReview {
		if _, ok := allowed[decisionApproved]; ok {
			return decisionApproved, true
		}
	}
	if incomingDecision == decisionNeedsChanges {
		if _, ok := allowed[decisionChangesRequested]; ok {
			return decisionChangesRequested, true
		}
	}
	if _, ok := allowed[decisionApproved]; ok {
		return decisionApproved, true
	}
	if len(node.Decisions) > 0 {
		return node.Decisions[0].ID, true
	}
	return "", false
}

func latestIncomingDecision(workflow *definitions.Workflow, runState *state.RunState, nodeID string) string {
	type candidate struct {
		decision string
		endedAt  time.Time
	}
	candidates := []candidate{}
	for _, edge := range workflow.Edges {
		if edge.To != nodeID {
			continue
		}
		nodeState, ok := runState.Nodes[edge.From]
		if !ok || nodeState.Status != statusCompleted {
			continue
		}
		if engine.IsExpression(edge.When) {
			continue // expression edges can't be statically matched here
		}
		if edge.When != "" && edge.When != edgeWhenDefault && edge.When != nodeState.Decision {
			continue
		}
		ended := time.Time{}
		if nodeState.EndedAt != nil {
			ended = *nodeState.EndedAt
		} else if nodeState.StartedAt != nil {
			ended = *nodeState.StartedAt
		}
		candidates = append(candidates, candidate{decision: nodeState.Decision, endedAt: ended})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].endedAt.After(candidates[j].endedAt)
	})
	return candidates[0].decision
}

func defaultApprovalMessage(root string, approval *approvals.Approval, decision string) string {
	switch decision {
	case decisionApproved:
		return "Approved."
	case decisionChangesRequested:
		return "Please revise the work based on feedback."
	case decisionClarified:
		if context := loadRunInput(root, approval.RunID, "context"); strings.TrimSpace(context) != "" {
			return context
		}
		if idea := loadRunInput(root, approval.RunID, "idea"); strings.TrimSpace(idea) != "" {
			return idea
		}
		return "Clarified."
	default:
		return fmt.Sprintf("Auto-approved by eval (%s).", decision)
	}
}

// cleanEvalArtifacts removes stale artifacts from previous eval runs to prevent
// cross-run contamination. Agents (especially codex) can discover and plagiarize
// code from prior runs if the project directory or tgwm repos aren't cleaned.
func cleanEvalArtifacts(root string, projectDir string) {
	// Remove the project directory so agents start fresh.
	if projectDir != "" {
		_ = os.RemoveAll(projectDir)
	}

	// Remove tgwm bare repos that may have stale branches from prior runs.
	reposDir := filepath.Join(root, "repos")
	if entries, err := os.ReadDir(reposDir); err == nil {
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(reposDir, entry.Name()))
		}
	}
}

func loadRunInput(root string, runID string, key string) string {
	runState, err := state.LoadState(filepath.Join(config.RunsDir(root), runID, "state.json"))
	if err != nil {
		return ""
	}
	if value, ok := runState.Inputs[key]; ok {
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}
