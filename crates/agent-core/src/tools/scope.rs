use super::{Tool, ToolCtx};
use crate::error::ToolError;
use async_trait::async_trait;

pub struct ScopeTool;

#[async_trait]
impl Tool for ScopeTool {
    fn name(&self) -> &str {
        "scope"
    }

    fn schema(&self) -> serde_json::Value {
        serde_json::json!({"type": "object"})
    }

    async fn run(
        &self,
        _args: serde_json::Value,
        _ctx: &ToolCtx,
    ) -> Result<serde_json::Value, ToolError> {
        Err(ToolError::Exec("not implemented in scaffold".to_string()))
    }
}
