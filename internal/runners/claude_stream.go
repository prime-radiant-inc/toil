package runners

import "encoding/json"

type ClaudeStream struct {
	SessionID string
	Result    string
	HasResult bool
}

func NewClaudeStream() *ClaudeStream {
	return &ClaudeStream{}
}

func (stream *ClaudeStream) Handle(line string) (string, error) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", err
	}

	if id, ok := event["session_id"].(string); ok {
		stream.SessionID = id
	}

	if eventType, ok := event["type"].(string); ok && eventType == "assistant" {
		message, ok := event[keyMessage].(map[string]any)
		if !ok {
			return "", nil
		}
		content, ok := message["content"].([]any)
		if !ok {
			return "", nil
		}
		for _, item := range content {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := itemMap["type"].(string)
			if itemType != typeText {
				continue
			}
			if text, ok := itemMap[typeText].(string); ok {
				return text, nil
			}
		}
	}

	if eventType, ok := event["type"].(string); ok && eventType == typeResult {
		if result, ok := event[typeResult].(string); ok {
			stream.Result = result
			stream.HasResult = true
		}
	}

	return "", nil
}
