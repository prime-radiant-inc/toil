package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/state"
)

func runNarratives(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "narratives requires a subcommand")
		os.Exit(1)
	}

	switch args[0] {
	case "preview":
		runNarrativesPreview(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "unknown narratives subcommand")
		os.Exit(1)
	}
}

func runNarrativesPreview(args []string) {
	flags := flag.NewFlagSet("narratives preview", flag.ExitOnError)
	runsDir := flags.String("runs-dir", "", "runs directory (defaults to TOIL_RUNS_DIR)")
	runID := flags.String("run-id", "", "run id (or pass as positional arg)")
	intent := flags.Bool("intent", false, "generate run intent (title + description)")
	summary := flags.Bool("summary", false, "generate run summary")
	promptOnly := flags.Bool("prompt-only", false, "print prompts only (no LLM call)")
	includePrompt := flags.Bool("include-prompt", false, "include prompts in JSON output")
	pretty := flags.Bool("pretty", false, "pretty-print JSON output")
	timeoutStr := flags.String("timeout", "", "timeout (e.g. 30s); defaults to TOIL_RUN_NARRATIVE_TIMEOUT or 30s")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *runID == "" && flags.NArg() >= 1 {
		*runID = strings.TrimSpace(flags.Arg(0))
	}
	if strings.TrimSpace(*runID) == "" {
		fmt.Fprintln(os.Stderr, "narratives preview requires <run_id> (or --run-id)")
		os.Exit(1)
	}
	if !*intent && !*summary {
		*intent = true
		*summary = true
	}
	if *promptOnly {
		*includePrompt = true
	}

	if strings.TrimSpace(*runsDir) == "" {
		*runsDir = strings.TrimSpace(os.Getenv("TOIL_RUNS_DIR"))
	}
	if strings.TrimSpace(*runsDir) == "" {
		fmt.Fprintln(os.Stderr, "runs dir not set: use --runs-dir or set TOIL_RUNS_DIR")
		os.Exit(1)
	}

	runDir := filepath.Join(strings.TrimSpace(*runsDir), strings.TrimSpace(*runID))
	runState, workflow, err := loadRunArtifacts(runDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	timeout := 30 * time.Second
	if strings.TrimSpace(*timeoutStr) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(*timeoutStr))
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --timeout %q: %v\n", *timeoutStr, err)
			os.Exit(1)
		}
		timeout = d
	} else if raw := strings.TrimSpace(os.Getenv("TOIL_RUN_NARRATIVE_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err == nil && d > 0 {
			timeout = d
		}
	}

	out := map[string]any{
		"run_id": strings.TrimSpace(*runID),
	}

	if *intent {
		obj := map[string]any{}
		if *promptOnly {
			if *includePrompt {
				obj["prompt"] = engine.BuildRunIntentPrompt(workflow, runState)
			}
		} else {
			callCtx, cancel := context.WithTimeout(context.Background(), timeout)
			narr, prompt, err := engine.GenerateRunIntentNarrative(callCtx, workflow, runState)
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			obj["title"] = narr.Title
			obj["description"] = narr.Description
			if *includePrompt {
				obj["prompt"] = prompt
			}
		}
		out["intent"] = obj
	}

	if *summary {
		obj := map[string]any{}
		if *promptOnly {
			if *includePrompt {
				obj["prompt"] = engine.BuildRunSummaryPrompt(workflow, runState)
			}
		} else {
			callCtx, cancel := context.WithTimeout(context.Background(), timeout)
			narr, prompt, err := engine.GenerateRunSummaryNarrative(callCtx, workflow, runState)
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			obj["summary"] = narr.Summary
			if *includePrompt {
				obj["prompt"] = prompt
			}
		}
		out["summary"] = obj
	}

	var b []byte
	if *pretty {
		b, err = json.MarshalIndent(out, "", "  ")
	} else {
		b, err = json.Marshal(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func loadRunArtifacts(runDir string) (*state.RunState, *definitions.Workflow, error) {
	st, err := state.LoadState(filepath.Join(runDir, "state.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("load %s: %w", filepath.Join(runDir, "state.json"), err)
	}

	b, err := os.ReadFile(filepath.Join(runDir, "workflow.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", filepath.Join(runDir, "workflow.yaml"), err)
	}
	var wf definitions.Workflow
	if err := yaml.Unmarshal(b, &wf); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", filepath.Join(runDir, "workflow.yaml"), err)
	}

	return st, &wf, nil
}
