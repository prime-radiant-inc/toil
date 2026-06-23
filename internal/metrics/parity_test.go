package metrics

import (
	_ "embed"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

//go:embed formatter_fixtures.json
var fixturesJSON []byte

type costCase struct {
	In   *float64 `json:"in"`
	Want string   `json:"want"`
}
type tokenCase struct {
	In   int    `json:"in"`
	Want string `json:"want"`
}
type fixtures struct {
	Cost   []costCase  `json:"cost"`
	Tokens []tokenCase `json:"tokens"`
}

func loadFixtures(t *testing.T) fixtures {
	t.Helper()
	var f fixtures
	if err := json.Unmarshal(fixturesJSON, &f); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	return f
}

// TestFormatters_GoAgainstFixtures pins Go's FormatCost / FormatTokens
// to the shared fixture file. If JS drifts, TestFormatters_JSParity below
// also fails; if Go drifts, this test catches it first.
func TestFormatters_GoAgainstFixtures(t *testing.T) {
	fx := loadFixtures(t)

	for _, c := range fx.Cost {
		if got := FormatCost(c.In); got != c.Want {
			t.Errorf("FormatCost(%v) = %q, want %q", c.In, got, c.Want)
		}
	}
	for _, c := range fx.Tokens {
		if got := FormatTokens(c.In); got != c.Want {
			t.Errorf("FormatTokens(%d) = %q, want %q", c.In, got, c.Want)
		}
	}
}

// TestFormatters_JSParity runs the dashboard's JS formatters against the
// same fixtures and asserts byte-for-byte parity with Go. Skipped when
// node(1) isn't on PATH so hermetic environments don't fail.
func TestFormatters_JSParity(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not in PATH; skipping JS parity check")
	}

	jsPath, err := filepath.Abs("../dashboard/static/js/toil.js")
	if err != nil {
		t.Fatal(err)
	}
	fxPath, err := filepath.Abs("formatter_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}

	// Evals toil.js into its own vm context to capture the module-level
	// MetricsFmt binding without touching the source file. Paths pass via
	// env vars because node's -e argv handling differs across versions.
	script := `
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const jsPath = process.env.TOIL_JS_PATH;
const fxPath = process.env.TOIL_FX_PATH;

let src = fs.readFileSync(jsPath, 'utf8');
// MetricsFmt is declared with const at module level in toil.js. Under vm,
// const bindings don't attach to the context object, so rewrite the
// declaration to a context-visible form before evaling. This is a test-
// only hack; the source file is not modified.
src = src.replace(/^const MetricsFmt =/m, 'this.MetricsFmt =');
// Stub the browser globals toil.js touches in its load-time IIFE. We
// only need to reach past the MetricsFmt declaration — failures after
// are caught and ignored.
const noop = () => {};
const ctx = {
  document: { addEventListener: noop, querySelector: noop, querySelectorAll: () => [] },
  window: { addEventListener: noop, location: { pathname: '/' } },
  EventSource: function() { return { addEventListener: noop, close: noop }; },
  fetch: () => Promise.resolve({ ok: true, json: () => ({}) }),
  console: console,
};
vm.createContext(ctx);
try {
  vm.runInContext(src, ctx, { filename: path.basename(jsPath) });
} catch (e) {
  if (!ctx.MetricsFmt) throw e;
}
const fx = JSON.parse(fs.readFileSync(fxPath, 'utf8'));

for (let i = 0; i < fx.cost.length; i++) {
  process.stdout.write("cost\t" + i + "\t" + ctx.MetricsFmt.cost(fx.cost[i].in) + "\n");
}
for (let i = 0; i < fx.tokens.length; i++) {
  process.stdout.write("tokens\t" + i + "\t" + ctx.MetricsFmt.tokens(fx.tokens[i].in) + "\n");
}
`

	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(cmd.Environ(), "TOIL_JS_PATH="+jsPath, "TOIL_FX_PATH="+fxPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node exec failed: %v\n%s", err, out)
	}

	fx := loadFixtures(t)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			t.Fatalf("unexpected line from node: %q", line)
		}
		kind, idxStr, got := parts[0], parts[1], parts[2]
		i, err := strconv.Atoi(idxStr)
		if err != nil {
			t.Fatalf("bad index %q in line %q", idxStr, line)
		}
		var want string
		switch kind {
		case "cost":
			want = fx.Cost[i].Want
		case "tokens":
			want = fx.Tokens[i].Want
		default:
			t.Errorf("unknown kind %q", kind)
			continue
		}
		if got != want {
			t.Errorf("JS %s fixture[%d] = %q, want %q (parity break with Go)", kind, i, got, want)
		}
	}
}
