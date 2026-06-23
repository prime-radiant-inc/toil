use toil_tui::sse::parse_sse_frame;

#[test]
fn parse_node_started_event() {
    let lines = vec![
        "event: node_started",
        r#"data: {"timestamp":"2026-03-12T10:00:00Z","type":"node_started","run_id":"r1","node_id":"plan"}"#,
        "",
    ];
    let event = parse_sse_frame(&lines).unwrap();
    assert_eq!(event.event_type, "node_started");
    assert_eq!(event.node_id.as_deref(), Some("plan"));
}

#[test]
fn parse_multiline_data() {
    let lines = vec![
        "event: node_output",
        r#"data: {"timestamp":"2026-03-12T10:00:00Z","type":"node_output","run_id":"r1","node_id":"write","text":"line1"}"#,
        "",
    ];
    let event = parse_sse_frame(&lines).unwrap();
    assert_eq!(event.event_type, "node_output");
    assert!(event.text.is_some());
}

#[test]
fn skip_comment_lines() {
    let lines = vec![": ping", ""];
    let event = parse_sse_frame(&lines);
    assert!(event.is_none());
}

#[test]
fn skip_empty_frame() {
    let lines: Vec<&str> = vec![""];
    let event = parse_sse_frame(&lines);
    assert!(event.is_none());
}
