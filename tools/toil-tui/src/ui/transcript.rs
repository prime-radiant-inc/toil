use crate::app::App;
use crate::transcript_parser::ItemKind;
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, Paragraph, Wrap};

/// Max content lines before a block is folded.
const FOLD_THRESHOLD: usize = 5;

pub fn draw(frame: &mut Frame, area: Rect, app: &mut App, run_id: &str, node_id: &str) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(2), Constraint::Min(0)])
        .split(area);

    // Header
    let follow_indicator = if app.transcript_following {
        Span::styled(" following ●", Style::default().fg(Color::Green))
    } else {
        Span::raw("")
    };
    let header = Line::from(vec![
        Span::styled(run_id, Style::default().fg(Color::DarkGray)),
        Span::raw(" › "),
        Span::styled(node_id, Style::default().fg(Color::Cyan)),
        follow_indicator,
    ]);
    frame.render_widget(Paragraph::new(header), chunks[0]);

    // Transcript body
    let mut fold_map = Vec::new();
    let mut fold_indicators: Vec<(usize, usize)> = Vec::new(); // (line_idx, block_idx)
    let mut lines = render_transcript_items(
        &app.transcript_items,
        &app.transcript_expanded,
        &mut fold_map,
        &mut fold_indicators,
    );
    app.transcript_fold_map = fold_map;

    let total_lines = lines.len();
    let visible_height = chunks[1].height as usize;
    app.transcript_visible_height = visible_height;

    let scroll = if app.transcript_following {
        total_lines.saturating_sub(visible_height)
    } else {
        app.transcript_scroll
            .min(total_lines.saturating_sub(visible_height))
    };

    // Find the first fold indicator visible in the viewport and highlight it
    let viewport_end = scroll + visible_height;
    app.transcript_selected_fold = None;
    for &(line_idx, block_idx) in &fold_indicators {
        if line_idx >= scroll && line_idx < viewport_end {
            app.transcript_selected_fold = Some(block_idx);
            // Restyle that line to stand out
            if let Some(line) = lines.get_mut(line_idx) {
                *line = Line::styled(
                    raw_line_text(line),
                    Style::default().fg(Color::Yellow),
                );
            }
            break;
        }
    }

    // Store for key handler to snapshot when leaving follow mode
    app.transcript_rendered_scroll = scroll;

    let block = Block::default().borders(Borders::TOP);
    let paragraph = Paragraph::new(lines)
        .block(block)
        .wrap(Wrap { trim: false })
        .scroll((scroll as u16, 0));
    frame.render_widget(paragraph, chunks[1]);
}

/// Extract the raw text from a Line (discarding style info).
fn raw_line_text(line: &Line) -> String {
    let mut s = String::new();
    for span in &line.spans {
        s.push_str(span.content.as_ref());
    }
    s
}

/// Export the transcript to a plain text file (all folds expanded).
/// Returns the path written to, or an error message.
pub fn export_transcript(raw_items: &[String], run_id: &str, node_id: &str) -> Result<String, String> {
    // Render with all blocks expanded
    let all_expanded: std::collections::HashSet<usize> = (0..10000).collect();
    let mut fold_map = Vec::new();
    let mut fold_indicators = Vec::new();
    let lines = render_transcript_items(raw_items, &all_expanded, &mut fold_map, &mut fold_indicators);

    let mut text = format!("# {} › {}\n\n", run_id, node_id);
    for line in &lines {
        text.push_str(&raw_line_text(line));
        text.push('\n');
    }

    let filename = format!("{}_{}.txt", run_id, node_id);
    // Sanitize: replace slashes and colons
    let filename = filename.replace('/', "_").replace(':', "_");
    std::fs::write(&filename, &text).map_err(|e| e.to_string())?;

    // Return absolute path if possible
    match std::env::current_dir() {
        Ok(cwd) => Ok(cwd.join(&filename).to_string_lossy().to_string()),
        Err(_) => Ok(filename),
    }
}

