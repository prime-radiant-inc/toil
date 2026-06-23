use toil_tui::models::*;

#[test]
fn deserialize_run_meta() {
    let json = r#"{
        "run_id": "eagle-flint-pacer",
        "workflow_id": "implement_task",
        "status": "running",
        "error": "",
        "title": "Implement auth validation",
        "description": "Adding JWT token validation",
        "summary": "",
        "started_at": "2026-03-12T10:00:00Z",
        "finished_at": null,
        "nodes": {
            "plan": {"status": "completed", "decision": "proceed"},
            "write_code": {"status": "running", "decision": ""}
        },
        "inputs": {"idea": "Add auth validation"}
    }"#;
    let meta: RunMeta = serde_json::from_str(json).unwrap();
    assert_eq!(meta.run_id, "eagle-flint-pacer");
    assert_eq!(meta.status, "running");
    assert_eq!(meta.title.as_deref(), Some("Implement auth validation"));
    assert!(meta.finished_at.is_none());
    assert_eq!(meta.nodes.len(), 2);
    assert_eq!(meta.nodes["plan"].status, "completed");
}

#[test]
fn deserialize_event() {
    let json = r#"{
        "timestamp": "2026-03-12T10:01:00Z",
        "type": "node_started",
        "run_id": "eagle-flint-pacer",
        "node_id": "write_code"
    }"#;
    let event: Event = serde_json::from_str(json).unwrap();
    assert_eq!(event.event_type, "node_started");
    assert_eq!(event.node_id.as_deref(), Some("write_code"));
}

#[test]
fn deserialize_node_output_event() {
    let json = r#"{
        "timestamp": "2026-03-12T10:01:30Z",
        "type": "node_output",
        "run_id": "eagle-flint-pacer",
        "node_id": "write_code",
        "text": "{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"I will implement...\"}]}"
    }"#;
    let event: Event = serde_json::from_str(json).unwrap();
    assert_eq!(event.event_type, "node_output");
    assert!(event.text.is_some());
}

#[test]
fn deserialize_topology_graph() {
    let json = r#"{
        "nodes": [
            {"id": "run1", "label": "My Run", "status": "running", "current": true},
            {"id": "run1::plan", "label": "plan", "parent": "run1", "status": "completed"}
        ],
        "edges": [
            {"id": "e0", "source": "run1::plan", "target": "run1::write", "label": "proceed"}
        ]
    }"#;
    let graph: TopologyGraph = serde_json::from_str(json).unwrap();
    assert_eq!(graph.nodes.len(), 2);
    assert_eq!(graph.edges.len(), 1);
    assert!(graph.nodes[0].current);
    assert_eq!(graph.nodes[1].parent.as_deref(), Some("run1"));
}

#[test]
fn deserialize_approval() {
    let json = r#"{
        "id": "appr-001",
        "run_id": "eagle-flint-pacer",
        "node_id": "review",
        "attempt": 1,
        "status": "pending",
        "question": "Approve the implementation?",
        "choices": ["approve", "reject", "revise"],
        "timeout_sec": 300,
        "default": "reject",
        "created_at": "2026-03-12T10:05:00Z"
    }"#;
    let approval: Approval = serde_json::from_str(json).unwrap();
    assert_eq!(approval.id, "appr-001");
    assert_eq!(approval.choices.len(), 3);
    assert_eq!(approval.timeout_sec, Some(300));
}

#[test]
fn deserialize_health() {
    let json = r#"{"status":"ok","uptime_seconds":120,"active_runs":3,"total_runs":15}"#;
    let health: Health = serde_json::from_str(json).unwrap();
    assert_eq!(health.active_runs, 3);
    assert_eq!(health.total_runs, 15);
}
