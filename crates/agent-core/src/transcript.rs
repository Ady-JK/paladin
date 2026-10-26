use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    System,
    User,
    Assistant,
    Tool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub role: Role,
    pub content: String,
    #[serde(default)]
    pub created_at: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_calls: Option<Vec<ToolCall>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub is_error: bool,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub is_notify: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Meta {
    pub schema_version: u32,
    #[serde(default)]
    pub session_id: String,
    pub cwd: String,
    pub created_at: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub provider: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
}

pub const SCHEMA_VERSION: u32 = 1;

impl Message {
    pub fn user(content: impl Into<String>) -> Self {
        Self {
            role: Role::User,
            content: content.into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: None,
            name: None,
            is_error: false,
            is_notify: false,
        }
    }

    pub fn assistant(content: impl Into<String>) -> Self {
        Self {
            role: Role::Assistant,
            content: content.into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: None,
            name: None,
            is_error: false,
            is_notify: false,
        }
    }

    /// Visible in history: users always, assistants only when non-blank.
    pub fn visible(&self) -> bool {
        match self.role {
            Role::User => true,
            Role::Assistant => {
                !self.content.trim().is_empty()
                    || self.tool_calls.as_ref().is_some_and(|c| !c.is_empty())
            }
            _ => true,
        }
    }
}

/// First user message truncated to 80 chars, used as session title.
pub fn title_for(messages: &[Message]) -> String {
    for msg in messages {
        if msg.role == Role::User && msg.visible() {
            let title = msg.content.trim();
            if title.is_empty() {
                continue;
            }
            return shorten(title, 80);
        }
    }
    String::new()
}

fn shorten(text: &str, n: usize) -> String {
    let runes: Vec<char> = text.chars().collect();
    if runes.len() <= n {
        return text.to_string();
    }
    let cut: String = runes[..n].iter().collect();
    if let Some(i) = cut.rfind([' ', '\n', '\t']) {
        if i > n / 2 {
            return cut[..i].trim_end().to_string() + "…";
        }
    }
    cut.trim_end().to_string() + "…"
}

/// A session: metadata plus its on-disk directory.
#[derive(Debug, Clone)]
pub struct Session {
    pub meta: Meta,
    pub dir: std::path::PathBuf,
}

impl Session {
    pub fn append(
        &self,
        role: Role,
        content: impl Into<String>,
    ) -> Result<(), crate::error::SessionError> {
        self.append_message(Message {
            role,
            content: content.into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: None,
            name: None,
            is_error: false,
            is_notify: false,
        })
    }

    pub fn append_message(&self, mut msg: Message) -> Result<(), crate::error::SessionError> {
        use crate::error::SessionError;
        use std::io::Write;
        if msg.created_at.timestamp_nanos_opt().is_none() {
            msg.created_at = Utc::now();
        }
        let mut file = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(self.dir.join("messages.jsonl"))
            .map_err(|e| SessionError::Io(format!("open transcript: {e}")))?;
        let line = serde_json::to_string(&msg)
            .map_err(|e| SessionError::Io(format!("encode message: {e}")))?;
        writeln!(file, "{line}").map_err(|e| SessionError::Io(format!("write message: {e}")))?;
        Ok(())
    }

    pub fn history(&self) -> Result<Vec<Message>, crate::error::SessionError> {
        load_transcript(&self.dir)
    }
}

#[derive(Debug, Clone)]
pub struct Info {
    pub meta: Meta,
    pub message_count: usize,
    pub title: String,
}

/// On-disk session store: `<root>/<encode_cwd>/<uuid>/`.
#[derive(Debug, Clone)]
pub struct Store {
    pub root: std::path::PathBuf,
}

impl Store {
    pub fn new(root: std::path::PathBuf) -> Self {
        Self { root }
    }