/// Render content lines, folding blocks longer than FOLD_THRESHOLD unless expanded.
/// Populates `fold_map` with block indices of all foldable blocks,
/// and `fold_indicators` with (line_index, block_idx) of each fold indicator line.
fn render_transcript_items(
    raw_items: &[String],
    expanded: &std::collections::HashSet<usize>,
    fold_map: &mut Vec<usize>,
    fold_indicators: &mut Vec<(usize, usize)>,
) -> Vec<Line<'static>> {
    let mut lines = Vec::new();
    let mut block_idx: usize = 0;
    // Track recent text for deduplication — tool call messages often echo in results and plain text
    let mut recent_texts: Vec<String> = Vec::new();

    for raw in raw_items {
        let items = crate::transcript_parser::parse_transcript_text(raw);
        for item in items {
            match item.kind {
                ItemKind::Prompt => {
                    lines.push(Line::styled(
                        "▶ prompt",
                        Style::default().fg(Color::Blue),
                    ));
                    let rendered = humanize_text(&item.text);
                    let content_lines: Vec<&str> = rendered.lines().collect();
                    push_foldable(
                        &mut lines, fold_map, fold_indicators, expanded, block_idx,
                        &content_lines,
                        "  ", Style::default().fg(Color::DarkGray),
                    );
                    block_idx += 1;
                    lines.push(Line::from(""));
                }
                ItemKind::AssistantText => {
                    lines.push(Line::styled(
                        "assistant",
                        Style::default().fg(Color::Magenta),
                    ));
                    let rendered = humanize_text(&item.text);
                    let content_lines: Vec<&str> = rendered.lines().collect();
                    push_foldable(
                        &mut lines, fold_map, fold_indicators, expanded, block_idx,
                        &content_lines,
                        "  ", Style::default(),
                    );
                    block_idx += 1;
                    lines.push(Line::from(""));
                }
                ItemKind::ToolUse => {
                    let name = item.tool_name.as_deref().unwrap_or("tool");
                    match item.tool_input.as_ref() {
                        Some(input) => {
                            // Track text from tool input for dedup of echoed results
                            extract_dedup_texts(input, &mut recent_texts);
                            let display_text = pretty_print_if_json(input);
                            let content_lines: Vec<&str> = display_text.lines().collect();
                            // Try compact: join all args on one line if short enough
                            let joined = content_lines
                                .iter()
                                .map(|l| l.trim())
                                .collect::<Vec<_>>()
                                .join("  ");
                            if name.len() + joined.len() + 4 <= 100 {
                                lines.push(Line::from(vec![
                                    Span::styled(
                                        format!("  {} ", name),
                                        Style::default().fg(Color::Yellow),
                                    ),
                                    Span::styled(
                                        joined,
                                        Style::default().fg(Color::DarkGray),
                                    ),
                                ]));
                            } else {
                                // Long input: box format with folding
                                lines.push(Line::from(vec![
                                    Span::raw("  ┌ "),
                                    Span::styled(
                                        name.to_string(),
                                        Style::default().fg(Color::Yellow),
                                    ),
                                ]));
                                push_foldable(
                                    &mut lines, fold_map, fold_indicators, expanded, block_idx,
                                    &content_lines,
                                    "  │ ", Style::default().fg(Color::DarkGray),
                                );
                                lines.push(Line::styled(
                                    "  └──",
                                    Style::default().fg(Color::DarkGray),
                                ));
                            }
                        }
                        None => {
                            lines.push(Line::styled(
                                format!("  {}", name),
                                Style::default().fg(Color::Yellow),
                            ));
                        }
                    }
                    block_idx += 1;
                }
                ItemKind::ToolResult => {
                    // Filter out result lines that duplicate recent tool input text
                    let display_text = pretty_print_if_json(&item.text);
                    let content_lines: Vec<&str> = display_text
                        .lines()
                        .filter(|l| !is_duplicate_text(l.trim(), &recent_texts))
                        .collect();
                    if content_lines.is_empty() {
                        block_idx += 1;
                        continue;
                    }
                    if content_lines.len() == 1 {
                        // Compact result: single line with arrow
                        let result_text = content_lines[0].trim();
                        lines.push(Line::from(vec![
                            Span::styled("    ↳ ", Style::default().fg(Color::DarkGray)),
                            Span::raw(result_text.to_string()),
                        ]));
                    } else {
                        // Multi-line result with header
                        lines.push(Line::styled(
                            "    result",
                            Style::default().fg(Color::DarkGray),
                        ));
                        push_foldable(
                            &mut lines, fold_map, fold_indicators, expanded, block_idx,
                            &content_lines,
                            "    ", Style::default().fg(Color::DarkGray),
                        );
                    }
                    block_idx += 1;
                    lines.push(Line::from(""));
                }
                ItemKind::PlainText => {
                    let trimmed = item.text.trim();
                    // Skip plain text that duplicates a recent tool call message
                    if is_duplicate_text(trimmed, &recent_texts) {
                        block_idx += 1;
                        continue;
                    }
                    let rendered = humanize_text(&item.text);
                    let content_lines: Vec<&str> = rendered.lines().collect();
                    push_foldable(
                        &mut lines, fold_map, fold_indicators, expanded, block_idx,
                        &content_lines,
                        "", Style::default(),
                    );
                    block_idx += 1;
                    lines.push(Line::from(""));
                }
            }
        }
    }
    lines
}

