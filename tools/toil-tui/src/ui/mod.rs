mod chrome;
pub mod graph;
mod report;
pub mod runs_tab;
mod workflows_tab;
mod approvals_tab;
pub mod transcript;

use crate::app::{App, Screen, Tab};
use ratatui::prelude::*;

pub fn draw(frame: &mut Frame, app: &mut App) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1), // status bar
            Constraint::Min(0),   // main content
            Constraint::Length(1), // key hints
        ])
        .split(frame.area());

    chrome::draw_status_bar(frame, chunks[0], app);

    // Clone screen variant data to avoid borrow conflict with &mut app
    enum ScreenKind {
        Transcript(String, String),
        Report(String, String),
        Modal,
        Home,
    }
    let kind = match &app.screen {
        Screen::Transcript { run_id, node_id } => {
            ScreenKind::Transcript(run_id.clone(), node_id.clone())
        }
        Screen::Report { run_id, content } => {
            ScreenKind::Report(run_id.clone(), content.clone())
        }
        Screen::ApprovalModal { .. } => ScreenKind::Modal,
        Screen::Home => ScreenKind::Home,
    };

    match kind {
        ScreenKind::Transcript(run_id, node_id) => {
            transcript::draw(frame, chunks[1], app, &run_id, &node_id);
        }
        ScreenKind::Report(run_id, content) => {
            report::draw(frame, chunks[1], app, &run_id, &content);
        }
        ScreenKind::Home | ScreenKind::Modal => {
            match app.tab {
                Tab::Runs => runs_tab::draw(frame, chunks[1], app),
                Tab::Workflows => workflows_tab::draw(frame, chunks[1], app),
                Tab::Approvals => approvals_tab::draw(frame, chunks[1], app),
            }
            if matches!(kind, ScreenKind::Modal) {
                approvals_tab::draw_modal(frame, app);
            }
        }
    }

    chrome::draw_key_hints(frame, chunks[2], app);
}
