package document

import "strings"

// LocalMarkerOpen and LocalMarkerClose delimit the attempt-specific section
// of a workflow prompt. Content between the markers is "local" and is shown
// in the document's quote-prompt block; everything else is "boilerplate"
// and is suppressed behind a "▸ show role prompt" strip.
const (
	LocalMarkerOpen  = "<!-- LOCAL -->"
	LocalMarkerClose = "<!-- /LOCAL -->"
)

// ExtractLocalPrompt returns (local, boilerplate) given a rendered prompt
// string. If no markers are present, the entire prompt is returned as
// local with empty boilerplate (conservative fallback).
func ExtractLocalPrompt(prompt string) (local, boilerplate string) {
	openIdx := strings.Index(prompt, LocalMarkerOpen)
	if openIdx < 0 {
		return prompt, ""
	}
	closeIdx := strings.Index(prompt[openIdx:], LocalMarkerClose)
	if closeIdx < 0 {
		return prompt, ""
	}
	closeIdx += openIdx
	before := strings.TrimSpace(prompt[:openIdx])
	after := strings.TrimSpace(prompt[closeIdx+len(LocalMarkerClose):])
	local = strings.TrimSpace(prompt[openIdx+len(LocalMarkerOpen) : closeIdx])
	var bp strings.Builder
	if before != "" {
		bp.WriteString(before)
	}
	if after != "" {
		if bp.Len() > 0 {
			bp.WriteString("\n\n")
		}
		bp.WriteString(after)
	}
	return local, bp.String()
}
