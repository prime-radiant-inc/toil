use crate::models::*;
use reqwest::Client;
use std::collections::HashMap;

pub struct ToilClient {
    base_url: String,
    http: Client,
}

impl ToilClient {
    pub fn new(base_url: &str) -> Self {
        Self {
            base_url: base_url.trim_end_matches('/').to_string(),
            http: Client::new(),
        }
    }

    pub async fn health(&self) -> Result<Health, reqwest::Error> {
        self.http.get(format!("{}/health", self.base_url)).send().await?.error_for_status()?.json().await
    }

    pub async fn list_runs(&self) -> Result<Vec<String>, reqwest::Error> {
        self.http.get(format!("{}/runs", self.base_url)).send().await?.error_for_status()?.json().await
    }

    pub async fn run_meta(&self, run_id: &str) -> Result<RunMeta, reqwest::Error> {
        self.http.get(format!("{}/runs/{}/meta", self.base_url, run_id)).send().await?.error_for_status()?.json().await
    }

    pub async fn run_events(&self, run_id: &str) -> Result<Vec<Event>, reqwest::Error> {
        let text = self.http.get(format!("{}/runs/{}/events", self.base_url, run_id)).send().await?.error_for_status()?.text().await?;
        let events: Vec<Event> = text.lines().filter(|l| !l.is_empty()).filter_map(|l| serde_json::from_str(l).ok()).collect();
        Ok(events)
    }

    pub async fn compound_graph(&self, run_id: &str) -> Result<TopologyGraph, reqwest::Error> {
        self.http.get(format!("{}/runs/{}/compound-graph", self.base_url, run_id)).send().await?.error_for_status()?.json().await
    }

    pub async fn workflow_graph(&self, workflow_id: &str) -> Result<TopologyGraph, reqwest::Error> {
        self.http.get(format!("{}/workflows/{}/graph", self.base_url, workflow_id)).send().await?.error_for_status()?.json().await
    }

    pub async fn workflow_yaml(&self, workflow_id: &str) -> Result<String, reqwest::Error> {
        self.http.get(format!("{}/workflows/{}", self.base_url, workflow_id)).send().await?.error_for_status()?.text().await
    }

    pub async fn list_workflows(&self) -> Result<Vec<String>, reqwest::Error> {
        self.http.get(format!("{}/workflows", self.base_url)).send().await?.error_for_status()?.json().await
    }

    pub async fn list_approvals(&self) -> Result<Vec<Approval>, reqwest::Error> {
        self.http.get(format!("{}/approvals", self.base_url)).send().await?.error_for_status()?.json().await
    }

    pub async fn resolve_approval(&self, approval_id: &str, decision: &str, message: &str, comment: &str) -> Result<(), reqwest::Error> {
        let body = serde_json::json!({"decision": decision, "message": message, "comment": comment});
        self.http.post(format!("{}/approvals/{}/resolve", self.base_url, approval_id)).json(&body).send().await?.error_for_status()?;
        Ok(())
    }

    pub async fn create_run(&self, workflow_id: &str, inputs: HashMap<String, String>, env: HashMap<String, String>) -> Result<String, reqwest::Error> {
        let body = serde_json::json!({"workflow_id": workflow_id, "inputs": inputs, "env": env});
        let resp: HashMap<String, String> = self.http.post(format!("{}/runs", self.base_url)).json(&body).send().await?.error_for_status()?.json().await?;
        Ok(resp.get("run_id").cloned().unwrap_or_default())
    }

    pub async fn cancel_run(&self, run_id: &str) -> Result<(), reqwest::Error> {
        self.http.post(format!("{}/runs/{}/cancel", self.base_url, run_id)).send().await?.error_for_status()?;
        Ok(())
    }

    pub async fn resume_run(&self, run_id: &str) -> Result<(), reqwest::Error> {
        self.http.post(format!("{}/runs/{}/resume", self.base_url, run_id)).send().await?.error_for_status()?;
        Ok(())
    }

    pub async fn retrigger_node(&self, run_id: &str, node_id: &str) -> Result<(), reqwest::Error> {
        let body = serde_json::json!({"node_id": node_id});
        self.http.post(format!("{}/runs/{}/retrigger", self.base_url, run_id)).json(&body).send().await?.error_for_status()?;
        Ok(())
    }

    pub fn events_stream_url(&self, run_id: &str) -> String {
        format!("{}/runs/{}/events/stream", self.base_url, run_id)
    }
}
