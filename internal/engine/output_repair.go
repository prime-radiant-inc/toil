package engine

import (
	"strings"
)

// buildIncompleteWorkPrompt is sent when the runner returned no output —
// the agent was likely interrupted (provider error, ran out of rounds, or
// stopped without calling its result tool). Unlike buildRepairPrompt which
// forbids further tool calls, this prompt invites the agent to continue
// its work and end with a JSON output object.
func buildIncompleteWorkPrompt(decisions []string, validationErrors []string) string {
	var builder strings.Builder
	builder.WriteString("Your previous turn ended without producing a final JSON output.\n")
	builder.WriteString("This usually means your work was interrupted before you could submit a result.\n\n")
	builder.WriteString("Please continue your work. You may call tools as needed. When you have a final answer, end your response with the required JSON output object.\n\n")
	builder.WriteString("CRITICAL:\n")
	builder.WriteString("- `message` must be a short human-readable summary. Do NOT embed the full JSON output inside `message`.\n")
	builder.WriteString("- Put all structured output fields under `data`.\n\n")
	if len(validationErrors) > 0 {
		builder.WriteString("Issues with your previous output:\n")
		for _, validationErr := range validationErrors {
			validationErr = strings.TrimSpace(validationErr)
			if validationErr == "" {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(validationErr)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	if len(decisions) > 0 {
		builder.WriteString("Allowed decisions: ")
		builder.WriteString(strings.Join(decisions, ", "))
		builder.WriteString("\n\n")
	}
	builder.WriteString("When ready, return this JSON shape:\n\n")
	builder.WriteString("```json\n")
	builder.WriteString("{\n")
	builder.WriteString("  \"decision\": \"<decision>\",\n")
	builder.WriteString("  \"message\": \"<one-line summary>\",\n")
	builder.WriteString("  \"data\": {},\n")
	builder.WriteString("  \"artifacts\": []\n")
	builder.WriteString("}\n")
	builder.WriteString("```\n")
	return strings.TrimSpace(builder.String()) + "\n"
}

func buildRepairPrompt(decisions []string, validationErrors []string) string {
	var builder strings.Builder
	builder.WriteString("IMPORTANT: Your previous response did not match the required JSON output contract.\n")
	builder.WriteString("You MUST return the final result immediately in the required JSON shape.\n")
	builder.WriteString("Do NOT do more analysis, file edits, searches, or subagent work.\n")
	builder.WriteString("Do NOT call any tools. Respond with ONLY a JSON object (or a fenced ```json block) and no extra text.\n\n")
	builder.WriteString("CRITICAL:\n")
	builder.WriteString("- `message` must be a short human-readable summary. Do NOT embed the full JSON output inside `message`.\n")
	builder.WriteString("- Put all structured output fields under `data`.\n\n")
	if len(validationErrors) > 0 {
		builder.WriteString("Validation errors to fix:\n")
		for _, validationErr := range validationErrors {
			validationErr = strings.TrimSpace(validationErr)
			if validationErr == "" {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(validationErr)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	if len(decisions) > 0 {
		builder.WriteString("Allowed decisions: ")
		builder.WriteString(strings.Join(decisions, ", "))
		builder.WriteString("\n\n")
	}
	builder.WriteString("Return this JSON shape:\n\n")
	builder.WriteString("```json\n")
	builder.WriteString("{\n")
	builder.WriteString("  \"decision\": \"<decision>\",\n")
	builder.WriteString("  \"message\": \"<one-line summary>\",\n")
	builder.WriteString("  \"data\": {},\n")
	builder.WriteString("  \"artifacts\": []\n")
	builder.WriteString("}\n")
	builder.WriteString("```\n")
	return strings.TrimSpace(builder.String()) + "\n"
}
