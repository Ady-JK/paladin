use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::config::ApiType;
use crate::error::ProviderError;

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct LlmMessage {
    pub role: String,
    pub content: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tool_calls: Vec<ToolCall>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub parameters: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, Default)]
pub struct ToolResponse {
    pub content: String,
    pub tool_calls: Vec<ToolCall>,
}

/// One streamed piece: text, a tool-call notice (name arrived, args still
/// streaming), final tool calls, or an error.
#[derive(Debug, Clone, Default)]
pub struct Chunk {
    pub text: String,
    pub tool_calls: Vec<ToolCall>,
    pub tool_notice: Option<ToolCall>,
    pub err: Option<String>,
}

impl Chunk {
    pub fn text(text: impl Into<String>) -> Self {
        Self {
            text: text.into(),
            ..Default::default()
        }
    }
}

#[async_trait]
pub trait Provider: Send + Sync {
    fn name(&self) -> &str;
    fn api_type(&self) -> ApiType;
    fn model(&self) -> &str;
    async fn complete_tools(
        &self,
        system: &str,
        messages: &[LlmMessage],
        tools: &[ToolDefinition],
        session_id: &str,
    ) -> Result<ToolResponse, ProviderError>;
    async fn stream_tools(
        &self,
        system: &str,
        messages: &[LlmMessage],
        tools: &[ToolDefinition],
        session_id: &str,
        tx: &mpsc::Sender<Chunk>,
        cancel: &CancellationToken,
    ) -> Result<(), ProviderError>;
    async fn list_models(&self) -> Result<Vec<String>, ProviderError>;
}

pub fn build_provider(
    api_type: ApiType,
    name: String,
    model: String,
    base_url: String,
    api_key: String,
    timeout: std::time::Duration,
) -> Result<std::sync::Arc<dyn Provider>, ProviderError> {
    if crate::config::is_placeholder_key(&api_key) {
        return Err(ProviderError::PlaceholderKey(format!(
            "provider {name:?}: api key is still the placeholder"
        )));
    }
    match api_type {
        ApiType::Openai => Ok(std::sync::Arc::new(super::openai::OpenAiProvider::new(
            name, model, base_url, api_key, timeout,
        ))),
        ApiType::Anthropic => Err(ProviderError::Config(
            "anthropic dialect not implemented in v1".to_string(),
        )),
        ApiType::Google => Err(ProviderError::Config(
            "google dialect not implemented in v1".to_string(),
        )),
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn chunk_text_carries_text() {
        let chunk = super::Chunk::text("hi");
        assert_eq!(chunk.text, "hi");
        assert!(chunk.tool_calls.is_empty());
    }
}
