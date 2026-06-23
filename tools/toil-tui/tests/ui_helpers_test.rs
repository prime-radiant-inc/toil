#[test]
fn test_status_icon_mapping() {
    let cases = vec![
        ("running", "●"),
        ("completed", "✓"),
        ("failed", "✗"),
        ("paused", "◉"),
        ("cancelled", "⊘"),
        ("pending", "○"),
        ("unknown_status", "○"),  // fallback is ○ not ?
    ];
    for (status, expected_icon) in cases {
        let (icon, _) = toil_tui::ui::runs_tab::status_icon(status);
        assert_eq!(icon, expected_icon, "status '{}' should map to '{}'", status, expected_icon);
    }
}

#[test]
fn test_truncate_ascii() {
    assert_eq!(toil_tui::ui::runs_tab::truncate("hello world", 5), "hello");
    assert_eq!(toil_tui::ui::runs_tab::truncate("hi", 10), "hi");
}

#[test]
fn test_truncate_unicode_safe() {
    let s = "héllo wörld";
    let result = toil_tui::ui::runs_tab::truncate(s, 5);
    assert_eq!(result.chars().count(), 5);
}

#[test]
fn test_format_duration() {
    assert_eq!(toil_tui::ui::runs_tab::format_duration(30), "30s");
    assert_eq!(toil_tui::ui::runs_tab::format_duration(90), "1m");
    assert_eq!(toil_tui::ui::runs_tab::format_duration(3600), "1h");
}
