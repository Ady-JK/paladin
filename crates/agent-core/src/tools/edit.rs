use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use super::file_engine::{ContextLine, Engine, EngineError, MatchSpan};
use super::{Tool, ToolCtx, ToolDefinition};
use crate::error::ToolError;

#[derive(Debug, Clone)]
pub struct EditTool {
    pub engine: Engine,
}

impl EditTool {
    pub fn new(engine: Engine) -> Self {
        Self { engine }
    }
}

#[derive(Debug, Deserialize)]
struct EditArgs {
    #[serde(default)]
    path: String,
    #[serde(default)]
    old_string: String,
    #[serde(default)]
    new_string: String,
    #[serde(default)]
    replace_all: bool,
}

#[derive(Debug, Serialize)]
struct EditResponse {
    requested_path: String,
    resolved_path: String,
    scope: String,
    replacements: usize,
    bytes: usize,
    lines: usize,
    match_start_line: usize,
    match_end_line: usize,
    matches: Vec<MatchSpan>,
}

#[derive(Debug, Serialize)]
struct WireContextLine {
    line: usize,
    text: String,
}

#[derive(Debug, Serialize)]
struct OccurrencePayload {
    occurrence: usize,
    start_line: usize,
    end_line: usize,
    before: Option<WireContextLine>,
    after: Option<WireContextLine>,
}

fn convert_line(line: &Option<ContextLine>) -> Option<WireContextLine> {
    line.as_ref().map(|l| WireContextLine {
        line: l.line,
        text: l.text.clone(),
    })
}

#[async_trait]
impl Tool for EditTool {
    fn name(&self) -> &str {
        "edit"
    }

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: "edit".to_string(),
            description: "Replace text verbatim in an existing file. Pass the path as \
                workspace/<path> for a file inside the workspace or as a full \
                absolute path elsewhere; bare relative paths are rejected. \
                old_string must match \
                exactly as it appears in the file, so read the file first. When \
                old_string occurs more than once the call fails and lists every \
                occurrence with its line numbers and the nearest non-blank lines \
                above and below: retry with more surrounding lines in old_string so \
                it matches exactly once, or pass replace_all true to replace every \
                occurrence. Edits outside the workspace are allowed but reported as outside."
                .to_string(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "The file to edit: workspace/<path> inside the workspace, or a full absolute path outside it."},
                    "old_string": {"type": "string", "description": "The exact text to find in the file. Must match verbatim, including line endings."},
                    "new_string": {"type": "string", "description": "The replacement text. May be empty to delete old_string."},
                    "replace_all": {"type": "boolean", "description": "Replace every occurrence of old_string. Defaults to false, which requires exactly one match."}
                },
                "required": ["path", "old_string", "new_string"]
            }),
        }
    }

    async fn call(&self, args: serde_json::Value, ctx: &ToolCtx) -> Result<String, ToolError> {
        let args: EditArgs = serde_json::from_value(args)
            .map_err(|e| ToolError::args(format!("edit: the arguments are not valid JSON: {e}")))?;
        if args.path.trim().is_empty() {
            return Err(ToolError::args("edit: path is required"));
        }
        if args.old_string.is_empty() {
            return Err(ToolError::args(
                "edit: old_string is required and cannot be empty",
            ));
        }
        match self.engine.edit(
            &ctx.workspace,
            &args.path,
            &args.old_string,
            &args.new_string,
            args.replace_all,
        ) {
            Ok(res) => serde_json::to_string(&EditResponse {
                requested_path: res.requested_path,
                resolved_path: res.resolved_path,
                scope: res.scope,
                replacements: res.replacements,
                bytes: res.bytes,
                lines: res.lines,
                match_start_line: res.match_start_line,
                match_end_line: res.match_end_line,
                matches: res.matches,
            })
            .map_err(|e| ToolError::args(format!("edit: encode the result: {e}"))),
            Err(EngineError::NotFound {
                requested_path,
                resolved_path,
                scope,
            }) => {
                let content = serde_json::json!({
                    "error": "edit: old text not found in file; read the file and copy the exact text",
                    "requested_path": requested_path,
                    "resolved_path": resolved_path,
                    "scope": scope,
                })
                .to_string();
                Err(ToolError::with_content(
                    content,
                    "edit: old text not found in file; read the file and copy the exact text",
                ))
            }
            Err(EngineError::Ambiguous {
                requested_path,
                resolved_path,
                scope,
                count,
                occurrences,
            }) => {
                let occ: Vec<OccurrencePayload> = occurrences
                    .iter()
                    .map(|o| OccurrencePayload {
                        occurrence: o.occurrence,
                        start_line: o.start_line,
                        end_line: o.end_line,
                        before: convert_line(&o.before),
                        after: convert_line(&o.after),
                    })
                    .collect();
                let msg = format!(
                    "edit: old text occurs {count} times; retry with more context around the one you want, or replace all"
                );
                let content = serde_json::json!({
                    "error": msg,
                    "requested_path": requested_path,
                    "resolved_path": resolved_path,
                    "scope": scope,
                    "count": count,
                    "occurrences": occ,
                    "hint": "copy more surrounding lines into old_string so it matches exactly once, or pass replace_all true",
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
    async fn requires_old_string() {
        let dir = tempfile::tempdir().unwrap();
        let ctx = ToolCtx {
            workspace: dir.path().to_path_buf(),
            session_dir: dir.path().to_path_buf(),
            cancel: tokio_util::sync::CancellationToken::new(),
        };
        let tool = EditTool::new(Engine::new());
        assert!(tool
            .call(
                serde_json::json!({"path": "workspace/a.txt", "old_string": "", "new_string": "b"}),
                &ctx
            )
            .await
            .is_err());
    }
}
