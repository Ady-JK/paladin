use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use super::file_engine::Engine;
use super::{Tool, ToolCtx, ToolDefinition};
use crate::error::ToolError;

pub const DEFAULT_READ_LIMIT: usize = 2000;
pub const MAX_READ_BYTES: usize = 50 * 1024;
const MAX_LINE_RUNES: usize = 2000;
const MAX_LINE_BYTES: usize = MAX_LINE_RUNES * 4;
const TRUNCATED_MARKER: &str = " … [truncated]";
const HEAD_BYTES: usize = 8 * 1024;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReadResponse {
    pub requested_path: String,
    pub resolved_path: String,
    pub scope: String,
    pub content: String,
    pub start_line: usize,
    pub end_line: usize,
    pub has_more: bool,
    pub stop_reason: String,
}

#[derive(Debug, Clone)]
pub struct ReadTool;

#[derive(Debug, Deserialize)]
struct ReadArgs {
    #[serde(default)]
    path: String,
    #[serde(default)]
    offset: i64,
    #[serde(default)]
    limit: i64,
}

#[async_trait]
impl Tool for ReadTool {
    fn name(&self) -> &str {
        "read"
    }

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: "read".to_string(),
            description: "Read a text file from disk. Pass workspace/<path> for a file \
                inside the workspace, or a full absolute path for a file outside it. \
                The reply holds the file content, the line range that \
                was returned, and whether more lines follow: continue with a larger offset \
                when it does. Reads outside the workspace are allowed but reported as outside. \
                Use read to inspect a file before describing or changing it."
                .to_string(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "The file to read: workspace/<path> for a file inside the workspace, or a full absolute path for a file outside it."},
                    "offset": {"type": "integer", "description": "The first line to read, 1-based. Defaults to 1.", "minimum": 1},
                    "limit": {"type": "integer", "description": "How many lines to read. Defaults to 2000; the content is also capped at 50 KB.", "minimum": 1}
                },
                "required": ["path"]
            }),
        }
    }

    async fn call(&self, args: serde_json::Value, ctx: &ToolCtx) -> Result<String, ToolError> {
        let args: ReadArgs = serde_json::from_value(args)
            .map_err(|e| ToolError::args(format!("read: the arguments are not valid JSON: {e}")))?;
        if args.path.trim().is_empty() {
            return Err(ToolError::args("read: path is required"));
        }
        if args.offset < 0 {
            return Err(ToolError::args(
                "read: offset is 1-based and cannot be negative",
            ));
        }
        if args.limit < 0 {
            return Err(ToolError::args("read: limit cannot be negative"));
        }
        let offset = if args.offset == 0 {
            1
        } else {
            args.offset as usize
        };
        let limit = if args.limit == 0 {
            DEFAULT_READ_LIMIT
        } else {
            args.limit as usize
        };
        let (resolved, scope) =
            super::scope::resolve_scoped_path("read", "path", &args.path, &ctx.workspace)
                .map_err(|e| ToolError::args(e.to_string()))?;
        let scope_name = scope.to_string();
        let info = std::fs::metadata(&resolved).map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                ToolError::args(format!("read: path {:?} does not exist", args.path))
            } else if e.kind() == std::io::ErrorKind::PermissionDenied {
                ToolError::args(format!(
                    "read: path {:?} cannot be read: permission denied",
                    args.path
                ))
            } else {
                ToolError::args(format!("read: path {:?} cannot be read: {e}", args.path))
            }
        })?;
        if info.is_dir() {
            return Err(ToolError::args(format!(
                "read: path {:?} is a directory, not a file",
                args.path
            )));
        }
        if !info.file_type().is_file() {
            return Err(ToolError::args(format!(
                "read: path {:?} is not a regular file",
                args.path
            )));
        }
        let (content, start, end, has_more, reason) =
            read_range(&resolved, offset, limit).map_err(ToolError::args)?;
        let out = ReadResponse {
            requested_path: args.path.clone(),
            resolved_path: resolved.display().to_string(),
            scope: scope_name,
            content,
            start_line: start,
            end_line: end,
            has_more,
            stop_reason: reason,
        };
        serde_json::to_string(&out)
            .map_err(|e| ToolError::args(format!("read: encode the result: {e}")))
    }
}

