use super::graph;
use chrono::{DateTime, Utc};
use crate::app::{App, Pane, RunDetailView};
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Paragraph};

pub fn draw(frame: &mut Frame, area: Rect, app: &App) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(45), Constraint::Percentage(55)])
        .split(area);

    draw_run_list(frame, chunks[0], app);
    draw_run_detail(frame, chunks[1], app);
}

pub fn status_icon(status: &str) -> (&str, Color) {
    match status {
        "running" => ("●", Color::Green),
        "completed" => ("✓", Color::Green),
        "failed" => ("✗", Color::Red),
        "paused" => ("◉", Color::Yellow),
        "cancelled" => ("⊘", Color::DarkGray),
        _ => ("○", Color::DarkGray),
    }
}

pub fn format_duration(seconds: i64) -> String {
    if seconds < 60 {
        format!("{}s", seconds)
    } else if seconds < 3600 {
        format!("{}m", seconds / 60)
    } else {
        format!("{}h", seconds / 3600)
    }
}

fn run_duration_str(meta: &crate::models::RunMeta) -> String {
    let start = match meta.started_at {
        Some(s) => s,
        None => return "--".to_string(),
    };
    let end = meta.finished_at.unwrap_or_else(chrono::Utc::now);
    let secs = (end - start).num_seconds();
    format_duration(secs)
}

pub fn truncate(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        s.to_string()
    } else {
        s.chars().take(max).collect()
    }
}

/// Format a date for the group header. "Today", "Yesterday", or "Mon Mar 10".
fn date_label(date: chrono::NaiveDate) -> String {
    let today = chrono::Utc::now().date_naive();
    if date == today {
        "Today".to_string()
    } else if date == today - chrono::Duration::days(1) {
        "Yesterday".to_string()
    } else {
        date.format("%a %b %-d").to_string()
    }
}

