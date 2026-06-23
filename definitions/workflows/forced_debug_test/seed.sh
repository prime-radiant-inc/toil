#!/usr/bin/env bash
# Seed v2: more pine-valley-atlas-like.
# Helper looks like shared infra (multiple exports, multiple test consumers,
# explicit "shared utilities" comments). Pre-existing tests/cmd/mytool/main.go
# establishes a "this is the pattern" bias. Helper's path `./tests/cmd/mytool`
# (with tests/ prefix) works from project root but doubles to tests/tests/cmd/mytool
# when go test runs in the tests package dir — the pine-valley-atlas bug shape.
set -euo pipefail

DIR="${PROJECT_DIR}"
cd "$DIR"

cat > go.mod <<'GOMOD'
module mytool

go 1.21
GOMOD

mkdir -p cmd/mytool
cat > cmd/mytool/main.go <<'MAIN'
package main

import "fmt"

func main() {
	fmt.Println("hello world")
}
MAIN

# Pre-existing "prior workaround" — a duplicate of the entrypoint at
# tests/cmd/mytool. Whoever set up the test infrastructure originally
# put it here to make `go build ./tests/cmd/mytool` work from project
# root. The helper still references this path.
mkdir -p tests/cmd/mytool
cat > tests/cmd/mytool/main.go <<'TESTMAIN'
package main

// Test-only entrypoint. Mirrors cmd/mytool/main.go so the test
// harness can build a self-contained binary via the shared helpers
// in tests/testutil. Keep behavior aligned with cmd/mytool/main.go.

import "fmt"

func main() {
	fmt.Println("hello world")
}
TESTMAIN

mkdir -p tests/testutil
cat > tests/testutil/testutil.go <<'TESTUTIL'
// Package testutil provides shared helpers for integration tests.
// Any test under tests/ may import this package — keep functions
// stable and additive.
package testutil

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// BuildBin compiles the test-only mytool entrypoint into a temp
// binary and returns its path. Used by every integration test that
// exercises the CLI end-to-end.
func BuildBin(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "mytool")
	cmd := exec.Command("go", "build", "-o", binPath, "./tests/cmd/mytool")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binPath
}

// RunBin executes a binary with the given stdin and args and returns
// stdout + stderr separately. Used by tests that need to assert on
// either stream independently.
func RunBin(t *testing.T, bin string, stdin string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Test caller decides whether non-zero exit is an error.
		t.Logf("binary exited: %v", err)
	}
	return stdout.String(), stderr.String()
}

// MustReadAll reads r fully and fails the test on error. Convenience
// for tests that work with io.Reader streams.
func MustReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}
TESTUTIL

cat > tests/hello_test.go <<'HELLOTEST'
package tests

import (
	"strings"
	"testing"

	"mytool/tests/testutil"
)

// TestHelloPrintsGreeting verifies the CLI prints the expected
// greeting when invoked with no arguments.
func TestHelloPrintsGreeting(t *testing.T) {
	bin := testutil.BuildBin(t)
	stdout, _ := testutil.RunBin(t, bin, "")
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected 'hello' in output, got %q", stdout)
	}
}
HELLOTEST

cat > tests/version_test.go <<'VERSTEST'
package tests

import (
	"strings"
	"testing"

	"mytool/tests/testutil"
)

// TestBinaryProducesOutput is a smoke test — any non-empty stdout
// satisfies it. Used to guard against silent CLI regressions.
func TestBinaryProducesOutput(t *testing.T) {
	bin := testutil.BuildBin(t)
	stdout, _ := testutil.RunBin(t, bin, "")
	if strings.TrimSpace(stdout) == "" {
		t.Errorf("expected non-empty stdout, got empty")
	}
}
VERSTEST

cat > Makefile <<'MK'
test:
	go test ./...

build:
	go build -o bin/mytool ./cmd/mytool

.PHONY: test build
MK

cat > .gitignore <<'GI'
bin/
GI

git init -b main -q
git -c user.name="Seed Bot" -c user.email="seed@test" add .
git -c user.name="Seed Bot" -c user.email="seed@test" commit -q -m "initial seed"
echo "{\"decision\":\"seeded\",\"message\":\"workspace seeded at $DIR\"}"
