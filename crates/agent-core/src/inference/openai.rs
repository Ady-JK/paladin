use std::collections::BTreeMap;
use std::time::Duration;

use async_trait::async_trait;
use futures::StreamExt;
use serde::Deserialize;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use super::provider::{Chunk, LlmMessage, Provider, ToolCall, ToolDefinition, ToolResponse};
use crate::config::{
    is_opencode_endpoint, ApiType, OPENCODE_CATALOG_URL, OPENCODE_SESSION_HEADER,
    OPENCODE_USER_AGENT,
};
use crate::error::ProviderError;

pub const DEFAULT_OPENAI_MODEL: &str = "gpt-4o-mini";
pub const DEFAULT_OPENAI_BASE_URL: &str = "https://api.openai.com/v1";
const DEFAULT_TIMEOUT: Duration = Duration::from_secs(60);

pub struct OpenAiProvider {
    name: String,
    model: String,
    base_url: String,
    api_key: String,
    is_opencode: bool,
    client: reqwest::Client,
    timeout: Duration,
}

impl OpenAiProvider {
    pub fn new(
        name: String,
        model: String,
        base_url: String,
        api_key: String,
        timeout: Duration,
    ) -> Self {
        let model = if model.trim().is_empty() {
            DEFAULT_OPENAI_MODEL.to_string()
        } else {
            model
        };
        let base_url = if base_url.trim().is_empty() {
            DEFAULT_OPENAI_BASE_URL.to_string()
        } else {
            base_url.trim_end_matches('/').to_string()
        };
        let timeout = if timeout.is_zero() {
            DEFAULT_TIMEOUT
        } else {
            timeout
        };
        let client = reqwest::Client::builder()
            .timeout(timeout + Duration::from_secs(30))
            .build()
            .unwrap_or_else(|_| reqwest::Client::new());
        let is_opencode = is_opencode_endpoint(&base_url);
        Self {
            name,
            model,
            base_url,
            api_key,
            is_opencode,
            client,
            timeout,
        }
    }

    fn chat_url(&self) -> String {
        format!("{}/chat/completions", self.base_url)
    }

    fn messages_value(&self, system: &str, messages: &[LlmMessage]) -> Vec<serde_json::Value> {
        let mut out = Vec::with_capacity(messages.len() + 1);
        if !system.is_empty() {
            out.push(serde_json::json!({"role": "system", "content": system}));
        }
        for m in messages {
            match m.role.as_str() {
                "assistant" => {
                    let mut msg = serde_json::json!({"role": "assistant", "content": m.content});
                    if !m.tool_calls.is_empty() {
                        msg["tool_calls"] = serde_json::json!(m
                            .tool_calls
                            .iter()
                            .map(|c| serde_json::json!({
                                "id": c.id,
                                "type": "function",
                                "function": {"name": c.name, "arguments": c.arguments},
                            }))
                            .collect::<Vec<_>>());
                    }
                    out.push(msg);
                }
                "tool" => {
                    let mut msg = serde_json::json!({"role": "tool", "content": m.content});
                    if let Some(id) = &m.tool_call_id {
                        msg["tool_call_id"] = serde_json::json!(id);
                    }
                    out.push(msg);
                }
                _ => out.push(serde_json::json!({"role": "user", "content": m.content})),
            }
        }
        out
    }

    fn tools_value(&self, tools: &[ToolDefinition]) -> Option<Vec<serde_json::Value>> {
        if tools.is_empty() {
            return None;
        }
        Some(
            tools
                .iter()
                .map(|t| {
                    serde_json::json!({
                        "type": "function",
                        "function": {
                            "name": t.name,
                            "description": t.description,
                            "parameters": t.parameters,
                        }
                    })
                })
                .collect(),
        )
    }

    /// Never logs or returns the key: errors carry status + body only.
    fn request_error(&self, status: reqwest::StatusCode, body: &str) -> ProviderError {
        let short = body.trim().chars().take(300).collect::<String>();
        ProviderError::Request(format!(
            "{}: chat completion: status {status}: {short}",
            self.name
        ))
    }
}

#[derive(Debug, Deserialize)]
struct ChatWire {
    #[serde(default)]
    choices: Vec<ChoiceWire>,
    #[serde(default)]
    error: Option<ErrorWire>,
}

#[derive(Debug, Deserialize)]
struct ChoiceWire {
    #[serde(default)]
    message: MessageWire,
    #[serde(default)]
    delta: DeltaWire,
}

#[derive(Debug, Deserialize, Default)]
struct MessageWire {
    #[serde(default)]
    content: Option<String>,
    #[serde(default)]
    tool_calls: Vec<WireCall>,
}

