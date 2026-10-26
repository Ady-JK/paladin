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
}

#[async_trait]
pub trait Tool: Send + Sync {
    fn name(&self) -> &str;
    fn schema(&self) -> serde_json::Value;
    async fn run(
        &self,
        args: serde_json::Value,
        ctx: &ToolCtx,
    ) -> Result<serde_json::Value, ToolError>;
}

#[derive(Default)]
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

    pub fn definitions(&self) -> Vec<ToolDefinition> {
        let mut names: Vec<_> = self.tools.keys().cloned().collect();
        names.sort();
        names
            .into_iter()
            .filter_map(|n| self.tools.get(&n))
            .map(|t| ToolDefinition {
                name: t.name().to_string(),
                description: String::new(),
                parameters: t.schema(),
            })
            .collect()
    }
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolResult {
    pub call_id: String,
    pub name: String,
    pub content: String,
    pub is_error: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Scope {
    pub workspace: PathBuf,
    pub session_dir: PathBuf,
}

pub const SCOPE_INSIDE: &str = "inside";
pub const SCOPE_OUTSIDE: &str = "outside";

pub mod edit;
pub mod file_engine;
pub mod read;
pub mod scope;
pub mod shell;
pub mod write;

pub fn builtin_registry() -> ToolRegistry {
    let mut registry = ToolRegistry::new();
    registry.register(Arc::new(read::ReadTool));
    registry.register(Arc::new(write::WriteTool));
    registry.register(Arc::new(edit::EditTool));
    registry.register(Arc::new(shell::ShellTool));
    registry.register(Arc::new(file_engine::FileEngineTool));
    registry.register(Arc::new(scope::ScopeTool));
    registry
}
