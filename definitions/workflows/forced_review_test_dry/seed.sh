#!/usr/bin/env bash
# Seed a workspace recreating the duplicated-escape-functions DRY
# violation from the crest-harbor-meadow eval:
#  - inline.go defines escapeHTML (escapes & < > ")
#  - blocks_collections.go defines escapeCodeBlockHTML (escapes & < >
#    only — missing the " case)
# Same package, near-identical functions with divergent rules.
# Tests pass because no test exercises " inside a code block.
set -euo pipefail

DIR="${PROJECT_DIR}"
cd "$DIR"

cat > go.mod <<'GOMOD'
module mdrender

go 1.21
GOMOD

mkdir -p internal/mdrender

cat > internal/mdrender/inline.go <<'INLINE'
package mdrender

import "strings"

// RenderInline turns supported inline markdown into HTML.
func RenderInline(src string) string {
	var out strings.Builder
	for _, r := range src {
		out.WriteString(escapeHTML(string(r)))
	}
	return out.String()
}

func escapeHTML(src string) string {
	var out strings.Builder
	for _, r := range src {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			out.WriteString("&quot;")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
INLINE

cat > internal/mdrender/blocks_collections.go <<'BLOCKS'
package mdrender

import "strings"

func escapeCodeBlockHTML(src string) string {
	var out strings.Builder
	for _, r := range src {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// RenderCodeBlock wraps src in <pre><code>...</code></pre> with
// HTML escaping suitable for code content.
func RenderCodeBlock(src string) string {
	return "<pre><code>" + escapeCodeBlockHTML(src) + "</code></pre>"
}
BLOCKS

mkdir -p tests
cat > tests/render_test.go <<'TEST'
package tests

import (
	"strings"
	"testing"

	"mdrender/internal/mdrender"
)

func TestRenderInlineEscapesAmpAndAngles(t *testing.T) {
	got := mdrender.RenderInline("a & b < c")
	want := "a &amp; b &lt; c"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderCodeBlockEscapesAmpAndAngles(t *testing.T) {
	got := mdrender.RenderCodeBlock("a & <b>")
	want := "<pre><code>a &amp; &lt;b&gt;</code></pre>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderInlineHandlesEmpty(t *testing.T) {
	if got := mdrender.RenderInline(""); got != "" {
		t.Fatalf("got %q want empty", got)
	}
	if !strings.HasPrefix(mdrender.RenderCodeBlock(""), "<pre>") {
		t.Fatal("code block should always wrap")
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
git -c user.name="Seed Bot" -c user.email="seed@test" commit -q -m "initial seed (duplicated escape functions)"
echo "{\"decision\":\"seeded\",\"message\":\"workspace seeded at $DIR\"}"
