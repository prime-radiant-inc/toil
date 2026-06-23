package dashboard

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.TaskList,
		extension.Strikethrough,
		extension.Table,
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
	),
)

var markdownPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")
	return p
}()

func renderMarkdown(input string) template.HTML {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	var buffer bytes.Buffer
	if err := markdownRenderer.Convert([]byte(input), &buffer); err != nil {
		escaped := template.HTMLEscapeString(input)
		return template.HTML(escaped)
	}
	clean := markdownPolicy.Sanitize(buffer.String())
	return template.HTML(clean)
}

func formatValueWithHTML(value any) (string, template.HTML, bool) {
	if value == nil {
		return "", "", false
	}
	if text, ok := value.(string); ok {
		return text, renderMarkdown(text), false
	}
	formatted := formatOutputValue(value)
	if formatted == "" {
		return "", "", false
	}
	wrapped := "```json\n" + formatted + "\n```"
	return formatted, renderMarkdown(wrapped), true
}
