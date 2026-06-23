package definitions

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FindEnvKeys returns the set of env identifiers referenced by ${env.X}
// expressions anywhere in the workflow (prompts, workspace paths, node
// Inputs, edge Passes, emit Output blocks). Used by captureEnv to
// snapshot only the relevant subset of process env into runState.Env at
// run creation.
func FindEnvKeys(w *Workflow) []string {
	seen := map[string]bool{}
	collect := func(s string) {
		for _, ref := range FindExpressionRefs(s) {
			if ref.Namespace == nsEnv && ref.Path != "" {
				// Path may have dots in theory; env keys are single
				// identifiers (validated elsewhere). Defensive split.
				key := ref.Path
				if dot := strings.Index(key, "."); dot >= 0 {
					key = key[:dot]
				}
				seen[key] = true
			}
		}
	}

	if w.WorkspaceDefaults != nil {
		collect(w.WorkspaceDefaults.Path)
	}
	for _, n := range w.Nodes {
		collect(n.Prompt)
		if n.Workspace != nil {
			collect(n.Workspace.Path)
		}
		for _, raw := range n.Inputs {
			if s, ok := raw.(string); ok {
				collect(s)
			}
		}
		if n.Output != nil {
			collect(n.Output.Message)
			collectFromData(n.Output.Data, collect)
		}
	}
	for _, e := range w.Edges {
		collect(e.Prompt)
		for _, raw := range e.Passes {
			if s, ok := raw.(string); ok {
				collect(s)
			}
		}
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func collectFromData(data map[string]any, collect func(string)) {
	for _, v := range data {
		switch tv := v.(type) {
		case string:
			collect(tv)
		case map[string]any:
			collectFromData(tv, collect)
		}
	}
}

func LoadWorkflowFile(path string) (*Workflow, error) {
	workflow, warnings, err := loadWorkflowWithWarnings(path)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		slog.Warn("toil.workflow.unknown_key", "file", path, "detail", w)
	}
	return workflow, nil
}

// LoadWorkflowFileWithWarnings loads a workflow and returns any unknown-key
// warnings instead of logging them. Used by tests and validation tools.
func LoadWorkflowFileWithWarnings(path string) ([]string, error) {
	_, warnings, err := loadWorkflowWithWarnings(path)
	return warnings, err
}

func loadWorkflowWithWarnings(path string) (*Workflow, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	// Reject the deprecated node-level `outputs:` field with a clear, actionable
	// error before any other decoding happens. The field was replaced by
	// `outputs_schema:` (see docs/plans/2026-04-23-outputs-schema-design.md).
	if nodeIDs, found := detectLegacyNodeOutputs(data); found {
		return nil, nil, fmt.Errorf("workflow %q: node-level `outputs:` field is no longer supported; replace it with `outputs_schema:` (JSON Schema describing data). Offending nodes: %s",
			path, strings.Join(nodeIDs, ", "))
	}

	// First pass: strict decode to detect unknown keys.
	var warnings []string
	strictDec := yaml.NewDecoder(bytes.NewReader(data))
	strictDec.KnownFields(true)
	var probe Workflow
	if strictErr := strictDec.Decode(&probe); strictErr != nil {
		warnings = append(warnings, strictErr.Error())
	}

	// Second pass: normal decode (tolerates unknown keys).
	var workflow Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, nil, err
	}

	if err := validateWorkflow(&workflow); err != nil {
		return nil, nil, err
	}

	workflow.SourcePath = path

	return &workflow, warnings, nil
}

// detectLegacyNodeOutputs returns the IDs of nodes that still declare the
// deprecated `outputs:` field (removed in favor of `outputs_schema:`).
// Inspects raw YAML so the probe can run before struct decoding.
func detectLegacyNodeOutputs(data []byte) ([]string, bool) {
	var probe struct {
		Nodes []map[string]any `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, false
	}
	var offending []string
	for _, node := range probe.Nodes {
		if _, ok := node["outputs"]; !ok {
			continue
		}
		id, _ := node["id"].(string)
		if id == "" {
			id = "<unknown>"
		}
		offending = append(offending, id)
	}
	return offending, len(offending) > 0
}

func LoadWorkflowSnapshot(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var workflow Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, err
	}

	if err := validateWorkflow(&workflow); err != nil {
		return nil, err
	}

	workflow.SourcePath = path

	return &workflow, nil
}

func LoadWorkflowsDir(dir string) (map[string]*Workflow, error) {
	return loadWorkflowsDir(dir, LoadWorkflowFile)
}

func LoadWorkflowsDirSnapshot(dir string) (map[string]*Workflow, error) {
	return loadWorkflowsDir(dir, LoadWorkflowSnapshot)
}

func loadWorkflowsDir(dir string, loadFunc func(string) (*Workflow, error)) (map[string]*Workflow, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	workflows := make(map[string]*Workflow)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".yaml" && filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		workflow, err := loadFunc(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if _, exists := workflows[workflow.ID]; exists {
			return nil, fmt.Errorf("duplicate workflow id: %s", workflow.ID)
		}
		workflows[workflow.ID] = workflow
	}

	return workflows, nil
}

func validateWorkflow(workflow *Workflow) error {
	if workflow.ID == "" {
		return fmt.Errorf("workflow id is required")
	}
	if workflow.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if workflow.Version == 0 {
		return fmt.Errorf("workflow version is required")
	}
	if workflow.Nodes == nil {
		workflow.Nodes = []Node{}
	}
	if workflow.Edges == nil {
		workflow.Edges = []Edge{}
	}

	for _, node := range workflow.Nodes {
		if len(node.RunnerEnv) > 0 && node.Runner == "" {
			slog.Warn("toil.workflow.runner_env_without_runner",
				"workflow", workflow.ID,
				"node", node.ID,
				"detail", "runner_env is set but runner is not — env will be passed to the default runner")
		}
	}

	if err := ValidateExpressions(workflow); err != nil {
		return fmt.Errorf("workflow %q: %w", workflow.ID, err)
	}

	result := ValidateGraph(workflow)
	if result.HasErrors() {
		return fmt.Errorf("workflow %q: %s", workflow.ID, result.Error())
	}

	return nil
}
