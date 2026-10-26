use std::path::{Component, Path, PathBuf};

pub const SCOPE_INSIDE: &str = "inside";
pub const SCOPE_OUTSIDE: &str = "outside";

const WORKSPACE_PREFIX: &str = "workspace";

/// Resolve a model-supplied path under the scoping rule.
/// `workspace/`-prefixed input resolves inside the workspace and reports
/// inside; anything else must be an absolute path and reports outside.
/// A prefixed path escaping the workspace is rejected.
pub fn resolve_scoped_path(
    tool: &str,
    field: &str,
    input: &str,
    workspace: &Path,
) -> Result<(PathBuf, &'static str), crate::error::ToolError> {
    use crate::error::ToolError;
    let trimmed = input.trim();
    if trimmed.is_empty() {
        return Err(ToolError::args(format!("{tool}: {field} is required")));
    }
    if let Some(rest) = cut_workspace_prefix(trimmed) {
        if workspace.as_os_str().is_empty() {
            return Err(ToolError::args(format!(
                "{tool}: no workspace to resolve {field} against"
            )));
        }
        let joined = workspace.join(rest);
        let cleaned = clean(&joined);
        if escapes(&cleaned, workspace) {
            return Err(ToolError::args(format!(
                "{tool}: {field} must stay inside the workspace; use the absolute path for files outside it"
            )));
        }
        if let Some(real) = resolve_existing(&cleaned) {
            if escapes(&real, workspace) {
                return Err(ToolError::args(format!(
                    "{tool}: {field} must stay inside the workspace; use the absolute path for files outside it"
                )));
            }
        }
        return Ok((cleaned, SCOPE_INSIDE));
    }
    let path = Path::new(trimmed);
    if !path.is_absolute() {
        return Err(ToolError::args(format!(
            "{tool}: {field} must start with {WORKSPACE_PREFIX}/ for workspace files or be an absolute path, got {trimmed:?}"
        )));
    }
    Ok((clean(path), SCOPE_OUTSIDE))
}

fn cut_workspace_prefix(path: &str) -> Option<PathBuf> {
    if path == WORKSPACE_PREFIX {
        return Some(PathBuf::new());
    }
    for sep in ["/", "\\"] {
        if let Some(rest) = path.strip_prefix(&format!("{WORKSPACE_PREFIX}{sep}")) {
            let rest = rest.replace('\\', "/");
            return Some(PathBuf::from(rest));
        }
    }
    None
}

fn clean(path: &Path) -> PathBuf {
    let mut out = PathBuf::new();
    for comp in path.components() {
        match comp {
            Component::CurDir => {}
            Component::ParentDir => {
                out.pop();
            }
            c => out.push(c.as_os_str()),
        }
    }
    if out.as_os_str().is_empty() {
        out.push("/");
    }
    out
}

fn escapes(cleaned: &Path, workspace: &Path) -> bool {
    let root = real_path(workspace);
    let target = clean(cleaned);
    target.strip_prefix(&root).is_err()
}

fn resolve_existing(cleaned: &Path) -> Option<PathBuf> {
    let mut path = cleaned.to_path_buf();
    let mut tail = Vec::new();
    loop {
        if std::fs::symlink_metadata(&path).is_ok() {
            break;
        }
        match path.parent() {
            Some(parent) if parent != path => {
                if let Some(name) = path.file_name() {
                    tail.push(name.to_os_string());
                }
                path = parent.to_path_buf();
            }
            _ => return None,
        }
    }
    let mut real = std::fs::canonicalize(&path).ok()?;
    for part in tail.iter().rev() {
        real.push(part);
    }
    Some(clean(&real))
}

fn real_path(path: &Path) -> PathBuf {
    if let Ok(abs) = path.canonicalize() {
        return abs;
    }
    let abs = if path.is_absolute() {
        path.to_path_buf()
    } else {
        std::env::current_dir().unwrap_or_default().join(path)
    };
    clean(&abs)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_escape_and_bare_relative() {
        let ws = Path::new("/tmp/ws");
        assert!(resolve_scoped_path("read", "path", "workspace/../etc/hosts", ws).is_err());
        assert!(resolve_scoped_path("read", "path", "src/main.rs", ws).is_err());
        let (resolved, scope) =
            resolve_scoped_path("read", "path", "workspace/src/main.rs", ws).unwrap();
        assert_eq!(scope, SCOPE_INSIDE);
        assert_eq!(resolved, PathBuf::from("/tmp/ws/src/main.rs"));
        let (resolved, scope) = resolve_scoped_path("read", "path", "/etc/hosts", ws).unwrap();
        assert_eq!(scope, SCOPE_OUTSIDE);
        assert_eq!(resolved, PathBuf::from("/etc/hosts"));
    }
}
