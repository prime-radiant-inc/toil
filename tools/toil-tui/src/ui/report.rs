use crate::app::App;
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, Paragraph, Wrap};

pub fn draw(frame: &mut Frame, area: Rect, app: &mut App, run_id: &str, content: &str) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(2), Constraint::Min(0)])
        .split(area);

    // Header
    let header = Line::from(vec![
        Span::styled("Session Report", Style::default().fg(Color::Cyan).bold()),
        Span::raw(" › "),
        Span::styled(run_id.to_string(), Style::default().fg(Color::DarkGray)),
    ]);
    frame.render_widget(Paragraph::new(header), chunks[0]);

    // Render content with styling
    let lines = style_report(content);
    let total_lines = lines.len();
    let visible_height = chunks[1].height as usize;
    app.report_visible_height = visible_height;

    let scroll = app
        .report_scroll
        .min(total_lines.saturating_sub(visible_height));
    app.report_scroll = scroll;

    let block = Block::default().borders(Borders::TOP);
    let paragraph = Paragraph::new(lines)
        .block(block)
        .wrap(Wrap { trim: false })
        .scroll((scroll as u16, 0));
    frame.render_widget(paragraph, chunks[1]);
}

/// Apply syntax highlighting to the report content.
fn style_report(content: &str) -> Vec<Line<'static>> {
    content
        .lines()
        .map(|line| {
            // Header lines
            if line.starts_with("# ") {
                return Line::styled(
                    line.to_string(),
                    Style::default().fg(Color::Cyan).bold(),
                );
            }
            if line == "---" {
                return Line::styled(
                    "────────────────────────────────────────",
                    Style::default().fg(Color::DarkGray),
                );
            }

            // Metadata lines (Workflow:, Started:, etc.)
            if let Some((key, val)) = line.split_once(": ") {
                if matches!(
                    key,
                    "Workflow" | "Title" | "Started" | "Finished" | "Status"
                ) {
                    return Line::from(vec![
                        Span::styled(
                            format!("{}: ", key),
                            Style::default().fg(Color::DarkGray),
                        ),
                        Span::raw(val.to_string()),
                    ]);
                }
            }

            // Communicate message lines (indented with │)
            if line.contains("│ ") {
                let indent_end = line.find('│').unwrap_or(0);
                let indent = &line[..indent_end];
                let msg = &line[indent_end + "│ ".len()..];
                return Line::from(vec![
                    Span::raw(indent.to_string()),
                    Span::styled("│ ", Style::default().fg(Color::DarkGray)),
                    Span::styled(msg.to_string(), Style::default().fg(Color::White)),
                ]);
            }

            // Status icon lines
            let trimmed = line.trim_start();
            let indent = &line[..line.len() - trimmed.len()];
            if trimmed.starts_with("✓ ") {
                return Line::from(vec![
                    Span::raw(indent.to_string()),
                    Span::styled("✓ ", Style::default().fg(Color::Green)),
                    Span::raw(trimmed[4..].to_string()),
                ]);
            }
            if trimmed.starts_with("✗ ") {
                return Line::from(vec![
                    Span::raw(indent.to_string()),
                    Span::styled("✗ ", Style::default().fg(Color::Red)),
                    Span::raw(trimmed[4..].to_string()),
                ]);
            }
            if trimmed.starts_with("◉ ") {
                return Line::from(vec![
                    Span::raw(indent.to_string()),
                    Span::styled("◉ ", Style::default().fg(Color::Yellow)),
                    Span::raw(trimmed[4..].to_string()),
                ]);
            }
            if trimmed.starts_with("○ ") || trimmed.starts_with("· ") {
                let icon_end = trimmed.char_indices().nth(2).map(|(i, _)| i).unwrap_or(2);
                return Line::from(vec![
                    Span::raw(indent.to_string()),
                    Span::styled(
                        trimmed[..icon_end].to_string(),
                        Style::default().fg(Color::DarkGray),
                    ),
                    Span::styled(
                        trimmed[icon_end..].to_string(),
                        Style::default().fg(Color::DarkGray),
                    ),
                ]);
            }

            Line::raw(line.to_string())
        })
        .collect()
}
