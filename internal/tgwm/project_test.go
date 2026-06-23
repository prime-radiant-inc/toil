package tgwm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInit_LocalSourceNotGitRepo_ReturnsClearError pins the user-facing
// error when --source points at a directory that exists but isn't a git
// repository. Previously this failed deep inside `git fetch origin` with
// an obscure "does not appear to be a git repository" message leaked
// from stderr. Init now validates up front and returns a direct error
// that says what's wrong and how to fix it.
func TestInit_LocalSourceNotGitRepo_ReturnsClearError(t *testing.T) {
	root := t.TempDir()
	// Create a plain directory — exists, but no .git.
	source := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := Init(root, "", InitOpts{Source: source})
	if err == nil {
		t.Fatal("expected error when source is not a git repo; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not a git repository") {
		t.Errorf("error should name the problem (%q not found in %q)", "not a git repository", msg)
	}
	if !strings.Contains(msg, source) {
		t.Errorf("error should name the path (%q not in %q)", source, msg)
	}
	if !strings.Contains(msg, "git init") {
		t.Errorf("error should suggest a fix (%q not in %q)", "git init", msg)
	}
}

// TestInit_LocalSourceMissing_ReturnsClearError covers the related case
// where --source points at a directory that doesn't exist at all.
func TestInit_LocalSourceMissing_ReturnsClearError(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "does-not-exist")

	_, _, err := Init(root, "", InitOpts{Source: source})
	if err == nil {
		t.Fatal("expected error when source directory doesn't exist; got nil")
	}
	if !strings.Contains(err.Error(), source) {
		t.Errorf("error should name the missing path; got %q", err.Error())
	}
}

// TestDefaultBranchFor pins that tgwm reads HEAD from the bare repo
// rather than hardcoding "main". Projects shipped on master, develop,
// trunk, or a renamed default branch must work without tweaking tgwm.
func TestDefaultBranchFor(t *testing.T) {
	t.Run("reads HEAD from bare repo", func(t *testing.T) {
		_, bareRepoPath := setupBareRepo(t)
		if got := defaultBranchFor(bareRepoPath); got != "main" {
			t.Errorf("default branch = %q, want %q", got, "main")
		}
	})

	t.Run("respects renamed default branch", func(t *testing.T) {
		_, bareRepoPath := setupBareRepo(t)
		// Rename main to trunk and update HEAD.
		localGitOutput(t, bareRepoPath, "branch", "-m", "main", "trunk")
		if got := defaultBranchFor(bareRepoPath); got != "trunk" {
			t.Errorf("default branch = %q, want %q after rename", got, "trunk")
		}
	})

	t.Run("falls back to main when bare repo path is empty", func(t *testing.T) {
		if got := defaultBranchFor(""); got != "main" {
			t.Errorf("default branch = %q, want %q", got, "main")
		}
	})
}

// TestMergeIntoRenamedDefault confirms that a non-main default branch
// is the merge target when no --target flag is given. Previously every
// tgwm operation hardcoded "main"; renaming the default broke merges.
func TestMergeIntoRenamedDefault(t *testing.T) {
	workDir, bareRepoPath := setupBareRepo(t)
	localGitOutput(t, bareRepoPath, "branch", "-m", "main", "trunk")

	wt, err := WorktreeCreate(workDir, bareRepoPath, "feature", WorktreeOpts{})
	if err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}
	addCommit(t, wt, "feature.txt", "feature work", "add feature.txt")

	if err := Merge(bareRepoPath, "feature", MergeOpts{}); err != nil {
		t.Fatalf("Merge into default-rename trunk: %v", err)
	}

	out, err := gitOutput(bareRepoPath, "show", "trunk:feature.txt")
	if err != nil {
		t.Fatalf("git show trunk:feature.txt: %v", err)
	}
	if strings.TrimSpace(out) != "feature work" {
		t.Errorf("trunk:feature.txt = %q, want %q", strings.TrimSpace(out), "feature work")
	}
}

// makeRepoWithCommit creates a non-bare repo with one commit and returns its path.
func makeRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Force the initial branch to "main" so tests are hermetic regardless of
	// the host/CI git `init.defaultBranch` (CI defaults to "master").
	localGitOutput(t, dir, "init", "-b", "main")
	localGitOutput(t, dir, "config", "user.email", "test@example.com")
	localGitOutput(t, dir, "config", "user.name", "Test User")
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("makeRepoWithCommit: write README: %v", err)
	}
	localGitOutput(t, dir, "add", "README.md")
	localGitOutput(t, dir, "commit", "-m", "initial commit")
	return dir
}

// addCommit adds a file and commits it in the given repo.
func addCommit(t *testing.T, repoDir, filename, content, message string) {
	t.Helper()
	path := filepath.Join(repoDir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("addCommit: write %s: %v", filename, err)
	}
	localGitOutput(t, repoDir, "add", filename)
	localGitOutput(t, repoDir, "commit", "-m", message)
}

