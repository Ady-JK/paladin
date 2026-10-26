use std::path::PathBuf;

use serde::{Deserialize, Serialize};

use super::scope::resolve_scoped_path;
use super::{Tool, ToolCtx};
use crate::error::ToolError;

pub const MAX_FILE_BYTES: u64 = 10 << 20;

#[derive(Debug, Clone)]
pub struct Engine;

impl Engine {
    pub fn new() -> Self {
        Self
    }
}

impl Default for Engine {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct WriteResult {
    pub requested_path: String,
    pub resolved_path: String,
    pub scope: String,
    pub created: bool,
    pub overwritten: bool,
    pub bytes: usize,
    pub lines: usize,
}

#[derive(Debug, Clone, Serialize)]
pub struct EditResult {
    pub requested_path: String,
    pub resolved_path: String,
    pub scope: String,
    pub replacements: usize,
    pub bytes: usize,
    pub lines: usize,
    pub match_start_line: usize,
    pub match_end_line: usize,
    pub matches: Vec<MatchSpan>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MatchSpan {
    pub start_line: usize,
    pub end_line: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextLine {
    pub line: usize,
    pub text: String,
}

#[derive(Debug, Clone)]
pub struct Occurrence {
    pub occurrence: usize,
    pub start_line: usize,
    pub end_line: usize,
    pub before: Option<ContextLine>,
    pub after: Option<ContextLine>,
}

#[derive(Debug, thiserror::Error)]
pub enum EngineError {
    #[error("{0}")]
    Message(String),
    #[error("write: path {requested_path:?} already exists; pass overwrite to replace it")]
    Exists {
        requested_path: String,
        resolved_path: String,
        scope: String,
    },
    #[error("edit: old text not found in file; read the file and copy the exact text")]
    NotFound {
        requested_path: String,
        resolved_path: String,
        scope: String,
    },
    #[error("edit: old text occurs {count} times; retry with more context around the one you want, or replace all")]
    Ambiguous {
        requested_path: String,
        resolved_path: String,
        scope: String,
        count: usize,
        occurrences: Vec<Occurrence>,
    },
}

impl Engine {
    pub fn write(
        &self,
        workspace: &std::path::Path,
        requested_path: &str,
        content: &str,
        overwrite: bool,
    ) -> Result<WriteResult, EngineError> {
        if requested_path.trim().is_empty() {
            return Err(EngineError::Message("write: path is required".to_string()));
        }
        let (resolved, scope) = resolve_scoped_path("write", "path", requested_path, workspace)
            .map_err(|e| EngineError::Message(e.to_string()))?;
        let scope = scope.to_string();
        match std::fs::metadata(&resolved) {
            Ok(info) => {
                if info.is_dir() {
                    return Err(EngineError::Message(format!(
                        "write: path {requested_path:?} is a directory, not a file"
                    )));
                }
                if !overwrite {
                    return Err(EngineError::Exists {
                        requested_path: requested_path.to_string(),
                        resolved_path: resolved.display().to_string(),
                        scope,
                    });
                }
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => {
                return Err(EngineError::Message(format!(
                    "write: path {requested_path:?} cannot be used: {e}"
                )));
            }
        }
        if let Some(parent) = resolved.parent() {
            if !parent.as_os_str().is_empty() {
                std::fs::create_dir_all(parent).map_err(|e| {
                    EngineError::Message(format!("write: create parent directories: {e}"))
                })?;
            }
        }
        atomic_write(&resolved, content.as_bytes(), 0o644)
            .map_err(|e| EngineError::Message(format!("write: {e}")))?;
        Ok(WriteResult {
            requested_path: requested_path.to_string(),
            resolved_path: resolved.display().to_string(),
            scope,
            created: !overwrite,
            overwritten: overwrite && std::fs::metadata(&resolved).is_ok(),
            bytes: content.len(),
            lines: count_lines(content),
        })
    }

    pub fn edit(
        &self,
        workspace: &std::path::Path,
        requested_path: &str,
        old_text: &str,
        new_text: &str,
        replace_all: bool,
    ) -> Result<EditResult, EngineError> {
        if requested_path.trim().is_empty() {
            return Err(EngineError::Message("edit: path is required".to_string()));
        }
        if old_text.is_empty() {
            return Err(EngineError::Message(
                "edit: old text is required and cannot be empty".to_string(),
            ));
        }
        let (resolved, scope) = resolve_scoped_path("edit", "path", requested_path, workspace)
            .map_err(|e| EngineError::Message(e.to_string()))?;
        let scope = scope.to_string();
        let info = std::fs::metadata(&resolved).map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                EngineError::Message(format!("edit: path {requested_path:?} does not exist"))
            } else {
                EngineError::Message(format!("edit: path {requested_path:?} cannot be read: {e}"))
            }
        })?;
        if info.is_dir() {
            return Err(EngineError::Message(format!(
                "edit: path {requested_path:?} is a directory, not a file"
            )));
        }
        if !info.file_type().is_file() {
            return Err(EngineError::Message(format!(
                "edit: path {requested_path:?} is not a regular file"
            )));
        }
        if info.len() > MAX_FILE_BYTES {
            return Err(EngineError::Message(format!(
                "edit: file is {} bytes, over the {MAX_FILE_BYTES} byte limit",
                info.len()
            )));
        }
        let raw = std::fs::read(&resolved).map_err(|e| {
            EngineError::Message(format!("edit: path {requested_path:?} cannot be read: {e}"))
        })?;
        if raw.contains(&0) {
            return Err(EngineError::Message(
                "edit: the file is binary, not text".to_string(),
            ));
        }
        let content = String::from_utf8_lossy(&raw).into_owned();
        let count = content.matches(old_text).count();
        if count == 0 {
            return Err(EngineError::NotFound {
                requested_path: requested_path.to_string(),
                resolved_path: resolved.display().to_string(),
                scope,
            });
        }
        if count > 1 && !replace_all {
            return Err(EngineError::Ambiguous {
                requested_path: requested_path.to_string(),
                resolved_path: resolved.display().to_string(),
                scope,
                count,
                occurrences: find_occurrences(&content, old_text),
            });
        }
        let updated = if replace_all {
            content.replace(old_text, new_text)
        } else {
            content.replacen(old_text, new_text, 1)
        };
        #[cfg(unix)]
        let perm = {
            use std::os::unix::fs::PermissionsExt;
            info.permissions().mode() & 0o777
        };
        #[cfg(not(unix))]
        let perm = 0o644;
        atomic_write_mode(&resolved, updated.as_bytes(), perm)
            .map_err(|e| EngineError::Message(format!("edit: {e}")))?;
        let occurrences = find_occurrences(&content, old_text);
        let shown = if replace_all {
            occurrences.clone()
        } else {
            occurrences[..1].to_vec()
        };
        let replacements = if replace_all { count } else { 1 };
        Ok(EditResult {
            requested_path: requested_path.to_string(),
            resolved_path: resolved.display().to_string(),
            scope,
            replacements,
            bytes: updated.len(),
            lines: count_lines(&updated),
            match_start_line: shown[0].start_line,
            match_end_line: shown[0].end_line,
            matches: shown
                .iter()
                .map(|o| MatchSpan {
                    start_line: o.start_line,
                    end_line: o.end_line,
                })
                .collect(),
        })
    }
}

fn find_occurrences(content: &str, needle: &str) -> Vec<Occurrence> {
    let lines: Vec<&str> = content.split('\n').collect();
    let mut line_starts = Vec::with_capacity(lines.len());
    let mut off = 0;
    for line in &lines {
        line_starts.push(off);
        off += line.len() + 1;
    }
    let mut out = Vec::new();
    let mut search_from = 0;
    let total = content.matches(needle).count();
    while out.len() < total {
        let Some(pos) = content[search_from..].find(needle) else {
            break;
        };
        let start = search_from + pos;
        let end = start + needle.len();
        let start_line = line_of(&line_starts, start);
        let end_line = line_of(&line_starts, end.saturating_sub(1));
        out.push(Occurrence {
            occurrence: out.len() + 1,
            start_line,
            end_line,
            before: nearest_non_blank(&lines, start_line.saturating_sub(1), -1),
            after: nearest_non_blank(&lines, end_line + 1, 1),
        });
        search_from = end;
    }
    out
}

fn line_of(line_starts: &[usize], offset: usize) -> usize {
    let mut lo = 0;
    let mut hi = line_starts.len().saturating_sub(1);
    while lo < hi {
        let mid = (lo + hi).div_ceil(2);
        if line_starts[mid] <= offset {
            lo = mid;
        } else {
            hi = mid - 1;
        }
    }
    lo + 1
}

fn nearest_non_blank(lines: &[&str], start: usize, dir: i64) -> Option<ContextLine> {
    let mut i = start as i64;
    while i >= 1 && (i as usize) <= lines.len() {
        let text = lines[i as usize - 1].trim_end_matches('\r');
        if !text.trim().is_empty() {
            return Some(ContextLine {
                line: i as usize,
                text: text.to_string(),
            });
        }
        i += dir;
    }
    None
}

pub fn count_lines(text: &str) -> usize {
    if text.is_empty() {
        return 0;
    }
    let mut n = text.matches('\n').count();
    if !text.ends_with('\n') {
        n += 1;
    }
    n
}

fn atomic_write(path: &std::path::Path, data: &[u8], perm: u32) -> Result<(), std::io::Error> {
    let dir = path.parent().unwrap_or_else(|| std::path::Path::new("."));
    let mut tmp = tempfile::NamedTempFile::new_in(dir)?;
    use std::io::Write;
    tmp.write_all(data)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tmp.as_file()
            .set_permissions(std::fs::Permissions::from_mode(perm))?;
    }
    let _ = perm;
    tmp.persist(path).map_err(|e| e.error)?;
    Ok(())
}

