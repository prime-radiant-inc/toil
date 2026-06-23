#!/usr/bin/env bash
# Seed a workspace where the SYMPTOMATIC fix has been applied:
#  - tests/cmd/mytool/main.go is the pre-existing duplicate (from prior task)
#  - tests/tests/cmd/mytool/main.go is the NEW symptomatic fix
#  - tests/testutil/testutil.go still has the buggy ./tests/cmd/mytool path
# This is exactly the pine-valley-atlas committed state. Reviewer should
# catch both the duplicate-at-suspicious-path AND the shadow test infra.
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

# Pre-existing duplicate (from prior workflow scaffold)
mkdir -p tests/cmd/mytool
cat > tests/cmd/mytool/main.go <<'TESTMAIN'
package main

// Test-only entrypoint mirroring cmd/mytool/main.go.
import "fmt"

func main() {
	fmt.Println("hello world")
}
TESTMAIN

# NEW symptomatic "fix" at the doubled path
mkdir -p tests/tests/cmd/mytool
cat > tests/tests/cmd/mytool/main.go <<'DOUBLED'
package main

// Added to satisfy the go build path in tests/testutil/testutil.go,
// which resolves "./tests/cmd/mytool" relative to the tests/ package dir.
import "fmt"

func main() {
	fmt.Println("hello world")
}
DOUBLED

mkdir -p tests/testutil
cat > tests/testutil/testutil.go <<'TESTUTIL'
// Package testutil provides shared helpers for integration tests.
// Any test under tests/ may import this package — keep functions
// stable and additive.
package testutil

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// BuildBin compiles the test-only mytool entrypoint into a temp binary.
func BuildBin(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "mytool")
	cmd := exec.Command("go", "build", "-o", binPath, "./tests/cmd/mytool")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binPath
}

func RunBin(t *testing.T, bin string, stdin string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String()
}
TESTUTIL

cat > tests/hello_test.go <<'HT'
package tests

import (
	"strings"
	"testing"

	"mytool/tests/testutil"
)

func TestHelloPrintsGreeting(t *testing.T) {
	bin := testutil.BuildBin(t)
	stdout, _ := testutil.RunBin(t, bin, "")
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected 'hello' in output, got %q", stdout)
	}
}
HT

cat > tests/version_test.go <<'VT'
package tests

import (
	"strings"
	"testing"

	"mytool/tests/testutil"
)

func TestBinaryProducesOutput(t *testing.T) {
	bin := testutil.BuildBin(t)
	stdout, _ := testutil.RunBin(t, bin, "")
	if strings.TrimSpace(stdout) == "" {
		t.Errorf("expected non-empty stdout, got empty")
	}
}
VT

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
git -c user.name="Seed Bot" -c user.email="seed@test" commit -q -m "initial seed (symptomatic fix applied)"
echo "{\"decision\":\"seeded\",\"message\":\"workspace seeded at $DIR\"}"
