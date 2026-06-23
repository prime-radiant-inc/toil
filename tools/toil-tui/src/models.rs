use chrono::{DateTime, Utc};
use serde::Deserialize;
use std::collections::HashMap;

#[derive(Debug, Deserialize)]
pub struct Health {
    pub status: String,
    pub uptime_seconds: u64,
    pub active_runs: u32,
    pub total_runs: u32,
}

#[derive(Debug, Deserialize)]
pub struct RunMeta {
    pub run_id: String,
    pub workflow_id: String,
    pub status: String,
    #[serde(default)]
    pub error: String,
    pub title: Option<String>,
    pub description: Option<String>,
    pub summary: Option<String>,
    pub started_at: Option<DateTime<Utc>>,
    pub finished_at: Option<DateTime<Utc>>,
    pub nodes: HashMap<String, NodeMeta>,
    #[serde(default)]
    pub inputs: HashMap<String, serde_json::Value>,
    #[serde(default)]
    pub parent_run: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct NodeMeta {
    pub status: String,
    #[serde(default)]
    pub decision: String,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub data: Option<HashMap<String, serde_json::Value>>,
}

#[derive(Debug, Deserialize)]
pub struct Event {
    pub timestamp: DateTime<Utc>,
    #[serde(rename = "type")]
    pub event_type: String,
    pub run_id: String,
    pub node_id: Option<String>,
    pub stream: Option<String>,
    pub text: Option<String>,
    pub data: Option<HashMap<String, serde_json::Value>>,
    pub duration_ms: Option<i64>,
}

#[derive(Debug, Deserialize)]
pub struct TopologyGraph {
    pub nodes: Vec<TopologyNode>,
    pub edges: Vec<TopologyEdge>,
}

#[derive(Debug, Deserialize)]
pub struct TopologyNode {
    pub id: String,
    pub label: String,
    pub parent: Option<String>,
    pub status: Option<String>,
    pub kind: Option<String>,
    pub workflow: Option<String>,
    #[serde(default)]
    pub selected: bool,
    #[serde(default)]
    pub current: bool,
    pub decision: Option<String>,
    #[serde(default)]
    pub attempts: i32,
    pub prompt: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct TopologyEdge {
    pub id: String,
    pub source: String,
    pub target: String,
    pub label: Option<String>,
    #[serde(default, rename = "isEscape")]
    pub is_escape: bool,
}

#[derive(Debug, Deserialize)]
pub struct Approval {
    pub id: String,
    pub run_id: String,
    pub node_id: String,
    #[serde(default)]
    pub attempt: i32,
    pub status: String,
    pub question: Option<String>,
    #[serde(default)]
    pub choices: Vec<String>,
    pub timeout_sec: Option<i32>,
    pub default: Option<String>,
    pub decision: Option<String>,
    pub message: Option<String>,
    pub comment: Option<String>,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}