    pub fn create(
        &self,
        cwd: &str,
        provider: &str,
        model: &str,
    ) -> Result<Session, crate::error::SessionError> {
        use crate::error::SessionError;
        if cwd.trim().is_empty() {
            return Err(SessionError::Io("session: cwd is required".to_string()));
        }
        let id = uuid::Uuid::new_v4().to_string();
        let dir = self.root.join(encode_cwd(cwd)).join(&id);
        std::fs::create_dir_all(&dir)
            .map_err(|e| SessionError::Io(format!("session: create {}: {e}", dir.display())))?;
        let meta = Meta {
            schema_version: SCHEMA_VERSION,
            session_id: id,
            cwd: cwd.to_string(),
            created_at: Utc::now(),
            provider: if provider.is_empty() {
                None
            } else {
                Some(provider.to_string())
            },
            model: if model.is_empty() {
                None
            } else {
                Some(model.to_string())
            },
        };
        write_meta(&dir, &meta)?;
        Ok(Session { meta, dir })
    }

    /// Sessions of a workspace, newest first. Empty cwd lists all namespaces.
    pub fn list(&self, cwd: &str, limit: usize) -> Result<Vec<Info>, crate::error::SessionError> {
        use crate::error::SessionError;
        let mut dirs = Vec::new();
        if !cwd.is_empty() {
            dirs.extend(session_dirs(&self.root, cwd));
        } else {
            let entries = match std::fs::read_dir(&self.root) {
                Ok(e) => e,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
                Err(e) => {
                    return Err(SessionError::Io(format!(
                        "session: read {}: {e}",
                        self.root.display()
                    )))
                }
            };
            for entry in entries.flatten() {
                if entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
                    dirs.push(entry.path());
                }
            }
        }
        let mut seen = std::collections::HashSet::new();
        let mut infos = Vec::new();
        for dir in dirs {
            let entries = match std::fs::read_dir(&dir) {
                Ok(e) => e,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
                Err(e) => {
                    return Err(SessionError::Io(format!(
                        "session: read {}: {e}",
                        dir.display()
                    )))
                }
            };
            for entry in entries.flatten() {
                if !entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
                    continue;
                }
                let name = entry.file_name().to_string_lossy().to_string();
                if !seen.insert(name.clone()) {
                    continue;
                }
                let session_dir = entry.path();
                let Ok(meta) = read_meta(&session_dir) else {
                    continue;
                };
                infos.push(info(&session_dir, meta));
            }
        }
        infos.sort_by_key(|a| std::cmp::Reverse(a.meta.created_at));
        if limit > 0 && infos.len() > limit {
            infos.truncate(limit);
        }
        Ok(infos)
    }

    /// Load a session by id, scoping to cwd's namespace when given.
    pub fn load(&self, cwd: &str, session_id: &str) -> Result<Session, crate::error::SessionError> {
        use crate::error::SessionError;
        if session_id.is_empty() {
            return Err(SessionError::NotFound(
                "session: session id is required".to_string(),
            ));
        }
        if !valid_id(session_id) {
            return Err(SessionError::NotFound(format!(
                "session: invalid session id {session_id:?}"
            )));
        }
        if !cwd.is_empty() {
            for ns in session_dirs(&self.root, cwd) {
                if let Ok(sess) = self.load_dir(&ns.join(session_id), session_id) {
                    return Ok(sess);
                }
            }
        } else {
            let entries = match std::fs::read_dir(&self.root) {
                Ok(e) => e,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    return Err(SessionError::NotFound(format!(
                        "session: {session_id:?} not found"
                    )));
                }
                Err(e) => {
                    return Err(SessionError::Io(format!(
                        "session: read {}: {e}",
                        self.root.display()
                    )))
                }
            };
            for entry in entries.flatten() {
                if !entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
                    continue;
                }
                if let Ok(sess) = self.load_dir(&entry.path().join(session_id), session_id) {
                    return Ok(sess);
                }
            }
        }
        Err(SessionError::NotFound(format!(
            "session: {session_id:?} not found"
        )))
    }

    fn load_dir(
        &self,
        dir: &std::path::Path,
        session_id: &str,
    ) -> Result<Session, crate::error::SessionError> {
        use crate::error::SessionError;
        let mut meta = read_meta(dir)?;
        if meta.session_id.is_empty() {
            meta.session_id = session_id.to_string();
        } else if meta.session_id != session_id {
            return Err(SessionError::NotFound(format!(
                "session: {session_id:?} not found"
            )));
        }
        Ok(Session {
            meta,
            dir: dir.to_path_buf(),
        })
    }

    /// Delete a session and remove its namespace dir when empty.
    pub fn delete(&self, cwd: &str, session_id: &str) -> Result<(), crate::error::SessionError> {
        use crate::error::SessionError;
        let dir = self.session_dir(cwd, session_id)?;
        std::fs::remove_dir_all(&dir)
            .map_err(|e| SessionError::Io(format!("session: delete {}: {e}", dir.display())))?;
        let parent = dir.parent().unwrap_or(&self.root);
        if std::fs::read_dir(parent)
            .map(|mut e| e.next().is_none())
            .unwrap_or(false)
        {
            let _ = std::fs::remove_dir(parent);
        }
        Ok(())
    }

    fn session_dir(
        &self,
        cwd: &str,
        session_id: &str,
    ) -> Result<std::path::PathBuf, crate::error::SessionError> {
        use crate::error::SessionError;
        if session_id.is_empty() {
            return Err(SessionError::NotFound(
                "session: session id is required".to_string(),
            ));
        }
        if !valid_id(session_id) {
            return Err(SessionError::NotFound(format!(
                "session: invalid session id {session_id:?}"
            )));
        }
        if !cwd.is_empty() {
            for ns in session_dirs(&self.root, cwd) {
                let dir = ns.join(session_id);
                if dir.is_dir() {
                    return Ok(dir);
                }
            }
        }
        let entries = match std::fs::read_dir(&self.root) {
            Ok(e) => e,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                return Err(SessionError::NotFound(format!(
                    "session: {session_id:?} not found"
                )));
            }
            Err(e) => {
                return Err(SessionError::Io(format!(
                    "session: read {}: {e}",
                    self.root.display()
                )))
            }
        };
        for entry in entries.flatten() {
            if !entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
                continue;
            }
            let dir = entry.path().join(session_id);
            if dir.is_dir() {
                return Ok(dir);
            }
        }
        Err(SessionError::NotFound(format!(
            "session: {session_id:?} not found"
        )))
    }
}

