use toil_tui::app::{App, RunDetailView, RunForm, Screen, Tab, Pane};
use toil_tui::client::ToilClient;
use toil_tui::ui;
use crossterm::{
    event::{Event as CrosstermEvent, EventStream, KeyCode, KeyModifiers},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use futures_util::StreamExt;
use ratatui::prelude::*;
use std::collections::HashMap;
use std::io;
use tokio::sync::mpsc;
use tokio::task::JoinHandle;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let base_url =
        std::env::var("TOIL_URL").unwrap_or_else(|_| "http://127.0.0.1:8080".to_string());

    let mut app = App::new(base_url.clone());
    let client = ToilClient::new(&base_url);

    // Initial data load
    if let Ok(health) = client.health().await {
        app.connected = true;
        app.active_runs = health.active_runs;
        app.total_runs = health.total_runs;
    }
    if let Ok(ids) = client.list_runs().await {
        app.run_ids = ids;
        // Fetch meta for all runs (initial load)
        for id in &app.run_ids {
            if let Ok(meta) = client.run_meta(id).await {
                app.run_cache.insert(id.clone(), meta);
            }
        }
        // Sort by started_at descending
        app.run_ids.sort_by(|a, b| {
            let a_time = app.run_cache.get(a).and_then(|m| m.started_at);
            let b_time = app.run_cache.get(b).and_then(|m| m.started_at);
            b_time.cmp(&a_time)
        });
    }
    // Load compound graph for initially selected run
    if let Some(run_id) = app.run_ids.first().cloned() {
        if let Ok(graph) = client.compound_graph(&run_id).await {
            app.compound_graph = Some(graph);
        }
    }
    if let Ok(ids) = client.list_workflows().await {
        app.workflow_ids = ids;
    }
    if let Ok(approvals) = client.list_approvals().await {
        app.pending_approvals = approvals.iter().filter(|a| a.status == "pending").count();
        app.approvals = approvals;
    }

    // Setup terminal
    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen)?;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    // SSE channel (Task 13 wires events into this)
    let (sse_tx, sse_rx) = tokio::sync::mpsc::unbounded_channel::<toil_tui::models::Event>();

    // Main loop
    let result = run_loop(&mut terminal, &mut app, &client, sse_tx, sse_rx).await;

    // Restore terminal
    disable_raw_mode()?;
    execute!(terminal.backend_mut(), LeaveAlternateScreen)?;
    terminal.show_cursor()?;

    result
}

async fn run_loop(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    app: &mut App,
    client: &ToilClient,
    sse_tx: mpsc::UnboundedSender<toil_tui::models::Event>,
    mut sse_rx: mpsc::UnboundedReceiver<toil_tui::models::Event>,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut event_stream = EventStream::new();
    // Track which run we have an active SSE connection for
    let mut sse_run_id: Option<String> = None;
    let mut sse_handle: Option<JoinHandle<()>> = None;
    let mut poll_interval = tokio::time::interval(std::time::Duration::from_secs(5));
    poll_interval.tick().await; // consume the immediate first tick

    loop {
        terminal.draw(|frame| ui::draw(frame, app))?;

        // (Re)connect SSE when viewing a transcript or when selected run changes
        let desired_sse_run = match &app.screen {
            Screen::Transcript { run_id, .. } => Some(run_id.clone()),
            _ => app.selected_run_id().map(|s| s.to_string()),
        };
        if desired_sse_run != sse_run_id {
            // Abort the old SSE connection before starting a new one
            if let Some(handle) = sse_handle.take() {
                handle.abort();
            }
            if let Some(ref run_id) = desired_sse_run {
                let url = client.events_stream_url(run_id);
                let tx = sse_tx.clone();
                sse_handle = Some(tokio::spawn(async move {
                    let _ = toil_tui::sse::connect_sse(&url, tx).await;
                }));
            }
            sse_run_id = desired_sse_run;
        }

        // Use tokio::select! so terminal events, SSE, and polling can interleave
        // without blocking the async runtime.
        tokio::select! {
            maybe_event = event_stream.next() => {
                if let Some(Ok(CrosstermEvent::Key(key))) = maybe_event {
                    handle_key(app, key.code, key.modifiers, client).await;
                }
            }
            Some(event) = sse_rx.recv() => {
                handle_sse_event(app, &event);
                // Drain any remaining buffered events
                while let Ok(event) = sse_rx.try_recv() {
                    handle_sse_event(app, &event);
                }
            }
            _ = poll_interval.tick() => {
                poll_data(app, client).await;
            }
        }

        if app.should_quit {
            break;
        }
    }
    Ok(())
}

