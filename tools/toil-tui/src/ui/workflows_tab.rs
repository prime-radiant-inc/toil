use crate::app::{App, Pane, RunForm};
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, List, ListItem, Paragraph};

pub fn draw(frame: &mut Frame, area: Rect, app: &App) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(40), Constraint::Percentage(60)])
        .split(area);

    draw_workflow_list(frame, chunks[0], app);
    draw_workflow_detail(frame, chunks[1], app);
}

fn draw_workflow_list(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Left {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    let items: Vec<ListItem> = app
        .workflow_ids
        .iter()
        .enumerate()
        .map(|(i, id)| {
            let style = if i == app.selected_workflow {
                Style::default().bg(Color::DarkGray)
            } else {
                Style::default()
            };
            ListItem::new(id.as_str()).style(style)
        })
        .collect();

    let list = List::new(items).block(
        Block::default()
            .borders(Borders::ALL)
            .border_style(border_style)
            .title(" Workflows "),
    );
    frame.render_widget(list, area);
}

fn draw_workflow_detail(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Right {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(border_style)
        .title(" Detail ");

    if let Some(ref form) = app.run_form {
        draw_run_form(frame, area, form, block);
    } else if let Some(id) = app.workflow_ids.get(app.selected_workflow) {
        let lines = vec![
            Line::styled(id.as_str(), Style::default().bold()),
            Line::from(""),
            Line::styled("[r] Run this workflow", Style::default().fg(Color::DarkGray)),
        ];
        let p = Paragraph::new(lines).block(block);
        frame.render_widget(p, area);
    } else {
        let p = Paragraph::new("No workflow selected").block(block);
        frame.render_widget(p, area);
    }
}

fn draw_run_form(frame: &mut Frame, area: Rect, form: &RunForm, block: Block) {
    let mut lines = vec![
        Line::styled(
            format!("Run: {}", form.workflow_id),
            Style::default().bold(),
        ),
        Line::from("─".repeat(36)),
        Line::from(""),
    ];

    for (i, (name, value)) in form.fields.iter().enumerate() {
        let is_current = i == form.current_field;
        let label_style = if is_current {
            Style::default().fg(Color::Cyan)
        } else {
            Style::default().fg(Color::DarkGray)
        };
        lines.push(Line::styled(name.as_str(), label_style));

        let cursor = if is_current { "▎" } else { "" };
        lines.push(Line::from(format!("> {}{}", value, cursor)));
        lines.push(Line::from(""));
    }

    let p = Paragraph::new(lines).block(block);
    frame.render_widget(p, area);
}
