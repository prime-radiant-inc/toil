package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"primeradiant.com/toil/internal/tgwm"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage()
	case "init":
		runInit(os.Args[2:])
	case "worktree":
		runWorktree(os.Args[2:])
	case "merge":
		runMerge(os.Args[2:])
	case "cleanup":
		runCleanup(os.Args[2:])
	case "prune":
		runPrune(os.Args[2:])
	case "export":
		runExport(os.Args[2:])
	case "push":
		runPush(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "tgwm: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// printUsage prints the top-level help text.
func printUsage() {
	fmt.Print(`tgwm - Toil Git Worktree Manager

CONCEPT

  tgwm manages a bare git repository as the source of truth for a project,
  with lightweight worktrees as disposable working copies for each workflow
  run. Each run gets its own branch (run/<name>), agents work in isolation,
  and finished branches are merged back into the default branch.

  Workflow:

    1. Run "tgwm init --source <path>" once per project. tgwm creates the
       bare repo at $TOIL_ROOT/repos/<slug>.git and prints its absolute
       path on stdout. Capture that path:

           REPO=$(tgwm init --source /path/to/project)

    2. Pass --repo "$REPO" to every subsequent tgwm command. The bare
       repo path is the canonical handle — there is no implicit lookup,
       no slug guessing, no env-var fallback.

  Concurrent operations against the same bare repo serialize on a
  per-bare-repo flock; callers don't coordinate.

ENVIRONMENT VARIABLES

  TOIL_ROOT                    Root directory for toil data. Used by
                               "tgwm init" to compute where to place
                               <slug>.git.

  TOIL_CURRENT_WORKFLOW_DIR    Working directory for the current workflow
                               execution. Worktrees are created under
                               $TOIL_CURRENT_WORKFLOW_DIR/worktrees/<name>.

COMMANDS

  init [--source <path-or-url>] [--slug <id>]
      Initialize or update a bare repo. Prints the bare repo path on
      stdout; capture it and pass forward as --repo.

  worktree create <name> --repo <path> [--base <branch>]
      Create a new worktree and branch for a run.

  worktree path <name>
      Print the filesystem path of a named worktree.

  worktree destroy <name> --repo <path> [--keep-branch]
      Atomically remove a worktree directory and its branch.

  merge <name> --repo <path> [--target <branch>] [--keep-on-conflict]
      Merge run/<name> into the target branch.

  cleanup <name> --repo <path> [--keep-on-failure]
      Remove a worktree (branch survives in the bare repo).

  prune <pattern> --repo <path>
      Destroy every worktree whose name matches a glob pattern.

  export <path> --repo <path>
      Check out the default branch to a destination path.

  push <remote> --repo <path> [--branch <name>]
      Push a branch from the bare repo to a remote.

  status --repo <path>
      Show project state: bare repo, worktrees, and merge status.

  help
      Print this message.

Run "tgwm <command> --help" for details on a specific command.
`)
}

// mustRoot reads TOIL_ROOT or exits.
func mustRoot() string {
	root, err := tgwm.ToilRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgwm: "+err.Error())
		os.Exit(1)
	}
	return root
}

// mustWorkflowDir reads TOIL_CURRENT_WORKFLOW_DIR or exits.
func mustWorkflowDir() string {
	dir, err := tgwm.WorkflowDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgwm: "+err.Error())
		os.Exit(1)
	}
	return dir
}

// die prints msg to stderr and exits 1.
func die(msg string) {
	fmt.Fprintln(os.Stderr, "tgwm: "+msg)
	os.Exit(1)
}

// dieErr prints err to stderr and exits 1.
func dieErr(err error) {
	die(err.Error())
}

// --- init ---

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm init - Initialize or update the bare git repo for this project

USAGE
  tgwm init [--source <path-or-url>] [--slug <id>]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Creates a bare repo at $TOIL_ROOT/repos/<project-id>.git. If the bare
  repo already exists, fetches from origin (if configured). Safe to run
  multiple times.

  The project ID is resolved in order:
    1. From origin remote URL of --source repo (e.g. "acme-todo-cli")
    2. From --slug flag
    3. From basename of TOIL_CURRENT_WORKFLOW_DIR

EXAMPLE
  TOIL_ROOT=/data/toil TOIL_CURRENT_WORKFLOW_DIR=/data/toil/runs/abc \
    tgwm init --source https://github.com/acme/todo-cli.git