async fn handle_key(app: &mut App, code: KeyCode, modifiers: KeyModifiers, client: &ToilClient) {
    // Don't intercept 'q' when user is typing text (modal message or filter input)
    let typing = matches!(app.screen, Screen::ApprovalModal { .. }) || app.filter_active;
    if !typing {
        if let KeyCode::Char('q') = code {
            app.should_quit = true;
            return;
        }
    }

    match &app.screen {
        Screen::Home => handle_home_key(app, code, modifiers, client).await,
        Screen::Transcript { .. } => handle_transcript_key(app, code),
        Screen::Report { .. } => handle_report_key(app, code),
        Screen::ApprovalModal { .. } => handle_modal_key(app, code, client).await,
    }
}

async fn handle_home_key(
    app: &mut App,
    code: KeyCode,
    _modifiers: KeyModifiers,
    client: &ToilClient,
) {
    // When run form is active, capture form input
    if let Some(ref mut form) = app.run_form {
        match code {
            KeyCode::Char(c) => {
                if let Some((_, value)) = form.fields.get_mut(form.current_field) {
                    value.push(c);
                }
                return;
            }
            KeyCode::Backspace => {
                if let Some((_, value)) = form.fields.get_mut(form.current_field) {
                    value.pop();
                }
                return;
            }
            KeyCode::Enter => {
                if form.current_field + 1 < form.fields.len() {
                    form.current_field += 1;
                } else {
                    // Submit: last field, create the run
                    let workflow_id = form.workflow_id.clone();
                    let mut inputs = HashMap::new();
                    let mut env = HashMap::new();
                    for (name, value) in &form.fields {
                        if name == "project_dir" {
                            env.insert("PROJECT_DIR".to_string(), value.clone());
                        } else {
                            inputs.insert(name.clone(), value.clone());
                        }
                    }
                    app.run_form = None;
                    if let Ok(run_id) = client.create_run(&workflow_id, inputs, env).await {
                        // Switch to runs tab with new run selected
                        app.tab = Tab::Runs;
                        if !app.run_ids.contains(&run_id) {
                            app.run_ids.insert(0, run_id.clone());
                        }
                        if let Ok(meta) = client.run_meta(&run_id).await {
                            app.run_cache.insert(run_id, meta);
                        }
                        app.selected_run = 0;
                    }
                }
                return;
            }
            KeyCode::Esc => {
                app.run_form = None;
                return;
            }
            _ => return,
        }
    }

    // When filter is active, capture text input instead of dispatching commands
    if app.filter_active {
        match code {
            KeyCode::Char(c) => {
                app.filter_text.push(c);
                return;
            }
            KeyCode::Backspace => {
                app.filter_text.pop();
                return;
            }
            KeyCode::Esc => {
                app.filter_active = false;
                app.filter_text.clear();
                return;
            }
            KeyCode::Enter => {
                app.filter_active = false;
                return;
            }
            _ => {} // arrow keys etc. fall through to normal handling
        }
    }

    match code {
        KeyCode::Char('1') => app.tab = Tab::Runs,
        KeyCode::Char('2') => app.tab = Tab::Workflows,
        KeyCode::Char('3') => app.tab = Tab::Approvals,
        KeyCode::Tab => {
            app.pane = match app.pane {
                Pane::Left => Pane::Right,
                Pane::Right => Pane::Left,
            };
        }
        KeyCode::BackTab => {
            app.pane = match app.pane {
                Pane::Left => Pane::Right,
                Pane::Right => Pane::Left,
            };
        }
        KeyCode::Char('j') | KeyCode::Down => {
            match (app.tab, app.pane) {
                (Tab::Runs, Pane::Left) => {
                    app.select_next_root_run();
                    // Fetch compound graph for the newly selected run
                    if let Some(run_id) = app.selected_run_id() {
                        if let Ok(graph) = client.compound_graph(run_id).await {
                            app.compound_graph = Some(graph);
                        }
                    }
                }
                (Tab::Runs, Pane::Right) => {
                    let count = app.right_pane_node_count();
                    if app.selected_node + 1 < count {
                        app.selected_node += 1;
                    }
                }
                (Tab::Workflows, Pane::Left) => {
                    if app.selected_workflow + 1 < app.workflow_ids.len() {
                        app.selected_workflow += 1;
                    }
                }
                (Tab::Approvals, Pane::Left) => {
                    let count = app.pending_approvals_list().len();
                    if app.selected_approval + 1 < count {
                        app.selected_approval += 1;
                    }
                }
                _ => {}
            }
        }
        KeyCode::Char('k') | KeyCode::Up => {
            match (app.tab, app.pane) {
                (Tab::Runs, Pane::Left) => {
                    app.select_prev_root_run();
                    // Fetch compound graph for the newly selected run
                    if let Some(run_id) = app.selected_run_id() {
                        if let Ok(graph) = client.compound_graph(run_id).await {
                            app.compound_graph = Some(graph);
                        }
                    }
                }
                (Tab::Runs, Pane::Right) => {
                    app.selected_node = app.selected_node.saturating_sub(1);
                }
                (Tab::Workflows, Pane::Left) => {
                    app.selected_workflow = app.selected_workflow.saturating_sub(1);
                }
                (Tab::Approvals, Pane::Left) => {
                    app.selected_approval = app.selected_approval.saturating_sub(1);
                }
                _ => {}
            }
        }
        KeyCode::Char('g') if app.tab == Tab::Runs && app.pane == Pane::Right => {
            app.run_detail_view = match app.run_detail_view {
                RunDetailView::List => RunDetailView::Graph,
                RunDetailView::Graph => RunDetailView::List,
            };
        }
        KeyCode::Char('/') => {
            app.filter_active = true;
            app.filter_text.clear();
        }
        KeyCode::Enter => match app.tab {
            Tab::Runs if app.pane == Pane::Right => {
                // Try compound graph node first (execution group view)
                if let Some((run_id, Some(node_id))) = app.selected_compound_node() {
                    app.screen = Screen::Transcript {
                        run_id: run_id.clone(),
                        node_id: node_id.clone(),
                    };
                    app.transcript_items.clear();
                    app.transcript_following = false;
                    app.transcript_scroll = 0;
                    app.transcript_expanded.clear();
                    if let Ok(events) = client.run_events(&run_id).await {
                        app.transcript_items = events
                            .iter()
                            .filter(|e| {
                                e.event_type == "node_output"
                                    && e.node_id.as_deref() == Some(node_id.as_str())
                            })
                            .filter_map(|e| e.text.clone())
                            .collect();
                    }
                } else if let Some(meta) = app.selected_run_meta() {
                    // Flat run detail view — use meta.nodes
                    let mut node_ids: Vec<&String> = meta.nodes.keys().collect();
                    node_ids.sort();
                    if let Some(node_id) = node_ids.get(app.selected_node) {
                        let run_id = meta.run_id.clone();
                        let node_id = node_id.to_string();
                        app.screen = Screen::Transcript {
                            run_id: run_id.clone(),
                            node_id: node_id.clone(),
                        };
                        app.transcript_items.clear();
                        app.transcript_following = false;
                        app.transcript_scroll = 0;
                        app.transcript_expanded.clear();
                        if let Ok(events) = client.run_events(&run_id).await {
                            app.transcript_items = events
                                .iter()
                                .filter(|e| {
                                    e.event_type == "node_output"
                                        && e.node_id.as_deref() == Some(node_id.as_str())
                                })
                                .filter_map(|e| e.text.clone())
                                .collect();
                        }
                    }
                }
            }
            Tab::Approvals => {
                if !app.pending_approvals_list().is_empty() {
                    app.modal_decision = 0;
                    app.modal_message.clear();
                    app.screen = Screen::ApprovalModal {
                        approval_index: app.selected_approval,
                    };
                }
            }
            _ => {}
        },
        KeyCode::Char('c') if app.tab == Tab::Runs => {
            if let Some(id) = app.selected_run_id() {
                let _ = client.cancel_run(id).await;
            }
        }
        KeyCode::Char('p') if app.tab == Tab::Runs => {
            if let Some(id) = app.selected_run_id() {
                let _ = client.resume_run(id).await;
            }
        }
        KeyCode::Char('R') if app.tab == Tab::Runs => {
            // Generate and display session report for the selected run
            if let Some(run_id) = app.selected_run_id().map(|s| s.to_string()) {
                match toil_tui::report::generate_report(client, &run_id).await {
                    Ok(content) => {
                        app.report_scroll = 0;
                        app.screen = Screen::Report {
                            run_id: run_id.clone(),
                            content,
                        };
                    }
                    Err(e) => {
                        app.flash_message = Some(format!("Report failed: {}", e));
                    }
                }
            }
        }
        KeyCode::Char('r') if app.tab == Tab::Workflows => {
            if let Some(wf_id) = app.workflow_ids.get(app.selected_workflow).cloned() {
                app.run_form = Some(RunForm {
                    workflow_id: wf_id,
                    fields: vec![
                        ("idea".to_string(), String::new()),
                        ("project_dir".to_string(),
                         std::env::var("PROJECT_DIR").unwrap_or_default()),
                    ],
                    current_field: 0,
                });
            }
        }
        KeyCode::Char('r') if app.tab == Tab::Runs && app.pane == Pane::Right => {
            if let Some(meta) = app.selected_run_meta() {
                let mut node_ids: Vec<&String> = meta.nodes.keys().collect();
                node_ids.sort();
                if let Some(node_id) = node_ids.get(app.selected_node) {
                    let _ = client.retrigger_node(&meta.run_id, node_id).await;
                }
            }
        }
        KeyCode::Char('a') if app.tab == Tab::Approvals => {
            if !app.pending_approvals_list().is_empty() {
                app.modal_decision = 0;
                app.modal_message.clear();
                app.screen = Screen::ApprovalModal {
                    approval_index: app.selected_approval,
                };
            }
        }
        _ => {}
    }
}

