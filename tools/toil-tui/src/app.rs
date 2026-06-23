use crate::models::*;
use std::collections::HashMap;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tab {
    Runs,
    Workflows,
    Approvals,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Pane {
    Left,
    Right,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RunDetailView {
    List,
    Graph,
}

#[derive(Debug)]
pub enum Screen {
    Home,
    Transcript { run_id: String, node_id: String },
    Report { run_id: String, content: String },
    ApprovalModal { approval_index: usize },
}

pub struct App {
    pub tab: Tab,
    pub pane: Pane,
    pub screen: Screen,
    pub run_detail_view: RunDetailView,

    // Connection
    pub base_url: String,
    pub connected: bool,

    // Runs
    pub run_ids: Vec<String>,
    pub run_cache: HashMap<String, RunMeta>,
    pub selected_run: usize,
    pub selected_node: usize,

    // Execution group
    pub compound_graph: Option<TopologyGraph>,

    // Workflows
    pub workflow_ids: Vec<String>,
    pub selected_workflow: usize,
    pub run_form: Option<RunForm>,

    // Approvals
    pub approvals: Vec<Approval>,
    pub selected_approval: usize,

    // Transcript
    pub transcript_items: Vec<String>,
    pub transcript_following: bool,
    pub transcript_scroll: usize,
    /// Scroll position computed during last render (used to snapshot when leaving follow mode)
    pub transcript_rendered_scroll: usize,
    /// Viewport height of transcript body, stored during render for PageUp/PageDown
    pub transcript_visible_height: usize,
    /// Set of block indices whose content is expanded (overrides the 5-line fold)
    pub transcript_expanded: std::collections::HashSet<usize>,
    /// Block indices of all foldable blocks (populated during render)
    pub transcript_fold_map: Vec<usize>,
    /// Block index of the fold currently highlighted in viewport (for per-item 'e')
    pub transcript_selected_fold: Option<usize>,

    // Approval modal
    pub modal_decision: usize,
    pub modal_message: String,

    // Counts for status bar
    pub active_runs: u32,
    pub total_runs: u32,
    pub pending_approvals: usize,

    // Filter
    pub filter_active: bool,
    pub filter_text: String,

    // Report viewer
    pub report_scroll: usize,
    pub report_visible_height: usize,

    /// Transient status message shown in the status bar (cleared on next key press)
    pub flash_message: Option<String>,

    pub should_quit: bool,
}

pub struct RunForm {
    pub workflow_id: String,
    pub fields: Vec<(String, String)>, // (name, value)
    pub current_field: usize,
}

impl App {
    pub fn new(base_url: String) -> Self {
        Self {
            tab: Tab::Runs,
            pane: Pane::Left,
            screen: Screen::Home,
            run_detail_view: RunDetailView::List,
            base_url,
            connected: false,
            run_ids: Vec::new(),
            run_cache: HashMap::new(),
            selected_run: 0,
            selected_node: 0,
            compound_graph: None,
            workflow_ids: Vec::new(),
            selected_workflow: 0,
            run_form: None,
            approvals: Vec::new(),
            selected_approval: 0,
            transcript_items: Vec::new(),
            transcript_following: false,
            transcript_scroll: 0,
            transcript_rendered_scroll: 0,
            transcript_visible_height: 0,
            transcript_expanded: std::collections::HashSet::new(),
            transcript_fold_map: Vec::new(),
            transcript_selected_fold: None,
            report_scroll: 0,
            report_visible_height: 0,
            modal_decision: 0,
            modal_message: String::new(),
            active_runs: 0,
            total_runs: 0,
            pending_approvals: 0,
            filter_active: false,
            filter_text: String::new(),
            flash_message: None,
            should_quit: false,
        }
    }

    pub fn selected_run_id(&self) -> Option<&str> {
        self.run_ids.get(self.selected_run).map(|s| s.as_str())
    }

    pub fn selected_run_meta(&self) -> Option<&RunMeta> {
        self.selected_run_id()
            .and_then(|id| self.run_cache.get(id))
    }

    pub fn pending_approvals_list(&self) -> Vec<&Approval> {
        self.approvals
            .iter()
            .filter(|a| a.status == "pending")
            .collect()
    }

    /// Returns true if the run at the given index is a top-level (non-child) run.
    fn is_root_run(&self, idx: usize) -> bool {
        self.run_ids.get(idx).map_or(false, |id| {
            self.run_cache
                .get(id)
                .and_then(|m| m.parent_run.as_ref())
                .map_or(true, |p| p.is_empty())
        })
    }

    /// Move selected_run to the next top-level run (skipping child runs).
    pub fn select_next_root_run(&mut self) {
        let mut next = self.selected_run + 1;
        while next < self.run_ids.len() {
            if self.is_root_run(next) {
                self.selected_run = next;
                self.selected_node = 0;
                return;
            }
            next += 1;
        }
    }

    /// Move selected_run to the previous top-level run (skipping child runs).
    pub fn select_prev_root_run(&mut self) {
        if self.selected_run == 0 {
            return;
        }
        let mut prev = self.selected_run - 1;
        loop {
            if self.is_root_run(prev) {
                self.selected_run = prev;
                self.selected_node = 0;
                return;
            }
            if prev == 0 {
                return;
            }
            prev -= 1;
        }
    }

    /// Count of nodes in the right pane (compound graph nodes or run meta nodes).
    pub fn right_pane_node_count(&self) -> usize {
        if let Some(ref graph) = self.compound_graph {
            if graph.nodes.iter().any(|n| n.parent.is_some()) {
                return graph.nodes.len();
            }
        }
        self.selected_run_meta()
            .map(|m| m.nodes.len())
            .unwrap_or(0)
    }

    /// Resolve the selected compound graph node into a (run_id, node_id) pair
    /// for transcript loading. Compound node IDs are `{runID}::{nodeID}`.
    /// Run container nodes (no "::") return None for node_id.
    pub fn selected_compound_node(&self) -> Option<(String, Option<String>)> {
        let graph = self.compound_graph.as_ref()?;
        if !graph.nodes.iter().any(|n| n.parent.is_some()) {
            return None; // Not a compound graph with children
        }

        // Flatten the tree in render order (time-sorted) to find the node at selected_node
        let mut roots: Vec<&TopologyNode> = graph
            .nodes
            .iter()
            .filter(|n| n.parent.is_none())
            .collect();
        roots.sort_by_key(|n| self.run_cache.get(&n.id).and_then(|m| m.started_at));

        let mut flat_nodes: Vec<&TopologyNode> = Vec::new();
        for root in &roots {
            self.flatten_tree_sorted(&mut flat_nodes, graph, root);
        }

        let node = flat_nodes.get(self.selected_node)?;
        if let Some(pos) = node.id.find("::") {
            let run_id = node.id[..pos].to_string();
            let node_id = node.id[pos + 2..].to_string();
            Some((run_id, Some(node_id)))
        } else {
            // Run container node
            Some((node.id.clone(), None))
        }
    }

    fn flatten_tree_sorted<'a>(
        &self,
        out: &mut Vec<&'a TopologyNode>,
        graph: &'a TopologyGraph,
        node: &'a TopologyNode,
    ) {
        out.push(node);
        let mut children: Vec<&TopologyNode> = graph
            .nodes
            .iter()
            .filter(|n| n.parent.as_deref() == Some(&node.id))
            .collect();
        children.sort_by_key(|n| {
            let run_id = n.id.split("::").next().unwrap_or(&n.id);
            self.run_cache.get(run_id).and_then(|m| m.started_at)
        });
        for child in children {
            self.flatten_tree_sorted(out, graph, child);
        }
    }
}
