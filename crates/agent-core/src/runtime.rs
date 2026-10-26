use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tokio::sync::{mpsc, RwLock};
use tokio_util::sync::CancellationToken;

use crate::config::Provider as ConfigProvider;
use crate::config::{
    clamp_max_tool_rounds, default_providers_file, ensure_seeded, is_placeholder_key,
    validate_provider_input, ApiType, Config, ProviderInfo, ProviderInput, Store,
    OPENCODE_BASE_URL, OPENROUTER_BASE_URL,
};
use crate::error::ApiError;
use crate::inference::provider::build_provider;
use crate::inference::Provider as InferenceProvider;
use crate::prompt::{self, SYSTEM_PROMPT, TOOL_GUIDANCE};
use crate::session::{history_view, HistoryMessage, SessionInfo};
use crate::tools::shell::ShellTool;
use crate::tools::{builtin_registry, ToolRegistry};
use crate::transcript::Store as TranscriptStore;
use crate::turn::{Agent, ChatChunk, ChatOutcome, ChatRequest};

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

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
struct Settings {
    #[serde(default)]
    system_prompt: String,
    #[serde(default)]
    tool_guidance: String,
    #[serde(default)]
    max_tool_rounds: u32,
}

struct TurnState {
    cancel: CancellationToken,
    cwd: String,
}

struct Tracker {
    inner: Mutex<HashMap<String, TurnState>>,
}

impl Tracker {
    fn new() -> Self {
        Self {
            inner: Mutex::new(HashMap::new()),
        }
    }

    fn track(&self, session_id: &str, cwd: &str, cancel: CancellationToken) -> bool {
        let mut inner = self.inner.lock().unwrap(); // Track needs mut; stop doesn't.

        if inner.contains_key(session_id) {
            return false;
        }
        inner.insert(
            session_id.to_string(),
            TurnState {
                cancel,
                cwd: cwd.to_string(),
            },
        );
        true
    }

    fn untrack(&self, session_id: &str) {
        self.inner.lock().unwrap().remove(session_id);
    }

    /// Stop one turn by session, or every turn of a workspace when the
    /// session id is empty. Reports whether anything was running.
    fn stop(&self, session_id: &str, cwd: &str) -> bool {
        let inner = self.inner.lock().unwrap();
        if !session_id.is_empty() {
            if let Some(state) = inner.get(session_id) {
                state.cancel.cancel();
                return true;
            }
            return false;
        }
        let mut stopped = false;
        for state in inner.values() {
            if cwd.is_empty() || state.cwd == cwd {
                state.cancel.cancel();
                stopped = true;
            }
        }
        stopped
    }
}

pub struct AgentRuntime {
    data_dir: PathBuf,
    config: Arc<RwLock<Config>>,
    store: Store,
    transcripts: TranscriptStore,
    tools: Arc<ToolRegistry>,
    shell: Arc<ShellTool>,
    tracker: Tracker,
    providers_lock: Mutex<()>,
    refresh_lock: Mutex<()>,
    provider_cache: Mutex<HashMap<String, Arc<dyn InferenceProvider>>>,
}

impl AgentRuntime {
    pub fn new(data_dir: PathBuf, config: Config) -> Self {
        Self::with_key_store(data_dir, config, Arc::new(crate::config::OsKeyStore))
    }

    pub fn with_key_store(
        data_dir: PathBuf,
        config: Config,
        keys: Arc<dyn crate::config::KeyStore>,
    ) -> Self {
        let store = Store::new(data_dir.clone(), keys);
        let transcripts = TranscriptStore::new(data_dir.join("sessions"));
        Self {
            data_dir,
            config: Arc::new(RwLock::new(config)),
            store,
            transcripts,
            tools: Arc::new(builtin_registry()),
            shell: Arc::new(ShellTool::new()),
            tracker: Tracker::new(),
            providers_lock: Mutex::new(()),
            refresh_lock: Mutex::new(()),
            provider_cache: Mutex::new(HashMap::new()),
        }
    }

    fn settings_path(&self) -> PathBuf {
        self.data_dir.join("config").join("settings.json")
    }

    fn load_settings(&self) -> Settings {
        std::fs::read_to_string(self.settings_path())
            .ok()
            .and_then(|data| serde_json::from_str(&data).ok())
            .unwrap_or_default()
    }

    fn evict_provider_cache(&self) {
        self.provider_cache.lock().unwrap().clear();
    }