fn handle_transcript_key(app: &mut App, code: KeyCode) {
    // Clear flash message on any key press
    app.flash_message = None;

    match code {
        KeyCode::Esc => {
            app.screen = Screen::Home;
        }
        KeyCode::Char('f') => {
            app.transcript_following = !app.transcript_following;
        }
        KeyCode::Char('x') => {
            // Export transcript to file
            if let Screen::Transcript { ref run_id, ref node_id } = app.screen {
                let run_id = run_id.clone();
                let node_id = node_id.clone();
                match crate::ui::transcript::export_transcript(
                    &app.transcript_items, &run_id, &node_id,
                ) {
                    Ok(path) => {
                        app.flash_message = Some(format!("Exported to {}", path));
                    }
                    Err(e) => {
                        app.flash_message = Some(format!("Export failed: {}", e));
                    }
                }
            }
        }
        KeyCode::Char('e') => {
            // Toggle the selected fold (the one highlighted in the viewport)
            if let Some(block_idx) = app.transcript_selected_fold {
                if app.transcript_expanded.contains(&block_idx) {
                    app.transcript_expanded.remove(&block_idx);
                } else {
                    app.transcript_expanded.insert(block_idx);
                }
            }
        }
        KeyCode::Char('E') => {
            // Toggle all folds: expand all if any are collapsed, collapse all otherwise
            if app.transcript_expanded.len() < app.transcript_fold_map.len() {
                for &idx in &app.transcript_fold_map {
                    app.transcript_expanded.insert(idx);
                }
            } else {
                app.transcript_expanded.clear();
            }
        }
        KeyCode::Char('j') | KeyCode::Down => {
            leave_follow_mode(app);
            app.transcript_scroll = app.transcript_scroll.saturating_add(1);
        }
        KeyCode::Char('k') | KeyCode::Up => {
            leave_follow_mode(app);
            app.transcript_scroll = app.transcript_scroll.saturating_sub(1);
        }
        KeyCode::PageDown | KeyCode::Char('d') => {
            leave_follow_mode(app);
            let page = app.transcript_visible_height.max(1);
            app.transcript_scroll = app.transcript_scroll.saturating_add(page);
        }
        KeyCode::PageUp | KeyCode::Char('u') => {
            leave_follow_mode(app);
            let page = app.transcript_visible_height.max(1);
            app.transcript_scroll = app.transcript_scroll.saturating_sub(page);
        }
        KeyCode::Home | KeyCode::Char('g') => {
            leave_follow_mode(app);
            app.transcript_scroll = 0;
        }
        KeyCode::End | KeyCode::Char('G') => {
            app.transcript_following = true;
        }
        _ => {}
    }
}