/// Push content lines with optional folding. If content exceeds FOLD_THRESHOLD
/// and the block is not expanded, show only the first FOLD_THRESHOLD lines
/// plus a fold indicator. Records fold metadata for selection highlighting.
fn push_foldable(
    lines: &mut Vec<Line<'static>>,
    fold_map: &mut Vec<usize>,
    fold_indicators: &mut Vec<(usize, usize)>,
    expanded: &std::collections::HashSet<usize>,
    block_idx: usize,
    content_lines: &[&str],
    prefix: &str,
    style: Style,
) {
    let is_foldable = content_lines.len() > FOLD_THRESHOLD;
    if is_foldable {
        fold_map.push(block_idx);
    }

    let show_folded = is_foldable && !expanded.contains(&block_idx);
    let visible = if show_folded { FOLD_THRESHOLD } else { content_lines.len() };

    for text_line in &content_lines[..visible] {
        lines.push(Line::styled(format!("{}{}", prefix, text_line), style));
    }

    if show_folded {
        let hidden = content_lines.len() - FOLD_THRESHOLD;
        let indicator_line_idx = lines.len();
        fold_indicators.push((indicator_line_idx, block_idx));
        lines.push(Line::styled(
            format!("{}  ▸ {} more lines (e)", prefix, hidden),
            Style::default().fg(Color::DarkGray).add_modifier(Modifier::DIM),
        ));
    }
}

/// Extract string values from a JSON tool input for dedup tracking.
/// Pulls out "message", "text", "content" fields that runners commonly echo back.
fn extract_dedup_texts(input: &str, recent: &mut Vec<String>) {
    if let Ok(val) = serde_json::from_str::<serde_json::Value>(input) {
        for key in &["message", "text", "content"] {
            if let Some(s) = val.get(key).and_then(|v| v.as_str()) {
                if s.len() > 10 {
                    recent.push(s.to_string());
                    // Keep only last few to avoid false positives on unrelated text
                    if recent.len() > 8 {
                        recent.remove(0);
                    }
                }
            }
        }
    }
}

/// Check if text duplicates any recently seen content.
fn is_duplicate_text(text: &str, recent: &[String]) -> bool {
    if text.len() < 10 {
        return false;
    }
    recent.iter().any(|r| r.contains(text) || text.contains(r.as_str()))
}

