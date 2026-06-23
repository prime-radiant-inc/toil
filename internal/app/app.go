package app

import (
	"fmt"

	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/runners"
)

type App struct {
	Root        string
	Definitions *definitions.Bundle
	Engine      *engine.Engine
}

func Load(root string) (*App, error) {
	bundle, err := definitions.LoadBundle(root)
	if err != nil {
		return nil, err
	}

	registry, err := buildRunnerRegistry(bundle.Runners)
	if err != nil {
		return nil, err
	}

	runsDir := config.RunsDir(root)
	toilRoot := config.ToilRoot(root)
	engine := engine.NewEngine(bundle, registry, runsDir, toilRoot)

	return &App{Root: root, Definitions: bundle, Engine: engine}, nil
}

func LoadForResume(root string) (*App, error) {
	bundle, err := definitions.LoadBundleNoEnv(root)
	if err != nil {
		return nil, err
	}

	registry, err := buildRunnerRegistry(bundle.Runners)
	if err != nil {
		return nil, err
	}

	runsDir := config.RunsDir(root)
	toilRoot := config.ToilRoot(root)
	engine := engine.NewEngine(bundle, registry, runsDir, toilRoot)

	return &App{Root: root, Definitions: bundle, Engine: engine}, nil
}

func buildRunnerRegistry(runnerDefs map[string]*definitions.Runner) (*runners.Registry, error) {
	registry := runners.NewRegistry()
	for id, def := range runnerDefs {
		var runner runners.Runner
		config := runners.Config{
			Command:    def.Command,
			Args:       def.Args,
			Env:        def.Env,
			TimeoutSec: def.TimeoutSec,
			Resume:     def.Resume,
		}
		switch def.Type {
		case "codex":
			runner = runners.NewCodexRunner(config)
		case "claude":
			runner = runners.NewClaudeRunner(config)
		case "serf":
			runner = runners.NewSerfRunner(config)
		case "human":
			runner = runners.NewHumanRunner(config)
		case "shell":
			runner = runners.NewShellRunner(config)
		default:
			return nil, fmt.Errorf("unknown runner type: %s", def.Type)
		}

		if err := registry.Register(id, runner); err != nil {
			return nil, err
		}
	}

	return registry, nil
}