    fn resolve_provider(
        &self,
        name_override: &str,
        model_override: &str,
    ) -> Result<(ConfigProvider, String, Arc<dyn InferenceProvider>), String> {
        let (mut entry, key) = self
            .store
            .resolve(name_override)
            .map_err(|e| e.to_string())?;
        if !model_override.trim().is_empty() {
            entry.model = model_override.trim().to_string();
        }
        let cache_key = format!("{}\x00{}", entry.name, entry.model);
        if let Some(cached) = self.provider_cache.lock().unwrap().get(&cache_key) {
            return Ok((entry, key, cached.clone()));
        }
        let provider = build_provider(
            entry.api_type,
            entry.name.clone(),
            entry.model.clone(),
            entry.api_url.clone(),
            key.clone(),
            Duration::from_secs(60),
        )
        .map_err(|e| e.to_string())?;
        self.provider_cache
            .lock()
            .unwrap()
            .insert(cache_key, provider.clone());
        Ok((entry, key, provider))
    }

    fn system_prompt(&self, override_prompt: &str) -> String {
        if !override_prompt.trim().is_empty() {
            return override_prompt.to_string();
        }
        let settings = self.load_settings();
        if !settings.system_prompt.trim().is_empty() {
            return settings.system_prompt;
        }
        if let Ok(cfg) = self.config.try_read() {
            if !cfg.system_prompt.trim().is_empty() {
                return cfg.system_prompt.clone();
            }
        }
        SYSTEM_PROMPT.to_string()
    }

    fn tool_guidance(&self) -> String {
        let settings = self.load_settings();
        if !settings.tool_guidance.trim().is_empty() {
            return settings.tool_guidance;
        }
        TOOL_GUIDANCE.to_string()
    }

    fn max_rounds(&self) -> u32 {
        let settings = self.load_settings();
        if settings.max_tool_rounds > 0 {
            return clamp_max_tool_rounds(settings.max_tool_rounds);
        }
        if let Ok(cfg) = self.config.try_read() {
            return clamp_max_tool_rounds(cfg.max_tool_rounds);
        }
        clamp_max_tool_rounds(0)
    }

    pub async fn chat(
        &self,
        req: ChatRequest,
        tx: mpsc::Sender<ChatChunk>,
    ) -> Result<ChatOutcome, String> {
        if req.session_id.trim().is_empty() {
            return Err("sessionId is required: create one with open_session first".to_string());
        }
        if req.message.trim().is_empty() && !req.resume {
            return Err("message is required".to_string());
        }
        let session = self
            .transcripts
            .load(&req.cwd, &req.session_id)
            .map_err(|e| e.to_string())?;
        let (entry, _key, provider) = self.resolve_provider(
            req.provider.as_deref().unwrap_or_default(),
            req.model.as_deref().unwrap_or_default(),
        )?;
        let _ = entry;
        let history = session.history().map_err(|e| e.to_string())?;
        let base = self.system_prompt(req.system_prompt.as_deref().unwrap_or_default());
        let system = prompt::tool_system(&base, &self.tool_guidance(), &session.meta.cwd);
        let tool_defs = self
            .tools
            .definitions()
            .into_iter()
            .map(|d| crate::inference::provider::ToolDefinition {
                name: d.name,
                description: d.description,
                parameters: d.parameters,
            })
            .collect::<Vec<_>>();
        let cancel = CancellationToken::new();
        if !self
            .tracker
            .track(&req.session_id, &req.cwd, cancel.clone())
        {
            return Err(format!(
                "a turn is already running for session {:?}",
                req.session_id
            ));
        }
        let mut agent = Agent::new(
            session,
            provider,
            self.tools.clone(),
            self.shell.clone(),
            cancel,
            system,
            self.max_rounds(),
            history,
            tool_defs,
        );
        let outcome = agent.run_turn(req.message.clone(), req.resume, &tx).await;
        self.tracker.untrack(&req.session_id);
        // Always close with a done chunk so the UI can settle.
        match &outcome {
            Ok(o) => {
                let _ = tx
                    .send(ChatChunk {
                        session_id: o.session_id.clone(),
                        delta: String::new(),
                        tool: None,
                        done: true,
                        stopped: o.stopped,
                        needs_continue: o.needs_continue,
                        error: None,
                    })
                    .await;
            }
            Err(e) => {
                let _ = tx
                    .send(ChatChunk {
                        session_id: req.session_id.clone(),
                        delta: String::new(),
                        tool: None,
                        done: true,
                        stopped: false,
                        needs_continue: false,
                        error: Some(ApiError::new("internal", e.clone())),
                    })
                    .await;
            }
        }
        outcome
    }

    pub async fn stop(&self, session_id: &str) -> bool {
        self.tracker.stop(session_id, "")
    }

