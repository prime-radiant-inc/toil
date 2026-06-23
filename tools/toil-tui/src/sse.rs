use crate::models::Event;
use tokio::sync::mpsc;

/// Parse a single SSE frame (collected lines between blank-line delimiters).
/// Returns None for comment-only frames or empty frames.
pub fn parse_sse_frame(lines: &[&str]) -> Option<Event> {
    let mut data_parts: Vec<&str> = Vec::new();

    for line in lines {
        if line.starts_with(':') || line.is_empty() {
            continue;
        }
        if let Some(value) = line.strip_prefix("data: ") {
            data_parts.push(value);
        } else if let Some(value) = line.strip_prefix("data:") {
            data_parts.push(value);
        }
        // We ignore "event:" and "id:" lines — the type is in the JSON data.
    }

    if data_parts.is_empty() {
        return None;
    }

    let data = data_parts.join("\n");
    serde_json::from_str(&data).ok()
}

/// Connect to an SSE endpoint and yield parsed Events through a channel.
pub async fn connect_sse(
    url: &str,
    tx: mpsc::UnboundedSender<Event>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let client = reqwest::Client::new();
    let response = client
        .get(url)
        .header("Accept", "text/event-stream")
        .send()
        .await?
        .error_for_status()?;

    let mut stream = response.bytes_stream();
    let mut buffer = String::new();

    use futures_util::StreamExt;
    while let Some(chunk) = stream.next().await {
        let chunk = chunk?;
        buffer.push_str(&String::from_utf8_lossy(&chunk));

        while let Some(pos) = buffer.find("\n\n") {
            let frame = buffer[..pos].to_string();
            buffer = buffer[pos + 2..].to_string();

            let lines: Vec<&str> = frame.lines().collect();
            if let Some(event) = parse_sse_frame(&lines) {
                if tx.send(event).is_err() {
                    return Ok(());
                }
            }
        }
    }

    Ok(())
}
