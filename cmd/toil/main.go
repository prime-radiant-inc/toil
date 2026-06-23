package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
	"primeradiant.com/toil/internal/api"
	"primeradiant.com/toil/internal/app"
	"primeradiant.com/toil/internal/client"
	"primeradiant.com/toil/internal/config"
	"primeradiant.com/toil/internal/dashboard"
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/engine"
	"primeradiant.com/toil/internal/eval"
	"primeradiant.com/toil/internal/interrogate"
	"primeradiant.com/toil/internal/orchestrator"
	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

const subcmdList = "list"

const aspectCompare = "compare"

func main() {
	loadEnvFiles()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage()
	case "version", "-v", "--version":
		fmt.Println("toil dev")
	case "validate":
		runValidate()
	case "workflows":
		runWorkflows(os.Args[2:])
	case "run":
		runWorkflow(os.Args[2:])
	case "resume":
		runResume(os.Args[2:])
	case "cancel":
		runCancel(os.Args[2:])
	case "runs":
		runRuns(os.Args[2:])
	case "narratives":
		runNarratives(os.Args[2:])
	case "approvals":
		runApprovals(os.Args[2:])
	case "visualize":
		runVisualize(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "eval":
		runEval(os.Args[2:])
	case "inspect":
		runInspect(os.Args[2:])
	case "interrogate":
		runInterrogate(os.Args[2:])
	case "pause":
		runPause()
	case "drain":
		runDrain(os.Args[2:])
	case "human":
		runHuman()
	default:
		printUsage()
		os.Exit(1)
	}
}

func runValidate() {
	root := mustRoot()
	bundle, err := definitions.LoadBundleNoEnv(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	hasErrors := false

	for id, wf := range bundle.Workflows {
		result := definitions.ValidateGraph(wf)
		for _, d := range result.Diagnostics {
			prefix := "info"
			switch d.Severity {
			case definitions.SeverityWarning:
				prefix = "warning"
			case definitions.SeverityError:
				prefix = "error"
				hasErrors = true
			}
			fmt.Fprintf(os.Stderr, "%s: workflow %q: %s\n", prefix, id, d.Message)
		}
	}

	bundleResult := definitions.ValidateBundle(bundle)
	for _, d := range bundleResult.Diagnostics {
		prefix := "info"
		switch d.Severity {
		case definitions.SeverityWarning:
			prefix = "warning"
		case definitions.SeverityError:
			prefix = "error"
			hasErrors = true
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", prefix, d.Message)
	}

	if hasErrors {
		os.Exit(1)
	}
	fmt.Println("ok")
}

func runWorkflows(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "workflows requires a subcommand")
		os.Exit(1)
	}
	apiClient := client.NewFromEnv()
	ctx := context.Background()

	switch args[0] {
	case subcmdList:
		ids, err := apiClient.ListWorkflows(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Println(id)
		}
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "workflows show <id>")
			os.Exit(1)
		}
		data, err := apiClient.WorkflowShow(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(data))
	default:
		fmt.Fprintln(os.Stderr, "unknown workflows subcommand")
		os.Exit(1)
	}
}

// errMissingWorkflowID is returned by the run-arg parsing helpers when no
// positional workflow ID is present. runWorkflow translates it into the
// user-facing usage message.
var errMissingWorkflowID = errors.New("missing workflow id")

// reorderRunArgs lets `run` accept flags before, after, or interleaved with
// the positional workflow ID. stdlib flag.Parse stops at the first
// non-flag, so we rebuild args with all flag tokens up front and
// positionals trailing. valueTaking lists flag names (without leading
// dashes) that consume the following arg when written as `--name value`;
// `--name=value` forms are handled inline.
func reorderRunArgs(args []string, valueTaking map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Pass through "--" and everything after as positionals.
			positionals = append(positionals, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				continue
			}
			if valueTaking[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return append(flags, positionals...)
}

func runWorkflow(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	inputs := &inputList{}
	flags.Var(inputs, "input", "workflow input key=value (repeatable)")
	reordered := reorderRunArgs(args, map[string]bool{"input": true})
	if err := flags.Parse(reordered); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "run <workflow_id> --input key=value")
		os.Exit(1)
	}
	workflowID := flags.Arg(0)
	inputMap, err := inputs.ToMap()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx := context.Background()
	apiClient := client.NewFromEnv()
	if err := validateRunInputs(ctx, apiClient, workflowID, inputMap); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	env := map[string]string{}
	if value, ok := os.LookupEnv("PROJECT_DIR"); ok {
		env["PROJECT_DIR"] = value
	}
	runID, err := apiClient.CreateRun(ctx, workflowID, inputMap, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(runID)
}