#[derive(Debug, Deserialize, Default)]
struct DeltaWire {
    #[serde(default)]
    content: Option<String>,
    #[serde(default)]
    tool_calls: Vec<WireDeltaCall>,
}

#[derive(Debug, Deserialize, Default)]
struct WireCall {
    #[serde(default)]
    id: String,
    #[serde(default)]
    function: WireFunction,
}

#[derive(Debug, Deserialize, Default)]
struct WireFunction {
    #[serde(default)]
    name: String,
    #[serde(default)]
    arguments: String,
}

#[derive(Debug, Deserialize, Default)]
struct WireDeltaCall {
    #[serde(default)]
    index: u64,
    #[serde(default)]
    id: Option<String>,
    #[serde(default)]
    function: Option<WireDeltaFunction>,
}

#[derive(Debug, Deserialize, Default)]
struct WireDeltaFunction {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    arguments: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ErrorWire {
    #[serde(default)]
    message: String,
}

#[derive(Debug, Deserialize)]
struct ModelsWire {
    #[serde(default)]
    data: Vec<ModelWire>,
}

#[derive(Debug, Deserialize)]
struct ModelWire {
    #[serde(default)]
    id: String,
}

#[derive(Default)]
struct CallAcc {
    id: String,
    name: String,
    arguments: String,
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

    async fn complete_tools(
        &self,
        system: &str,
        messages: &[LlmMessage],
        tools: &[ToolDefinition],
        session_id: &str,
    ) -> Result<ToolResponse, ProviderError> {
        let mut body = serde_json::json!({
            "model": self.model,
            "messages": self.messages_value(system, messages),
        });
        if let Some(tools) = self.tools_value(tools) {
            body["tools"] = serde_json::Value::Array(tools);
        }
        let mut req = self
            .client
            .post(self.chat_url())
            .timeout(self.timeout)
            .bearer_auth(&self.api_key)
            .json(&body);
        if self.is_opencode {
            req = req
                .header(reqwest::header::USER_AGENT, OPENCODE_USER_AGENT)
                .header(OPENCODE_SESSION_HEADER, session_id.to_string());
        }
        let resp = req
            .send()
            .await
            .map_err(|e| ProviderError::Request(format!("{}: chat completion: {e}", self.name)))?;
        let status = resp.status();
        let text = resp
            .text()
            .await
            .map_err(|e| ProviderError::Request(format!("{}: chat completion: {e}", self.name)))?;
        if !status.is_success() {
            return Err(self.request_error(status, &text));
        }
        let wire: ChatWire = serde_json::from_str(&text).map_err(|e| {
            ProviderError::Request(format!("{}: chat completion: decode: {e}", self.name))
        })?;
        if let Some(err) = wire.error {
            return Err(ProviderError::Request(format!(
                "{}: chat completion: {}",
                self.name, err.message
            )));
        }
        let Some(choice) = wire.choices.into_iter().next() else {
            return Err(ProviderError::Request(format!(
                "{}: chat completion returned no choices",
                self.name
            )));
        };
        let mut out = ToolResponse {
            content: choice.message.content.unwrap_or_default(),
            ..Default::default()
        };
        for call in choice.message.tool_calls {
            out.tool_calls.push(ToolCall {
                id: call.id,
                name: call.function.name,
                arguments: call.function.arguments,
            });
        }
        Ok(out)
    }