    pub async fn stop_in(&self, session_id: &str, cwd: &str) -> bool {
        self.tracker.stop(session_id, cwd)
    }

    pub async fn open_session(&self, session_id: &str, cwd: &str) -> Result<Session, String> {
        if !session_id.trim().is_empty() {
            let sess = self
                .transcripts
                .load(cwd, session_id)
                .map_err(|e| e.to_string())?;
            return Ok(Session {
                session_id: sess.meta.session_id.clone(),
                cwd: sess.meta.cwd.clone(),
                provider: sess.meta.provider.clone().unwrap_or_default(),
                model: sess.meta.model.clone().unwrap_or_default(),
            });
        }
        if cwd.trim().is_empty() {
            return Err("cwd is required to create a session".to_string());
        }
        let (entry, _key, provider) = self.resolve_provider("", "")?;
        let sess = self
            .transcripts
            .create(cwd, &entry.name, provider.model())
            .map_err(|e| e.to_string())?;
        Ok(Session {
            session_id: sess.meta.session_id.clone(),
            cwd: sess.meta.cwd.clone(),
            provider: entry.name,
            model: provider.model().to_string(),
        })
    }

    pub async fn list_sessions(&self, cwd: &str, limit: i32) -> Result<Vec<SessionInfo>, String> {
        let limit = if limit < 0 { 0 } else { limit as usize };
        let infos = self
            .transcripts
            .list(cwd, limit)
            .map_err(|e| e.to_string())?;
        Ok(infos.into_iter().map(SessionInfo::from).collect())
    }

    pub async fn session_history(
        &self,
        session_id: &str,
        cwd: &str,
    ) -> Result<Vec<HistoryMessage>, String> {
        if session_id.trim().is_empty() {
            return Err("session id is required".to_string());
        }
        let sess = self
            .transcripts
            .load(cwd, session_id)
            .map_err(|e| e.to_string())?;
        let msgs = sess.history().map_err(|e| e.to_string())?;
        Ok(history_view(&msgs))
    }

    pub async fn delete_session(&self, session_id: &str, cwd: &str) -> Result<bool, String> {
        if session_id.trim().is_empty() {
            return Err("session id is required".to_string());
        }
        self.transcripts
            .delete(cwd, session_id)
            .map_err(|e| e.to_string())?;
        Ok(true)
    }

    pub fn list_providers(&self) -> Vec<ProviderInfo> {
        self.store.provider_infos().unwrap_or_default()
    }

    pub async fn refresh_providers(&self) -> bool {
        if self.refresh_lock.try_lock().is_err() {
            return false;
        }
        // Detach: the catalog fetch can take a while; the UI refetches.
        let data_dir = self.data_dir.clone();
        let keys: Arc<dyn crate::config::KeyStore> = self.store.keys.clone();
        tokio::spawn(async move {
            let store = Store::new(data_dir, keys);
            let _ = refresh_catalog(&store).await;
        });
        true
    }

    pub async fn set_default_provider(&self, name: &str) -> Result<Vec<ProviderInfo>, String> {
        let name = name.trim();
        if name.is_empty() {
            return Err("provider name is required".to_string());
        }
        let _guard = self.providers_lock.lock().unwrap();
        let mut file = self.store.load_providers().map_err(|e| e.to_string())?;
        if !file.providers.iter().any(|p| p.name == name) {
            return Err(format!("provider {name:?} not found"));
        }
        file.default_provider = name.to_string();
        self.store
            .save_providers(&file)
            .map_err(|e| e.to_string())?;
        Ok(self.list_providers())
    }

    pub async fn add_provider(&self, req: ProviderInput) -> Result<ProviderInfo, String> {
        let (name, url, api_type) =
            validate_provider_input(&req, true).map_err(|e| e.to_string())?;
        let key = req.api_key.trim().to_string();
        {
            let _guard = self.providers_lock.lock().unwrap();
            let mut file = self.store.load_providers().map_err(|e| e.to_string())?;
            if file
                .providers
                .iter()
                .any(|p| p.name.eq_ignore_ascii_case(&name))
            {
                return Err(format!("provider {name:?} already exists"));
            }
            file.providers.push(ConfigProvider {
                name: name.clone(),
                api_url: url,
                api_type,
                model: String::new(),
            });
            self.store
                .save_providers(&file)
                .map_err(|e| e.to_string())?;
            self.store
                .keys
                .set(&name, &key)
                .map_err(|e| e.to_string())?;
            self.evict_provider_cache();
        }
        let _ = self.refresh_providers().await;
        self.list_providers()
            .into_iter()
            .find(|p| p.name == name)
            .ok_or_else(|| format!("provider {name:?} not found"))
    }