func validateRunInputs(ctx context.Context, apiClient *client.Client, workflowID string, inputs map[string]any) error {
	data, err := apiClient.WorkflowShow(ctx, workflowID)
	if err != nil {
		return err
	}
	var workflow definitions.Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return err
	}
	return definitions.ValidateInputs(&workflow, inputs)
}

func runResume(args []string) {
	// With no arguments, remove the drain .paused marker (daemon resume).
	// With a run ID argument, resume a specific paused/waiting run via the API.
	if len(args) == 0 {
		runResumeDaemon()
		return
	}
	ctx := context.Background()
	apiClient := client.NewFromEnv()
	if err := apiClient.ResumeRun(ctx, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(args[0])
}

func runCancel(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "cancel <run_id>")
		os.Exit(1)
	}
	ctx := context.Background()
	apiClient := client.NewFromEnv()
	if err := apiClient.CancelRun(ctx, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("cancelled %s\n", args[0])
}

func runRuns(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "runs requires a subcommand")
		os.Exit(1)
	}
	// `runs tree` reads state.json directly from disk and does not
	// require the HTTP API — handle it before constructing the client.
	if args[0] == "tree" {
		runRunsTree(args[1:])
		return
	}
	apiClient := client.NewFromEnv()
	ctx := context.Background()

	switch args[0] {
	case subcmdList:
		var workflowFilter, statusFilter string
		var limit int
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--workflow":
				if i+1 < len(args) {
					workflowFilter = args[i+1]
					i++
				}
			case "--status":
				if i+1 < len(args) {
					statusFilter = args[i+1]
					i++
				}
			case "--limit":
				if i+1 < len(args) {
					_, _ = fmt.Sscanf(args[i+1], "%d", &limit)
					i++
				}
			}
		}

		if workflowFilter != "" || statusFilter != "" || limit > 0 {
			data, err := apiClient.ListRunsFiltered(ctx, workflowFilter, statusFilter, limit)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			var result struct {
				Runs []struct {
					RunID                string  `json:"run_id"`
					WorkflowID           string  `json:"workflow_id"`
					Status               string  `json:"status"`
					HasUnresolvedFailure bool    `json:"has_unresolved_failure"`
					StartedAt            string  `json:"started_at"`
					FinishedAt           *string `json:"finished_at"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				fmt.Fprintln(os.Stderr, "parse response:", err)
				os.Exit(1)
			}
			for _, r := range result.Runs {
				dur := ""
				if r.FinishedAt != nil {
					var st, ft time.Time
					if e := st.UnmarshalText([]byte(r.StartedAt)); e == nil {
						if e := ft.UnmarshalText([]byte(*r.FinishedAt)); e == nil {
							dur = fmt.Sprintf("%.0fs", ft.Sub(st).Seconds())
						}
					}
				}
				effectiveStatus := state.EffectiveStatus(r.Status, r.HasUnresolvedFailure)
				fmt.Printf("%-25s %-20s %-12s %s  %s\n", r.RunID, r.WorkflowID, effectiveStatus, r.StartedAt, dur)
			}
		} else {
			ids, err := apiClient.ListRuns(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			for _, id := range ids {
				fmt.Println(id)
			}
		}
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "runs show <run_id>")
			os.Exit(1)
		}
		data, err := apiClient.RunState(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(data))
	case "events":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "runs events <run_id>")
			os.Exit(1)
		}
		data, err := apiClient.RunEvents(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(data))
	default:
		fmt.Fprintln(os.Stderr, "unknown runs subcommand")
		os.Exit(1)
	}
}

func runApprovals(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "approvals requires a subcommand")
		os.Exit(1)
	}

	apiClient := client.NewFromEnv()
	ctx := context.Background()
	switch args[0] {
	case subcmdList:
		data, err := apiClient.ListApprovals(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	case "resolve":
		flags := flag.NewFlagSet("approvals resolve", flag.ExitOnError)
		decision := flags.String("decision", "", "decision to record")
		message := flags.String("message", "", "message to record")
		comment := flags.String("comment", "", "comment to record")
		if err := flags.Parse(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if flags.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "approvals resolve <approval_id> --decision <decision> --message <message>")
			os.Exit(1)
		}
		if *decision == "" || *message == "" {
			fmt.Fprintln(os.Stderr, "--decision and --message are required")
			os.Exit(1)
		}

		approvalID := flags.Arg(0)
		if err := apiClient.ResolveApproval(ctx, approvalID, *decision, *message, *comment); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(approvalID)
	default:
		fmt.Fprintln(os.Stderr, "unknown approvals subcommand")
		os.Exit(1)
	}
}

func runVisualize(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "visualize workflow <id> | visualize run <run_id>")
		os.Exit(1)
	}
	apiClient := client.NewFromEnv()
	ctx := context.Background()

	switch args[0] {
	case "workflow":
		data, err := apiClient.WorkflowGraph(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(data))
	case "run":
		data, err := apiClient.RunGraph(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(data))
	default:
		fmt.Fprintln(os.Stderr, "unknown visualize subcommand")
		os.Exit(1)
	}
}

func runServe(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", ":8080", "address to bind")
	daemon := flags.Bool("daemon", false, "run server in background")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	root := mustRoot()

	// Initialize structured logging
	logLevel := slog.LevelInfo
	if lvl := os.Getenv("TOIL_LOG_LEVEL"); lvl != "" {
		switch strings.ToUpper(lvl) {
		case "DEBUG":
			logLevel = slog.LevelDebug
		case "WARN":
			logLevel = slog.LevelWarn
		case "ERROR":
			logLevel = slog.LevelError
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("toil.server.starting",
		"addr", *addr,
		"root", root,
		"runs_dir", config.RunsDir(root),
		"restore_enabled", config.RestoreEnabled(),
	)

	if *daemon && os.Getenv("TOIL_DAEMONIZED") == "" {
		pid, logPath, err := spawnServeDaemon(root, *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintf(os.Stdout, "toil server daemonized (pid %d). logs: %s\n", pid, logPath)
		return
	}

	application, err := app.LoadForResume(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runsDir := config.RunsDir(root)
	manager := orchestrator.NewManager(application.Engine, runsDir)
	manager.WireInterviewTrigger()
	manager.WireWebhookCallback()
	if config.RestoreEnabled() {
		if err := manager.Restore(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	interrogationMgr := interrogate.NewManager()
	srvCtx, srvCancel := context.WithCancel(context.Background())
	interrogationMgr.StartExpiry(srvCtx)

	server := &api.Server{
		App:            application,
		RunsDir:        runsDir,
		Manager:        manager,
		Interrogations: interrogationMgr,
	}

	dashboardServer := dashboard.NewServer(application, runsDir, manager, "/ui")

	mux := http.NewServeMux()
	mux.Handle("/ui/", http.StripPrefix("/ui", dashboardServer))
	mux.Handle("/ui", http.RedirectHandler("/ui/", http.StatusFound))
	mux.Handle("/health", api.HealthHandler(manager.RunCounts))
	specJSON := api.BuildSpecJSON()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specJSON)
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/" && wantsHTML(request) {
			http.Redirect(writer, request, "/ui/", http.StatusFound)
			return
		}
		server.ServeHTTP(writer, request)
	})

	slog.Info("toil.server.ready", "addr", *addr)
	go backfillTotals(runsDir)

	handler := api.LogRequests(slog.Default())(mux)
	httpServer := &http.Server{Addr: *addr, Handler: handler}

	// Graceful shutdown: on SIGTERM/SIGINT, stop accepting new requests,
	// cancel in-flight runs so they flush state, then exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		slog.Info("toil.server.shutting_down", "signal", sig.String())
		manager.Shutdown()
		_ = httpServer.Close()
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		srvCancel()
		os.Exit(1)
	}
	srvCancel()
}

func spawnServeDaemon(root, addr string) (int, string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return 0, "", err
	}
	runsDir := config.RunsDir(root)
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return 0, "", err
	}
	logPath := filepath.Join(runsDir, "server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(execPath, "serve", "--addr", addr)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TOIL_DAEMONIZED=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdin = devNull

	if err := daemonizeCommand(cmd); err != nil {
		return 0, "", err
	}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return 0, "", err
	}
	return pid, logPath, nil
}

func wantsHTML(request *http.Request) bool {
	accept := strings.ToLower(request.Header.Get("Accept"))
	return strings.Contains(accept, "text/html")
}

func runEval(args []string) {
	flags := flag.NewFlagSet("eval", flag.ExitOnError)
	projectDir := flags.String("project-dir", "", "project directory for the eval run")
	if err := flags.Parse(normalizeEvalArgs(args)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "eval <id> --project-dir /path/to/project")
		os.Exit(1)
	}
	specID := flags.Arg(0)

	root := mustRoot()
	specPath := filepath.Join(root, "tests", "eval", specID+".yaml")
	spec, err := eval.LoadSpec(specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *projectDir != "" {
		spec.ProjectDir = *projectDir
	}
	if spec.ProjectDir == "" {
		// No --project-dir flag and no project_dir in the YAML — pick a
		// fresh tmpdir per invocation so concurrent or sequential runs
		// can't contaminate each other's project state. The bare repo
		// (created by tgwm init in ensure_repo) derives its slug from
		// this dir's basename, so it gets a unique path too.
		autoDir, err := os.MkdirTemp("", "toil-eval-"+spec.ID+"-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to create eval project dir:", err)
			os.Exit(1)
		}
		spec.ProjectDir = autoDir
		fmt.Fprintln(os.Stderr, "eval: using auto-generated project dir:", autoDir)
	}

	result, runErr := eval.Run(context.Background(), root, spec)
	payload, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(payload))

	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

func normalizeEvalArgs(args []string) []string {
	flagArgs := []string{}
	posArgs := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--project-dir" {
			if i+1 < len(args) {
				flagArgs = append(flagArgs, arg, args[i+1])
				i++
				continue
			}
		}
		if strings.HasPrefix(arg, "--project-dir=") {
			flagArgs = append(flagArgs, arg)
			continue
		}
		posArgs = append(posArgs, arg)
	}
	return append(flagArgs, posArgs...)
}

func runInspect(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: toil inspect <run-id> [<node-id>] [<aspect>] [--follow]")
		os.Exit(1)
	}

	apiClient := client.NewFromEnv()
	ctx := context.Background()
	runID := args[0]

	var nodeID, aspect, attempt string
	var followMode bool
	// Track whether the previous positional was the `compare` aspect so
	// the next positional is consumed as the other-run-id into the
	// aspect path (compare/<other-run-id>) rather than as a nodeID.
	expectCompareRunID := false
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--follow" {
			followMode = true
			continue
		}
		if arg == "--attempt" {
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "--attempt requires a value")
				os.Exit(1)
			}
			attempt = rest[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "--attempt=") {
			attempt = strings.TrimPrefix(arg, "--attempt=")
			continue
		}
		if strings.HasPrefix(arg, "--") {
			continue
		}
		if expectCompareRunID {
			aspect = "compare/" + arg
			expectCompareRunID = false
			continue
		}
		if nodeID == "" {
			if isKnownAspect(arg) {
				aspect = arg
				if arg == aspectCompare {
					expectCompareRunID = true
				}
			} else {
				nodeID = arg
			}
		} else if aspect == "" {
			aspect = arg
			if arg == aspectCompare {
				expectCompareRunID = true
			}
		}
	}
	if expectCompareRunID {
		fmt.Fprintln(os.Stderr, "Usage: toil inspect <run-id> compare <other-run-id>")
		os.Exit(1)
	}

	if followMode {
		var err error
		if nodeID != "" {
			err = apiClient.InspectNodeFollow(ctx, runID, nodeID, aspect, func(data []byte) {
				fmt.Println(string(data))
			})
		} else {
			err = apiClient.InspectFollow(ctx, runID, aspect, func(data []byte) {
				fmt.Println(string(data))
			})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	var data []byte
	var err error
	if nodeID != "" {
		data, err = apiClient.InspectNodeAttempt(ctx, runID, nodeID, aspect, attempt)
	} else {
		if attempt != "" {
			fmt.Fprintln(os.Stderr, "--attempt requires a node ID")
			os.Exit(1)
		}
		data, err = apiClient.Inspect(ctx, runID, aspect)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}

func isKnownAspect(name string) bool {
	aspects := []string{
		"overview", "flow", "timing", "tokens", "decisions",
		"errors", "prompts", "inputs", "outputs", "transcript", "tree", aspectCompare,
	}
	for _, a := range aspects {
		if a == name {
			return true
		}
	}
	return false
}

func runInterrogate(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: toil interrogate <run-id> <node-id> <question> [--once]")
		os.Exit(1)
	}

	runID := args[0]
	nodeID := args[1]
	question := args[2]
	once := false
	for _, arg := range args[3:] {
		if arg == "--once" {
			once = true
		}
	}

	apiClient := client.NewFromEnv()
	apiClient.HTTP.Timeout = 3 * time.Minute

	ctx := context.Background()
	data, err := apiClient.InterrogationCreate(ctx, runID, nodeID, question)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var result struct {
		ID       string `json:"id"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		fmt.Fprintln(os.Stderr, "parse response:", err)
		os.Exit(1)
	}

	fmt.Println(result.Response)

	if once {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		followUp := strings.TrimSpace(scanner.Text())
		if followUp == "" || followUp == "exit" || followUp == "quit" {
			break
		}

		data, err := apiClient.InterrogationAsk(ctx, result.ID, followUp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			break
		}

		var askResult struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal(data, &askResult); err != nil {
			fmt.Fprintln(os.Stderr, "parse response:", err)
			break
		}

		fmt.Println(askResult.Response)
	}
}

// runPause creates the .paused marker file in the runs directory.
// Once present, the daemon rejects new run creation with HTTP 503.
func runPause() {
	runsDir := config.RunsDir(mustRoot())
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	markerPath := config.PausedMarkerPath(runsDir)
	f, err := os.OpenFile(markerPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = f.Close()
	fmt.Printf("paused: new run creation disabled (%s)\n", markerPath)
}

// runResumeDaemon removes the .paused marker file from the runs directory.
// The daemon begins accepting new runs immediately on the next request.
// Named runResumeDaemon to avoid collision with runResume (resume a specific run).
func runResumeDaemon() {
	runsDir := config.RunsDir(mustRoot())
	markerPath := config.PausedMarkerPath(runsDir)
	if err := os.Remove(markerPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("not paused (no marker file present)")
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("resumed: new run creation re-enabled")
}

// runDrain pauses new run creation then lists or cancels in-flight runs.
//
//	--dry-run     list in-flight runs and exit (do not pause, do not cancel)
//	--force-cancel pause + cancel all in-flight runs non-interactively
//	--wait         pause + wait for runs to complete naturally (non-interactive)
func runDrain(args []string) {
	flags := flag.NewFlagSet("drain", flag.ExitOnError)
	dryRun := flags.Bool("dry-run", false, "list in-flight runs only; do not pause or cancel")
	forceCancel := flags.Bool("force-cancel", false, "pause and cancel all in-flight runs non-interactively")
	wait := flags.Bool("wait", false, "pause and wait for runs to complete naturally")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runsDir := config.RunsDir(mustRoot())
	ctx := context.Background()
	apiClient := client.NewFromEnv()

	// Fetch in-flight runs (running or paused).
	inFlight, err := listInFlightRuns(ctx, apiClient)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to list runs:", err)
		os.Exit(1)
	}

	if *dryRun {
		printInFlightRuns(inFlight)
		return
	}

	// Pause new run creation.
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	markerPath := config.PausedMarkerPath(runsDir)
	f, err := os.OpenFile(markerPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = f.Close()
	fmt.Printf("paused: new run creation disabled (%s)\n", markerPath)

	if len(inFlight) == 0 {
		fmt.Println("no in-flight runs — safe to deploy")
		return
	}

	printInFlightRuns(inFlight)

	if *forceCancel {
		cancelInFlightRuns(ctx, apiClient, inFlight)
		return
	}

	if *wait {
		fmt.Printf("waiting for %d in-flight run(s) to complete naturally...\n", len(inFlight))
		fmt.Println("(use Ctrl-C to abort wait; runs will continue, pause marker remains)")
		waitForRunCompletion(ctx, apiClient, inFlight)
		return
	}

	// Interactive prompt.
	fmt.Printf("\n%d in-flight run(s) detected. Choose an action:\n", len(inFlight))
	fmt.Println("  [w] Wait for natural completion")
	fmt.Println("  [c] Cancel all in-flight runs now")
	fmt.Println("  [q] Quit (leave paused; runs continue)")
	fmt.Print("\nChoice [w/c/q]: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.ToLower(strings.TrimSpace(choice))

	switch choice {
	case "c":
		cancelInFlightRuns(ctx, apiClient, inFlight)
	case "w":
		fmt.Println("waiting for runs to complete naturally...")
		waitForRunCompletion(ctx, apiClient, inFlight)
	default:
		fmt.Println("exiting — pause marker remains; runs continue")
	}
}

type runSummaryRow struct {
	RunID                string
	WorkflowID           string
	Status               string
	HasUnresolvedFailure bool
	StartedAt            string
}

func listInFlightRuns(ctx context.Context, apiClient *client.Client) ([]runSummaryRow, error) {
	var rows []runSummaryRow
	for _, status := range []string{"running", "paused"} {
		data, err := apiClient.ListRunsFiltered(ctx, "", status, 0)
		if err != nil {
			return nil, err
		}
		var result struct {
			Runs []struct {
				RunID                string `json:"run_id"`
				WorkflowID           string `json:"workflow_id"`
				Status               string `json:"status"`
				HasUnresolvedFailure bool   `json:"has_unresolved_failure"`
				StartedAt            string `json:"started_at"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		for _, r := range result.Runs {
			rows = append(rows, runSummaryRow{
				RunID:                r.RunID,
				WorkflowID:           r.WorkflowID,
				Status:               r.Status,
				HasUnresolvedFailure: r.HasUnresolvedFailure,
				StartedAt:            r.StartedAt,
			})
		}
	}
	return rows, nil
}

func printInFlightRuns(rows []runSummaryRow) {
	if len(rows) == 0 {
		fmt.Println("no in-flight runs")
		return
	}
	fmt.Printf("%d in-flight run(s):\n", len(rows))
	fmt.Printf("  %-25s %-30s %-10s %s\n", "RUN ID", "WORKFLOW", "STATUS", "STARTED AT")
	for _, r := range rows {
		effectiveStatus := state.EffectiveStatus(r.Status, r.HasUnresolvedFailure)
		fmt.Printf("  %-25s %-30s %-10s %s\n", r.RunID, r.WorkflowID, effectiveStatus, r.StartedAt)
	}
}

func cancelInFlightRuns(ctx context.Context, apiClient *client.Client, rows []runSummaryRow) {
	for _, r := range rows {
		if err := apiClient.CancelRun(ctx, r.RunID); err != nil {
			fmt.Fprintf(os.Stderr, "cancel %s: %v\n", r.RunID, err)
			continue
		}
		fmt.Printf("cancelled %s\n", r.RunID)
	}
	fmt.Println("all in-flight runs cancelled — safe to deploy")
}

func waitForRunCompletion(ctx context.Context, apiClient *client.Client, rows []runSummaryRow) {
	pending := make(map[string]bool, len(rows))
	for _, r := range rows {
		pending[r.RunID] = true
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for runID := range pending {
				data, err := apiClient.RunState(ctx, runID)
				if err != nil {
					continue
				}
				var rs struct {
					Status               string `json:"status"`
					HasUnresolvedFailure bool   `json:"has_unresolved_failure"`
				}
				if err := json.Unmarshal(data, &rs); err != nil {
					continue
				}
				switch rs.Status {
				case "completed", "failed", "cancelled":
					effectiveStatus := state.EffectiveStatus(rs.Status, rs.HasUnresolvedFailure)
					fmt.Printf("%s finished (%s)\n", runID, effectiveStatus)
					delete(pending, runID)
				}
			}
			if len(pending) == 0 {
				fmt.Println("all in-flight runs finished — safe to deploy")
				return
			}
			fmt.Printf("still waiting on %d run(s)...\n", len(pending))
		}
	}
}

func runHuman() {
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var request runners.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	encoded, err := readHumanResponse(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, string(encoded))
}

// readHumanResponse opens a TTY, prompts the user, and returns the JSON-encoded response.
func readHumanResponse(request runners.Request) ([]byte, error) {
	tty, ttyOpened := openTTY()
	if ttyOpened {
		defer func() { _ = tty.Close() }()
	}

	reader := bufio.NewReader(tty)

	fmt.Fprintln(os.Stderr, "--- Human input required ---")
	if request.Prompt != "" {
		fmt.Fprintln(os.Stderr, request.Prompt)
	}
	if len(request.Decisions) > 0 {
		fmt.Fprintln(os.Stderr, "Allowed decisions:", strings.Join(request.Decisions, ", "))
	}

	fmt.Fprint(os.Stderr, "Decision: ")
	decision, _ := reader.ReadString('\n')
	fmt.Fprint(os.Stderr, "Message: ")
	message, _ := reader.ReadString('\n')

	response := map[string]any{
		"decision":  strings.TrimSpace(decision),
		"message":   strings.TrimSpace(message),
		"data":      map[string]any{},
		"artifacts": []string{},
	}
	return json.Marshal(response)
}

// openTTY opens /dev/tty for interactive input. Returns os.Stdin and false
// as a fallback when /dev/tty is unavailable.
func openTTY() (*os.File, bool) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return os.Stdin, false
	}
	return f, true
}

// loadEnvFiles loads environment variables from .env files. It checks
// TOIL_ENV_FILE first, then falls back to .env in the current directory.
// Missing files are silently ignored. Existing env vars are never overwritten.
func loadEnvFiles() {
	if path := os.Getenv("TOIL_ENV_FILE"); path != "" {
		if err := config.LoadEnvFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load TOIL_ENV_FILE=%s: %v\n", path, err)
		}
		return
	}
	// Best-effort: load .env from cwd if it exists.
	_ = config.LoadEnvFile(".env")
}

func mustRoot() string {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return root
}

type inputList []string

func (list *inputList) String() string {
	return strings.Join(*list, ",")
}

func (list *inputList) Set(value string) error {
	*list = append(*list, value)
	return nil
}

func (list *inputList) ToMap() (map[string]any, error) {
	inputs := make(map[string]any)
	for _, item := range *list {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid input: %s", item)
		}
		inputs[parts[0]] = parts[1]
	}
	return inputs, nil
}

// printUsage prints the top-level help text.
func printUsage() {
	fmt.Print(`toil - File-defined workflow orchestrator

CONCEPT

  Toil runs workflows defined as YAML graphs of nodes and edges. Each node
  dispatches to a runner (codex, claude, serf, shell, or human); the engine
  resolves expressions, expands ForEach, applies retries and approvals, and
  persists every run to disk as append-only events — no database.

  Most run-management commands are thin HTTP clients against a running
  server (see TOIL_URL); start one with "toil serve".

ENVIRONMENT VARIABLES

  TOIL_URL                     Server address (default http://127.0.0.1:8080).

  TOIL_RUNS_DIR                Override the runs directory.

  TOIL_RUN_NARRATIVE_TIMEOUT   Timeout used by narrative previews (e.g. 30s).

  TOIL_DISABLE_RESTORE         Skip run restore when the server starts.

OPERATING WORKFLOWS

  Authoring, launching, and managing runs, plus running the server.

  Definitions:
    validate
        Validate all workflow and runner definitions.
    workflows list
        List available workflow IDs.
    workflows show <id>
        Print a workflow definition as JSON.

  Runs:
    run <workflow_id> --input key=value
        Start a new run of a workflow.
    resume <run_id>
        Resume a specific paused or waiting run.
    cancel <run_id>
        Cancel an in-flight run.
    runs list [--workflow <id>] [--status <status>] [--limit <n>]
        List runs, optionally filtered by workflow or status.
    runs show <run_id>
        Show a run's status and node states.
    runs events <run_id>
        Print a run's raw event log.
    runs tree <root_run_id>
        Show a run and its sub-runs as a tree.

  Approvals:
    approvals list
        List pending approval requests.
    approvals resolve <id> --decision <decision> --message <message> --comment <comment>
        Resolve a pending approval with a decision.

  Server & daemon:
    serve --addr :8080 [--daemon]
        Start the API and dashboard server.
    pause
        Stop new runs from starting.
    resume
        Re-enable new runs after a pause or drain.
    drain [--dry-run] [--force-cancel] [--wait]
        Pause and wind down in-flight runs.

DEBUGGING & DEVELOPING TOIL

  Introspection tools (used by the debug-run / watch-run skills) and the
  eval harness for testing toil itself.

  Inspection:
    inspect <run-id> [<aspect>]
        Inspect a run (status, events, cost, and more).
    inspect <run-id> <node-id> [<aspect>]
        Inspect a single node's execution.
    interrogate <run-id> <node-id> <question> [--once]
        Ask a node's agent a question (serf runners only).
    visualize workflow <id>
        Print a workflow's graph as JSON.
    visualize run <run_id>
        Print a run's execution graph as JSON.
    narratives preview <run_id> [--intent] [--summary] [--prompt-only] [--include-prompt] [--pretty] [--runs-dir <dir>]
        Generate a run's LLM-written title and summary text.

  Testing:
    eval <id>
        Run the eval spec at tests/eval/<id>.yaml end-to-end: execute its
        workflow for real (live runner/LLM calls), auto-resolve any
        approvals, then run the spec's verify command to decide pass/fail.
        A scenario test of toil itself — non-deterministic, spends real API
        budget, and writes its result to runs/<run-id>/eval.json.

OTHER

  version
      Print the toil version.
  help
      Print this message.
`)
}

// backfillTotals walks every run dir and, for any terminal run with
// no persisted Totals, computes them from events.jsonl and writes
// them back to state.json. Runs once at server start; subsequent
// starts find everything backfilled and skip in microseconds.
//
// PRI-1351: gives existing runs (created before run totals became
// canonical state) a one-shot upgrade so the first dashboard view
// after this change doesn't pay the events.jsonl scan cost per run.
func backfillTotals(runsDir string) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		slog.Warn("toil.backfill.readdir_failed", "error", err)
		return
	}

	processed, backfilled, errs := 0, 0, 0
	start := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, entry.Name())
		statePath := filepath.Join(runDir, "state.json")
		rs, err := state.LoadState(statePath)
		if err != nil {
			slog.Warn("toil.backfill.load_failed", "run_id", entry.Name(), "error", err)
			errs++
			continue
		}

		// Skip if already backfilled.
		if rs.Totals != nil {
			processed++
			continue
		}
		// Skip non-terminal runs — they re-compute on each request and
		// are written by the engine when they reach a terminal status.
		if rs.Status != "completed" && rs.Status != "failed" && rs.Status != "cancelled" {
			processed++
			continue
		}

		if err := engine.FinalizeRunTotals(rs, runDir); err != nil {
			slog.Warn("toil.backfill.finalize_failed", "run_id", entry.Name(), "error", err)
			errs++
			continue
		}
		// FinalizeRunTotals tolerates missing events.jsonl by leaving
		// Totals nil. Don't persist that — it's a no-op write.
		if rs.Totals == nil {
			processed++
			continue
		}
		// Narrow the race: a user could have retriggered the run between
		// our LoadState above and now. If state.json's status changed,
		// don't clobber the retrigger.
		current, err := state.LoadState(statePath)
		if err != nil {
			slog.Warn("toil.backfill.reload_failed", "run_id", entry.Name(), "error", err)
			errs++
			continue
		}
		if current.Status != rs.Status {
			slog.Info("toil.backfill.skipped_status_changed", "run_id", entry.Name(), "from", rs.Status, "to", current.Status)
			processed++
			continue
		}
		// Carry our freshly-computed Totals onto the current state and save.
		current.Totals = rs.Totals
		if err := state.SaveState(statePath, current); err != nil {
			slog.Warn("toil.backfill.save_failed", "run_id", entry.Name(), "error", err)
			errs++
			continue
		}

		processed++
		backfilled++
		if backfilled%100 == 0 {
			slog.Info("toil.backfill.progress", "processed", processed, "backfilled", backfilled, "errs", errs)
		}
	}

	slog.Info("toil.backfill.done",
		"total", processed,
		"backfilled", backfilled,
		"errs", errs,
		"duration", time.Since(start))
}