fn handle_report_key(app: &mut App, code: KeyCode) {
    // Clear flash message on any key press
    app.flash_message = None;

    match code {
        KeyCode::Esc => {
            app.screen = Screen::Home;
        }
        KeyCode::Char('x') => {
            // Export report to file
            if let Screen::Report { ref run_id, ref content } = app.screen {
                match toil_tui::report::export_report(content, run_id) {
                    Ok(path) => {
                        app.flash_message = Some(format!("Exported to {}", path));
                    }
                    Err(e) => {
                        app.flash_message = Some(format!("Export failed: {}", e));
                    }
                }
            }
        }
        KeyCode::Char('j') | KeyCode::Down => {
            app.report_scroll = app.report_scroll.saturating_add(1);
        }
        KeyCode::Char('k') | KeyCode::Up => {
            app.report_scroll = app.report_scroll.saturating_sub(1);
        }
        KeyCode::PageDown | KeyCode::Char('d') => {
            let page = app.report_visible_height.max(1);
            app.report_scroll = app.report_scroll.saturating_add(page);
        }
        KeyCode::PageUp | KeyCode::Char('u') => {
            let page = app.report_visible_height.max(1);
            app.report_scroll = app.report_scroll.saturating_sub(page);
        }
        KeyCode::Home | KeyCode::Char('g') => {
            app.report_scroll = 0;
        }
        KeyCode::End | KeyCode::Char('G') => {
            app.report_scroll = usize::MAX;
        }
        _ => {}
    }
}

