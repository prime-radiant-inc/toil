package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"primeradiant.com/toil/internal/metrics"
	"primeradiant.com/toil/internal/state"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/js/*.js
//go:embed static/js/tool_renderers/*.js
//go:embed static/css/*.css
var staticFS embed.FS

// devMode returns true when TOIL_DEV=1 is set.
func devMode() bool {
	return os.Getenv("TOIL_DEV") == "1"
}

// unpricedBadge returns an HTML snippet appended after a cost when one or
// more events in a run couldn't be priced. Visible warning that the
// displayed total is incomplete and the pricing catalog needs an update.
func unpricedBadge(n int) string {
	if n <= 0 {
		return ""
	}
	title := fmt.Sprintf("%d calls used a model not in the pricing catalog — cost shown is incomplete.", n)
	return ` <span title="` + template.HTMLEscapeString(title) +
		`" class="inline-block text-[10px] font-medium px-1.5 py-0 rounded bg-amber-100 text-amber-800 align-middle">pricing incomplete</span>`
}

func LoadTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"formatMaybeTime": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "-"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"statusClass":      statusClass,
		"storyStatusClass": storyStatusClass,
		"statusDotClass":   statusDotClass,
		"humanizeStatus":   humanizeStatus,
		// effectiveStatus collapses HasUnresolvedFailure into the status string
		// for rendering. Accepts a RunSummary (or any struct with Status string
		// and HasUnresolvedFailure bool) via the dashboard.RunSummary helper.
		"effectiveRunStatus": func(run RunSummary) string {
			return EffectiveStatus(run.Status, run.HasUnresolvedFailure)
		},
		"timeAgo":        timeAgo,
		"renderMarkdown": renderMarkdown,
		"renderTool": func(item TranscriptItem) ToolRender {
			return RenderToolWithState(item.ToolName, item.Input, item.Output, item.IsError, item.ToolState)
		},
		"toJSON": func(value any) template.JS {
			data, err := json.Marshal(value)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(data)
		},
		"jsonPretty": func(value any) string {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return "{}"
			}
			return string(data)
		},
		"unixMillis": func(t time.Time) int64 {
			if t.IsZero() {
				return 0
			}
			return t.UnixMilli()
		},
		"deref": func(t *time.Time) time.Time {
			if t == nil {
				return time.Time{}
			}
			return *t
		},
		"eventBadgeClass": EventBadgeClass,
		"roleLabel":       RoleLabel,
		"roleColor":       RoleColor,
		"buildKVRows": func(input map[string]any) []KeyValue {
			if input == nil {
				return nil
			}
			var rows []KeyValue
			for k, v := range input {
				rows = append(rows, KeyValue{Key: k, Value: formatOutputValue(v)})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
			return rows
		},
		"formatOutput": formatOutputValue,
		"derefInt64": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"divFloat": func(a int64, b float64) float64 {
			return float64(a) / b
		},
		"lastIndex": func(s []string) int {
			if len(s) == 0 {
				return 0
			}
			return len(s) - 1
		},
		"isTerminal": func(status string) bool {
			return status == statusCompleted ||
				status == statusFailed ||
				status == statusFailedHandled ||
				status == statusSkipped ||
				status == statusCancelled
		},
		"countByStatus": func(nodes []NodeSummary, status string) int {
			n := 0
			for _, nd := range nodes {
				if nd.Status == status {
					n++
				}
			}
			return n
		},
		"mul": func(a, b int) int {
			return a * b
		},
		"statusIcon": statusIcon,
		"lower":      strings.ToLower,
		"hasKey": func(m map[string]bool, key string) bool {
			return m[key]
		},
		"dict": func(values ...any) map[string]any {
			if len(values)%2 != 0 {
				panic("dict requires even number of arguments")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					panic("dict keys must be strings")
				}
				m[key] = values[i+1]
			}
			return m
		},
		"formatMetrics": func(total *state.NodeTotals) template.HTML {
			if total == nil {
				return template.HTML("—")
			}
			dur := metrics.FormatDuration(total.DurationMs)
			tok := metrics.FormatTokens(total.Tokens.Total)
			cost := metrics.FormatCost(total.CostUSD)
			return template.HTML(
				template.HTMLEscapeString(dur) +
					` <span class="text-muted">·</span> ` +
					template.HTMLEscapeString(tok) + " tok" +
					` <span class="text-muted">·</span> ` +
					template.HTMLEscapeString(cost) +
					unpricedBadge(total.UnpricedEventCount),
			)
		},
		"formatTokensAndCost": func(total *state.NodeTotals) template.HTML {
			if total == nil {
				return template.HTML("—")
			}
			tok := metrics.FormatTokens(total.Tokens.Total)
			cost := metrics.FormatCost(total.CostUSD)
			return template.HTML(
				template.HTMLEscapeString(tok) + " tok" +
					` <span class="opacity-50">·</span> ` +
					template.HTMLEscapeString(cost) +
					unpricedBadge(total.UnpricedEventCount),
			)
		},
		"firstLetter": func(s string) string {
			if s == "" {
				return "·"
			}
			return strings.ToUpper(s[:1])
		},
		"add1": func(n int) int { return n + 1 },
		"sidPrefix": func(s string) string {
			if len(s) < 6 {
				return s
			}
			return s[:6] + "…"
		},
		"formatDurationMs": func(ms int64) string {
			if ms <= 0 {
				return ""
			}
			secs := ms / 1000
			if secs < 60 {
				return fmt.Sprintf("%ds", secs)
			}
			return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
		},
		"isRow": func(v any) bool {
			m, ok := v.(map[string]any)
			return ok && m["kind"] == "row"
		},
		"isSubrun": func(v any) bool {
			m, ok := v.(map[string]any)
			return ok && m["kind"] == "subrun"
		},
		"isParallel": func(v any) bool {
			m, ok := v.(map[string]any)
			return ok && m["kind"] == "parallel"
		},
		"runFromSubrun": func(v any) any {
			m, _ := v.(map[string]any)
			return m["run"]
		},
		// formatTimeHMSAny accepts a JSON-unmarshalled timestamp (string in
		// RFC3339 form) and returns it as 24h HH:MM:SS in local time.
		// Returns "" for zero/missing values; "-" reserved for the
		// time.Time variant elsewhere.
		"formatTimeHMSAny": func(v any) string {
			s, ok := v.(string)
			if !ok || s == "" {
				return ""
			}
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return ""
			}
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("15:04:05")
		},
		// formatCostUSDAny formats a *float64 cost (which JSON-unmarshals as
		// float64 or nil) as "$0.04". Returns "" when nil/missing.
		"formatCostUSDAny": func(v any) string {
			if v == nil {
				return ""
			}
			f, ok := v.(float64)
			if !ok {
				return ""
			}
			return fmt.Sprintf("$%.2f", f)
		},
		// formatDurationMsAny accepts the float64 that JSON-unmarshalled maps
		// produce for numeric fields, converting to int64 before formatting.
		"formatDurationMsAny": func(v any) string {
			if v == nil {
				return ""
			}
			var ms int64
			switch n := v.(type) {
			case float64:
				ms = int64(n)
			case int64:
				ms = n
			case int:
				ms = int64(n)
			default:
				return ""
			}
			if ms <= 0 {
				return ""
			}
			secs := ms / 1000
			if secs < 60 {
				return fmt.Sprintf("%ds", secs)
			}
			return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
		},
	}

	tmpl := template.New("templates").Funcs(funcs)
	if devMode() {
		return tmpl.ParseFS(os.DirFS("internal/dashboard/templates"), "*.html")
	}
	return tmpl.ParseFS(templateFS, "templates/*.html")
}

func StaticFileHandler() http.Handler {
	if devMode() {
		return http.StripPrefix("/static/", http.FileServer(http.Dir("internal/dashboard/static")))
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("failed to get static subdirectory: " + err.Error())
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

func humanizeStatus(status string) string {
	if status == "" {
		return ""
	}
	if status == statusFailedHandled {
		return "Failed (handled)"
	}
	words := strings.Split(status, "_")
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func statusIcon(status string) template.HTML {
	switch status {
	case statusCompleted:
		return `<span class="text-emerald-600">✓</span>`
	case statusFailed:
		return `<span class="text-red-500">✗</span>`
	case statusFailedHandled:
		return `<span class="text-amber-500">⚠</span>`
	case statusRunning:
		return `<span class="text-blue-500">●</span>`
	case statusPaused, statusAwaitingApproval:
		return `<span class="text-yellow-500">◉</span>`
	case statusSkipped:
		return `<span class="text-gray-400">○</span>`
	case statusCancelled:
		return `<span class="text-gray-400">⊘</span>`
	default:
		return `<span class="text-gray-300">·</span>`
	}
}

func statusDotClass(status string) string {
	switch status {
	case statusRunning:
		return "bg-blue-500 animate-pulse"
	case statusCompleted:
		return "bg-emerald-500"
	case statusFailed:
		return "bg-red-500"
	case statusFailedHandled:
		return "bg-amber-500"
	case statusPaused, statusAwaitingApproval:
		return "bg-yellow-500"
	case statusCancelled:
		return "bg-gray-400"
	default:
		return "bg-muted"
	}
}

// storyStatusClass maps an enumerated set of story status values to
// Tailwind classes. goconst's hit on "ready" is a false positive — the
// string is an external status identifier, not a duplicated value.
//
//nolint:goconst
func storyStatusClass(status string) string {
	switch strings.ToLower(status) {
	case "refined":
		return "bg-amber-400 text-white"
	case "ready":
		return "bg-accent text-white"
	case "in-progress":
		return "bg-blue-500 text-white"
	case "done":
		return "bg-green-600 text-white"
	default:
		return "bg-gray-400/30 text-gray-600"
	}
}

func statusClass(status string) string {
	switch status {
	case statusRunning:
		return decisionPillBlue
	case statusCompleted:
		return decisionPillGreen
	case statusFailed:
		return decisionPillRed
	case statusFailedHandled:
		return "bg-amber-50 text-amber-700 ring-1 ring-amber-200"
	case statusPaused, statusAwaitingApproval:
		return "bg-yellow-100 text-yellow-700"
	case statusSkipped:
		return badgeClassMuted
	case statusCancelled:
		return "bg-gray-100 text-gray-600"
	default:
		return badgeClassMuted
	}
}
