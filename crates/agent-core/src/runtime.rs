use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use tokio::sync::{mpsc, Mutex, RwLock};
use tokio_util::sync::CancellationToken;

use crate::config::{Config, ProviderInfo, ProviderInput};
use crate::error::ApiError;
use crate::tools::{builtin_registry, ToolRegistry};
use crate::transcript::Message;
use crate::transcript::Store as SessionStore;
use crate::turn::{ChatChunk, ChatOutcome, ChatRequest};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    pub session_id: String,
    pub cwd: String,
    pub provider: String,
    pub model: String,
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

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FsList {
    pub current_path: String,
    pub parent_path: String,
    pub directories: Vec<FsDirectory>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FsDirectory {
    pub name: String,
    pub path: String,
}

pub struct AgentRuntime {
    pub data_dir: PathBuf,
    pub config: Arc<RwLock<Config>>,
    pub sessions: SessionStore,
    pub tools: ToolRegistry,
    pub turns: Mutex<HashMap<String, CancellationToken>>,
}

impl AgentRuntime {
    pub fn new(data_dir: PathBuf, config: Config) -> Self {
        let sessions = SessionStore::new(data_dir.join("sessions"));
        Self {
            data_dir,
            config: Arc::new(RwLock::new(config)),
            sessions,
            tools: builtin_registry(),
            turns: Mutex::new(HashMap::new()),
        }
    }

    pub async fn chat(
        &self,
        _req: ChatRequest,
        _tx: mpsc::Sender<ChatChunk>,
    ) -> anyhow::Result<ChatOutcome> {
        anyhow::bail!("chat not implemented in scaffold")
    }

    pub async fn stop(&self, session_id: &str) -> bool {
        let turns = self.turns.lock().await;
        if let Some(token) = turns.get(session_id) {
            token.cancel();
            true
        } else {
            false
        }
    }

    pub async fn open_session(&self, _session_id: &str, _cwd: &str) -> anyhow::Result<Session> {
        anyhow::bail!("not implemented in scaffold")
    }

    pub async fn list_sessions(&self, _cwd: &str, _limit: i32) -> anyhow::Result<Vec<SessionInfo>> {
        Ok(Vec::new())
    }

    pub async fn session_history(
        &self,
        _session_id: &str,
        _cwd: &str,
    ) -> anyhow::Result<Vec<Message>> {
        Ok(Vec::new())
    }

    pub async fn delete_session(&self, _session_id: &str, _cwd: &str) -> anyhow::Result<bool> {
        Ok(false)
    }

    pub fn list_providers(&self) -> Vec<ProviderInfo> {
        Vec::new()
    }

    pub async fn refresh_providers(&self) -> bool {
        false
    }

    pub async fn set_default_provider(&self, _name: &str) -> anyhow::Result<Vec<ProviderInfo>> {
        anyhow::bail!("not implemented in scaffold")
    }

    pub async fn add_provider(&self, _req: ProviderInput) -> anyhow::Result<ProviderInfo> {
        anyhow::bail!("not implemented in scaffold")
    }

    pub async fn update_provider(&self, _req: ProviderInput) -> anyhow::Result<ProviderInfo> {
        anyhow::bail!("not implemented in scaffold")
    }

    pub fn get_workspace(&self) -> String {
        String::new()
    }

    pub async fn set_workspace(&self, _path: &str) -> anyhow::Result<String> {
        anyhow::bail!("not implemented in scaffold")
    }

    pub fn fs_home(&self) -> String {
        directories::UserDirs::new()
            .map(|d| d.home_dir().to_string_lossy().to_string())
            .unwrap_or_else(|| ".".to_string())
    }

    pub async fn fs_list(&self, path: &str) -> anyhow::Result<FsList> {
        let current = if path.trim().is_empty() {
            self.fs_home()
        } else {
            path.to_string()
        };
        let current_path = PathBuf::from(&current);
        let parent = current_path
            .parent()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_default();
        let mut directories = Vec::new();
        if let Ok(entries) = std::fs::read_dir(&current_path) {
            for entry in entries.flatten() {
                if let Ok(ft) = entry.file_type() {
                    if ft.is_dir() {
                        directories.push(FsDirectory {
                            name: entry.file_name().to_string_lossy().to_string(),
                            path: entry.path().to_string_lossy().to_string(),
                        });
                    }
                }
            }
        }
        directories.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(FsList {
            current_path: current,
            parent_path: parent,
            directories,
        })
    }

    pub fn api_error(code: &str, message: &str) -> ApiError {
        ApiError::new(code, message)
    }
}
