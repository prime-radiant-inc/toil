use crate::app::{App, Pane, Screen};
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, Clear, List, ListItem, Paragraph, Wrap};

pub fn draw(frame: &mut Frame, area: Rect, app: &App) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(45), Constraint::Percentage(55)])
        .split(area);

    draw_approval_list(frame, chunks[0], app);
    draw_approval_detail(frame, chunks[1], app);
}

pub fn draw_modal(frame: &mut Frame, app: &App) {
    let Screen::ApprovalModal { approval_index } = &app.screen else {
        return;
    };

    let pending = app.pending_approvals_list();
    let Some(approval) = pending.get(*approval_index) else {
        return;
    };

    let area = frame.area();
    let modal_width = 52.min(area.width.saturating_sub(4));
    let modal_height = (10 + approval.choices.len() as u16).min(area.height.saturating_sub(4));
    let x = (area.width.saturating_sub(modal_width)) / 2;
    let y = (area.height.saturating_sub(modal_height)) / 2;
    let modal_area = Rect::new(x, y, modal_width, modal_height);

    frame.render_widget(Clear, modal_area);

    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(ratatui::widgets::BorderType::Double)
        .title(format!(" Resolve: {} ", approval.node_id));

    let inner = block.inner(modal_area);
    frame.render_widget(block, modal_area);

    let mut lines = Vec::new();

    // Question
    if let Some(question) = &approval.question {
        for line in question.lines() {
            lines.push(Line::from(line.to_string()));
        }
        lines.push(Line::from(""));
    }

    // Decisions
    lines.push(Line::styled("Decision:", Style::default().bold()));
    for (i, choice) in approval.choices.iter().enumerate() {
        let marker = if i == app.modal_decision { "●" } else { "○" };
        let style = if i == app.modal_decision {
            Style::default().fg(Color::Cyan)
        } else {
            Style::default()
        };
        lines.push(Line::styled(format!("  {} {}", marker, choice), style));
    }
    lines.push(Line::from(""));

    // Message
    lines.push(Line::styled("Message:", Style::default().bold()));
    lines.push(Line::from(format!("> {}▎", app.modal_message)));

    let p = Paragraph::new(lines).wrap(Wrap { trim: false });
    frame.render_widget(p, inner);
}

fn draw_approval_list(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Left {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    let pending = app.pending_approvals_list();
    let items: Vec<ListItem> = pending
        .iter()
        .enumerate()
        .map(|(i, approval)| {
            let age = chrono::Utc::now()
                .signed_duration_since(approval.created_at)
                .num_minutes();
            let age_str = if age < 60 {
                format!("{}m ago", age)
            } else {
                format!("{}h ago", age / 60)
            };
            let run_label = app.run_cache.get(&approval.run_id)
                .and_then(|m| m.title.as_deref())
                .unwrap_or(&approval.run_id);
            let text = format!("{} › {}  {}", run_label, approval.node_id, age_str);
            let style = if i == app.selected_approval {
                Style::default().bg(Color::DarkGray)
            } else {
                Style::default()
            };
            ListItem::new(text).style(style)
        })
        .collect();

    let title = format!(" Approvals ({}) ", pending.len());
    let list = List::new(items).block(
        Block::default()
            .borders(Borders::ALL)
            .border_style(border_style)
            .title(title),
    );
    frame.render_widget(list, area);
}

fn draw_approval_detail(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Right {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(border_style)
        .title(" Detail ");

    let pending = app.pending_approvals_list();
    let Some(approval) = pending.get(app.selected_approval) else {
        let p = Paragraph::new("No pending approvals").block(block);
        frame.render_widget(p, area);
        return;
    };

    let mut lines = vec![
        Line::styled(&approval.node_id, Style::default().bold()),
        Line::styled(
            format!("Run: {}", approval.run_id),
            Style::default().fg(Color::DarkGray),
        ),
        Line::from(""),
    ];

    if let Some(question) = &approval.question {
        for line in question.lines() {
            lines.push(Line::from(line.to_string()));
        }
        lines.push(Line::from(""));
    }

    // Show deadline countdown if set
    if let Some(timeout_sec) = approval.timeout_sec {
        let elapsed = chrono::Utc::now()
            .signed_duration_since(approval.created_at)
            .num_seconds();
        let remaining = (timeout_sec as i64) - elapsed;
        if remaining > 0 {
            lines.push(Line::styled(
                format!("Deadline: {}s remaining", remaining),
                Style::default().fg(Color::Yellow),
            ));
        } else {
            lines.push(Line::styled(
                "Deadline: expired",
                Style::default().fg(Color::Red),
            ));
        }
        lines.push(Line::from(""));
    }

    lines.push(Line::styled(
        format!("Choices: {}", approval.choices.join(", ")),
        Style::default().fg(Color::DarkGray),
    ));

    lines.push(Line::from(""));
    lines.push(Line::styled(
        "[a] resolve",
        Style::default().fg(Color::Cyan),
    ));

    let p = Paragraph::new(lines).block(block).wrap(Wrap { trim: false });
    frame.render_widget(p, area);
}
