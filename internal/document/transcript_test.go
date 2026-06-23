package document

import (
	"strings"
	"testing"
	"time"

	"primeradiant.com/toil/internal/state"
)

func TestBuildTranscript_SingleAttemptSuccess(t *testing.T) {
	now := time.Now()
	events := []state.Event{
		{Timestamp: now, Type: "node_prompt", NodeID: "n1", Text: "system+user task prompt"},
		{Timestamp: now, Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Timestamp: now, Type: "node_output", NodeID: "n1", Stream: "stdout", Text: "thinking..."},
		{Timestamp: now, Type: "node_output", NodeID: "n1", Stream: "stderr", Text: `{"kind":"TOOL_CALL_START","data":{"tool_name":"read_file","call_id":"t1","arguments_json":"{\"file_path\":\"/a\"}"}}`},
		{Timestamp: now, Type: "node_output", NodeID: "n1", Stream: "stderr", Text: `{"kind":"TOOL_CALL_END","data":{"tool_name":"read_file","call_id":"t1","output":"file contents"}}`},
		{Timestamp: now, Type: "node_output", NodeID: "n1", Stream: "stdout", Text: "done"},
		{Timestamp: now, Type: "node_completed", NodeID: "n1", Data: map[string]any{"attempt": 1, "decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(tr.Attempts))
	}
	a := tr.Attempts[0]
	if a.Outcome != "succeeded" {
		t.Fatalf("want succeeded, got %s", a.Outcome)
	}
	// Expect: user_prompt, assistant("thinking...done"), tool_call(t1 with result), assistant("done") OR coalesced, decision(pass)
	if len(a.Messages) < 4 {
		t.Fatalf("want >=4 messages, got %d: %+v", len(a.Messages), a.Messages)
	}
	if a.Messages[0].Kind != "user_prompt" {
		t.Fatalf("first message kind: %s", a.Messages[0].Kind)
	}
	// Find the tool_call message
	var foundToolCall bool
	var foundDecision bool
	for _, m := range a.Messages {
		if m.Kind == "tool_call" && m.ToolCall != nil && m.ToolCall.ToolID == "t1" && m.ToolCall.Result != nil {
			foundToolCall = true
		}
		if m.Kind == "decision" && m.Decision != nil && m.Decision.ID == "pass" {
			foundDecision = true
		}
	}
	if !foundToolCall {
		t.Fatalf("missing tool_call with result: %+v", a.Messages)
	}
	if !foundDecision {
		t.Fatalf("missing decision: %+v", a.Messages)
	}
}

func TestBuildTranscript_MultiAttemptWithFailure(t *testing.T) {
	events := []state.Event{
		{Type: "node_prompt", NodeID: "n1", Text: "task"},
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Type: "node_output", NodeID: "n1", Text: "try one"},
		{Type: "node_attempt_failed", NodeID: "n1", Data: map[string]any{"attempt": 1, "reason": "rate_limit"}},
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 2}},
		{Type: "node_output", NodeID: "n1", Text: "try two"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"attempt": 2, "decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(tr.Attempts))
	}
	if tr.Attempts[0].Outcome != "failed" || tr.Attempts[0].FailureReason != "rate_limit" {
		t.Fatalf("first attempt: %+v", tr.Attempts[0])
	}
	if tr.Attempts[1].Outcome != "succeeded" {
		t.Fatalf("second attempt: %+v", tr.Attempts[1])
	}
}

func TestBuildTranscript_OtherNodeEventsIgnored(t *testing.T) {
	events := []state.Event{
		{Type: "node_prompt", NodeID: "n1", Text: "for n1"},
		{Type: "node_output", NodeID: "n2", Text: "should be ignored"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(tr.Attempts))
	}
	for _, m := range tr.Attempts[0].Messages {
		if m.Text == "should be ignored" {
			t.Fatalf("leaked event from n2: %+v", m)
		}
	}
}

