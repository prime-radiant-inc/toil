use crate::models::{TopologyGraph, TopologyEdge};
use ratatui::prelude::*;
use ratatui::widgets::{Block, Borders, Paragraph};
use std::collections::{HashMap, HashSet};

#[derive(Debug)]
pub struct GraphLayout {
    pub rows: Vec<GraphRow>,
    pub forward_edges: Vec<ForwardEdge>,
    pub back_edges: Vec<BackEdge>,
}

#[derive(Debug)]
pub struct GraphRow {
    pub node_id: String,
    pub label: String,
    pub status: String,
}

#[derive(Debug)]
pub struct ForwardEdge {
    pub from_row: usize,
    pub to_row: usize,
    pub label: Option<String>,
}

#[derive(Debug)]
pub struct BackEdge {
    pub from_row: usize,
    pub to_row: usize,
    pub label: Option<String>,
    pub is_escape: bool,
}

/// Topological sort with cycle detection. Returns ordered node IDs and back-edges.
pub fn layout_graph(graph: &TopologyGraph) -> GraphLayout {
    // Filter to non-parent nodes only (no compound containers).
    let parent_ids: HashSet<&str> = graph
        .nodes
        .iter()
        .filter_map(|n| n.parent.as_deref())
        .collect();
    let leaf_nodes: Vec<&str> = graph
        .nodes
        .iter()
        .filter(|n| !parent_ids.contains(n.id.as_str()) && n.parent.is_none())
        .map(|n| n.id.as_str())
        .collect();

    let node_set: HashSet<&str> = leaf_nodes.iter().copied().collect();

    // Build adjacency from edges.
    let mut adj: HashMap<&str, Vec<&TopologyEdge>> = HashMap::new();
    let mut in_degree: HashMap<&str, usize> = HashMap::new();
    for id in &leaf_nodes {
        adj.entry(id).or_default();
        in_degree.entry(id).or_insert(0);
    }
    for edge in &graph.edges {
        if node_set.contains(edge.source.as_str()) && node_set.contains(edge.target.as_str()) {
            adj.entry(edge.source.as_str()).or_default().push(edge);
            *in_degree.entry(edge.target.as_str()).or_insert(0) += 1;
        }
    }

    // Kahn's algorithm for topological order.
    let mut queue: Vec<&str> = in_degree
        .iter()
        .filter(|(_, &deg)| deg == 0)
        .map(|(&id, _)| id)
        .collect();
    queue.sort(); // deterministic

    let mut order: Vec<&str> = Vec::new();
    let mut visited: HashSet<&str> = HashSet::new();

    while let Some(node) = queue.first().copied() {
        queue.remove(0);
        if visited.contains(node) {
            continue;
        }
        visited.insert(node);
        order.push(node);
        if let Some(edges) = adj.get(node) {
            for edge in edges {
                if let Some(deg) = in_degree.get_mut(edge.target.as_str()) {
                    *deg = deg.saturating_sub(1);
                    if *deg == 0 && !visited.contains(edge.target.as_str()) {
                        queue.push(edge.target.as_str());
                        queue.sort();
                    }
                }
            }
        }
    }

    // Any nodes not in order are in cycles — add them at the end.
    for id in &leaf_nodes {
        if !visited.contains(id) {
            order.push(id);
        }
    }

    let row_index: HashMap<&str, usize> = order.iter().enumerate().map(|(i, &id)| (id, i)).collect();

    let node_map: HashMap<&str, &crate::models::TopologyNode> =
        graph.nodes.iter().map(|n| (n.id.as_str(), n)).collect();

    let rows: Vec<GraphRow> = order
        .iter()
        .map(|&id| {
            let node = node_map.get(id);
            GraphRow {
                node_id: id.to_string(),
                label: node.map(|n| n.label.clone()).unwrap_or_else(|| id.to_string()),
                status: node
                    .and_then(|n| n.status.as_deref())
                    .unwrap_or("pending")
                    .to_string(),
            }
        })
        .collect();

    let mut forward_edges = Vec::new();
    let mut back_edges = Vec::new();

    for edge in &graph.edges {
        if !node_set.contains(edge.source.as_str()) || !node_set.contains(edge.target.as_str()) {
            continue;
        }
        let from = row_index.get(edge.source.as_str()).copied().unwrap_or(0);
        let to = row_index.get(edge.target.as_str()).copied().unwrap_or(0);
        if to > from {
            forward_edges.push(ForwardEdge {
                from_row: from,
                to_row: to,
                label: edge.label.clone(),
            });
        } else {
            back_edges.push(BackEdge {
                from_row: from,
                to_row: to,
                label: edge.label.clone(),
                is_escape: edge.is_escape,
            });
        }
    }

    GraphLayout {
        rows,
        forward_edges,
        back_edges,
    }
}

fn status_icon(status: &str) -> (&str, Color) {
    match status {
        "running" => ("▶", Color::Green),
        "completed" => ("✓", Color::Green),
        "failed" => ("✗", Color::Red),
        "paused" => ("◉", Color::Yellow),
        "cancelled" => ("⊘", Color::DarkGray),
        _ => ("○", Color::DarkGray),
    }
}

pub fn draw(frame: &mut Frame, area: Rect, graph: &TopologyGraph, selected_node: usize) {
    let layout = layout_graph(graph);
    let block = Block::default().borders(Borders::ALL).title(" Graph ");
    let inner = block.inner(area);
    frame.render_widget(block, area);

    let row_height = 3u16;
    let gap = 1u16;
    let box_width = inner.width.saturating_sub(8).min(40);

    for (i, row) in layout.rows.iter().enumerate() {
        let y_offset = i as u16 * (row_height + gap);
        if y_offset + row_height > inner.height {
            break;
        }

        let (icon, icon_color) = status_icon(&row.status);
        let content = format!("{} {}", icon, row.label);
        let is_selected = i == selected_node;

        let node_area = Rect::new(inner.x, inner.y + y_offset, box_width, row_height);

        let style = if is_selected {
            Style::default().bg(Color::DarkGray)
        } else {
            Style::default()
        };

        let node_block = Block::default().borders(Borders::ALL).style(style);
        let node_text =
            Paragraph::new(Span::styled(content, Style::default().fg(icon_color))).block(node_block);
        frame.render_widget(node_text, node_area);

        if i + 1 < layout.rows.len() {
            let connector_y = inner.y + y_offset + row_height;
            if connector_y < inner.y + inner.height {
                let connector = Paragraph::new("  │");
                let conn_area = Rect::new(inner.x, connector_y, 4, 1);
                frame.render_widget(connector, conn_area);
            }
        }
    }

    let rail_x = inner.x + box_width + 2;
    for (rail_idx, back_edge) in layout.back_edges.iter().enumerate() {
        let x = rail_x + (rail_idx as u16 * 2);
        if x >= inner.x + inner.width {
            break;
        }
        let from_y = inner.y + back_edge.from_row as u16 * (row_height + gap) + 1;
        let to_y = inner.y + back_edge.to_row as u16 * (row_height + gap) + 1;

        let rail_char = if back_edge.is_escape { "╌" } else { "│" };
        let rail_color = if back_edge.is_escape {
            Color::Yellow
        } else {
            Color::DarkGray
        };

        for y in to_y..=from_y {
            if y < inner.y + inner.height {
                let span = Span::styled(rail_char, Style::default().fg(rail_color));
                let p = Paragraph::new(span);
                frame.render_widget(p, Rect::new(x, y, 1, 1));
            }
        }
    }
}
