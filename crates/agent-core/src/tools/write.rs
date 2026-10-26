use async_trait::async_trait;
use serde::Deserialize;

use super::file_engine::{Engine, EngineError};
use super::{Tool, ToolCtx, ToolDefinition};
use crate::error::ToolError;

#[derive(Debug, Clone)]
pub struct WriteTool {
    pub engine: Engine,
}

impl WriteTool {
    pub fn new(engine: Engine) -> Self {
        Self { engine }
    }
}

#[derive(Debug, Deserialize)]
struct WriteArgs {
    #[serde(default)]
    path: String,
    #[serde(default)]
    content: String,
    #[serde(default)]
    overwrite: bool,
}

#[async_trait]
impl Tool for WriteTool {
    fn name(&self) -> &str {
        "write"
    }

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: "write".to_string(),
            description: "Create a file with the given content. Pass the path as \
                workspace/<path> for a file inside the workspace or as a full \
                absolute path elsewhere; bare relative paths are rejected. \
                Missing parent directories are created. \
                When the file already exists the call fails unless overwrite is true, \
                in which case the file is replaced wholesale. Writes outside the \
                workspace are allowed but reported as outside. Read the file first \
                when replacing one, and prefer edit for small changes to an existing file."
                .to_string(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "The file to create: workspace/<path> inside the workspace, or a full absolute path outside it."},
                    "content": {"type": "string", "description": "The full content to write into the file."},
                    "overwrite": {"type": "boolean", "description": "Replace the file when it already exists. Defaults to false."}
                },
                "required": ["path", "content"]
            }),
        }
    }

    async fn call(&self, args: serde_json::Value, ctx: &ToolCtx) -> Result<String, ToolError> {
        let args: WriteArgs = serde_json::from_value(args).map_err(|e| {
            ToolError::args(format!("write: the arguments are not valid JSON: {e}"))
        })?;
        if args.path.trim().is_empty() {
            return Err(ToolError::args("write: path is required"));
        }
        match self
            .engine
            .write(&ctx.workspace, &args.path, &args.content, args.overwrite)
        {
            Ok(res) => serde_json::to_string(&res)
                .map_err(|e| ToolError::args(format!("write: encode the result: {e}"))),
            Err(EngineError::Exists {
                requested_path,
                resolved_path,
                scope,
            }) => {
                let msg = format!(
                    "write: path {requested_path:?} already exists; pass overwrite to replace it"
                );
                let content = serde_json::json!({
                    "error": msg,
                    "requested_path": requested_path,
                    "resolved_path": resolved_path,
                    "scope": scope,
                    "exists": true,
                })
                .to_string();
                Err(ToolError::with_content(content, msg))
            }
            Err(e) => Err(ToolError::args(e.to_string())),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn refuses_clobber_without_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::create_dir_all(&ws).unwrap();
        let ctx = ToolCtx {
            workspace: ws.clone(),
            session_dir: dir.path().to_path_buf(),
            cancel: tokio_util::sync::CancellationToken::new(),
        };
        let tool = WriteTool::new(Engine::new());
        tool.call(
            serde_json::json!({"path": "workspace/a.txt", "content": "a"}),
            &ctx,
        )
        .await
        .unwrap();
        let err = tool
            .call(
                serde_json::json!({"path": "workspace/a.txt", "content": "b"}),
                &ctx,
            )
            .await
            .unwrap_err();
        assert!(err.to_string().contains("already exists"));
        assert!(err.content().contains("\"exists\":true"));
    }
}
