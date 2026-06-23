use chrono::{DateTime, Utc};
use crate::client::ToilClient;
use crate::models::{TopologyGraph, TopologyNode};
use std::collections::HashMap;

/// Generate a session report for an execution group.
///
/// Walks the compound graph tree, fetches events per run, and extracts
/// `communicate` tool call messages as natural node summaries.
/// Returns the report as a formatted string.
pub async fn generate_report(client: &ToilClient, run_id: &str) -> Result<String, String> {
    let graph = client
        .compound_graph(run_id)
        .await
        .map_err(|e| format!("Failed to fetch compound graph: {}", e))?;

    let meta = client
        .run_meta(run_id)
        .await
        .map_err(|e| format!("Failed to fetch run meta: {}", e))?;

    // Collect all unique run IDs from the compound graph
    let run_ids = collect_run_ids(&graph);

    // Fetch metadata for all runs (needed for time-ordering)
    let mut run_times: HashMap<String, Option<DateTime<Utc>>> = HashMap::new();
    for rid in &run_ids {
        let started = client.run_meta(rid).await.ok().and_then(|m| m.started_at);
        run_times.insert(rid.clone(), started);
    }

    // Fetch events for each run and extract communicate messages per node
    let mut node_summaries: HashMap<String, Vec<String>> = HashMap::new();
    for rid in &run_ids {
        if let Ok(events) = client.run_events(rid).await {
            for event in &events {
                if event.event_type != "node_output" {
                    continue;
                }
                let Some(ref text) = event.text else { continue };
                let Some(ref nid) = event.node_id else { continue };

                if let Some(msg) = extract_communicate_message(text) {
                    let key = format!("{}::{}", rid, nid);
                    node_summaries.entry(key).or_default().push(msg);
                }
            }
        }
    }

    // Build the report
    let mut report = String::new();
    report.push_str(&format!("# Session Report: {}\n", run_id));
    report.push_str(&format!(
        "Workflow: {}  Status: {}\n",
        meta.workflow_id, meta.status
    ));
    if let Some(ref title) = meta.title {
        report.push_str(&format!("Title: {}\n", title));
    }
    if let Some(started) = meta.started_at {
        report.push_str(&format!("Started: {}\n", started.format("%Y-%m-%d %H:%M:%S UTC")));
    }
    if let Some(finished) = meta.finished_at {
        report.push_str(&format!("Finished: {}\n", finished.format("%Y-%m-%d %H:%M:%S UTC")));
    }
    report.push_str("\n---\n\n");

    // Render tree (sorted by time)
    let mut roots: Vec<&TopologyNode> = graph
        .nodes
        .iter()
        .filter(|n| n.parent.is_none())
        .collect();
    roots.sort_by_key(|n| run_times.get(&n.id).copied().flatten());

    for root in &roots {
        render_tree_node(&mut report, &graph, root, &node_summaries, &run_times, 0);
    }

    Ok(report)
}

/// Extract a communicate message from a node_output event text.
/// Looks for TOOL_CALL_START with tool_name "communicate" and extracts the message,
/// or ASSISTANT_TEXT_END with a JSON decision block containing a message.
pub fn extract_communicate_message(text: &str) -> Option<String> {
    let val: serde_json::Value = serde_json::from_str(text).ok()?;

    // Serf log: TOOL_CALL_START for communicate tool
    if let Some(kind) = val.get("kind").and_then(|k| k.as_str()) {
        if kind == "TOOL_CALL_START" {
            let data = val.get("data")?;
            let tool_name = data.get("tool_name").and_then(|n| n.as_str())?;
            if tool_name == "communicate" {
                let args_json = data.get("arguments_json").and_then(|a| a.as_str())?;
                let args: serde_json::Value = serde_json::from_str(args_json).ok()?;
                return args.get("message").and_then(|m| m.as_str()).map(String::from);
            }
        }
        return None;
    }

    // Anthropic format: tool_use block with name "communicate"
    if let Some(content) = val.get("content").and_then(|c| c.as_array()) {
        for block in content {
            if block.get("type").and_then(|t| t.as_str()) == Some("tool_use") {
                if block.get("name").and_then(|n| n.as_str()) == Some("communicate") {
                    if let Some(input) = block.get("input") {
                        return input.get("message").and_then(|m| m.as_str()).map(String::from);
                    }
                }
            }
        }
    }

    // Toil envelope wrapping tool_use
    if let Some(inner) = val.get("message") {
        if let Some(content) = inner.get("content").and_then(|c| c.as_array()) {
            for block in content {
                if block.get("type").and_then(|t| t.as_str()) == Some("tool_use") {
                    if block.get("name").and_then(|n| n.as_str()) == Some("communicate") {
                        if let Some(input) = block.get("input") {
                            return input
                                .get("message")
                                .and_then(|m| m.as_str())
                                .map(String::from);
                        }
                    }
                }
            }
        }
    }

    None
}

