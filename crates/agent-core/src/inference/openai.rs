use async_trait::async_trait;

use super::provider::{Message, Provider};
use crate::config::ApiType;
use crate::error::ProviderError;

pub const DEFAULT_OPENAI_MODEL: &str = "gpt-4o-mini";
pub const DEFAULT_OPENAI_BASE_URL: &str = "https://api.openai.com/v1";

pub struct OpenAiProvider {
    name: String,
    model: String,
    #[allow(dead_code)]
    base_url: String,
    #[allow(dead_code)]
    api_key: String,
    #[allow(dead_code)]
    client: reqwest::Client,
}

impl OpenAiProvider {
    pub fn new(name: String, model: String, base_url: String, api_key: String) -> Self {
        Self {
            name,
            model: if model.is_empty() {
                DEFAULT_OPENAI_MODEL.to_string()
            } else {
                model
            },
            base_url: if base_url.is_empty() {
                DEFAULT_OPENAI_BASE_URL.to_string()
            } else {
                base_url
            },
            api_key,
            client: reqwest::Client::new(),
        }
    }
}

#[async_trait]
impl Provider for OpenAiProvider {
    fn name(&self) -> &str {
        &self.name
    }

    fn api_type(&self) -> ApiType {
        ApiType::Openai
    }

    fn model(&self) -> &str {
        &self.model
    }

    async fn complete(
        &self,
        _system: &str,
        _history: &[Message],
        _prompt: &str,
    ) -> Result<String, ProviderError> {
        // Streaming chat completions land in a later phase (plan §10 step 5).
        Err(ProviderError::Request(
            "not implemented in scaffold".to_string(),
        ))
    }
}
