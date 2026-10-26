use serde::{Deserialize, Serialize};

#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    #[error("io: {0}")]
    Io(String),
    #[error("parse: {0}")]
    Parse(String),
    #[error("keyring unavailable: {0}")]
    Keyring(String),
}

#[derive(Debug, thiserror::Error)]
pub enum ProviderError {
    #[error("unknown provider: {0}")]
    Unknown(String),
    #[error("provider has placeholder key: {0}")]
    PlaceholderKey(String),
    #[error("request failed: {0}")]
    Request(String),
    #[error("config: {0}")]
    Config(String),
}

#[derive(Debug, thiserror::Error)]
pub enum ToolError {
    #[error("unknown tool: {0}")]
    Unknown(String),
    #[error("bad arguments: {0}")]
    Args(String),
    #[error("execution failed: {0}")]
    Exec(String),
    #[error("{msg}")]
    Failed { content: String, msg: String },
}

impl ToolError {
    /// A failure that preserves model-facing output.
    pub fn with_content(content: impl Into<String>, msg: impl Into<String>) -> Self {
        Self::Failed {
            content: content.into(),
            msg: msg.into(),
        }
    }

    pub fn args(msg: impl Into<String>) -> Self {
        let msg = msg.into();
        Self::Failed {
            content: serde_json::json!({"error": msg}).to_string(),
            msg,
        }
    }

    pub fn content(&self) -> String {
        match self {
            Self::Failed { content, .. } => content.clone(),
            e => serde_json::json!({"error": e.to_string()}).to_string(),
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum SessionError {
    #[error("io: {0}")]
    Io(String),
    #[error("not found: {0}")]
    NotFound(String),
    #[error("version too new: {0}")]
    VersionTooNew(String),
}

#[derive(Debug, thiserror::Error)]
pub enum ChatError {
    #[error("no provider available")]
    NoProvider,
    #[error("provider: {0}")]
    Provider(String),
    #[error("tool: {0}")]
    Tool(String),
    #[error("cancelled")]
    Cancelled,
    #[error("concurrent turn for session: {0}")]
    Concurrent(String),
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiError {
    pub code: String,
    pub message: String,
}

impl ApiError {
    pub fn new(code: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            code: code.into(),
            message: message.into(),
        }
    }
}
