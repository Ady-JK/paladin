use serde::{Deserialize, Serialize};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::error::ApiError;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatRequest {
    pub session_id: String,
    pub message: String,
    pub cwd: String,
    #[serde(default)]
    pub provider: Option<String>,
    #[serde(default)]
    pub model: Option<String>,
    #[serde(default)]
    pub system_prompt: Option<String>,
    #[serde(default)]
    pub resume: bool,
    #[serde(skip)]
    pub cancel: Option<CancellationToken>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolActivity {
    pub id: String,
    pub name: String,
    pub arguments: String,
    pub result: String,
    pub status: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatChunk {
    pub session_id: String,
    pub delta: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool: Option<ToolActivity>,
    #[serde(default)]
    pub done: bool,
    #[serde(default)]
    pub stopped: bool,
    #[serde(default)]
    pub needs_continue: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ApiError>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatOutcome {
    pub session_id: String,
    pub reply: String,
    pub stopped: bool,
    pub needs_continue: bool,
}

pub struct TurnOutcome {
    pub reply: String,
    pub stopped: bool,
    pub needs_continue: bool,
}

/// Per-turn owned state (plan §9.5): created at `chat()` entry, dropped at exit.
pub struct Turn {
    pub session_id: String,
    pub cwd: std::path::PathBuf,
    pub cancel: CancellationToken,
    pub rounds: u32,
    pub outcome: TurnOutcome,
}

pub async fn run_turn(
    _request: ChatRequest,
    _tx: mpsc::Sender<ChatChunk>,
    _max_rounds: u32,
) -> anyhow::Result<ChatOutcome> {
    // Full tool-round loop lands in plan §10 step 6.
    anyhow::bail!("turn loop not implemented in scaffold")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chunk_defaults_are_not_done() {
        let chunk = ChatChunk {
            session_id: "s".to_string(),
            delta: "hi".to_string(),
            tool: None,
            done: false,
            stopped: false,
            needs_continue: false,
            error: None,
        };
        assert!(!chunk.done);
    }
}