fn read_range(
    path: &std::path::Path,
    offset: usize,
    limit: usize,
) -> Result<(String, usize, usize, bool, String), String> {
    use std::io::{BufRead, Read};
    let mut file = std::fs::File::open(path).map_err(|e| format!("read: {e}"))?;
    let mut head = vec![0u8; HEAD_BYTES];
    let mut n = 0;
    while n < HEAD_BYTES {
        match file.read(&mut head[n..]) {
            Ok(0) => break,
            Ok(k) => n += k,
            Err(e) => return Err(format!("read: {e}")),
        }
    }
    head.truncate(n);
    if head.contains(&0) {
        return Err("read: the file is binary, not text".to_string());
    }
    let chained: Vec<u8> = head.clone();
    let rest = file;
    let reader = std::io::BufReader::with_capacity(64 * 1024, chained.chain(rest));
    let mut content = String::new();
    let mut line_no = 0usize;
    let (mut start, mut end) = (0usize, 0usize);
    let mut has_more = false;
    let mut reason = "ok".to_string();
    for line in reader.split(b'\n') {
        let line = line.map_err(|e| format!("read: {e}"))?;
        line_no += 1;
        if line_no < offset {
            continue;
        }
        let mut text = String::from_utf8_lossy(&line).into_owned();
        let mut cut = false;
        if text.len() > MAX_LINE_BYTES {
            text = String::from_utf8_lossy(truncate_bytes(&line, MAX_LINE_BYTES)).into_owned();
            cut = true;
        }
        text = text.trim_end_matches('\r').to_string();
        let capped = cap_runes(&text);
        if capped.1 || cut {
            text = capped.0 + TRUNCATED_MARKER;
        }
        if start > 0 {
            if end - start + 1 >= limit {
                has_more = true;
                reason = "line_limit".to_string();
                break;
            }
            if content.len() + text.len() + 1 > MAX_READ_BYTES {
                has_more = true;
                reason = "byte_limit".to_string();
                break;
            }
        }
        if start == 0 {
            start = line_no;
        }
        content.push_str(&text);
        content.push('\n');
        end = line_no;
    }
    Ok((content, start, end, has_more, reason))
}

fn truncate_bytes(line: &[u8], max: usize) -> &[u8] {
    if line.len() <= max {
        return line;
    }
    // Cut at a UTF-8 boundary at or before max.
    let mut end = max;
    while end > 0
        && !line
            .get(..end)
            .is_some_and(|s| std::str::from_utf8(s).is_ok())
    {
        end -= 1;
    }
    &line[..end]
}

fn cap_runes(line: &str) -> (String, bool) {
    if line.chars().count() <= MAX_LINE_RUNES {
        return (line.to_string(), false);
    }
    (line.chars().take(MAX_LINE_RUNES).collect(), true)
}

#[allow(dead_code)]
fn _use_engine(_: &Engine) {}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx() -> ToolCtx {
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::create_dir_all(&ws).unwrap();
        // Leak the tempdir so workspace outlives the context in tests.
        let leaked = Box::leak(Box::new(dir));
        ToolCtx {
            workspace: leaked.path().join("ws"),
            session_dir: leaked.path().join("sess"),
            cancel: tokio_util::sync::CancellationToken::new(),
        }
    }

    #[tokio::test]
    async fn reads_with_offset_and_limit() {
        let ctx = ctx();
        std::fs::write(ctx.workspace.join("a.txt"), "l1\nl2\nl3\n").unwrap();
        let tool = ReadTool;
        let out: ReadResponse = serde_json::from_str(
            &tool
                .call(
                    serde_json::json!({"path": "workspace/a.txt", "offset": 2, "limit": 1}),
                    &ctx,
                )
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(out.content, "l2\n");
        assert!(out.has_more);
        assert_eq!(out.stop_reason, "line_limit");
        let _ = "x";
    }

    #[tokio::test]
    async fn refuses_binary_files() {
        let ctx = ctx();
        std::fs::write(ctx.workspace.join("b.bin"), [0u8, 1, 2]).unwrap();
        let tool = ReadTool;
        assert!(tool
            .call(serde_json::json!({"path": "workspace/b.bin"}), &ctx)
            .await
            .is_err());
    }

    #[tokio::test]
    async fn rejects_bare_relative_paths() {
        let ctx = ctx();
        let tool = ReadTool;
        assert!(tool
            .call(serde_json::json!({"path": "a.txt"}), &ctx)
            .await
            .is_err());
        let _ = "x";
    }
}