// bareHasRef returns true if the bare repo has the given ref.
func bareHasRef(t *testing.T, bareDir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Dir = bareDir
	err := cmd.Run()
	return err == nil
}

// localGitOutput runs a git command in a directory for package-internal tests.
func localGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, stderr)
	}
	return string(out)
}

// initRepoWithOrigin creates a repo with an "origin" remote URL set.
func initRepoWithOrigin(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := makeRepoWithCommit(t)
	localGitOutput(t, dir, "remote", "add", "origin", remoteURL)
	return dir
}

func TestSlugFromRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "HTTPS URL with .git suffix",
			url:  "https://github.com/acme/todo-cli.git",
			want: "github-com-acme-todo-cli",
		},
		{
			name: "HTTPS URL without .git suffix",
			url:  "https://github.com/acme/todo-cli",
			want: "github-com-acme-todo-cli",
		},
		{
			name: "SSH URL with .git suffix",
			url:  "git@github.com:acme/todo-cli.git",
			want: "github-com-acme-todo-cli",
		},
		{
			name: "SSH URL with ssh:// scheme",
			url:  "ssh://git@github.com/acme/todo-cli.git",
			want: "github-com-acme-todo-cli",
		},
		{
			name: "different hosts produce different slugs",
			url:  "https://gitlab.com/acme/todo-cli.git",
			want: "gitlab-com-acme-todo-cli",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugFromRemoteURL(tt.url)
			if got != tt.want {
				t.Errorf("slugFromRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestSlugFromLocalPath(t *testing.T) {
	t.Run("absolute path becomes hyphen-separated slug", func(t *testing.T) {
		got, err := slugFromLocalPath("/tmp/sample-app")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// On macOS /tmp resolves to /private/tmp via symlink.
		// Accept either form.
		if got != "tmp-sample-app" && got != "private-tmp-sample-app" {
			t.Errorf("got %q, want %q or %q", got, "tmp-sample-app", "private-tmp-sample-app")
		}
	})

	t.Run("different paths with same basename produce different slugs", func(t *testing.T) {
		dirA := t.TempDir()
		dirB := t.TempDir()

		slugA, err := slugFromLocalPath(dirA)
		if err != nil {
			t.Fatalf("slugA: %v", err)
		}
		slugB, err := slugFromLocalPath(dirB)
		if err != nil {
			t.Fatalf("slugB: %v", err)
		}
		if slugA == slugB {
			t.Errorf("different paths produced same slug: %q", slugA)
		}
	})

	t.Run("symlink resolves to same slug as target", func(t *testing.T) {
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		slugTarget, err := slugFromLocalPath(target)
		if err != nil {
			t.Fatalf("target: %v", err)
		}
		slugLink, err := slugFromLocalPath(link)
		if err != nil {
			t.Fatalf("link: %v", err)
		}
		if slugTarget != slugLink {
			t.Errorf("target slug %q != link slug %q", slugTarget, slugLink)
		}
	})
}

func TestResolveProjectID(t *testing.T) {
	t.Run("source repo with origin uses slug from remote", func(t *testing.T) {
		dir := initRepoWithOrigin(t, "https://github.com/acme/todo-cli.git")
		got, err := resolveProjectID(dir, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "github-com-acme-todo-cli" {
			t.Errorf("got %q, want %q", got, "github-com-acme-todo-cli")
		}
	})

	t.Run("local source without origin uses path-based slug", func(t *testing.T) {
		dir := makeRepoWithCommit(t)
		got, err := resolveProjectID(dir, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should be derived from the resolved absolute path, not empty or "workflow"
		want, _ := slugFromLocalPath(dir)
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("explicit slug overrides local source path", func(t *testing.T) {
		dir := makeRepoWithCommit(t)
		got, err := resolveProjectID(dir, "my-slug", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "my-slug" {
			t.Errorf("got %q, want %q", got, "my-slug")
		}
	})

	t.Run("no source + explicit slug uses slug", func(t *testing.T) {
		got, err := resolveProjectID("", "my-project", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "my-project" {
			t.Errorf("got %q, want %q", got, "my-project")
		}
	})

	t.Run("no source + no slug uses basename of workDir", func(t *testing.T) {
		workDir := "/some/path/my-workflow"
		got, err := resolveProjectID("", "", workDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "my-workflow" {
			t.Errorf("got %q, want %q", got, "my-workflow")
		}
	})

	t.Run("all empty returns error", func(t *testing.T) {
		_, err := resolveProjectID("", "", "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestInit(t *testing.T) {
	t.Run("local repo with commit creates bare repo", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		projectID, bareRepoPath, err := Init(root, "", InitOpts{Source: source, Slug: "local-proj"})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if projectID != "local-proj" {
			t.Errorf("projectID = %q, want %q", projectID, "local-proj")
		}
		wantBare := filepath.Join(root, "repos", "local-proj.git")
		if bareRepoPath != wantBare {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, wantBare)
		}
		// Bare repo should exist and contain the source's commits
		if !bareHasRef(t, bareRepoPath, "HEAD") {
			t.Error("bare repo has no HEAD ref")
		}
		// Verify the origin remote points to source (absolute path)
		absSource, _ := filepath.Abs(source)
		originURL, err := gitRemoteURL(bareRepoPath, "origin")
		if err != nil {
			t.Fatalf("gitRemoteURL: %v", err)
		}
		if originURL != absSource {
			t.Errorf("origin = %q, want %q", originURL, absSource)
		}
	})

	t.Run("empty source creates bare repo with main branch", func(t *testing.T) {
		root := t.TempDir()
		workDir := "/some/path/my-project"

		projectID, bareRepoPath, err := Init(root, workDir, InitOpts{})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if projectID != "my-project" {
			t.Errorf("projectID = %q, want %q", projectID, "my-project")
		}
		wantBare := filepath.Join(root, "repos", "my-project.git")
		if bareRepoPath != wantBare {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, wantBare)
		}
		// Should have a main branch with an empty commit
		if !bareHasRef(t, bareRepoPath, "refs/heads/main") {
			t.Error("bare repo has no main branch")
		}
	})

	t.Run("second call is idempotent (fetch on existing)", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()
		workDir := "/some/path/my-project"

		_, _, err := Init(root, workDir, InitOpts{Source: source, Slug: "my-project"})
		if err != nil {
			t.Fatalf("first Init: %v", err)
		}

		// Second call should succeed without error
		projectID, bareRepoPath, err := Init(root, workDir, InitOpts{Source: source, Slug: "my-project"})
		if err != nil {
			t.Fatalf("second Init: %v", err)
		}
		if projectID != "my-project" {
			t.Errorf("projectID = %q, want %q", projectID, "my-project")
		}
		wantBare := filepath.Join(root, "repos", "my-project.git")
		if bareRepoPath != wantBare {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, wantBare)
		}
	})

	t.Run("fetch picks up new commits from source", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		_, bareRepoPath, err := Init(root, "", InitOpts{Source: source, Slug: "test-proj"})
		if err != nil {
			t.Fatalf("first Init: %v", err)
		}

		// Add a new commit to source
		addCommit(t, source, "new.txt", "new content", "second commit")
		newSHA := strings.TrimSpace(localGitOutput(t, source, "rev-parse", "HEAD"))

		// Second Init should fetch the new commit
		_, _, err = Init(root, "", InitOpts{Source: source, Slug: "test-proj"})
		if err != nil {
			t.Fatalf("second Init: %v", err)
		}

		// After fetch, the new commit should be in the bare repo on refs/heads/main.
		gotSHA := strings.TrimSpace(localGitOutput(t, bareRepoPath, "rev-parse", "refs/heads/main"))
		if gotSHA != newSHA {
			t.Errorf("refs/heads/main = %s, want %s", gotSHA, newSHA)
		}
	})

	t.Run("explicit slug overrides derived ID", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		projectID, bareRepoPath, err := Init(root, "", InitOpts{Source: source, Slug: "custom-slug"})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if projectID != "custom-slug" {
			t.Errorf("projectID = %q, want %q", projectID, "custom-slug")
		}
		wantBare := filepath.Join(root, "repos", "custom-slug.git")
		if bareRepoPath != wantBare {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, wantBare)
		}
	})
}

// TestInitReturnsBareRepoPath pins that Init's returned path is the
// canonical handle every other tgwm operation needs. There is no
// implicit lookup — callers capture this path and thread it through
// as --repo.
func TestInitReturnsBareRepoPath(t *testing.T) {
	t.Run("returns absolute path under TOIL_ROOT/repos/", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		projectID, bareRepoPath, err := Init(root, "", InitOpts{Source: source})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		want := filepath.Join(root, "repos", projectID+".git")
		if bareRepoPath != want {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, want)
		}
	})

	t.Run("explicit --slug controls the path deterministically", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		_, bareRepoPath, err := Init(root, "", InitOpts{Source: source, Slug: "custom-slug"})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if bareRepoPath != filepath.Join(root, "repos", "custom-slug.git") {
			t.Errorf("bareRepoPath = %q, want %q", bareRepoPath, filepath.Join(root, "repos", "custom-slug.git"))
		}
	})

	t.Run("re-running Init with the same source returns the same path", func(t *testing.T) {
		source := makeRepoWithCommit(t)
		root := t.TempDir()

		_, first, err := Init(root, "", InitOpts{Source: source})
		if err != nil {
			t.Fatalf("Init #1: %v", err)
		}
		_, second, err := Init(root, "", InitOpts{Source: source})
		if err != nil {
			t.Fatalf("Init #2: %v", err)
		}
		if first != second {
			t.Errorf("Init returned %q then %q for the same source — paths must be stable", first, second)
		}
	})
}