/// Process text for human display. Handles:
/// - Bare JSON strings → rendered with human-friendly formatting
/// - Text with ``` code fences containing JSON → JSON rendered inline
/// - Plain text → returned as-is
fn humanize_text(s: &str) -> String {
    let trimmed = s.trim();

    // Bare JSON at top level
    if (trimmed.starts_with('{') && trimmed.ends_with('}'))
        || (trimmed.starts_with('[') && trimmed.ends_with(']'))
    {
        return pretty_print_if_json(trimmed);
    }

    // Check for code fences with JSON content
    if !trimmed.contains("```") {
        return s.to_string();
    }

    let mut result = String::new();
    let mut lines_iter = s.lines().peekable();
    while let Some(line) = lines_iter.next() {
        let stripped = line.trim();
        if stripped.starts_with("```json") || stripped.starts_with("```JSON") {
            // Collect everything until closing ```
            let mut json_buf = String::new();
            for inner in lines_iter.by_ref() {
                if inner.trim() == "```" {
                    break;
                }
                json_buf.push_str(inner);
                json_buf.push('\n');
            }
            result.push_str(&pretty_print_if_json(json_buf.trim()));
            result.push('\n');
        } else if stripped == "```" {
            // Non-JSON code fence — pass through
            result.push_str(line);
            result.push('\n');
            for inner in lines_iter.by_ref() {
                result.push_str(inner);
                result.push('\n');
                if inner.trim() == "```" {
                    break;
                }
            }
        } else {
            result.push_str(line);
            result.push('\n');
        }
    }
    result
}

/// Render a string for human consumption. If it's JSON, format it as
/// labeled fields with real newlines in string values (not JSON escapes).
/// Otherwise return as-is.
fn pretty_print_if_json(s: &str) -> String {
    let trimmed = s.trim();
    if (trimmed.starts_with('{') && trimmed.ends_with('}'))
        || (trimmed.starts_with('[') && trimmed.ends_with(']'))
    {
        if let Ok(val) = serde_json::from_str::<serde_json::Value>(trimmed) {
            let mut out = String::new();
            render_value_human(&mut out, &val, 0);
            return out;
        }
    }
    s.to_string()
}

/// Render a JSON value in a human-readable way:
/// - Objects: "key: value" with indentation
/// - Strings: actual content with real newlines (not JSON-escaped)
/// - Arrays: one item per line
/// - Numbers/bools/null: inline
fn render_value_human(out: &mut String, val: &serde_json::Value, depth: usize) {
    let indent = "  ".repeat(depth);
    match val {
        serde_json::Value::Object(map) => {
            for (key, value) in map {
                match value {
                    serde_json::Value::String(s) if s.contains('\n') => {
                        out.push_str(&format!("{}{}:\n", indent, key));
                        let inner = "  ".repeat(depth + 1);
                        for line in s.lines() {
                            out.push_str(&format!("{}{}\n", inner, line));
                        }
                    }
                    serde_json::Value::String(s) => {
                        out.push_str(&format!("{}{}: {}\n", indent, key, s));
                    }
                    serde_json::Value::Object(_) | serde_json::Value::Array(_) => {
                        out.push_str(&format!("{}{}:\n", indent, key));
                        render_value_human(out, value, depth + 1);
                    }
                    _ => {
                        out.push_str(&format!("{}{}: {}\n", indent, key, value));
                    }
                }
            }
        }
        serde_json::Value::Array(arr) => {
            for item in arr {
                match item {
                    serde_json::Value::String(s) => {
                        out.push_str(&format!("{}- {}\n", indent, s));
                    }
                    serde_json::Value::Object(_) | serde_json::Value::Array(_) => {
                        out.push_str(&format!("{}-\n", indent));
                        render_value_human(out, item, depth + 1);
                    }
                    _ => {
                        out.push_str(&format!("{}- {}\n", indent, item));
                    }
                }
            }
        }
        serde_json::Value::String(s) => {
            out.push_str(&format!("{}{}\n", indent, s));
        }
        _ => {
            out.push_str(&format!("{}{}\n", indent, val));
        }
    }
}