`)
	}
	source := fs.String("source", "", "local path or remote URL of source git repo (empty = fresh repo)")
	slug := fs.String("slug", "", "explicit project ID override")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	root := mustRoot()
	workDir, _ := tgwm.WorkflowDir() // optional for init

	projectID, bareRepoPath, err := tgwm.Init(root, workDir, tgwm.InitOpts{
		Source: *source,
		Slug:   *slug,
	})
	if err != nil {
		dieErr(err)
	}

	fmt.Fprintf(os.Stderr, "Initialized %s at %s\n", projectID, bareRepoPath)
	// Stdout is the machine-readable channel for callers that capture
	// `REPO=$(tgwm init --source ...)` and pass it forward via --repo.
	fmt.Println(bareRepoPath)
}

// mustRepo returns the bare repo path from --repo or exits with a
// clear error. Every operation except `tgwm init` requires --repo;
// callers obtain the path from `tgwm init --source <path>`'s stdout
// at the start of a workflow and thread it forward. tgwm has no
// implicit slug-resolution path — there is exactly one way to
// identify the bare repo.
func mustRepo(repoFlag string) string {
	if repoFlag == "" {
		die("--repo is required (capture from `tgwm init` stdout)")
	}
	return repoFlag
}

// --- worktree ---

func runWorktree(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "tgwm worktree: subcommand required (create, path, destroy)")
		fmt.Fprintln(os.Stderr, "Run 'tgwm help' for usage.")
		os.Exit(1)
	}

	switch args[0] {
	case "create":
		runWorktreeCreate(args[1:])
	case "path":
		runWorktreePath(args[1:])
	case "destroy":
		runWorktreeDestroy(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "tgwm worktree: unknown subcommand %q\n", args[0])
		fmt.Fprintln(os.Stderr, "Run 'tgwm help' for usage.")
		os.Exit(1)
	}
}

func runWorktreeCreate(args []string) {
	fs := flag.NewFlagSet("worktree create", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm worktree create - Create a new worktree and branch for a run

USAGE
  tgwm worktree create <name> --repo <path> [--base <branch>] [--force]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Creates a worktree at $TOIL_CURRENT_WORKFLOW_DIR/worktrees/<name>
  and a branch named run/<name> based off <base> (default: the bare
  repo's HEAD). Prints the worktree path to stdout — capture it in a
  script.

  With --force, any existing worktree directory and branch matching
  the same name are destroyed before creation. Use when re-running
  work that may have left stale state behind (e.g., a re-planned
  task reusing an ID whose previous attempt's branch is preserved).

  Concurrent worktree creates against the same bare repo serialize
  internally via a per-bare-repo flock — no caller coordination needed.

EXAMPLE
  REPO=$(tgwm init --source /path/to/proj)
  TOIL_CURRENT_WORKFLOW_DIR=/data/toil/runs/abc \
    tgwm worktree create --repo "$REPO" my-run
`)
	}
	base := fs.String("base", "", "branch to base new branch off (default: main)")
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")
	force := fs.Bool("force", false, "destroy any existing worktree+branch with this name first")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm worktree create: <name> is required")
		fs.Usage()
		os.Exit(1)
	}
	name := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)
	workDir := mustWorkflowDir()

	path, err := tgwm.WorktreeCreate(workDir, bareRepoPath, name, tgwm.WorktreeOpts{
		Base:  *base,
		Force: *force,
	})
	if err != nil {
		dieErr(err)
	}

	fmt.Println(path)
}