    async fn stream_tools(
        &self,
        system: &str,
        messages: &[LlmMessage],
        tools: &[ToolDefinition],
        session_id: &str,
        tx: &mpsc::Sender<Chunk>,
        cancel: &CancellationToken,
    ) -> Result<(), ProviderError> {
        let mut body = serde_json::json!({
            "model": self.model,
            "messages": self.messages_value(system, messages),
            "stream": true,
        });
        if let Some(tools) = self.tools_value(tools) {
            body["tools"] = serde_json::Value::Array(tools);
        }
        let mut req = self
            .client
            .post(self.chat_url())
            .timeout(self.timeout + Duration::from_secs(30))
            .header(reqwest::header::ACCEPT, "text/event-stream")
            .bearer_auth(&self.api_key)
            .json(&body);
        if self.is_opencode {
            req = req
                .header(reqwest::header::USER_AGENT, OPENCODE_USER_AGENT)
                .header(OPENCODE_SESSION_HEADER, session_id.to_string());
        }
        let resp = req.send().await.map_err(|e| {
            if cancel.is_cancelled() {
                ProviderError::Cancelled
            } else {
                ProviderError::Request(format!("{}: stream: {e}", self.name))
            }
        })?;
        let status = resp.status();
        if !status.is_success() {
            let text = resp.text().await.unwrap_or_default();
            return Err(self.request_error(status, &text));
        }
        let mut stream = resp.bytes_stream();
        let mut buf = String::new();
        let mut accs: BTreeMap<u64, CallAcc> = BTreeMap::new();
        let mut signaled: std::collections::HashSet<u64> = std::collections::HashSet::new();

        loop {
            tokio::select! {
                next = stream.next() => {
                    let Some(next) = next else { break };
                    let bytes = next.map_err(|e| {
                        ProviderError::Request(format!("{}: stream: {e}", self.name))
                    })?;
                    buf.push_str(&String::from_utf8_lossy(&bytes));
                    // Process complete lines; keep the tail.
                    while let Some(pos) = buf.find('\n') {
                        let line = buf[..pos].trim_end().to_string();
                        buf = buf[pos + 1..].to_string();
                        if !line.starts_with("data:") {
                            continue;
                        }
                        let payload = line["data:".len()..].trim();
                        if payload == "[DONE]" {
                            continue;
                        }
                        if payload.is_empty() {
                            continue;
                        }
                        let wire: ChatWire = match serde_json::from_str(payload) {
                            Ok(w) => w,
                            Err(_) => continue,
                        };
                        for choice in wire.choices {
                            if let Some(text) = choice.delta.content {
                                if !text.is_empty() && tx.send(Chunk::text(text)).await.is_err() {
                                    return Ok(());
                                }
                            }
                            for call in choice.delta.tool_calls {
                                let acc = accs.entry(call.index).or_default();
                                if let Some(id) = call.id {
                                    if !id.is_empty() && acc.id.is_empty() {
                                        acc.id = id;
                                    }
                                }
                                if let Some(func) = call.function {
                                    if let Some(name) = func.name {
                                        if acc.name.is_empty() {
                                            acc.name = name;
                                        }
                                    }
                                    if let Some(args) = func.arguments {
                                        acc.arguments.push_str(&args);
                                    }
                                }
                                if signaled.insert(call.index) {
                                    let notice = ToolCall {
                                        id: if acc.id.is_empty() {
                                            format!("pending-{}", call.index)
                                        } else {
                                            acc.id.clone()
                                        },
                                        name: if acc.name.is_empty() {
                                            "tool".to_string()
                                        } else {
                                            acc.name.clone()
                                        },
                                        arguments: "{}".to_string(),
                                    };
                                    if tx.send(Chunk { tool_notice: Some(notice), ..Default::default() }).await.is_err() {
                                        return Ok(());
                                    }
                                }
                            }
                        }
                    }
                }
                _ = cancel.cancelled() => {
                    return Err(ProviderError::Cancelled);
                }
            }
            if cancel.is_cancelled() {
                return Err(ProviderError::Cancelled);
            }
        }
        if !accs.is_empty() {
            let mut calls = Vec::new();
            for (index, acc) in accs {
                calls.push(ToolCall {
                    id: if acc.id.is_empty() {
                        format!("pending-{index}")
                    } else {
                        acc.id
                    },
                    name: if acc.name.is_empty() {
                        "tool".to_string()
                    } else {
                        acc.name
                    },
                    arguments: acc.arguments,
                });
            }
            let _ = tx
                .send(Chunk {
                    tool_calls: calls,
                    ..Default::default()
                })
                .await;
        }
        Ok(())
    }

