package dashboard

import (
	"strings"
	"testing"
)

func TestFormatValueWithHTML_String(t *testing.T) {
	text, html, isJSON := formatValueWithHTML("hello world")
	if text != "hello world" {
		t.Errorf("expected raw text, got: %s", text)
	}
	if !strings.Contains(string(html), "hello world") {
		t.Errorf("expected HTML to contain text, got: %s", html)
	}
	if isJSON {
		t.Error("plain string should not be marked as JSON")
	}
}

func TestFormatValueWithHTML_Map(t *testing.T) {
	input := map[string]any{"name": "test", "count": float64(42)}
	text, html, isJSON := formatValueWithHTML(input)
	if !isJSON {
		t.Error("map value should be marked as JSON")
	}
	if !strings.Contains(text, `"name"`) {
		t.Errorf("raw text should contain JSON, got: %s", text)
	}
	// goldmark wraps in <pre><code class="language-json">
	htmlStr := string(html)
	if !strings.Contains(htmlStr, "<pre>") || !strings.Contains(htmlStr, "<code") {
		t.Errorf("expected code block in HTML, got: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "language-json") {
		t.Errorf("expected language-json class, got: %s", htmlStr)
	}
}

func TestFormatValueWithHTML_Array(t *testing.T) {
	input := []any{"a", "b"}
	_, html, isJSON := formatValueWithHTML(input)
	if !isJSON {
		t.Error("array value should be marked as JSON")
	}
	if !strings.Contains(string(html), "language-json") {
		t.Errorf("expected language-json class, got: %s", html)
	}
}

func TestFormatValueWithHTML_Nil(t *testing.T) {
	text, html, isJSON := formatValueWithHTML(nil)
	if text != "" || html != "" || isJSON {
		t.Errorf("nil should return empty, got: %q %q %v", text, html, isJSON)
	}
}

func TestFormatValueWithHTML_HTMLEscaping(t *testing.T) {
	input := map[string]any{"html": "<script>alert(1)</script>"}
	_, html, _ := formatValueWithHTML(input)
	if strings.Contains(string(html), "<script>alert") {
		t.Error("HTML should be escaped/sanitized in output")
	}
}
