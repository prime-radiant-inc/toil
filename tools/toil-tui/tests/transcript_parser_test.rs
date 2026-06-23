use toil_tui::transcript_parser::*;

#[test]
fn parse_anthropic_text_message() {
    let text = r#"{"type":"message","role":"assistant","content":[{"type":"text","text":"I will implement the feature."}]}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::AssistantText);
    assert!(items[0].text.contains("I will implement"));
}

#[test]
fn parse_anthropic_tool_use() {
    let text = r#"{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/tmp/foo.rs"}}]}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::ToolUse);
    assert!(items[0].tool_name.as_deref() == Some("Read"));
}

#[test]
fn parse_tool_result() {
    let text = r#"{"type":"tool_result","tool_use_id":"tu_1","content":"file contents here"}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::ToolResult);
}

#[test]
fn parse_plain_text_fallback() {
    let text = "Just some plain text output";
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::PlainText);
    assert_eq!(items[0].text, "Just some plain text output");
}

/// Toil wraps messages in {"message":{...},"type":"user"} envelope.
/// The parser must unwrap this to extract the inner content blocks.
#[test]
fn parse_toil_user_envelope() {
    let text = r#"{"message":{"content":[{"text":"set -eu\ncd /tmp\necho hello","type":"text"}],"role":"user"},"synthetic":true,"type":"user"}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::Prompt);
    assert!(items[0].text.contains("set -eu"));
    assert!(items[0].text.contains("echo hello"));
}

/// Toil wraps assistant messages similarly.
#[test]
fn parse_toil_assistant_envelope() {
    let text = r#"{"message":{"content":[{"text":"Here is the result.","type":"text"}],"role":"assistant"},"type":"assistant"}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::AssistantText);
    assert!(items[0].text.contains("Here is the result"));
}

// --- Serf log record tests ---

#[test]
fn parse_serf_assistant_text_end() {
    let text = r#"{"kind":"ASSISTANT_TEXT_END","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"text":"Here is my analysis of the code.","finish_reason":"end_turn","input_tokens":500,"output_tokens":100}}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::AssistantText);
    assert_eq!(items[0].text, "Here is my analysis of the code.");
}

#[test]
fn parse_serf_assistant_text_end_empty() {
    // When finish_reason is tool_calls, text is empty — should produce no items
    let text = r#"{"kind":"ASSISTANT_TEXT_END","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"text":"","finish_reason":"tool_calls","input_tokens":500,"output_tokens":0}}"#;
    let items = parse_transcript_text(text);
    assert!(items.is_empty());
}

#[test]
fn parse_serf_tool_call_start() {
    let text = r#"{"kind":"TOOL_CALL_START","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"Read","tool_call_id":"tc_1","arguments_json":"{\"file_path\":\"/tmp/foo.rs\"}"}}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::ToolUse);
    assert_eq!(items[0].tool_name.as_deref(), Some("Read"));
    assert!(items[0].tool_input.as_ref().unwrap().contains("file_path"));
}

#[test]
fn parse_serf_tool_call_end_skipped() {
    // TOOL_CALL_END is skipped — results come from TOOL_CALL_OUTPUT_DELTA instead
    let text = r#"{"kind":"TOOL_CALL_END","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"Read","tool_call_id":"tc_1","output":"fn main() {}"}}"#;
    let items = parse_transcript_text(text);
    assert!(items.is_empty());
}

#[test]
fn parse_serf_tool_call_output_delta() {
    let text = r#"{"kind":"TOOL_CALL_OUTPUT_DELTA","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"shell","call_id":"tc_1","delta":"FFFF [100%]\n4 failed"}}"#;
    let items = parse_transcript_text(text);
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].kind, ItemKind::ToolResult);
    assert!(items[0].text.contains("FFFF"));
    assert_eq!(items[0].tool_name.as_deref(), Some("shell"));
}

#[test]
fn parse_serf_tool_call_output_delta_empty() {
    let text = r#"{"kind":"TOOL_CALL_OUTPUT_DELTA","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"shell","call_id":"tc_1","delta":""}}"#;
    let items = parse_transcript_text(text);
    assert!(items.is_empty());
}

#[test]
fn parse_serf_submit_result_filtered() {
    // SUBMIT_RESULT is redundant with tool call content — always filtered out
    let text = r#"{"kind":"SUBMIT_RESULT","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"message":"Task completed successfully"}}"#;
    let items = parse_transcript_text(text);
    assert!(items.is_empty());
}

#[test]
fn parse_serf_metadata_records_filtered() {
    for kind in &["SESSION_START", "SESSION_END", "ASSISTANT_TEXT_START", "PROMPT_LOADED", "USER_INPUT"] {
        let text = format!(r#"{{"kind":"{}","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{{}}}}"#, kind);
        let items = parse_transcript_text(&text);
        assert!(items.is_empty(), "Expected {} to be filtered out, got {} items", kind, items.len());
    }
}

#[test]
fn parse_serf_unknown_kind_filtered() {
    let text = r#"{"kind":"SOME_FUTURE_EVENT","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"foo":"bar"}}"#;
    let items = parse_transcript_text(text);
    assert!(items.is_empty());
}
