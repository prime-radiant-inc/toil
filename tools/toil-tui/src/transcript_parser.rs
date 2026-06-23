use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ItemKind {
    /// User/synthetic prompt sent to the node (shell script, LLM prompt)
    Prompt,
    /// Assistant/LLM text response
    AssistantText,
    /// Tool call by the assistant
    ToolUse,
    /// Result returned from a tool call
    ToolResult,
    /// Raw text output (stdout/stderr from runners)
    PlainText,
}

#[derive(Debug, Clone)]
pub struct TranscriptItem {
    pub kind: ItemKind,
    pub text: String,
    pub tool_name: Option<String>,
    pub tool_input: Option<String>,
}

/// Parse a single node_output event text into displayable transcript items.
///
/// Handles three formats:
///   1. Serf log records: `{"kind":"ASSISTANT_TEXT_END","data":{...}}`
///   2. Anthropic/toil message envelope: `{"message":{"content":[...]},"type":"user"}`
///   3. Plain text (stdout/stderr from shell runners)
pub fn parse_transcript_text(text: &str) -> Vec<TranscriptItem> {
    let Ok(val) = serde_json::from_str::<Value>(text) else {
        return vec![plain(text)];
    };

    // Serf log records have a "kind" field (uppercase like SESSION_START, TOOL_CALL_START)
    if let Some(kind) = val.get("kind").and_then(|k| k.as_str()) {
        return parse_serf_record(kind, &val);
    }

    let top_type = val.get("type").and_then(|v| v.as_str()).unwrap_or("");

    // Toil wraps messages as {"message":{...},"type":"user"|"assistant"}
    if let Some(inner) = val.get("message") {
        let role = inner
            .get("role")
            .and_then(|r| r.as_str())
            .unwrap_or(top_type);
        if let Some(items) = extract_content_blocks(inner, role) {
            if !items.is_empty() {
                return items;
            }
        }
    }

    // Flat {"type":"message","content":[...]}
    if top_type == "message" {
        if let Some(items) = extract_content_blocks(&val, "assistant") {
            if !items.is_empty() {
                return items;
            }
        }
    }

    // {"type":"tool_result","content":"..."}
    if top_type == "tool_result" {
        let content = val
            .get("content")
            .map(|v| match v {
                Value::String(s) => s.clone(),
                _ => v.to_string(),
            })
            .unwrap_or_default();
        return vec![TranscriptItem {
            kind: ItemKind::ToolResult,
            text: content,
            tool_name: None,
            tool_input: None,
        }];
    }

    // Has content array at top level (any type)
    if val.get("content").and_then(|c| c.as_array()).is_some() {
        let role = val
            .get("role")
            .and_then(|r| r.as_str())
            .unwrap_or("assistant");
        if let Some(items) = extract_content_blocks(&val, role) {
            if !items.is_empty() {
                return items;
            }
        }
    }

    // Unrecognized JSON — plain text
    vec![plain(text)]
}

/// Parse a serf log record by kind, extracting only human-readable content.
/// Returns empty vec for metadata-only records (they get filtered out).
fn parse_serf_record(kind: &str, val: &Value) -> Vec<TranscriptItem> {
    let data = val.get("data").unwrap_or(&Value::Null);

    match kind {
        "ASSISTANT_TEXT_END" => {
            let text = data
                .get("text")
                .and_then(|t| t.as_str())
                .unwrap_or("")
                .trim();
            if text.is_empty() {
                return Vec::new(); // No text (finish_reason was tool_calls)
            }
            vec![TranscriptItem {
                kind: ItemKind::AssistantText,
                text: text.to_string(),
                tool_name: None,
                tool_input: None,
            }]
        }
        "TOOL_CALL_START" => {
            let tool_name = data
                .get("tool_name")
                .and_then(|n| n.as_str())
                .map(String::from);
            let input = data
                .get("arguments_json")
                .and_then(|a| a.as_str())
                .map(String::from);
            vec![TranscriptItem {
                kind: ItemKind::ToolUse,
                text: String::new(),
                tool_name,
                tool_input: input,
            }]
        }
        "TOOL_CALL_END" => {
            // Prefer TOOL_CALL_OUTPUT_DELTA for result content (always populated).
            // Only show TOOL_CALL_END output if non-empty, as a fallback for
            // runners that don't emit deltas.
            Vec::new()
        }
        "TOOL_CALL_OUTPUT_DELTA" => {
            // Show delta content as tool result — some runners only populate
            // deltas and leave TOOL_CALL_END.output empty (e.g. shell runner).
            let delta = data
                .get("delta")
                .and_then(|d| d.as_str())
                .unwrap_or("")
                .trim();
            let tool_name = data
                .get("tool_name")
                .and_then(|n| n.as_str())
                .map(String::from);
            if delta.is_empty() {
                return Vec::new();
            }
            vec![TranscriptItem {
                kind: ItemKind::ToolResult,
                text: delta.to_string(),
                tool_name,
                tool_input: None,
            }]
        }
        // Skip metadata-only and redundant records
        "SUBMIT_RESULT" | "SESSION_START" | "SESSION_END" | "ASSISTANT_TEXT_START"
        | "PROMPT_LOADED" | "USER_INPUT" => Vec::new(),
        // Unknown kind — skip rather than dump raw JSON
        _ => Vec::new(),
    }
}

/// Extract content blocks from a value that has a "content" array.
fn extract_content_blocks(val: &Value, role: &str) -> Option<Vec<TranscriptItem>> {
    let content = val.get("content")?.as_array()?;
    let is_prompt = role == "user";

    let items: Vec<TranscriptItem> = content
        .iter()
        .filter_map(|block| {
            let block_type = block.get("type")?.as_str()?;
            match block_type {
                "text" => {
                    let text = block.get("text")?.as_str()?.to_string();
                    Some(TranscriptItem {
                        kind: if is_prompt {
                            ItemKind::Prompt
                        } else {
                            ItemKind::AssistantText
                        },
                        text,
                        tool_name: None,
                        tool_input: None,
                    })
                }
                "tool_use" => {
                    let name = block.get("name").and_then(|n| n.as_str()).map(String::from);
                    let input = block.get("input").map(|i| i.to_string());
                    Some(TranscriptItem {
                        kind: ItemKind::ToolUse,
                        text: String::new(),
                        tool_name: name,
                        tool_input: input,
                    })
                }
                "tool_result" => {
                    let text = match block.get("content") {
                        Some(Value::String(s)) => s.clone(),
                        Some(v) => v.to_string(),
                        None => String::new(),
                    };
                    Some(TranscriptItem {
                        kind: ItemKind::ToolResult,
                        text,
                        tool_name: None,
                        tool_input: None,
                    })
                }
                _ => None,
            }
        })
        .collect();

    Some(items)
}

fn plain(text: &str) -> TranscriptItem {
    TranscriptItem {
        kind: ItemKind::PlainText,
        text: text.to_string(),
        tool_name: None,
        tool_input: None,
    }
}