fn atomic_write_mode(path: &std::path::Path, data: &[u8], perm: u32) -> Result<(), std::io::Error> {
    if atomic_write(path, data, perm).is_ok() {
        return Ok(());
    }
    std::fs::write(path, data)
}

#[derive(Debug, Clone)]
pub struct FileEngineTool {
    pub engine: Engine,
}

impl FileEngineTool {
    pub fn shared() -> Engine {
        Engine::new()
    }
}

#[async_trait::async_trait]
impl Tool for FileEngineTool {
    fn name(&self) -> &str {
        "file_engine"
    }
    fn definition(&self) -> super::ToolDefinition {
        super::ToolDefinition {
            name: "file_engine".to_string(),
            description: "Shared file operations behind write and edit. Not called directly."
                .to_string(),
            parameters: serde_json::json!({"type": "object"}),
        }
    }
    async fn call(&self, _args: serde_json::Value, _ctx: &ToolCtx) -> Result<String, ToolError> {
        Err(ToolError::args(
            "file_engine: not called directly; use write or edit",
        ))
    }
}

#[allow(dead_code)]
fn _assert_pathbuf_used(_: &PathBuf) {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn write_then_edit_roundtrip() {
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::create_dir_all(&ws).unwrap();
        let engine = Engine::new();
        engine
            .write(&ws, "workspace/a.txt", "hello\n", false)
            .unwrap();
        // Second write without overwrite fails with exists.
        assert!(matches!(
            engine.write(&ws, "workspace/a.txt", "x", false),
            Err(EngineError::Exists { .. })
        ));
        let res = engine
            .edit(&ws, "workspace/a.txt", "hello", "bye", false)
            .unwrap();
        assert_eq!(res.replacements, 1);
        assert_eq!(std::fs::read_to_string(ws.join("a.txt")).unwrap(), "bye\n");
    }

    #[test]
    fn edit_ambiguous_lists_occurrences() {
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::create_dir_all(&ws).unwrap();
        let engine = Engine::new();
        engine
            .write(&ws, "workspace/a.txt", "x\nx\n", false)
            .unwrap();
        match engine.edit(&ws, "workspace/a.txt", "x", "y", false) {
            Err(EngineError::Ambiguous { count, .. }) => assert_eq!(count, 2),
            other => panic!("expected ambiguous, got {other:?}"),
        }
    }
}
