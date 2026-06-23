package tgwm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InitOpts configures behavior of Init.
type InitOpts struct {
	Source string // local path or remote URL (empty = fresh repo)
	Slug   string // explicit project ID override
}

// Init initializes or updates the bare repo for a project.
// Returns the project ID and bare repo path.
// On first run: clones or inits. On subsequent runs: fetches.
// Bare repo stored at $root/repos/<project-id>.git
//
// Acquires a per-bare-repo flock so concurrent inits and other
// tgwm operations against the same project serialize.
func Init(root, workDir string, opts InitOpts) (projectID, bareRepoPath string, err error) {
	// Validate local sources up front so downstream git failures surface
	// as actionable errors instead of raw stderr from `git fetch` or
	// `git clone --bare`. Remote URLs and empty source skip this check.
	if opts.Source != "" && !isRemoteURL(opts.Source) {
		if err := validateLocalSource(opts.Source); err != nil {
			return "", "", fmt.Errorf("Init: %w", err)
		}
	}

	projectID, err = resolveProjectID(opts.Source, opts.Slug, workDir)
	if err != nil {
		return "", "", fmt.Errorf("Init: %w", err)
	}

	bareRepoPath = filepath.Join(root, "repos", projectID+".git")

	// Parent dir must exist before we can create the lock file as a
	// sibling of bareRepoPath.
	if err := os.MkdirAll(filepath.Dir(bareRepoPath), 0o755); err != nil {
		return "", "", fmt.Errorf("Init: mkdir: %w", err)
	}

	release, err := acquireBareRepoLock(bareRepoPath)
	if err != nil {
		return "", "", fmt.Errorf("Init: %w", err)
	}
	defer release()

	// If bare repo already exists, fetch if origin is set.
	if _, statErr := os.Stat(bareRepoPath); statErr == nil {
		if _, remoteErr := gitRemoteURL(bareRepoPath, "origin"); remoteErr == nil {
			if err := runGit(bareRepoPath, "fetch", "origin"); err != nil {
				return "", "", fmt.Errorf("Init: fetch: %w", err)
			}
		}
		return projectID, bareRepoPath, nil
	}

	switch {
	case opts.Source == "":
		// Fresh empty bare repo with an initial empty commit on main. Pin the
		// initial branch to "main" so it does not depend on the host/CI git
		// `init.defaultBranch` (which may be "master").
		if err := runGit("", "init", "--bare", "-b", "main", bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: git init --bare: %w", err)
		}
		if err := createEmptyCommitInBare(bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: create empty commit: %w", err)
		}
	case isRemoteURL(opts.Source):
		// Clone from remote URL.
		if err := runGit("", "clone", "--bare", opts.Source, bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: git clone --bare %s: %w", opts.Source, err)
		}
		if err := configureFetchRefspec(bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: configure fetch refspec: %w", err)
		}
	default:
		// Clone from local path, then set origin to source path.
		if err := runGit("", "clone", "--bare", opts.Source, bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: git clone --bare %s: %w", opts.Source, err)
		}
		// git clone --bare sets origin to source; ensure it's the absolute path.
		absSource, absErr := filepath.Abs(opts.Source)
		if absErr != nil {
			absSource = opts.Source
		}
		if err := runGit(bareRepoPath, "remote", "set-url", "origin", absSource); err != nil {
			return "", "", fmt.Errorf("Init: set origin: %w", err)
		}
		if err := configureFetchRefspec(bareRepoPath); err != nil {
			return "", "", fmt.Errorf("Init: configure fetch refspec: %w", err)
		}
	}

	// Ensure the bare repo has a git identity for commits in worktrees.
	if err := configureBareRepoIdentity(bareRepoPath); err != nil {
		return "", "", fmt.Errorf("Init: configure identity: %w", err)
	}

	return projectID, bareRepoPath, nil
}

// configureBareRepoIdentity sets user.name and user.email on the bare repo
// so all worktrees inherit a valid git identity. Only sets if not already
// configured (respects system/global config).
func configureBareRepoIdentity(bareRepoPath string) error {
	// Check if user.name is already set (from system/global config).
	cmd := exec.Command("git", "-C", bareRepoPath, "config", "user.name")
	if err := cmd.Run(); err != nil {
		// Not set — configure defaults.
		if err := runGit(bareRepoPath, "config", "user.name", "Toil"); err != nil {
			return err
		}
	}
	cmd = exec.Command("git", "-C", bareRepoPath, "config", "user.email")
	if err := cmd.Run(); err != nil {
		if err := runGit(bareRepoPath, "config", "user.email", "toil@localhost"); err != nil {
			return err
		}
	}
	return nil
}

