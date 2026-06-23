package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type NodeOutput struct {
	Decision  string         `json:"decision"`
	Message   string         `json:"message"`
	Artifacts []string       `json:"artifacts,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	// Tags, Status, Attempts are engine-managed metadata copied from
	// NodeState at completion time so downstream `node.X.<field>`
	// expressions can route on them. They are not part of the runner's
	// output payload — runners never write these directly.
	Tags     []string `json:"tags,omitempty"`
	Status   string   `json:"status,omitempty"`
	Attempts int      `json:"attempts,omitempty"`
	// LastRoutingDecision is the meta-decision (_loop_exhausted, _timeout)
	// the engine synthesized for this node's last transition, if any.
	// Empty for nodes that completed with their own real decision.
	// Surfaces NodeState.LastRoutingDecision at envelope read time.
	LastRoutingDecision string `json:"last_routing_decision,omitempty"`
	// LoopIterations is the persisted per-dispatch loop counter at the
	// time of envelope construction. After _loop_exhausted has fired,
	// reads as the exhausted count (e.g., max_loop_iterations) — NOT
	// zero. Reset happens on the next dispatch start, not at emit time.
	LoopIterations int    `json:"loop_iterations,omitempty"`
	Raw            string `json:"-"`
	ToolCalls      int    `json:"-"`
}

// ToMap returns a map representation used for ForEach iteration results.
func (o NodeOutput) ToMap() map[string]any {
	return map[string]any{
		fieldDecision:            o.Decision,
		fieldMessage:             o.Message,
		fieldArtifacts:           o.Artifacts,
		fieldData:                o.Data,
		fieldSessionID:           o.SessionID,
		fieldTags:                o.Tags,
		fieldStatus:              o.Status,
		fieldAttempts:            o.Attempts,
		fieldLastRoutingDecision: o.LastRoutingDecision,
		fieldLoopIterations:      o.LoopIterations,
	}
}

func normalizeArtifacts(raw any) []string {
	switch typed := raw.(type) {
	case nil:
		return nil
	case string:
		if path, ok := sanitizeArtifactPath(typed); ok {
			return []string{path}
		}
		return nil
	case []any:
		var artifacts []string
		for _, item := range typed {
			switch value := item.(type) {
			case string:
				if path, ok := sanitizeArtifactPath(value); ok {
					artifacts = append(artifacts, path)
				}
			case map[string]any:
				if path, ok := value["path"].(string); ok {
					if normalized, valid := sanitizeArtifactPath(path); valid {
						artifacts = append(artifacts, normalized)
					}
				}
			}
		}
		return artifacts
	default:
		return nil
	}
}

func sanitizeArtifactPath(path string) (string, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", false
	}
	// Artifact entries must be filesystem paths, not inline markdown blobs.
	if strings.ContainsAny(trimmed, "\r\n") {
		return "", false
	}
	return trimmed, true
}

func ParseNodeOutput(output string) (NodeOutput, error) {
	payload, err := extractJSONPayload(output)
	if err != nil {
		return NodeOutput{}, err
	}

	decoded, err := decodeJSONObject(payload)
	if err != nil {
		return NodeOutput{}, err
	}

	parsed := NodeOutput{Raw: output}
	if decision, ok := decoded[fieldDecision].(string); ok {
		parsed.Decision = decision
	}
	if message, ok := decoded[fieldMessage].(string); ok {
		parsed.Message = message
	}
	if dataRaw, ok := decoded[fieldData]; ok {
		if data, isMap := dataRaw.(map[string]any); isMap {
			parsed.Data = data
		}
	}
	if artifactsRaw, ok := decoded[fieldArtifacts]; ok {
		parsed.Artifacts = normalizeArtifacts(artifactsRaw)
	}

	return parsed, nil
}

func extractJSONPayload(output string) (string, error) {
	if block, ok := extractFencedJSON(output); ok {
		if strings.TrimSpace(block) == "" {
			return "", fmt.Errorf("json output block is empty")
		}
		return block, nil
	}

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", fmt.Errorf("json output block not found")
	}
	return trimmed, nil
}

func decodeJSONObject(payload string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid json output: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid json output: multiple JSON values found")
		}
		return nil, fmt.Errorf("invalid json output: %w", err)
	}

	objectValue, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("node output must be a JSON object")
	}

	return objectValue, nil
}

func extractFencedJSON(output string) (string, bool) {
	lines := strings.Split(output, "\n")
	for start := len(lines) - 1; start >= 0; start-- {
		if !isJSONFenceStart(lines[start]) {
			continue
		}
		for end := start + 1; end < len(lines); end++ {
			if !isFenceLine(lines[end]) {
				continue
			}
			return strings.TrimSpace(strings.Join(lines[start+1:end], "\n")), true
		}
	}
	return "", false
}

func isFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

func isJSONFenceStart(line string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(line))
	if !strings.HasPrefix(trimmed, "```json") {
		return false
	}
	if len(trimmed) == len("```json") {
		return true
	}
	suffix := trimmed[len("```json")]
	return suffix == ' ' || suffix == '\t'
}
