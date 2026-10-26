use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use crate::config::ApiType;
use crate::error::ProviderError;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub role: String,
    pub content: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub parameters: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, Default)]
pub struct Chunk {
    pub text: String,
    pub tool_calls: Vec<ToolCall>,
}

#[async_trait]
pub trait Provider: Send + Sync {
    fn name(&self) -> &str;
    fn api_type(&self) -> ApiType;
    fn model(&self) -> &str;
    async fn complete(
        &self,
        system: &str,
        history: &[Message],
        prompt: &str,
    ) -> Result<String, ProviderError>;
}

pub fn factory(api_type: ApiType) -> &'static str {
    match api_type {
        ApiType::Openai => "openai-compatible",
        ApiType::Anthropic => "anthropic",
        ApiType::Google => "google",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn factory_names_openai_dialect() {
        assert_eq!(factory(ApiType::Openai), "openai-compatible");
    }
}