fn leave_follow_mode(app: &mut App) {
    if app.transcript_following {
        app.transcript_scroll = app.transcript_rendered_scroll;
        app.transcript_following = false;
    }
}

fn handle_sse_event(app: &mut App, event: &toil_tui::models::Event) {
    match event.event_type.as_str() {
        "node_started" | "node_completed" | "node_failed" | "node_skipped" => {
            if let Some(node_id) = &event.node_id {
                let status = event.event_type.strip_prefix("node_").unwrap_or("");
                if let Some(meta) = app.run_cache.get_mut(&event.run_id) {
                    if let Some(node) = meta.nodes.get_mut(node_id) {
                        node.status = status.to_string();
                    }
                }
            }
        }
        "run_started" | "run_completed" | "run_failed" | "run_cancelled" | "run_paused"
        | "run_resumed" => {
            let status = event.event_type.strip_prefix("run_").unwrap_or("");
            if let Some(meta) = app.run_cache.get_mut(&event.run_id) {
                meta.status = status.to_string();
            }
        }
        "node_output" => {
            if let Screen::Transcript { ref run_id, ref node_id } = app.screen {
                if event.run_id == *run_id
                    && event.node_id.as_deref() == Some(node_id.as_str())
                {
                    if let Some(text) = &event.text {
                        app.transcript_items.push(text.clone());
                    }
                }
            }
        }
        "approval_requested" => {
            app.pending_approvals = app.pending_approvals.saturating_add(1);
        }
        "approval_resolved" => {
            app.pending_approvals = app.pending_approvals.saturating_sub(1);
        }
        _ => {}
    }
}

async fn poll_data(app: &mut App, client: &ToilClient) {
    if let Ok(ids) = client.list_runs().await {
        for id in &ids {
            if !app.run_cache.contains_key(id) {
                if let Ok(meta) = client.run_meta(id).await {
                    app.run_cache.insert(id.clone(), meta);
                }
            }
        }
        app.run_ids = ids;
        app.run_ids.sort_by(|a, b| {
            let a_time = app.run_cache.get(a).and_then(|m| m.started_at);
            let b_time = app.run_cache.get(b).and_then(|m| m.started_at);
            b_time.cmp(&a_time)
        });
    }
    if let Ok(approvals) = client.list_approvals().await {
        app.pending_approvals = approvals.iter().filter(|a| a.status == "pending").count();
        app.approvals = approvals;
    }
    match client.health().await {
        Ok(health) => {
            app.connected = true;
            app.active_runs = health.active_runs;
            app.total_runs = health.total_runs;
        }
        Err(_) => {
            app.connected = false;
        }
    }
}

async fn handle_modal_key(app: &mut App, code: KeyCode, client: &ToilClient) {
    match code {
        KeyCode::Esc => {
            app.screen = Screen::Home;
        }
        KeyCode::Up | KeyCode::Char('k') => {
            app.modal_decision = app.modal_decision.saturating_sub(1);
        }
        KeyCode::Down | KeyCode::Char('j') => {
            // Bounds check against current approval's choices
            if let Screen::ApprovalModal { approval_index } = &app.screen {
                let pending = app.pending_approvals_list();
                if let Some(approval) = pending.get(*approval_index) {
                    if app.modal_decision + 1 < approval.choices.len() {
                        app.modal_decision += 1;
                    }
                }
            }
        }
        KeyCode::Tab => {
            // Focus moves to message field (handled by UI state)
        }
        KeyCode::Enter => {
            // Submit approval
            if let Screen::ApprovalModal { approval_index } = &app.screen {
                let pending = app.pending_approvals_list();
                if let Some(approval) = pending.get(*approval_index) {
                    let decision = approval
                        .choices
                        .get(app.modal_decision)
                        .cloned()
                        .unwrap_or_default();
                    let _ = client
                        .resolve_approval(
                            &approval.id,
                            &decision,
                            &app.modal_message,
                            "",
                        )
                        .await;
                }
            }
            app.modal_decision = 0;
            app.modal_message.clear();
            app.screen = Screen::Home;
        }
        KeyCode::Char(c) => {
            app.modal_message.push(c);
        }
        KeyCode::Backspace => {
            app.modal_message.pop();
        }
        _ => {}
    }
}
