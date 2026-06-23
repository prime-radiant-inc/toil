package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const inputPreviewBytes = 1024

// MaterializeDispatchInputs writes every input as a file under inputsDir
// (caller computes as runDir/dispatches/<stateNodeID>/<N>/inputs/).
//
// Idempotent: writing the same content to the same paths produces the
// same on-disk state. Called on every executeRole/executeHuman entry
// (not just first attempt) so a partial write on attempt 1 is fully
// recovered by attempt 2.
//
// Per-key serialization errors are tolerated (key absent from disk;
// prompt renderer falls back to previewInputFallback). Filesystem
// errors propagate.
//
// Handles keys containing path separators by mkdir'ing each file's
// parent. Workflow input keys come from workflow YAML (trusted
// authorship surface); path-traversal keys are not sanitized.
func MaterializeDispatchInputs(inputsDir string, inputs map[string]any) error {
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		return fmt.Errorf("create dispatch inputs dir: %w", err)
	}
	for _, key := range sortedKeys(inputs) {
		filename, content, err := serializeInput(key, inputs[key])
		if err != nil {
			continue // per-key tolerance
		}
		path := filepath.Join(inputsDir, filename)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create parent dir for %q: %w", key, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", key, err)
		}
	}
	return nil
}

// DiffAgainstPriorDispatch returns the subset of currentInputs whose
// serialized bytes differ from the file in priorInputsDir, keyed by
// the ORIGINAL key. Missing prior dir / unreadable prior file /
// current-value serialization failure → treat as changed.
//
// Returns map only (no error). Every failure is mapped to "include
// in deltas."
func DiffAgainstPriorDispatch(currentInputs map[string]any, priorInputsDir string) map[string]any {
	deltas := map[string]any{}
	for _, key := range sortedKeys(currentInputs) {
		filename, content, err := serializeInput(key, currentInputs[key])
		if err != nil {
			deltas[key] = currentInputs[key]
			continue
		}
		prior, err := os.ReadFile(filepath.Join(priorInputsDir, filename))
		if err != nil || !bytes.Equal(content, prior) {
			deltas[key] = currentInputs[key]
		}
	}
	return deltas
}

// serializeInput renders an input as (filename, bytes).
// Strings → key.md (raw). Everything else → key.json (MarshalIndent).
func serializeInput(key string, value any) (string, []byte, error) {
	if s, ok := value.(string); ok {
		return key + ".md", []byte(s), nil
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return key + ".json", b, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// previewInput renders one input as heading + fenced content.
// Fence sized to exceed any backtick run in content. Truncates above
// inputPreviewBytes at a UTF-8 codepoint boundary. Truncation marker
// references filePath when set.
func previewInput(headingKey string, content []byte, filename, filePath string) string {
	preview, truncated := truncatePreview(content)
	fence := pickFence(preview)
	// Heading shows the on-disk path so the LLM can read the file at any time;
	// falls back to just the filename when no path is known (approvals).
	headingFile := filename
	if filePath != "" {
		headingFile = filePath
	}
	marker := ""
	if truncated && filePath != "" {
		marker = fmt.Sprintf("\n…truncated; read %s for the full content\n", filePath)
	} else if truncated {
		marker = "\n…truncated\n"
	}
	return fmt.Sprintf(
		"### %s — %d bytes (file: %s)\n%s\n%s%s\n%s\n\n",
		headingKey, len(content), headingFile,
		fence,
		string(preview), marker,
		fence,
	)
}

func previewInputFallback(headingKey string, value any) string {
	return fmt.Sprintf("### %s — (could not serialize)\n```\n%#v\n```\n\n", headingKey, value)
}

func truncatePreview(content []byte) ([]byte, bool) {
	if len(content) <= inputPreviewBytes {
		return content, false
	}
	cut := inputPreviewBytes
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return content[:cut], true
}

func pickFence(content []byte) string {
	longest, current := 0, 0
	for _, b := range content {
		if b == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}