    pub async fn update_provider(&self, req: ProviderInput) -> Result<ProviderInfo, String> {
        let name = req.name.trim().to_string();
        if name.is_empty() {
            return Err("provider name is required".to_string());
        }
        let (valid_name, url, api_type) =
            validate_provider_input(&req, false).map_err(|e| e.to_string())?;
        {
            let _guard = self.providers_lock.lock().unwrap();
            let mut file = self.store.load_providers().map_err(|e| e.to_string())?;
            let mut found = false;
            for p in &mut file.providers {
                if p.name == name {
                    p.api_url = url.clone();
                    p.api_type = api_type;
                    // Model stays: the picker owns the model choice.
                    found = true;
                }
            }
            if !found {
                return Err(format!("provider {name:?} not found"));
            }
            // An empty key keeps the stored one.
            if !req.api_key.trim().is_empty() {
                self.store
                    .keys
                    .set(&name, req.api_key.trim())
                    .map_err(|e| e.to_string())?;
            }
            self.store
                .save_providers(&file)
                .map_err(|e| e.to_string())?;
            self.evict_provider_cache();
        }
        let _ = self.refresh_providers().await;
        self.list_providers()
            .into_iter()
            .find(|p| p.name == valid_name)
            .ok_or_else(|| format!("provider {name:?} not found"))
    }

    pub fn get_workspace(&self) -> String {
        self.store.workspace()
    }

    pub async fn set_workspace(&self, path: &str) -> Result<String, String> {
        let path = path.trim();
        let mut cleaned = String::new();
        if !path.is_empty() {
            let abs = if std::path::Path::new(path).is_absolute() {
                PathBuf::from(path)
            } else {
                std::env::current_dir()
                    .map_err(|e| format!("workspace {path:?} is not reachable: {e}"))?
                    .join(path)
            };
            let abs = clean_path(&abs);
            let info = std::fs::metadata(&abs)
                .map_err(|e| format!("workspace {path:?} is not reachable: {e}"))?;
            if !info.is_dir() {
                return Err(format!("workspace {path:?} is not a directory"));
            }
            cleaned = abs.display().to_string();
        }
        let _guard = self.providers_lock.lock().unwrap();
        let mut file = self.store.load_providers().map_err(|e| e.to_string())?;
        if file.providers.is_empty() {
            file = ensure_seeded(default_providers_file());
        }
        file.workspace = cleaned.clone();
        self.store
            .save_providers(&file)
            .map_err(|e| e.to_string())?;
        Ok(cleaned)
    }

    pub fn fs_home(&self) -> String {
        directories::UserDirs::new()
            .map(|d| d.home_dir().to_string_lossy().to_string())
            .unwrap_or_else(|| ".".to_string())
    }

    pub async fn fs_list(&self, path: &str) -> Result<FsList, String> {
        let path = path.trim();
        let target = if path.is_empty() {
            self.fs_home()
        } else {
            path.to_string()
        };
        let abs = if std::path::Path::new(&target).is_absolute() {
            PathBuf::from(&target)
        } else {
            std::env::current_dir()
                .map_err(|e| format!("path {target:?} is not valid: {e}"))?
                .join(&target)
        };
        let abs = clean_path(&abs);
        let entries = std::fs::read_dir(&abs).map_err(|e| {
            use std::io::ErrorKind;
            match e.kind() {
                ErrorKind::NotFound => format!("path {:?} does not exist", abs.display()),
                ErrorKind::PermissionDenied => {
                    format!("path {:?} cannot be read: permission denied", abs.display())
                }
                _ => format!("path {:?} cannot be listed: {e}", abs.display()),
            }
        })?;
        let mut directories = Vec::new();
        for entry in entries.flatten() {
            if entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
                // Skip Windows recycle/system noise; harmless elsewhere.
                let name = entry.file_name().to_string_lossy().to_string();
                match name.to_lowercase().as_str() {
                    "system volume information" | "$recycle.bin" | "recovery" => continue,
                    _ => {}
                }
                directories.push(FsDirectory {
                    name,
                    path: entry.path().display().to_string(),
                });
            }
        }
        directories.sort_by(|a, b| a.name.cmp(&b.name));
        let parent = abs
            .parent()
            .map(|p| p.display().to_string())
            .unwrap_or_default();
        Ok(FsList {
            current_path: abs.display().to_string(),
            parent_path: parent,
            directories,
        })
    }
}