fn draw_run_list(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Left {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    // Apply filter if active
    let filter = if !app.filter_text.is_empty() {
        Some(app.filter_text.to_lowercase())
    } else {
        None
    };

    // Build list items with date group headers.
    // Track which ListItem index corresponds to app.selected_run so we
    // can set ListState::selected correctly (date headers shift indices).
    let mut items: Vec<ListItem> = Vec::new();
    let mut selected_list_index: Option<usize> = None;
    let mut last_date: Option<chrono::NaiveDate> = None;

    for (run_idx, id) in app.run_ids.iter().enumerate() {
        let meta = app.run_cache.get(id);

        // Skip child runs — only show top-level execution group roots
        if let Some(m) = meta {
            if m.parent_run.as_ref().is_some_and(|p| !p.is_empty()) {
                continue;
            }
        }

        // Apply filter
        if let Some(ref filter) = filter {
            let title = meta.and_then(|m| m.title.as_deref()).unwrap_or("");
            let wf = meta.map(|m| m.workflow_id.as_str()).unwrap_or("");
            let status = meta.map(|m| m.status.as_str()).unwrap_or("");
            let matches = id.to_lowercase().contains(filter)
                || title.to_lowercase().contains(filter)
                || wf.to_lowercase().contains(filter)
                || status.to_lowercase().contains(filter);
            if !matches {
                continue;
            }
        }

        // Insert date header if this run's date differs from the previous
        let run_date = meta
            .and_then(|m| m.started_at)
            .map(|dt| dt.date_naive());
        if run_date != last_date {
            if let Some(date) = run_date {
                last_date = Some(date);
                items.push(
                    ListItem::new(Line::styled(
                        format!("── {} ──", date_label(date)),
                        Style::default().fg(Color::DarkGray).italic(),
                    ))
                );
            }
        }

        // Track the selected run's position in the list (accounting for headers)
        if run_idx == app.selected_run {
            selected_list_index = Some(items.len());
        }

        let status_str = meta.map(|m| m.status.as_str()).unwrap_or("pending");
        let (icon, color) = status_icon(status_str);
        let title = meta
            .and_then(|m| m.title.as_deref())
            .unwrap_or(meta.map(|m| m.workflow_id.as_str()).unwrap_or(id));
        let wf = meta.map(|m| m.workflow_id.as_str()).unwrap_or("");
        let dur = meta.map(|m| run_duration_str(m)).unwrap_or_else(|| "--".to_string());

        let line = Line::from(vec![
            Span::styled(format!("{} ", icon), Style::default().fg(color)),
            Span::raw(format!("{:<36} ", truncate(title, 36))),
            Span::styled(format!("{:<16} ", wf), Style::default().fg(Color::DarkGray)),
            Span::styled(dur, Style::default().fg(Color::DarkGray)),
        ]);

        items.push(ListItem::new(line));
    }

    let title = if app.filter_active {
        format!(" Runs [/{}] ", app.filter_text)
    } else if !app.filter_text.is_empty() {
        format!(" Runs (filter: {}) ", app.filter_text)
    } else {
        " Runs ".to_string()
    };

    let list = List::new(items)
        .block(
            Block::default()
                .borders(Borders::ALL)
                .border_style(border_style)
                .title(title),
        )
        .highlight_style(Style::default().bg(Color::DarkGray));

    let mut state = ListState::default().with_selected(selected_list_index);
    frame.render_stateful_widget(list, area, &mut state);
}

fn draw_execution_group_tree(frame: &mut Frame, area: Rect, app: &App) {
    let Some(ref graph) = app.compound_graph else {
        return;
    };

    let mut lines = Vec::new();

    // Sort root nodes by started_at from run metadata, falling back to node order
    let mut roots: Vec<&crate::models::TopologyNode> = graph
        .nodes
        .iter()
        .filter(|n| n.parent.is_none())
        .collect();
    roots.sort_by_key(|n| {
        app.run_cache
            .get(&n.id)
            .and_then(|m| m.started_at)
    });

    // Flatten the tree into lines, tracking which line index each node maps to.
    // We use selected_node as the index into this flat list.
    let mut line_idx = 0usize;
    for (i, root) in roots.iter().enumerate() {
        let is_last = i == roots.len() - 1;
        render_tree_node(&mut lines, graph, root, 0, is_last, "", app.selected_node, &mut line_idx, app.pane == Pane::Right, app);
    }

    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(if app.pane == Pane::Right {
            Style::default().fg(Color::Cyan)
        } else {
            Style::default().fg(Color::DarkGray)
        })
        .title(" Execution Group ");

    // Scroll to keep selected node visible
    let inner_height = area.height.saturating_sub(2) as usize; // subtract borders
    let total = lines.len();
    // Center the selection when it would be off-screen
    let scroll = if total <= inner_height {
        0
    } else if app.selected_node >= inner_height {
        // Keep selected node near bottom of viewport
        (app.selected_node + 1).saturating_sub(inner_height)
    } else {
        0
    };

    let p = Paragraph::new(lines)
        .block(block)
        .scroll((scroll as u16, 0));
    frame.render_widget(p, area);
}

fn render_tree_node(
    lines: &mut Vec<Line<'static>>,
    graph: &crate::models::TopologyGraph,
    node: &crate::models::TopologyNode,
    depth: usize,
    is_last: bool,
    prefix_str: &str,
    selected_node: usize,
    line_idx: &mut usize,
    pane_focused: bool,
    app: &App,
) {
    let connector = if depth == 0 {
        String::new()
    } else if is_last {
        format!("{}└─ ", prefix_str)
    } else {
        format!("{}├─ ", prefix_str)
    };
    let (icon, color) = status_icon(node.status.as_deref().unwrap_or("pending"));
    let label = &node.label;

    let is_selected = pane_focused && *line_idx == selected_node;
    let base_style = if is_selected {
        Style::default().bg(Color::DarkGray)
    } else {
        Style::default()
    };

    lines.push(Line::from(vec![
        Span::styled(connector, base_style),
        Span::styled(format!("{} ", icon), base_style.fg(color)),
        Span::styled(label.to_string(), base_style),
    ]));
    *line_idx += 1;

    let child_prefix = if depth == 0 {
        "  ".to_string()
    } else if is_last {
        format!("{}   ", prefix_str)
    } else {
        format!("{}│  ", prefix_str)
    };

    let mut children: Vec<&crate::models::TopologyNode> = graph
        .nodes
        .iter()
        .filter(|n| n.parent.as_deref() == Some(&node.id))
        .collect();

    // Sort children: run containers by started_at, task nodes by node order in run metadata
    children.sort_by(|a, b| {
        let a_time = child_sort_key(a, app);
        let b_time = child_sort_key(b, app);
        a_time.cmp(&b_time)
    });

    for (i, child) in children.iter().enumerate() {
        let child_is_last = i == children.len() - 1;
        render_tree_node(lines, graph, child, depth + 1, child_is_last, &child_prefix, selected_node, line_idx, pane_focused, app);
    }
}

/// Sort key for child nodes in the execution group tree.
/// Run containers (no "::") sort by their started_at timestamp.
/// Task nodes ("run_id::node_id") sort by their run's started_at (secondary: node label).
fn child_sort_key(node: &crate::models::TopologyNode, app: &App) -> Option<DateTime<Utc>> {
    if let Some(pos) = node.id.find("::") {
        // Task node — use the parent run's started_at
        let run_id = &node.id[..pos];
        app.run_cache.get(run_id).and_then(|m| m.started_at)
    } else {
        // Run container — use its own started_at
        app.run_cache.get(&node.id).and_then(|m| m.started_at)
    }
}

fn draw_run_detail(frame: &mut Frame, area: Rect, app: &App) {
    let border_style = if app.pane == Pane::Right {
        Style::default().fg(Color::Cyan)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    // Check for execution group (compound runs with children)
    if let Some(ref graph) = app.compound_graph {
        let has_children = graph.nodes.iter().any(|n| n.parent.is_some());
        if has_children && app.run_detail_view == RunDetailView::List {
            draw_execution_group_tree(frame, area, app);
            return;
        }
    }

    // Check for graph view mode
    if app.run_detail_view == RunDetailView::Graph {
        if let Some(ref topo) = app.compound_graph {
            graph::draw(frame, area, topo, app.selected_node);
            return;
        }
    }

    let meta = match app.selected_run_meta() {
        Some(m) => m,
        None => {
            let block = Block::default()
                .borders(Borders::ALL)
                .border_style(border_style)
                .title(" Detail ");
            let p = Paragraph::new("No run selected").block(block);
            frame.render_widget(p, area);
            return;
        }
    };

    let (icon, color) = status_icon(&meta.status);
    let title = meta.title.as_deref().unwrap_or(&meta.workflow_id);
    let run_id = &meta.run_id;

    let mut lines = vec![
        Line::from(vec![
            Span::styled(format!("{} ", icon), Style::default().fg(color)),
            Span::styled(title, Style::default().bold()),
        ]),
        Line::from(Span::styled(
            format!("{} · {}", run_id, meta.workflow_id),
            Style::default().fg(Color::DarkGray),
        )),
    ];

    // Timing info
    let dur = run_duration_str(meta);
    if let Some(started) = meta.started_at {
        lines.push(Line::from(Span::styled(
            format!("Started {} · Duration {}", started.format("%b %-d %H:%M"), dur),
            Style::default().fg(Color::DarkGray),
        )));
    }

    // Inputs
    if !meta.inputs.is_empty() {
        lines.push(Line::from(""));
        lines.push(Line::styled(
            "Inputs",
            Style::default().fg(Color::Yellow),
        ));
        let mut keys: Vec<&String> = meta.inputs.keys().collect();
        keys.sort();
        for key in keys {
            let val = &meta.inputs[key];
            let val_str = match val {
                serde_json::Value::String(s) => truncate(s, 60),
                other => truncate(&other.to_string(), 60),
            };
            lines.push(Line::from(vec![
                Span::styled(format!("  {}: ", key), Style::default().fg(Color::DarkGray)),
                Span::raw(val_str),
            ]));
        }
    }

    lines.push(Line::from(""));

    // Node list
    let mut node_ids: Vec<&String> = meta.nodes.keys().collect();
    node_ids.sort();
    for (i, node_id) in node_ids.iter().enumerate() {
        let node = &meta.nodes[*node_id];
        let (n_icon, n_color) = status_icon(&node.status);
        let selected = app.pane == Pane::Right && i == app.selected_node;
        let style = if selected {
            Style::default().bg(Color::DarkGray)
        } else {
            Style::default()
        };
        lines.push(Line::styled(
            format!("  {} {}", n_icon, node_id),
            style.fg(n_color),
        ));
    }

    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(border_style)
        .title(" Detail ");
    let p = Paragraph::new(lines).block(block);
    frame.render_widget(p, area);
}
