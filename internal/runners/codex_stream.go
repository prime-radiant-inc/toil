package runners

import "encoding/json"

type CodexStream struct {
	SessionID string
}

func NewCodexStream() *CodexStream {
	return &CodexStream{}
}

func (stream *CodexStream) Handle(line string) (string, error) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", err
	}

	if eventType, ok := event["type"].(string); ok {
		switch eventType {
		case "thread.started":
			if id, ok := event["thread_id"].(string); ok {
				stream.SessionID = id
			}
		case "item.completed":
			item, ok := event["item"].(map[string]any)
			if !ok {
				return "", nil
			}
			itemType, _ := item["type"].(string)
			if itemType != "agent_message" {
				return "", nil
			}
			if text, ok := item[typeText].(string); ok {
				return text, nil
			}
		}
	}

	return "", nil
}
