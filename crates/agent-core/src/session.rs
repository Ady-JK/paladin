use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::error::SessionError;
use crate::transcript::{title_for, Message, Meta, SCHEMA_VERSION};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Session {
    pub id: String,
    pub cwd: String,
    pub provider: String,
    pub model: String,
    #[serde(default)]
    pub created_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionInfo {
    pub session_id: String,
    pub cwd: String,
    pub created_at: i64,
    pub provider: String,
    pub model: String,
    pub message_count: i64,
    pub title: String,
}

#[derive(Debug, Clone)]
pub struct SessionStore {
    pub root: PathBuf,
}

impl SessionStore {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }

    pub fn dir_for(&self, cwd: &str, session_id: &str) -> PathBuf {
        self.root.join(encode_cwd(cwd)).join(session_id)
    }
}

/// Encode cwd by replacing `/` with `--`, mirroring the old layout
/// so existing sessions can be read.
pub fn encode_cwd(cwd: &str) -> String {
    cwd.replace('-', "-0")
        .replace(':', "-1")
        .replace(['/', '\\'], "--")
}

pub fn session_dir_name_ok(id: &str) -> bool {
    !id.is_empty()
        && id
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
}

pub fn load_transcript(dir: &Path) -> Result<Vec<Message>, SessionError> {
    let path = dir.join("messages.jsonl");
    if !path.exists() {
        return Ok(Vec::new());
    }
    let data = std::fs::read_to_string(&path).map_err(|e| SessionError::Io(e.to_string()))?;
    let mut out = Vec::new();
    for line in data.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let msg: Message =
            serde_json::from_str(line).map_err(|e| SessionError::Io(e.to_string()))?;
        out.push(msg);
    }
    Ok(out)
}

pub fn append_transcript(dir: &Path, messages: &[Message]) -> Result<(), SessionError> {
    use std::io::Write;
    std::fs::create_dir_all(dir).map_err(|e| SessionError::Io(e.to_string()))?;
    let path = dir.join("messages.jsonl");
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .map_err(|e| SessionError::Io(e.to_string()))?;
    for msg in messages {
        let line = serde_json::to_string(msg).map_err(|e| SessionError::Io(e.to_string()))?;
        writeln!(file, "{line}").map_err(|e| SessionError::Io(e.to_string()))?;
    }
    Ok(())
}

pub fn load_meta(dir: &Path) -> Result<Meta, SessionError> {
    let path = dir.join("meta.json");
    let data = std::fs::read_to_string(&path).map_err(|e| SessionError::Io(e.to_string()))?;
    let meta: Meta = serde_json::from_str(&data).map_err(|e| SessionError::Io(e.to_string()))?;
    if meta.schema_version > SCHEMA_VERSION {
        return Err(SessionError::VersionTooNew(format!(
            "schema {} > supported {SCHEMA_VERSION}",
            meta.schema_version
        )));
    }
    Ok(meta)
}

pub fn session_info(id: &str, cwd: &str, meta: &Meta, messages: &[Message]) -> SessionInfo {
    SessionInfo {
        session_id: id.to_string(),
        cwd: cwd.to_string(),
        created_at: meta.created_at.timestamp(),
        provider: meta.provider.clone().unwrap_or_default(),
        model: meta.model.clone().unwrap_or_default(),
        message_count: messages.iter().filter(|m| m.visible()).count() as i64,
        title: title_for(messages),
    }
}

/// Short hash suffix used to disambiguate encoded cwd dirs.
#[allow(dead_code)]
pub fn cwd_hash(cwd: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(cwd.as_bytes());
    format!("{:x}", hasher.finalize())[..8].to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encodes_cwd_slashes() {
        assert_eq!(encode_cwd("/home/u/proj"), "--home--u--proj");
    }

    #[test]
    fn round_trips_transcript() {
        let dir = tempfile::tempdir().unwrap();
        let msgs = vec![Message::user("hi")];
        append_transcript(dir.path(), &msgs).unwrap();
        let loaded = load_transcript(dir.path()).unwrap();
        assert_eq!(loaded.len(), 1);
    }
}