fn info(dir: &std::path::Path, meta: Meta) -> Info {
    let msgs = load_transcript(dir).unwrap_or_default();
    Info {
        message_count: msgs.iter().filter(|m| m.visible()).count(),
        title: title_for(&msgs),
        meta,
    }
}

/// Encode cwd to a namespace dir: escapes `-`, `:`, `/`, plus a hash suffix
/// so similar inputs never collide. Reads also probe the legacy layout.
pub fn encode_cwd(cwd: &str) -> String {
    use sha2::{Digest, Sha256};
    let name = cwd.replace('\\', "/");
    let mut hasher = Sha256::new();
    hasher.update(name.as_bytes());
    let suffix = format!("-{:x}", hasher.finalize());
    let suffix = suffix[..9].to_string();
    let mut name = name.replace('-', "-0");
    name = name.replace(':', "-1");
    name = name.replace('/', "--");
    if name.is_empty() || name == "." || name == ".." {
        return "_".to_string();
    }
    if is_reserved_windows_name(&name) {
        return format!("_{name}{suffix}");
    }
    format!("{name}{suffix}")
}

fn legacy_encode_cwd(cwd: &str) -> String {
    let name = cwd.replace('\\', "/");
    let name = name.replace(':', "-");
    let name = name.replace('/', "--");
    if name.is_empty() || name == "." || name == ".." {
        return "_".to_string();
    }
    if is_reserved_windows_name(&name) {
        return format!("_{name}");
    }
    name
}

fn session_dirs(root: &std::path::Path, cwd: &str) -> Vec<std::path::PathBuf> {
    let fresh = root.join(encode_cwd(cwd));
    let legacy = root.join(legacy_encode_cwd(cwd));
    if fresh == legacy {
        vec![fresh]
    } else {
        vec![fresh, legacy]
    }
}

fn is_reserved_windows_name(name: &str) -> bool {
    let device = name.split('.').next().unwrap_or(name);
    match device.to_uppercase().as_str() {
        "CON" | "PRN" | "AUX" | "NUL" => return true,
        _ => {}
    }
    if device.len() == 4 {
        let upper = device.to_uppercase();
        let prefix = &upper[..3];
        let last = device.chars().nth(3).unwrap_or('0');
        if (prefix == "COM" || prefix == "LPT") && ('1'..='9').contains(&last) {
            return true;
        }
    }
    false
}