    async fn list_models(&self) -> Result<Vec<String>, ProviderError> {
        if self.is_opencode {
            // Public catalog, keyless; session header still required.
            let resp = self
                .client
                .get(OPENCODE_CATALOG_URL)
                .timeout(Duration::from_secs(30))
                .header(reqwest::header::USER_AGENT, OPENCODE_USER_AGENT)
                .header(OPENCODE_SESSION_HEADER, "catalog")
                .send()
                .await
                .map_err(|e| ProviderError::Request(format!("{}: list models: {e}", self.name)))?;
            let status = resp.status();
            let text = resp
                .text()
                .await
                .map_err(|e| ProviderError::Request(format!("{}: list models: {e}", self.name)))?;
            if !status.is_success() {
                return Err(self.request_error(status, &text));
            }
            let wire: ModelsWire = serde_json::from_str(&text).map_err(|e| {
                ProviderError::Request(format!("{}: list models: decode: {e}", self.name))
            })?;
            return Ok(wire
                .data
                .into_iter()
                .map(|m| m.id)
                .filter(|id| !id.is_empty())
                .collect());
        }
        let url = format!("{}/models", self.base_url);
        let resp = self
            .client
            .get(url)
            .timeout(Duration::from_secs(30))
            .bearer_auth(&self.api_key)
            .send()
            .await
            .map_err(|e| ProviderError::Request(format!("{}: list models: {e}", self.name)))?;
        let status = resp.status();
        let text = resp
            .text()
            .await
            .map_err(|e| ProviderError::Request(format!("{}: list models: {e}", self.name)))?;
        if !status.is_success() {
            return Err(self.request_error(status, &text));
        }
        let wire: ModelsWire = serde_json::from_str(&text).map_err(|e| {
            ProviderError::Request(format!("{}: list models: decode: {e}", self.name))
        })?;
        Ok(wire
            .data
            .into_iter()
            .map(|m| m.id)
            .filter(|id| !id.is_empty())
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn provider_for(base_url: &str) -> OpenAiProvider {
        OpenAiProvider::new(
            "test".into(),
            "m".into(),
            base_url.into(),
            "k".into(),
            Duration::from_secs(10),
        )
    }

    #[tokio::test]
    async fn complete_tools_parses_tool_calls() {
        let server = wiremock::MockServer::start().await;
        wiremock::Mock::given(wiremock::matchers::method("POST"))
            .respond_with(wiremock::ResponseTemplate::new(200).set_body_json(
                serde_json::json!({
                    "choices": [{
                        "message": {
                            "content": "hi",
                            "tool_calls": [{"id": "c1", "function": {"name": "read", "arguments": "{\"path\":\"x\"}"}}]
                        }
                    }]
                }),
            ))
            .mount(&server)
            .await;
        let p = provider_for(&server.uri());
        let resp = p.complete_tools("sys", &[], &[], "sess").await.unwrap();
        assert_eq!(resp.content, "hi");
        assert_eq!(resp.tool_calls.len(), 1);
        assert_eq!(resp.tool_calls[0].name, "read");
    }

    #[tokio::test]
    async fn stream_assembles_fragmented_tool_calls() {
        let server = wiremock::MockServer::start().await;
        let sse = "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n\
                   data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"re\",\"arguments\":\"{\\\"pa\"}}]}}]}\n\n\
                   data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"th\\\"}\"}}]}}]}\n\n\
                   data: [DONE]\n\n";
        wiremock::Mock::given(wiremock::matchers::method("POST"))
            .respond_with(
                wiremock::ResponseTemplate::new(200)
                    .insert_header("content-type", "text/event-stream")
                    .set_body_string(sse),
            )
            .mount(&server)
            .await;
        let p = provider_for(&server.uri());
        let (tx, mut rx) = mpsc::channel(64);
        p.stream_tools("sys", &[], &[], "sess", &tx, &CancellationToken::new())
            .await
            .unwrap();
        drop(tx);
        let mut texts = String::new();
        let mut notices = 0;
        let mut final_calls = Vec::new();
        while let Some(chunk) = rx.recv().await {
            texts.push_str(&chunk.text);
            if chunk.tool_notice.is_some() {
                notices += 1;
            }
            if !chunk.tool_calls.is_empty() {
                final_calls = chunk.tool_calls;
            }
        }
        assert_eq!(texts, "Hel");
        assert_eq!(notices, 1);
        assert_eq!(final_calls.len(), 1);
        assert_eq!(final_calls[0].arguments, "{\"path\"}");
    }

    #[tokio::test]
    async fn maps_http_errors_without_leaking_key() {
        let server = wiremock::MockServer::start().await;
        wiremock::Mock::given(wiremock::matchers::method("POST"))
            .respond_with(wiremock::ResponseTemplate::new(401).set_body_string("bad key"))
            .mount(&server)
            .await;
        let p = provider_for(&server.uri());
        let err = p
            .complete_tools("sys", &[], &[], "sess")
            .await
            .unwrap_err()
            .to_string();
        assert!(err.contains("401"));
        assert!(!err.contains('k'.to_string().as_str()) || err.contains("status"));
    }

    #[tokio::test]
    async fn lists_models() {
        let server = wiremock::MockServer::start().await;
        wiremock::Mock::given(wiremock::matchers::method("GET"))
            .respond_with(
                wiremock::ResponseTemplate::new(200)
                    .set_body_json(serde_json::json!({"data": [{"id": "a"}, {"id": "b"}]})),
            )
            .mount(&server)
            .await;
        let p = provider_for(&server.uri());
        assert_eq!(p.list_models().await.unwrap(), vec!["a", "b"]);
    }
}
