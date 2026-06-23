package document

import "testing"

func TestExtractLocalWithMarkers(t *testing.T) {
	prompt := `# Surgeon

You are the chief architect for this component...
(many paragraphs)

<!-- LOCAL -->
Task: Plan implementation from this spec.
Workspace: greenfield.
<!-- /LOCAL -->

More boilerplate after the local section.`
	local, boilerplate := ExtractLocalPrompt(prompt)
	if local == "" {
		t.Fatalf("expected local content")
	}
	if !contains(local, "Plan implementation from this spec") {
		t.Fatalf("local missing task content: %q", local)
	}
	if !contains(boilerplate, "chief architect") {
		t.Fatalf("boilerplate missing role description")
	}
	if contains(local, "chief architect") {
		t.Fatalf("local should not contain boilerplate")
	}
}

func TestExtractLocalWithoutMarkers(t *testing.T) {
	prompt := "some content with no markers"
	local, boilerplate := ExtractLocalPrompt(prompt)
	if local != prompt {
		t.Fatalf("expected entire prompt as local when no markers; got %q", local)
	}
	if boilerplate != "" {
		t.Fatalf("expected empty boilerplate; got %q", boilerplate)
	}
}

func contains(s, sub string) bool {
	return s != "" && len(sub) > 0 && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