func TestBuildTranscript_PopulatesHTMLForAssistantAndUserPrompt(t *testing.T) {
	events := []state.Event{
		{Type: "node_prompt", NodeID: "n1", Text: "Please **read** the file."},
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Type: "node_output", NodeID: "n1", Text: "I'll start by reading.\n\n- step 1\n- step 2"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) == 0 {
		t.Fatalf("no attempts")
	}
	var foundUserHTML, foundAssistantHTML bool
	for _, m := range tr.Attempts[0].Messages {
		if m.Kind == "user_prompt" && m.HTML != "" {
			foundUserHTML = true
			if !strings.Contains(m.HTML, "<strong>read</strong>") {
				t.Errorf("user_prompt HTML missing bold: %q", m.HTML)
			}
		}
		if m.Kind == "assistant" && m.HTML != "" {
			foundAssistantHTML = true
			// Bulleted list should produce <ul><li>...
			if !strings.Contains(m.HTML, "<ul>") || !strings.Contains(m.HTML, "<li>") {
				t.Errorf("assistant HTML missing list: %q", m.HTML)
			}
		}
	}
	if !foundUserHTML {
		t.Fatalf("user_prompt HTML not populated")
	}
	if !foundAssistantHTML {
		t.Fatalf("assistant HTML not populated")
	}
}

func TestBuildTranscript_IgnoresNonToolSerfEvents(t *testing.T) {
	events := []state.Event{
		{Type: "node_prompt", NodeID: "n1", Text: "task"},
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Type: "node_output", NodeID: "n1", Stream: "stderr", Text: `{"kind":"SESSION_START","session_id":"01ABC"}`},
		{Type: "node_output", NodeID: "n1", Stream: "stdout", Text: "regular agent text"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) == 0 {
		t.Fatalf("no attempts")
	}
	// Should have user_prompt + assistant(regular agent text) + decision; no tool calls.
	var assistantText string
	for _, m := range tr.Attempts[0].Messages {
		if m.Kind == "assistant" {
			assistantText = m.Text
		}
		if m.Kind == "tool_call" {
			t.Fatalf("unexpected tool_call: %+v", m)
		}
	}
	if assistantText != "regular agent text" {
		t.Fatalf("missing or wrong assistant text: %q", assistantText)
	}
}

func TestBuildTranscript_FallbackToNodeStartedAttempts(t *testing.T) {
	// Old-style run: no node_attempt_started events; node_started marks each attempt.
	events := []state.Event{
		{Type: "node_started", NodeID: "n1", Data: map[string]any{"resume": false}},
		{Type: "node_prompt", NodeID: "n1", Text: "initial task"},
		{Type: "node_output", NodeID: "n1", Text: "first attempt output"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"decision": "reject"}},
		{Type: "node_started", NodeID: "n1", Data: map[string]any{"resume": true}},
		{Type: "node_prompt", NodeID: "n1", Text: "The plan reviewer rejected your plan..."},
		{Type: "node_output", NodeID: "n1", Text: "second attempt output"},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"attempt": 2, "decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d: %+v", len(tr.Attempts), tr.Attempts)
	}
	if tr.Attempts[0].Ordinal != 1 {
		t.Errorf("attempt[0].Ordinal = %d, want 1", tr.Attempts[0].Ordinal)
	}
	if tr.Attempts[1].Ordinal != 2 {
		t.Errorf("attempt[1].Ordinal = %d, want 2", tr.Attempts[1].Ordinal)
	}
	// Each attempt should have its own user_prompt.
	findKind := func(a Attempt, kind string) bool {
		for _, m := range a.Messages {
			if m.Kind == kind {
				return true
			}
		}
		return false
	}
	if !findKind(tr.Attempts[0], "user_prompt") {
		t.Errorf("attempt[0] missing user_prompt: %+v", tr.Attempts[0].Messages)
	}
	if !findKind(tr.Attempts[1], "user_prompt") {
		t.Errorf("attempt[1] missing user_prompt: %+v", tr.Attempts[1].Messages)
	}
	// Second attempt should have the re-run prompt text.
	var found bool
	for _, m := range tr.Attempts[1].Messages {
		if m.Kind == "user_prompt" && strings.Contains(m.Text, "rejected") {
			found = true
		}
	}
	if !found {
		t.Errorf("attempt[1] user_prompt missing re-run text: %+v", tr.Attempts[1].Messages)
	}
}

