use crate::app::App;
use ratatui::prelude::*;
use ratatui::widgets::Paragraph;

pub fn draw_status_bar(frame: &mut Frame, area: Rect, app: &App) {
    let tabs = vec![
        ("[1]Runs", app.tab == crate::app::Tab::Runs),
        ("[2]Workflows", app.tab == crate::app::Tab::Workflows),
        ("[3]Approvals", app.tab == crate::app::Tab::Approvals),
    ];

    let mut spans = Vec::new();
    for (label, active) in &tabs {
        let style = if *active {
            Style::default().fg(Color::Cyan).bold()
        } else {
            Style::default().fg(Color::DarkGray)
        };
        spans.push(Span::styled(*label, style));
        spans.push(Span::raw(" "));
    }

    let status = if app.connected { "connected" } else { "disconnected" };
    let info = format!(
        " {} active · {} approvals · {}",
        app.active_runs, app.pending_approvals, status
    );
    spans.push(Span::styled("── ", Style::default().fg(Color::DarkGray)));
    spans.push(Span::styled(info, Style::default().fg(Color::DarkGray)));

    // Use a two-column layout to right-align the server URL
    let left_line = Line::from(spans);
    let right_line = Line::from(Span::styled(
        app.base_url.clone(),
        Style::default().fg(Color::DarkGray),
    ));

    let columns = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Min(0), Constraint::Length(app.base_url.len() as u16 + 1)])
        .split(area);

    let left_bar = Paragraph::new(left_line).style(Style::default().bg(Color::Black));
    let right_bar = Paragraph::new(right_line)
        .style(Style::default().bg(Color::Black))
        .alignment(Alignment::Right);
    frame.render_widget(left_bar, columns[0]);
    frame.render_widget(right_bar, columns[1]);
}

pub fn draw_key_hints(frame: &mut Frame, area: Rect, app: &App) {
    // Flash message takes priority over key hints
    if let Some(ref msg) = app.flash_message {
        let bar = Paragraph::new(msg.as_str()).style(Style::default().fg(Color::Green));
        frame.render_widget(bar, area);
        return;
    }

    let hints = match &app.screen {
        crate::app::Screen::Home => "[1-3] tabs · [Tab] pane · [j/k] select · [Enter] open · [R] report · [/] filter · [q] quit",
        crate::app::Screen::Transcript { .. } => "[Esc] back · [f] follow · [e] expand · [x] export · [j/k] scroll · [q] quit",
        crate::app::Screen::Report { .. } => "[Esc] back · [x] export · [j/k] scroll · [d/u] page · [g/G] top/bottom · [q] quit",
        crate::app::Screen::ApprovalModal { .. } => "[↑↓] decision · [Tab] message · [Enter] submit · [Esc] cancel",
    };
    let bar = Paragraph::new(hints).style(Style::default().fg(Color::DarkGray));
    frame.render_widget(bar, area);
}
