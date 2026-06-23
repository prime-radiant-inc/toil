use toil_tui::report::extract_communicate_message;

#[test]
fn extract_serf_communicate_message() {
    let text = r#"{"kind":"TOOL_CALL_START","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"communicate","tool_call_id":"tc_1","arguments_json":"{\"message\":\"Wrote spectator dashboard tests; verified they currently fail.\",\"decision\":\"tests_written\",\"data\":{}}"}}"#;
    let msg = extract_communicate_message(text);
    assert_eq!(
        msg.as_deref(),
        Some("Wrote spectator dashboard tests; verified they currently fail.")
    );
}

#[test]
fn extract_serf_non_communicate_tool() {
    let text = r#"{"kind":"TOOL_CALL_START","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"tool_name":"Read","tool_call_id":"tc_1","arguments_json":"{\"file_path\":\"/tmp/foo.rs\"}"}}"#;
    let msg = extract_communicate_message(text);
    assert!(msg.is_none());
}

#[test]
fn extract_anthropic_communicate_message() {
    let text = r#"{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"communicate","input":{"message":"Implementation complete.","decision":"done","data":{}}}]}"#;
    let msg = extract_communicate_message(text);
    assert_eq!(msg.as_deref(), Some("Implementation complete."));
}

#[test]
fn extract_toil_envelope_communicate() {
    let text = r#"{"message":{"content":[{"type":"tool_use","id":"tu_1","name":"communicate","input":{"message":"All tests pass.","decision":"done","data":{}}}],"role":"assistant"},"type":"assistant"}"#;
    let msg = extract_communicate_message(text);
    assert_eq!(msg.as_deref(), Some("All tests pass."));
}

#[test]
fn extract_no_communicate_in_plain_text() {
    let msg = extract_communicate_message("Just some plain text");
    assert!(msg.is_none());
}

#[test]
fn extract_no_communicate_in_other_json() {
    let text = r#"{"kind":"ASSISTANT_TEXT_END","session_id":"s1","timestamp":"2026-03-12T00:00:00Z","data":{"text":"Hello","finish_reason":"end_turn"}}"#;
    let msg = extract_communicate_message(text);
    assert!(msg.is_none());
}