fn valid_id(id: &str) -> bool {
    if id.is_empty() || id == "." || id == ".." {
        return false;
    }
    if id.contains(['/', '\\']) {
        return false;
    }
    uuid::Uuid::parse_str(id).is_ok()
}

pub fn load_transcript(dir: &std::path::Path) -> Result<Vec<Message>, crate::error::SessionError> {
    use crate::error::SessionError;
    let path = dir.join("messages.jsonl");
    let data = match std::fs::read_to_string(&path) {
        Ok(d) => d,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(e) => return Err(SessionError::Io(format!("open transcript: {e}"))),
    };
    let mut out = Vec::new();
    for line in data.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let msg: Message = serde_json::from_str(line)
            .map_err(|e| SessionError::Io(format!("decode message: {e}")))?;
        out.push(msg);
    }
    Ok(out)
}

fn write_meta(dir: &std::path::Path, meta: &Meta) -> Result<(), crate::error::SessionError> {
    use crate::error::SessionError;
    let data = serde_json::to_string_pretty(meta)
        .map_err(|e| SessionError::Io(format!("encode meta: {e}")))?;
    std::fs::write(dir.join("meta.json"), data + "\n")
        .map_err(|e| SessionError::Io(format!("write meta: {e}")))?;
    Ok(())
}

fn read_meta(dir: &std::path::Path) -> Result<Meta, crate::error::SessionError> {
    use crate::error::SessionError;
    let data = std::fs::read_to_string(dir.join("meta.json"))
        .map_err(|e| SessionError::Io(format!("read meta: {e}")))?;
    let mut meta: Meta =
        serde_json::from_str(&data).map_err(|e| SessionError::Io(format!("decode meta: {e}")))?;
    if meta.schema_version == 0 {
        // Pre-versioned transcripts (temp-agent layout): treat as v1.
        meta.schema_version = SCHEMA_VERSION;
    }
    if meta.schema_version > SCHEMA_VERSION {
        return Err(SessionError::VersionTooNew(format!(
            "schema {} > supported {SCHEMA_VERSION}",
            meta.schema_version
        )));
    }
    Ok(meta)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn assistant_blank_without_tools_is_hidden() {
        assert!(!Message::assistant("   ").visible());
    }

    #[test]
    fn title_uses_first_user_message() {
        let msgs = vec![Message::user("hello world")];
        assert_eq!(title_for(&msgs), "hello world");
    }

    #[test]
    fn encodes_cwd_with_hash_suffix() {
        let enc = encode_cwd("/home/u/proj");
        assert!(enc.starts_with("--home--u--proj-"));
    }

    #[test]
    fn round_trips_transcript() {
        let dir = tempfile::tempdir().unwrap();
        let session_dir = dir.path().join("s");
        std::fs::create_dir_all(&session_dir).unwrap();
        let sess = Session {
            meta: Meta {
                schema_version: SCHEMA_VERSION,
                session_id: "x".into(),
                cwd: "/tmp".into(),
                created_at: Utc::now(),
                provider: None,
                model: None,
            },
            dir: session_dir,
        };
        sess.append(Role::User, "hi").unwrap();
        assert_eq!(sess.history().unwrap().len(), 1);
    }

    #[test]
    fn store_create_load_list_delete() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("sessions"));
        let sess = store.create("/tmp/proj", "openrouter", "m").unwrap();
        let loaded = store.load("/tmp/proj", &sess.meta.session_id).unwrap();
        assert_eq!(loaded.meta.cwd, "/tmp/proj");
        let infos = store.list("/tmp/proj", 100).unwrap();
        assert_eq!(infos.len(), 1);
        assert_eq!(infos[0].meta.provider.as_deref(), Some("openrouter"));
        store.delete("/tmp/proj", &sess.meta.session_id).unwrap();
        assert!(store.list("/tmp/proj", 100).unwrap().is_empty());
    }

    #[test]
    fn rejects_crafted_ids() {
        assert!(!valid_id("../evil"));
        assert!(!valid_id("not-a-uuid"));
    }
}