func runWorktreePath(args []string) {
	fs := flag.NewFlagSet("worktree path", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm worktree path - Print the filesystem path of a named worktree

USAGE
  tgwm worktree path <name>

DESCRIPTION
  Prints the absolute path to $TOIL_CURRENT_WORKFLOW_DIR/worktrees/<name>.
  Exits with an error if the worktree does not exist.

EXAMPLE
  TOIL_ROOT=/data/toil TOIL_CURRENT_WORKFLOW_DIR=/data/toil/runs/abc \
    tgwm worktree path my-run
`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm worktree path: <name> is required")
		fs.Usage()
		os.Exit(1)
	}
	name := fs.Arg(0)

	workDir := mustWorkflowDir()

	path, err := tgwm.WorktreePath(workDir, name)
	if err != nil {
		dieErr(err)
	}

	fmt.Println(path)
}

func runWorktreeDestroy(args []string) {
	fs := flag.NewFlagSet("worktree destroy", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm worktree destroy - Remove a worktree directory and delete its branch

USAGE
  tgwm worktree destroy <name> [--repo <path>] [--keep-branch]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Removes the worktree at $TOIL_CURRENT_WORKFLOW_DIR/worktrees/<name>
  AND deletes the run/<name> branch in the bare repo. Atomic under a
  per-bare-repo lock.

  Default behavior is force — task branches are ephemeral by design
  and the caller is responsible for ensuring any merge happened
  first. Use --keep-branch to preserve the branch.

  Use 'tgwm cleanup' instead if you want to remove only the worktree
  and keep the branch around.

EXAMPLE
  tgwm worktree destroy --repo "$REPO" comp-1-task-0
`)
	}
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")
	keepBranch := fs.Bool("keep-branch", false, "keep the branch in the bare repo (default: delete)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm worktree destroy: <name> is required")
		fs.Usage()
		os.Exit(1)
	}
	name := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)
	workDir := mustWorkflowDir()

	if err := tgwm.WorktreeDestroy(workDir, bareRepoPath, name, tgwm.DestroyOpts{
		KeepBranch: *keepBranch,
	}); err != nil {
		dieErr(err)
	}

	fmt.Fprintf(os.Stderr, "Destroyed %s\n", name)
}

// --- merge ---

func runMerge(args []string) {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	target := fs.String("target", "", "branch to merge into (default: main)")
	keepOnConflict := fs.Bool("keep-on-conflict", false, "leave temp worktree on conflict instead of cleaning up")
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")
	fs.Usage = func() {
		fmt.Print(`tgwm merge - Merge the named worktree's branch into a target branch

USAGE
  tgwm merge [--target <branch>] [--keep-on-conflict] [--repo <path>] <name>

DESCRIPTION
  Merges run/<name> into the target branch (default: main) in the bare repo
  using --no-ff (always creates a merge commit). Uses a temporary worktree
  internally when the target branch is not checked out; if it is (e.g., a
  parent component worktree during per-task merges), merges in that worktree
  instead.

  With --keep-on-conflict, the temporary worktree is left on disk when a
  conflict occurs, and its path is printed to stderr as "Conflict worktree: <path>".
  When merging into an already-checked-out worktree, conflicts are aborted
  immediately and --keep-on-conflict is ignored.

  Concurrent merges against the same bare repo serialize internally via a
  per-bare-repo flock — no caller coordination needed.

EXAMPLE
  REPO=$(tgwm init --source /path/to/proj)
  tgwm merge --repo "$REPO" my-run
  tgwm merge --repo "$REPO" --target integration my-run
  tgwm merge --repo "$REPO" --target run/comp-1 comp-1-task-0
`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm merge: <name> is required")
		fs.Usage()
		os.Exit(1)
	}
	name := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)

	opts := tgwm.MergeOpts{Target: *target, KeepOnConflict: *keepOnConflict}
	result, err := tgwm.MergeWithResult(bareRepoPath, name, opts)
	if err != nil {
		if result.WorktreePath != "" {
			fmt.Fprintf(os.Stderr, "Conflict worktree: %s\n", result.WorktreePath)
		}
		dieErr(err)
	}

	targetName := opts.Target
	if targetName == "" {
		targetName = "main"
	}
	fmt.Fprintf(os.Stderr, "Merged %s into %s\n", name, targetName)
}

// --- cleanup ---

func runCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm cleanup - Remove a worktree after a run completes

USAGE
  tgwm cleanup <name> [--keep-on-failure]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Removes the worktree at $TOIL_CURRENT_WORKFLOW_DIR/worktrees/<name>.
  The branch and commits remain in the bare repo.

  With --keep-on-failure, the worktree is only removed if run/<name> has
  been merged into main. This is useful in cleanup steps that run regardless
  of run outcome — the worktree is preserved when the run failed so you can
  inspect it.

EXAMPLE
  TOIL_ROOT=/data/toil TOIL_CURRENT_WORKFLOW_DIR=/data/toil/runs/abc \
    tgwm cleanup my-run --keep-on-failure
