// Package document builds the document model rendered by the toil run view.
// A Document is a tree of RunNodes describing an execution group as a single
// readable, foldable scroll.
package document

import (
	"encoding/json"
)

// ArtifactRef is a short reference to a produced artifact, shown inline on
// the row to give the reader a sense of what the agent produced without
// expanding the disclosure.
type ArtifactRef struct {
	Name string `json:"name"`           // e.g., "plan.json", "child_run", "commit"
	Kind string `json:"kind,omitempty"` // e.g., "file", "run", "commit", "list"
	Desc string `json:"desc,omitempty"` // short description: "1.7 KB", "→ aurora-lattice-zephyr", "3 proposals"
}

// BriefField is a single key/value pair rendered in the brief block when
// inputs.spec is absent but other structured inputs are present.
type BriefField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Document is the tree-shaped representation of a run plus metadata about the
// root run. Root holds the tree; the other fields describe the run as a
// whole.
type Document struct {
	RootRunID   string       `json:"root_run_id"`
	RootTitle   string       `json:"root_title,omitempty"`
	RootStatus  string       `json:"root_status,omitempty"`
	BriefText   string       `json:"brief_text,omitempty"`
	BriefSource string       `json:"brief_source,omitempty"`
	BriefFields []BriefField `json:"brief_fields,omitempty"`
	TotalRuns   int          `json:"total_runs,omitempty"`
	Root        *RunNode     `json:"root,omitempty"`
}

// Transcript is a node's execution trace, segmented per attempt.
type Transcript struct {
	Attempts []Attempt `json:"attempts"`
}

// Attempt is one runner invocation.
type Attempt struct {
	Ordinal       int       `json:"ordinal"`
	Outcome       string    `json:"outcome"` // "succeeded" | "failed" | "" (in-progress)
	FailureReason string    `json:"failure_reason,omitempty"`
	Messages      []Message `json:"messages"`
}

// Message is one message in the attempt's chronological stream.
// Kind is one of: system_prompt | user_prompt | assistant | tool_call | decision.
type Message struct {
	Kind     string           `json:"kind"`
	Text     string           `json:"text,omitempty"`
	HTML     string           `json:"html,omitempty"` // pre-rendered HTML for assistant/user_prompt (populated in a later task)
	ToolCall *MessageTool     `json:"tool_call,omitempty"`
	Decision *MessageDecision `json:"decision,omitempty"`
}

// MessageTool is the structured tool-call + result, paired by ToolID.
type MessageTool struct {
	ToolID   string                 `json:"tool_id"`
	ToolName string                 `json:"tool_name"`
	Args     map[string]interface{} `json:"args"`
	Result   *MessageToolResult     `json:"result,omitempty"`
	Diff     *MessageDiff           `json:"diff,omitempty"` // populated by the builder for edit_file tool calls
}

// MessageToolResult is the upstream tool_result payload.
type MessageToolResult struct {
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

// MessageDiff is a server-side-rendered unified diff for edit_file tool calls.
type MessageDiff struct {
	Hunks []DiffHunk `json:"hunks"`
}

// DiffHunk is one contiguous changed region in a unified diff.
type DiffHunk struct {
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines"` // each line starts with '+', '-', or ' '
}

// MessageDecision carries the decision id, the workflow-YAML description, and any tags.
type MessageDecision struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Family      string   `json:"family,omitempty"` // pass | fail | escalate | skip
	Tags        []string `json:"tags,omitempty"`
}
