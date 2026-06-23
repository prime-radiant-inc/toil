package engine

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"primeradiant.com/toil/internal/definitions"
)

var inputRefPattern = regexp.MustCompile(`\$\{input\.([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// ComposePrompt: 4-arg wrapper for backwards compat / tests.
func ComposePrompt(rolePrompt, nodePrompt string, inputs map[string]any, decisions definitions.DecisionList) (string, error) {
	return ComposePromptWithInputViews(rolePrompt, nodePrompt, inputs, inputs, decisions, "", nil, 0)
}

// ComposeQuestion: 4-arg wrapper, used by approvals.
func ComposeQuestion(rolePrompt, nodePrompt string, inputs map[string]any, decisions definitions.DecisionList) (string, error) {
	return ComposeQuestionWithInputViews(rolePrompt, nodePrompt, inputs, inputs, decisions)
}

// ComposeQuestionWithInputViews — approval path. Inline JSON inputs,
// no truncation. 5 args.
func ComposeQuestionWithInputViews(
	rolePrompt, nodePrompt string,
	interpolationInputs, displayInputs map[string]any,
	decisions definitions.DecisionList,
) (string, error) {
	var b strings.Builder
	writePromptHeader(&b, rolePrompt, nodePrompt, interpolationInputs)
	if len(displayInputs) > 0 {
		payload, err := json.MarshalIndent(displayInputs, "", "  ")
		if err != nil {
			return "", err
		}
		b.WriteString("\n\nInputs:\n```json\n")
		b.Write(payload)
		b.WriteString("\n```\n")
	}
	writeDecisionsBlock(&b, decisions)
	return strings.TrimSpace(b.String()), nil
}

// ComposePromptWithInputViews — dispatch path. Per-key previews +
// (sentinel-driven) deltas + REQUIRED OUTPUT FORMAT. 8 args.
//
// resumeDeltas semantics:
//
//	nil → fresh rendering (full inputs block from displayInputs)
//	non-nil (possibly empty) → resume rendering (deltas block;
//	                           empty renders "no changes" notice)
func ComposePromptWithInputViews(
	rolePrompt, nodePrompt string,
	interpolationInputs, displayInputs map[string]any,
	decisions definitions.DecisionList,
	dispatchInputsDir string,
	resumeDeltas map[string]any,
	dispatchN int,
) (string, error) {
	var b strings.Builder
	writePromptHeader(&b, rolePrompt, nodePrompt, interpolationInputs)

	if resumeDeltas != nil {
		b.WriteString("\n\n## New or updated for this turn\n\n")
		b.WriteString("(Each heading shows the input's name and which dispatch turn this update belongs to. The on-disk file is named after the original key. Prior turns' values remain accurate from your earlier reads.)\n\n")
		if len(resumeDeltas) == 0 {
			b.WriteString("(No changes since the prior turn — continue from your prior context.)\n\n")
		}
		for _, key := range sortedKeys(resumeDeltas) {
			headingKey := fmt.Sprintf("%s (iteration %d)", key, dispatchN)
			filename, content, err := serializeInput(key, resumeDeltas[key])
			if err != nil {
				b.WriteString(previewInputFallback(headingKey, resumeDeltas[key]))
				continue
			}
			filePath := filepath.Join(dispatchInputsDir, filename)
			b.WriteString(previewInput(headingKey, content, filename, filePath))
		}
	} else if len(displayInputs) > 0 {
		b.WriteString("\n\n## Inputs\n\n")
		for _, key := range sortedKeys(displayInputs) {
			filename, content, err := serializeInput(key, displayInputs[key])
			if err != nil {
				b.WriteString(previewInputFallback(key, displayInputs[key]))
				continue
			}
			var filePath string
			if dispatchInputsDir != "" {
				filePath = filepath.Join(dispatchInputsDir, filename)
			}
			b.WriteString(previewInput(key, content, filename, filePath))
		}
	}

	writeDecisionsBlock(&b, decisions)
	writeOutputFormatBlock(&b)
	return strings.TrimSpace(b.String()) + "\n", nil
}

// writePromptHeader writes role + node prompt sections (shared by both compose paths).
func writePromptHeader(b *strings.Builder, rolePrompt, nodePrompt string, interpolationInputs map[string]any) {
	rolePrompt = expandInputRefs(rolePrompt, interpolationInputs)
	nodePrompt = expandInputRefs(nodePrompt, interpolationInputs)
	if rolePrompt != "" {
		b.WriteString(rolePrompt)
	}
	if nodePrompt != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(nodePrompt)
	}
}

// writeDecisionsBlock writes the allowed-decisions section (shared by both compose paths).
func writeDecisionsBlock(b *strings.Builder, decisions definitions.DecisionList) {
	if len(decisions) == 0 {
		return
	}
	if decisions.HasDescriptions() {
		b.WriteString("\nAllowed decisions:\n")
		for _, d := range decisions {
			if d.Description != "" {
				fmt.Fprintf(b, "- `%s`: %s\n", d.ID, d.Description)
			} else {
				fmt.Fprintf(b, "- `%s`\n", d.ID)
			}
		}
	} else {
		b.WriteString("\nAllowed decisions: ")
		b.WriteString(strings.Join(decisions.IDs(), ", "))
	}
}

// writeOutputFormatBlock appends the REQUIRED OUTPUT FORMAT block (dispatch path only).
func writeOutputFormatBlock(b *strings.Builder) {
	b.WriteString("\n\n# REQUIRED OUTPUT FORMAT\n\n")
	b.WriteString("After completing your work, you MUST end your response with a JSON output object.\n")
	b.WriteString("This is how the system reads your decision. Without it, your work will be lost.\n\n")
	b.WriteString("If you are using the `communicate` tool, send this object as `communicate().output`.\n\n")
	b.WriteString("CRITICAL:\n")
	b.WriteString("- `message` must be a short human-readable summary. Do NOT embed the full JSON output inside `message`.\n")
	b.WriteString("- Put all structured fields under `data`.\n")
	b.WriteString("- If the prompt above specifies required `data.*` fields (for example `data.components`), they MUST be present.\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n  \"decision\": \"<decision>\",\n  \"message\": \"<one-line summary of what you did>\",\n  \"data\": {\n    \"...\": \"include required fields described above\"\n  },\n  \"artifacts\": []\n}\n")
	b.WriteString("```\n")
}

// expandInputRefs replaces ${input.key} references in text with values from
// the inputs map. Unresolved references are left as-is.
func expandInputRefs(text string, inputs map[string]any) string {
	if len(inputs) == 0 {
		return text
	}
	return inputRefPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := inputRefPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		if val, ok := inputs[groups[1]]; ok {
			return fmt.Sprint(val)
		}
		return match
	})
}

func selectPrompts(firstRun bool, rolePrompt string, nodePrompt string, edgePrompt string, allowNodePromptOnResume bool) (string, string) {
	if firstRun {
		return rolePrompt, nodePrompt
	}
	if strings.TrimSpace(edgePrompt) != "" {
		// Keep role instructions on transition prompts so loops preserve
		// role-specific constraints (output shape, domain rules, etc.).
		return rolePrompt, edgePrompt
	}
	if allowNodePromptOnResume {
		return "", nodePrompt
	}
	return "", ""
}