// isRemoteURL reports whether source looks like a remote git URL.
func isRemoteURL(source string) bool {
	for _, prefix := range []string{"http://", "https://", "git@", "ssh://"} {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

// createEmptyCommitInBare creates an initial empty commit on the bare
// repo's default branch (whatever HEAD points at after `git init --bare`,
// which honors init.defaultBranch — typically main, but master on older
// configs). After this call, HEAD is meaningful and downstream tgwm
// operations can use defaultBranchFor to resolve it.
func createEmptyCommitInBare(bareRepoPath string) error {
	tmpDir, err := os.MkdirTemp("", "tgwm-init-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	branch := defaultBranchFor(bareRepoPath)
	if err := runGit(bareRepoPath, "worktree", "add", "--orphan", "-b", branch, tmpDir); err != nil {
		return fmt.Errorf("worktree add: %w", err)
	}
	defer runGit(bareRepoPath, "worktree", "remove", "--force", tmpDir) //nolint:errcheck

	// Configure git identity for the commit.
	if err := runGit(tmpDir, "config", "user.email", "toil@localhost"); err != nil {
		return fmt.Errorf("config email: %w", err)
	}
	if err := runGit(tmpDir, "config", "user.name", "Toil"); err != nil {
		return fmt.Errorf("config name: %w", err)
	}

	// Create an empty commit.
	if err := runGit(tmpDir, "commit", "--allow-empty", "-m", "Initial empty commit"); err != nil {
		return fmt.Errorf("empty commit: %w", err)
	}

	return nil
}

// configureFetchRefspec adds a heads-to-heads fetch refspec to the origin remote
// so that git fetch origin updates local branch refs in the bare repo.
func configureFetchRefspec(bareRepoPath string) error {
	return runGit(bareRepoPath, "config", "--add", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
}

// gitOutput runs a git command and returns its stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs a git command in dir (may be empty for commands without a working dir).
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// slugFromRemoteURL extracts a project slug from a git remote URL.
// Includes the hostname to prevent collisions across git hosts.
// "https://github.com/acme/todo-cli.git" → "github-com-acme-todo-cli"
// "git@github.com:acme/todo-cli.git" → "github-com-acme-todo-cli"
func slugFromRemoteURL(rawURL string) string {
	var host, path string

	if !strings.Contains(rawURL, "://") {
		// scp-style: git@github.com:owner/repo.git
		// Extract host from user@host:path
		u := rawURL
		if atIdx := strings.Index(u, "@"); atIdx != -1 {
			u = u[atIdx+1:] // strip user@
		}
		if colonIdx := strings.Index(u, ":"); colonIdx != -1 {
			host = u[:colonIdx]
			path = u[colonIdx+1:]
		} else {
			path = u
		}
	} else {
		// scheme://[user@]host/path
		// Strip scheme
		u := rawURL
		if schemeIdx := strings.Index(u, "://"); schemeIdx != -1 {
			u = u[schemeIdx+3:]
		}
		// Strip user@ if present
		if atIdx := strings.Index(u, "@"); atIdx != -1 {
			// Only strip if @ comes before the first /
			if slashIdx := strings.Index(u, "/"); slashIdx == -1 || atIdx < slashIdx {
				u = u[atIdx+1:]
			}
		}
		// Split host/path
		if slashIdx := strings.Index(u, "/"); slashIdx != -1 {
			host = u[:slashIdx]
			path = u[slashIdx+1:]
		} else {
			host = u
		}
	}

	// Strip port from host (e.g., github.com:443 → github.com)
	if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}

	// Strip .git suffix from path
	path = strings.TrimSuffix(path, ".git")

	// Replace dots in host with hyphens, join host and path segments with hyphens
	hostSlug := strings.ReplaceAll(host, ".", "-")
	pathSegments := strings.Split(strings.Trim(path, "/"), "/")
	return hostSlug + "-" + strings.Join(pathSegments, "-")
}

// validateLocalSource checks that a local --source path exists and is a
// git repository. Returns an actionable error otherwise so the caller
// doesn't see raw git stderr from a later clone/fetch. Skipped for
// remote URLs (they're validated by the clone itself) and empty source
// (the "fresh empty bare repo" branch doesn't need a source).
func validateLocalSource(source string) error {
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source path %q does not exist", source)
		}
		return fmt.Errorf("stat source %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path %q is not a directory", source)
	}
	// Use git rev-parse so worktrees (which have .git as a file, not
	// a directory) and non-standard layouts are accepted.
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = source
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("source path %q is not a git repository — initialize it with 'git init' first, or pass --source as a remote URL", source)
	}
	return nil
}

// slugFromLocalPath derives a project slug from a local filesystem path.
// The path is resolved through symlinks and made absolute, then path
// separators are replaced with hyphens. This ensures that different paths
// with the same basename (e.g., /tmp/app and ~/app) produce distinct slugs,
// and that symlinks resolve to the same slug as their target.
func slugFromLocalPath(source string) (string, error) {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		resolved = source
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	slug := strings.ReplaceAll(abs, string(filepath.Separator), "-")
	slug = strings.TrimLeft(slug, "-")
	return slug, nil
}

// resolveProjectID determines the project ID using the precedence chain:
//  1. source is a remote URL → slug from URL (canonical, not overridable)
//  2. source is a local repo with origin remote → slug from origin URL
//  3. slug flag is non-empty → use that (explicit user override)
//  4. source is a local path without origin → slug from resolved path
//  5. workDir is non-empty → basename of workDir
//  6. error
//
// source: path to a local git repo or remote URL (may be empty).
// slug: explicit --slug value (may be empty).
// workDir: TOIL_CURRENT_WORKFLOW_DIR fallback (may be empty).
func resolveProjectID(source, slug, workDir string) (string, error) {
	if source != "" {
		if isRemoteURL(source) {
			return slugFromRemoteURL(source), nil
		}
		if remoteURL, err := gitRemoteURL(source, "origin"); err == nil && remoteURL != "" {
			// origin may be a remote URL (github) or a local path (for
			// bare repos cloned from a local source — tgwm.Init sets
			// origin to the absolute source path). Route each through
			// its matching slug function so the slug is stable whether
			// we derive it from the source itself (slugFromLocalPath)
			// or from a worktree's inherited origin (this path).
			if isRemoteURL(remoteURL) {
				return slugFromRemoteURL(remoteURL), nil
			}
			return slugFromLocalPath(remoteURL)
		}
	}

	if slug != "" {
		return slug, nil
	}

	if source != "" {
		return slugFromLocalPath(source)
	}

	if workDir != "" {
		return filepath.Base(workDir), nil
	}

	return "", fmt.Errorf("cannot determine project ID: no git origin, no --slug flag, and no TOIL_CURRENT_WORKFLOW_DIR")
}

// gitRemoteURL returns the URL of the named remote in the given repo directory.
func gitRemoteURL(repoDir, remote string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