func TestBuildTranscript_SuppressesStructuredOutputBlob(t *testing.T) {
	blob := `{"decision":"ready_for_review","message":"Here is my plan.","data":{"plan":{"tasks":[]}},"artifacts":[]}`
	events := []state.Event{
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Type: "node_output", NodeID: "n1", Text: "thinking about the problem"},
		{Type: "node_output", NodeID: "n1", Text: blob},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"attempt": 1, "decision": "ready_for_review"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(tr.Attempts))
	}
	for _, m := range tr.Attempts[0].Messages {
		if m.Kind == "assistant" && strings.Contains(m.Text, `"decision"`) {
			t.Errorf("structured-output blob leaked into assistant text: %q", m.Text)
		}
	}
	// The legitimate assistant message should still be there.
	var foundAssistant bool
	for _, m := range tr.Attempts[0].Messages {
		if m.Kind == "assistant" && m.Text == "thinking about the problem" {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Errorf("legitimate assistant text missing: %+v", tr.Attempts[0].Messages)
	}
}

// TestBuildTranscript_SingleExecutionSlice verifies that BuildTranscript, when
// called with events from a single execution window (as produced by
// sliceExecutionEvents), yields exactly one attempt with the correct content.
// The "interlude" concept is gone: review_plan rows appear as their own Row
// in the document rather than as an interlude inside plan_tasks's transcript.
func TestBuildTranscript_SingleExecutionSlice(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(5 * time.Second)
	// A single plan_tasks execution — events already sliced to just this window.
	events := []state.Event{
		{Timestamp: t1, Type: "node_attempt_started", NodeID: "plan_tasks", Data: map[string]any{"attempt": 1}},
		{Timestamp: t1, Type: "node_output", NodeID: "plan_tasks", Text: "first plan"},
		{Timestamp: t2, Type: "node_completed", NodeID: "plan_tasks", Data: map[string]any{"attempt": 1, "decision": "ready_for_review"}},
	}
	tr := BuildTranscript("plan_tasks", events)
	if len(tr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(tr.Attempts))
	}
	if tr.Attempts[0].Outcome != "succeeded" {
		t.Errorf("outcome: got %q, want succeeded", tr.Attempts[0].Outcome)
	}
	foundDecision := false
	for _, m := range tr.Attempts[0].Messages {
		if m.Kind == "decision" && m.Decision != nil && m.Decision.ID == "ready_for_review" {
			foundDecision = true
		}
		if m.Kind == "interlude" {
			t.Errorf("unexpected interlude in single-execution transcript")
		}
	}
	if !foundDecision {
		t.Errorf("decision message not found: %+v", tr.Attempts[0].Messages)
	}
}

func TestBuildTranscript_SplitsNodePromptIntoSystemAndUserPrompt(t *testing.T) {
	promptWithMarkers := "You are a skilled engineer.\n\n<!-- LOCAL -->\nPlease implement the feature.\n<!-- /LOCAL -->\n\nAlways follow best practices."
	events := []state.Event{
		{Type: "node_attempt_started", NodeID: "n1", Data: map[string]any{"attempt": 1}},
		{Type: "node_prompt", NodeID: "n1", Text: promptWithMarkers},
		{Type: "node_output", NodeID: "n1", Text: "I will implement it."},
		{Type: "node_completed", NodeID: "n1", Data: map[string]any{"attempt": 1, "decision": "pass"}},
	}
	tr := BuildTranscript("n1", events)
	if len(tr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(tr.Attempts))
	}
	msgs := tr.Attempts[0].Messages
	var systemMsg, userMsg *Message
	for i := range msgs {
		switch msgs[i].Kind {
		case "system_prompt":
			systemMsg = &msgs[i]
		case "user_prompt":
			userMsg = &msgs[i]
		}
	}
	if systemMsg == nil {
		t.Fatalf("missing system_prompt message: %+v", msgs)
	}
	if userMsg == nil {
		t.Fatalf("missing user_prompt message: %+v", msgs)
	}
	if !strings.Contains(systemMsg.Text, "You are a skilled engineer") {
		t.Errorf("system_prompt missing boilerplate: %q", systemMsg.Text)
	}
	if !strings.Contains(userMsg.Text, "Please implement the feature") {
		t.Errorf("user_prompt missing local content: %q", userMsg.Text)
	}
	// system_prompt should NOT have HTML rendered.
	if systemMsg.HTML != "" {
		t.Errorf("system_prompt should not have HTML rendered, got: %q", systemMsg.HTML)
	}
	// user_prompt should have HTML rendered.
	if userMsg.HTML == "" {
		t.Errorf("user_prompt should have HTML rendered")
	}
}
