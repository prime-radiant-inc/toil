package engine

import (
	"strings"
)

func buildArtifactRepairPrompt(missing []string, previousDecision string, decisions []string) string {
	var builder strings.Builder
	builder.WriteString("IMPORTANT: The artifacts referenced in your previous response do not exist.\n\n")
	builder.WriteString("Missing artifacts:\n")
	for _, path := range missing {
		builder.WriteString("- ")
		builder.WriteString(path)
		builder.WriteString("\n")
	}
	builder.WriteString("\nCreate these files in the workspace, or update `artifacts` to only include files that actually exist.\n")
	builder.WriteString("You MUST return the final result immediately in the required JSON shape.\n")
	builder.WriteString("Do NOT do unrelated analysis, searches, or subagent work.\n")
	builder.WriteString("Do NOT call any tools other than `communicate`.\n")
	builder.WriteString("If `communicate` is available, call it once with this JSON object in `output`.\n")
	builder.WriteString("Otherwise respond with ONLY a JSON object (or a fenced ```json block) and no extra text.\n\n")
	if previousDecision != "" {
		builder.WriteString("Keep the previous decision unless it must change: ")
		builder.WriteString(previousDecision)
		builder.WriteString("\n")
	}
	if len(decisions) > 0 {
		builder.WriteString("Allowed decisions: ")
		builder.WriteString(strings.Join(decisions, ", "))
		builder.WriteString("\n")
	}
	builder.WriteString("\nRequired JSON shape:\n")
	builder.WriteString("```json\n")
	builder.WriteString("{\n")
	builder.WriteString("  \"decision\": \"<decision>\",\n")
	builder.WriteString("  \"message\": \"<one-line summary>\",\n")
	builder.WriteString("  \"data\": {},\n")
	builder.WriteString("  \"artifacts\": []\n")
	builder.WriteString("}\n")
	builder.WriteString("```\n")

	return builder.String()
}