/// Collect all unique run IDs from the compound graph.
/// Run container nodes (no "::") are run IDs themselves.
/// Task nodes ("run_id::node_id") contribute the run_id portion.
fn collect_run_ids(graph: &TopologyGraph) -> Vec<String> {
    let mut ids: Vec<String> = Vec::new();
    for node in &graph.nodes {
        let rid = if let Some(pos) = node.id.find("::") {
            node.id[..pos].to_string()
        } else {
            node.id.clone()
        };
        if !ids.contains(&rid) {
            ids.push(rid);
        }
    }
    ids
}

/// Render a single tree node and its children recursively.
fn render_tree_node(
    out: &mut String,
    graph: &TopologyGraph,
    node: &TopologyNode,
    summaries: &HashMap<String, Vec<String>>,
    run_times: &HashMap<String, Option<DateTime<Utc>>>,
    depth: usize,
) {
    let indent = "  ".repeat(depth);
    let status = node
        .status
        .as_deref()
        .unwrap_or("unknown");
    let status_icon = match status {
        "completed" => "✓",
        "failed" => "✗",
        "running" | "started" => "◉",
        "skipped" => "○",
        "cancelled" => "⊘",
        _ => "·",
    };

    // Determine if this is a run container or a task node
    let is_run_container = !node.id.contains("::");

    if is_run_container {
        // Run container: show workflow and status
        let workflow = node.workflow.as_deref().unwrap_or("");
        out.push_str(&format!(
            "{}{} {} [{}] ({})\n",
            indent, status_icon, node.label, workflow, status
        ));
    } else {
        // Task node: show label, status, and communicate summary
        let decision = node.decision.as_deref().unwrap_or("");
        if !decision.is_empty() {
            out.push_str(&format!(
                "{}{} {} ({}) → {}\n",
                indent, status_icon, node.label, status, decision
            ));
        } else {
            out.push_str(&format!(
                "{}{} {} ({})\n",
                indent, status_icon, node.label, status
            ));
        }

        // Show communicate messages as indented summary
        if let Some(messages) = summaries.get(&node.id) {
            for msg in messages {
                out.push_str(&format!("{}  │ {}\n", indent, msg));
            }
        }
    }

    // Render children (sorted by time)
    let mut children: Vec<&TopologyNode> = graph
        .nodes
        .iter()
        .filter(|n| n.parent.as_deref() == Some(&node.id))
        .collect();
    children.sort_by_key(|n| {
        let rid = n.id.split("::").next().unwrap_or(&n.id);
        run_times.get(rid).copied().flatten()
    });

    for child in &children {
        render_tree_node(out, graph, child, summaries, run_times, depth + 1);
    }
}

/// Export the report to a file. Returns the path written to.
pub fn export_report(report: &str, run_id: &str) -> Result<String, String> {
    let filename = format!("{}_report.txt", run_id);
    std::fs::write(&filename, report).map_err(|e| e.to_string())?;
    match std::env::current_dir() {
        Ok(cwd) => Ok(cwd.join(&filename).to_string_lossy().to_string()),
        Err(_) => Ok(filename),
    }
}