fn clean_path(path: &std::path::Path) -> PathBuf {
    use std::path::Component;
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

async fn refresh_catalog(store: &Store) -> Result<(), String> {
    let file = store.load_providers().map_err(|e| e.to_string())?;
    let mut catalog = store.load_models().map_err(|e| e.to_string())?;
    for p in &file.providers {
        let key = store
            .keys
            .get(&p.name)
            .map_err(|e| e.to_string())?
            .unwrap_or_default();
        // The model catalog is public on OpenAI-dialect endpoints, so
        // listing works before any key is saved; private endpoints just
        // refuse and are skipped like before.
        let list_key = if is_placeholder_key(&key) {
            "public-listing".to_string()
        } else {
            key
        };
        let provider = match build_provider(
            p.api_type,
            p.name.clone(),
            p.model.clone(),
            p.api_url.clone(),
            list_key,
            Duration::from_secs(30),
        ) {
            Ok(provider) => provider,
            Err(_) => continue,
        };
        match provider.list_models().await {
            Ok(mut models) => {
                models.sort();
                let entry = catalog.providers.entry(p.name.clone()).or_default();
                entry.all_models = models;
                if entry.enabled_models.is_empty() {
                    entry.all_models.clone_into(&mut entry.enabled_models);
                }
            }
            Err(_) => continue,
        }
    }
    store.save_models(&catalog).map_err(|e| e.to_string())?;
    Ok(())
}

#[allow(dead_code)]
fn _api_type_used(_: ApiType) {}
#[allow(dead_code)]
fn _opencode_urls() -> (&'static str, &'static str) {
    (OPENCODE_BASE_URL, OPENROUTER_BASE_URL)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::MemoryKeyStore;

    fn runtime() -> (tempfile::TempDir, AgentRuntime) {
        let dir = tempfile::tempdir().unwrap();
        let rt = AgentRuntime::with_key_store(
            dir.path().to_path_buf(),
            Config::new(dir.path().to_path_buf()),
            Arc::new(MemoryKeyStore::new()),
        );
        // dir moved out to keep data alive.
        (dir, rt)
    }

    #[tokio::test]
    async fn open_list_history_delete_session() {
        let (_dir, rt) = runtime();
        rt.store.keys.set("openrouter", "sk-test").unwrap();
        let sess = rt.open_session("", "/tmp/proj").await.unwrap();
        assert!(!sess.session_id.is_empty());
        let list = rt.list_sessions("/tmp/proj", 100).await.unwrap();
        assert_eq!(list.len(), 1);
        let history = rt
            .session_history(&sess.session_id, "/tmp/proj")
            .await
            .unwrap();
        assert!(history.is_empty());
        assert!(rt
            .delete_session(&sess.session_id, "/tmp/proj")
            .await
            .unwrap());
    }

    #[tokio::test]
    async fn concurrent_turn_rejected() {
        let (_dir, rt) = runtime();
        let token = CancellationToken::new();
        assert!(rt.tracker.track("s", "/tmp", token));
        assert!(!rt.tracker.track("s", "/tmp", CancellationToken::new()));
    }

    #[tokio::test]
    async fn full_turn_with_mock_provider() {
        let server = wiremock::MockServer::start().await;
        wiremock::Mock::given(wiremock::matchers::method("POST"))
            .respond_with(
                wiremock::ResponseTemplate::new(200).set_body_json(
                    serde_json::json!({"choices": [{"message": {"content": "done"}}]}),
                ),
            )
            .mount(&server)
            .await;
        let (_dir, rt) = runtime();
        rt.store.keys.set("openrouter", "sk-test").unwrap();
        // Point openrouter at the mock server.
        {
            let _guard = rt.providers_lock.lock().unwrap();
            let mut file = rt.store.load_providers().unwrap();
            for p in &mut file.providers {
                if p.name == "openrouter" {
                    p.api_url = server.uri();
                }
            }
            rt.store.save_providers(&file).unwrap();
        }
        let sess = rt.open_session("", "/tmp/proj").await.unwrap();
        assert_eq!(sess.provider, "openrouter");
        let (tx, _rx) = mpsc::channel(64);
        let req = ChatRequest {
            session_id: sess.session_id.clone(),
            message: "hi".into(),
            cwd: "/tmp/proj".into(),
            provider: None,
            model: None,
            system_prompt: None,
            resume: false,
        };
        let outcome = rt.chat(req, tx).await.unwrap();
        assert_eq!(outcome.reply, "done");
        let history = rt
            .session_history(&sess.session_id, "/tmp/proj")
            .await
            .unwrap();
        assert_eq!(history.len(), 2);
    }
}
