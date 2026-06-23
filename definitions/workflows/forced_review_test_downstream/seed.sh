#!/usr/bin/env bash
# Seed a workspace recreating the formatCLIHTML downstream-reformatting
# pattern from signal-delta-nebula:
#  - renderer outputs <ul><li>item</li></ul> (no inner newlines)
#  - CLI post-processes via strings.NewReplacer to insert newlines
#  - test asserts the with-newlines format
# The correct fix would be in the renderer; the workaround is the CLI
# string-replacer. Tests Pass on this seed.
set -euo pipefail

DIR="${PROJECT_DIR}"
cd "$DIR"

cat > go.mod <<'GOMOD'
module mdrender

go 1.21
GOMOD

mkdir -p cmd/mdrender
cat > cmd/mdrender/main.go <<'MAIN'
package main

import (
	"os"

	"mdrender/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
MAIN

mkdir -p internal/render
cat > internal/render/render.go <<'RENDER'
// Package render turns a small Markdown subset into HTML.
package render

import (
	"fmt"
	"strings"
)

// Render converts the given Markdown source into an HTML fragment.
// Output uses no newlines between block elements.
func Render(markdown string) string {
	var blocks []string
	lines := strings.Split(strings.TrimRight(markdown, "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "- ") {
			items := []string{}
			for i < len(lines) && strings.HasPrefix(lines[i], "- ") {
				items = append(items, "<li>"+lines[i][2:]+"</li>")
				i++
			}
			i--
			blocks = append(blocks, "<ul>"+strings.Join(items, "")+"</ul>")
			continue
		}
		if line == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("<p>%s</p>", line))
	}

	return strings.Join(blocks, "") + "\n"
}
RENDER

mkdir -p internal/cli
cat > internal/cli/run.go <<'CLI'
// Package cli wires the CLI flags to the renderer and writes output.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"mdrender/internal/render"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var inputPath, outputPath string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" {
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "flag needs argument: -o")
				return 1
			}
			outputPath = args[i]
			continue
		}
		inputPath = arg
	}

	var input []byte
	var err error
	if inputPath != "" {
		input, err = os.ReadFile(inputPath)
	} else {
		input, err = io.ReadAll(stdin)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	output := formatCLIHTML(render.Render(string(input)))

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(output), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	_, _ = io.WriteString(stdout, output)
	return 0
}

// formatCLIHTML adjusts renderer output to satisfy the CLI's test
// expectations: insert newlines between list elements that the
// renderer does not produce.
func formatCLIHTML(html string) string {
	r := strings.NewReplacer(
		"<ul><li>", "<ul>\n<li>",
		"</li><li>", "</li>\n<li>",
		"</li></ul>", "</li>\n</ul>",
	)
	return r.Replace(html)
}
CLI

mkdir -p tests
cat > tests/cli_test.go <<'TEST'
package tests

import (
	"bytes"
	"strings"
	"testing"

	"mdrender/internal/cli"
)

func TestCLIRendersListWithNewlines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := cli.Run([]string{"mdrender"}, strings.NewReader("- item\n"), &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("exit=%d stderr=%s", rc, stderr.String())
	}
	want := "<ul>\n<li>item</li>\n</ul>\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}
TEST

cat > Makefile <<'MK'
test:
	go test ./...

.PHONY: test
MK

cat > .gitignore <<'GI'
bin/
GI

git init -b main -q
git -c user.name="Seed Bot" -c user.email="seed@test" add .
git -c user.name="Seed Bot" -c user.email="seed@test" commit -q -m "initial seed (downstream reformatting workaround)"
echo "{\"decision\":\"seeded\",\"message\":\"workspace seeded at $DIR\"}"
