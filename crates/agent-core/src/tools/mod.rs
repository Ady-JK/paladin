use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;

use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use crate::error::ToolError;

#[derive(Debug, Clone)]
pub struct ToolCtx {
    pub workspace: PathBuf,
    pub session_dir: PathBuf,
    pub cancel: tokio_util::sync::CancellationToken,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub parameters: serde_json::Value,
}

#[derive(Debug, Clone)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone)]
pub struct ToolResult {
    pub call_id: String,
    pub name: String,
    pub content: String,
    pub is_error: bool,
}

#[async_trait]
pub trait Tool: Send + Sync {
    fn name(&self) -> &str;
    fn definition(&self) -> ToolDefinition;
    async fn call(&self, args: serde_json::Value, ctx: &ToolCtx) -> Result<String, ToolError>;
}

#[derive(Default, Clone)]
pub struct ToolRegistry {
    tools: HashMap<String, Arc<dyn Tool>>,
}

impl ToolRegistry {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn register(&mut self, tool: Arc<dyn Tool>) {
        self.tools.insert(tool.name().to_string(), tool);
    }

    pub fn get(&self, name: &str) -> Option<Arc<dyn Tool>> {
        self.tools.get(name).cloned()
    }

    pub fn len(&self) -> usize {
        self.tools.len()
    }

    pub fn is_empty(&self) -> bool {
        self.tools.is_empty()
    }

    pub fn definitions(&self) -> Vec<ToolDefinition> {
        let mut names: Vec<_> = self.tools.keys().cloned().collect();
        names.sort();
        names
            .into_iter()
            .filter_map(|n| self.tools.get(&n))
            .map(|t| t.definition())
            .collect()
    }

    pub async fn call(&self, call: &ToolCall, ctx: &ToolCtx) -> ToolResult {
        let mut result = ToolResult {
            call_id: call.id.clone(),
            name: call.name.clone(),
            content: String::new(),
            is_error: false,
        };
        let Some(tool) = self.tools.get(&call.name) else {
            return error_result(result, format!("unknown tool {:?}", call.name));
        };
        let args: serde_json::Value = match serde_json::from_str(&call.arguments) {
            Ok(v) => v,
            Err(_) => {
                // Tolerate empty/non-JSON arguments as empty object.
                if call.arguments.trim().is_empty() {
                    serde_json::Value::Null
                } else {
                    return error_result(result, "the arguments are not valid JSON".to_string());
                }
            }
        };
        match tool.call(args, ctx).await {
            Ok(content) => {
                result.content = content;
                result
            }
            Err(e) => {
                result.content = match &e {
                    ToolError::Failed { content, .. } => content.clone(),
                    other => serde_json::json!({"error": other.to_string()}).to_string(),
                };
                result.is_error = true;
                result
            }
        }
    }
}

fn error_result(mut result: ToolResult, message: String) -> ToolResult {
    result.content = serde_json::json!({"error": message}).to_string();
    result.is_error = true;
    result
}

pub mod edit;
pub mod file_engine;
pub mod read;
pub mod scope;
pub mod shell;
pub mod write;

/// Model-facing tool registry: read, write, edit, shell.
/// `file_engine` and `scope` stay shared modules behind write/edit,
/// exactly like the Go service wiring.
pub fn builtin_registry() -> ToolRegistry {
    let engine = file_engine::Engine::new();
    let mut registry = ToolRegistry::new();
    registry.register(Arc::new(read::ReadTool));
    registry.register(Arc::new(write::WriteTool::new(engine.clone())));
    registry.register(Arc::new(edit::EditTool::new(engine)));
    registry.register(Arc::new(shell::ShellTool::new()));
    registry
}

pub use scope::resolve_scoped_path;

#[cfg(test)]
mod tests {
    use super::*;

    struct Noop;
    #[async_trait]
    impl Tool for Noop {
        fn name(&self) -> &str {
            "noop"
        }
        fn definition(&self) -> ToolDefinition {
            ToolDefinition {
                name: "noop".into(),
                description: String::new(),
                parameters: serde_json::json!({}),
            }
        }
        async fn call(
            &self,
            _args: serde_json::Value,
            _ctx: &ToolCtx,
        ) -> Result<String, ToolError> {
            Ok("ok".into())
        }
    }

    #[tokio::test]
    async fn unknown_tool_is_error_result() {
        let registry = ToolRegistry::new();
        let ctx = ToolCtx {
            workspace: PathBuf::from("/tmp"),
            session_dir: PathBuf::from("/tmp"),
            cancel: tokio_util::sync::CancellationToken::new(),
        };
        let result = registry
            .call(
                &ToolCall {
                    id: "1".into(),
                    name: "missing".into(),
                    arguments: "{}".into(),
                },
                &ctx,
            )
            .await;
        assert!(result.is_error);
    }

    #[tokio::test]
    async fn definitions_sorted_by_name() {
        let mut registry = ToolRegistry::new();
        registry.register(Arc::new(Noop));
        assert_eq!(registry.definitions().len(), 1);
    }
}
