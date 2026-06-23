use toil_tui::ui::graph::layout_graph;
use toil_tui::models::{TopologyGraph, TopologyNode, TopologyEdge};

fn make_linear_graph() -> TopologyGraph {
    TopologyGraph {
        nodes: vec![
            TopologyNode { id: "a".into(), label: "plan".into(), parent: None, status: Some("completed".into()), kind: None, workflow: None, selected: false, current: false, decision: None, attempts: 0, prompt: None },
            TopologyNode { id: "b".into(), label: "write".into(), parent: None, status: Some("running".into()), kind: None, workflow: None, selected: false, current: false, decision: None, attempts: 0, prompt: None },
            TopologyNode { id: "c".into(), label: "test".into(), parent: None, status: Some("pending".into()), kind: None, workflow: None, selected: false, current: false, decision: None, attempts: 0, prompt: None },
        ],
        edges: vec![
            TopologyEdge { id: "e0".into(), source: "a".into(), target: "b".into(), label: None, is_escape: false },
            TopologyEdge { id: "e1".into(), source: "b".into(), target: "c".into(), label: None, is_escape: false },
        ],
    }
}

#[test]
fn layout_linear_graph_has_three_rows() {
    let graph = make_linear_graph();
    let layout = layout_graph(&graph);
    assert_eq!(layout.rows.len(), 3);
    assert_eq!(layout.rows[0].node_id, "a");
    assert_eq!(layout.rows[1].node_id, "b");
    assert_eq!(layout.rows[2].node_id, "c");
}

#[test]
fn layout_includes_forward_edges() {
    let graph = make_linear_graph();
    let layout = layout_graph(&graph);
    assert!(!layout.forward_edges.is_empty());
}

fn make_cycle_graph() -> TopologyGraph {
    TopologyGraph {
        nodes: vec![
            TopologyNode { id: "a".into(), label: "write".into(), parent: None, status: None, kind: None, workflow: None, selected: false, current: false, decision: None, attempts: 0, prompt: None },
            TopologyNode { id: "b".into(), label: "test".into(), parent: None, status: None, kind: None, workflow: None, selected: false, current: false, decision: None, attempts: 0, prompt: None },
        ],
        edges: vec![
            TopologyEdge { id: "e0".into(), source: "a".into(), target: "b".into(), label: Some("proceed".into()), is_escape: false },
            TopologyEdge { id: "e1".into(), source: "b".into(), target: "a".into(), label: Some("fail".into()), is_escape: false },
        ],
    }
}

#[test]
fn layout_detects_back_edges() {
    let graph = make_cycle_graph();
    let layout = layout_graph(&graph);
    assert!(!layout.back_edges.is_empty(), "should detect back-edge from b->a");
}