`)
	}
	keepOnFailure := fs.Bool("keep-on-failure", false, "only remove if branch is merged into main")
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm cleanup: <name> is required")
		fs.Usage()
		os.Exit(1)
	}
	name := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)
	workDir := mustWorkflowDir()

	if err := tgwm.Cleanup(workDir, bareRepoPath, name, tgwm.CleanupOpts{
		KeepOnFailure: *keepOnFailure,
	}); err != nil {
		dieErr(err)
	}

	fmt.Fprintf(os.Stderr, "Cleaned up %s\n", name)
}

// --- prune ---

func runPrune(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm prune - Destroy every worktree whose name matches a glob pattern

USAGE
  tgwm prune <pattern> [--repo <path>]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Lists every worktree on the bare repo, matches each name against
  <pattern> (filepath.Match — * matches non-separator chars, ? matches
  one), and destroys the matching ones (worktree directory + branch,
  same as "tgwm worktree destroy").

  Best-effort: failed destroys are recorded but don't abort the prune.

  Pattern is matched against worktree names (e.g., "comp-x-parent-1-
  task-0"), not full paths. Quote the pattern in your shell so * isn't
  expanded against the local filesystem.

  Use cases: tearing down task worktrees from a canceled implement_spec run,
  reclaiming space after a series of failed builds, scripted cleanup.

EXAMPLE
  # Clean up all task worktrees from a canceled run
  tgwm prune "implementation-canceled-run-id-*"

  # Clean up everything from a particular component
  tgwm prune --repo "$REPO" "frontend-*"
`)
	}
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm prune: <pattern> is required")
		fs.Usage()
		os.Exit(1)
	}
	pattern := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)
	workDir := mustWorkflowDir()

	result, err := tgwm.Prune(workDir, bareRepoPath, pattern)
	for _, name := range result.Destroyed {
		fmt.Fprintf(os.Stderr, "Pruned %s\n", name)
	}
	if err != nil {
		dieErr(err)
	}
	if len(result.Destroyed) == 0 {
		fmt.Fprintf(os.Stderr, "No worktrees matched %q.\n", pattern)
	}
}

// --- export ---

func runExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm export - Check out main from the bare repo to a destination path

USAGE
  tgwm export <path> [--repo <path>]

DESCRIPTION
  Clones main from the bare repo to <path> as a regular (non-bare) git
  repository. Useful for handing off a clean checkout to downstream tools.

EXAMPLE
  TOIL_ROOT=/data/toil \
    tgwm export /tmp/my-project-export
`)
	}
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm export: <path> is required")
		fs.Usage()
		os.Exit(1)
	}
	destPath := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)

	if err := tgwm.Export(bareRepoPath, destPath); err != nil {
		dieErr(err)
	}

	fmt.Fprintf(os.Stderr, "Exported main to %s\n", destPath)
}

// --- push ---

func runPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm push - Push a branch from the bare repo to a remote

USAGE
  tgwm push <remote> [--branch <name>]

FLAGS
`)
		fs.PrintDefaults()
		fmt.Print(`
DESCRIPTION
  Pushes <branch> (default: main) from the bare repo to <remote>.
  <remote> may be a URL or the name of a configured remote (e.g. "origin").

EXAMPLE
  TOIL_ROOT=/data/toil \
    tgwm push origin --branch main
`)
	}
	branch := fs.String("branch", "", "branch to push (default: main)")
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "tgwm push: <remote> is required")
		fs.Usage()
		os.Exit(1)
	}
	remote := fs.Arg(0)

	bareRepoPath := mustRepo(*repo)

	effectiveBranch := *branch
	if effectiveBranch == "" {
		effectiveBranch = "main"
	}

	if err := tgwm.Push(bareRepoPath, remote, *branch); err != nil {
		dieErr(err)
	}

	fmt.Fprintf(os.Stderr, "Pushed %s to %s\n", effectiveBranch, remote)
}

// --- status ---

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`tgwm status - Show project state: bare repo, worktrees, and merge status

USAGE
  tgwm status [--repo <path>]

DESCRIPTION
  Scans $TOIL_ROOT/repos/ for the bare repo and reports all active worktrees
  with their branch and merge status.

EXAMPLE
  TOIL_ROOT=/data/toil tgwm status
`)
	}
	repo := fs.String("repo", "", "bare repo path (required; capture from `tgwm init`)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	bareRepoPath := mustRepo(*repo)

	info, err := tgwm.Status(bareRepoPath)
	if err != nil {
		dieErr(err)
	}

	fmt.Printf("Project:  %s\n", info.ProjectID)
	fmt.Printf("Bare repo: %s\n", info.BareRepo)
	fmt.Println()

	if len(info.Worktrees) == 0 {
		fmt.Println("No active worktrees.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBRANCH\tMERGED\tPATH")
	for _, wt := range info.Worktrees {
		merged := "no"
		if wt.Merged {
			merged = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", wt.Name, wt.Branch, merged, wt.Path)
	}
	if err := w.Flush(); err != nil {
		dieErr(err)
	}
}
