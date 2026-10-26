// commands.rs implements the Tauri backend for Paladin.
// Thin wrappers over AgentRuntime; no HTTP, no daemon — in-process only.

use std::sync::Arc;

use agent_core::turn::ChatRequest;
use agent_core::{AgentRuntime, ApiType, Config};
use tauri::{AppHandle, Emitter, Manager, Runtime, State};
use tokio::sync::mpsc;

pub const CHAT_EVENT: &str = "chat://delta";
pub const CHAT_TOOL_EVENT: &str = "chat://tool";

#[derive(Debug, Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatResult {
    pub session_id: String,
    pub reply: String,
    pub stopped: bool,
    pub needs_continue: bool,
}

#[derive(Debug, Clone, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatArgs {
    pub session_id: Option<String>,
    pub message: String,
    pub cwd: String,
    pub provider: Option<String>,
    pub model: Option<String>,
    pub system_prompt: Option<String>,
    pub resume: Option<bool>,
}

pub fn default_runtime() -> Arc<AgentRuntime> {
    let data_dir = agent_core::data_dir();
    agent_core::logging::init(&data_dir);
    Arc::new(AgentRuntime::new(data_dir.clone(), Config::new(data_dir)))
}

#[tauri::command]
pub async fn list_providers(
    runtime: State<'_, Arc<AgentRuntime>>,
) -> Result<Vec<agent_core::config::ProviderInfo>, String> {
    Ok(runtime.list_providers())
}

#[tauri::command]
pub async fn refresh_providers(runtime: State<'_, Arc<AgentRuntime>>) -> Result<bool, String> {
    Ok(runtime.refresh_providers().await)
}

#[tauri::command]
pub async fn set_default_provider(
    runtime: State<'_, Arc<AgentRuntime>>,
    name: String,
) -> Result<Vec<agent_core::config::ProviderInfo>, String> {
    runtime
        .set_default_provider(&name)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn add_provider(
    runtime: State<'_, Arc<AgentRuntime>>,
    name: String,
    api_url: String,
    api_key: String,
    api_type: String,
) -> Result<agent_core::config::ProviderInfo, String> {
    runtime
        .add_provider(agent_core::config::ProviderInput {
            name,
            api_url,
            api_key,
            api_type,
        })
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn update_provider(
    runtime: State<'_, Arc<AgentRuntime>>,
    name: String,
    api_url: String,
    api_key: String,
    api_type: String,
) -> Result<agent_core::config::ProviderInfo, String> {
    runtime
        .update_provider(agent_core::config::ProviderInput {
            name,
            api_url,
            api_key,
            api_type,
        })
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn get_workspace(runtime: State<'_, Arc<AgentRuntime>>) -> Result<String, String> {
    Ok(runtime.get_workspace())
}

#[tauri::command]
pub async fn set_workspace(
    runtime: State<'_, Arc<AgentRuntime>>,
    path: String,
) -> Result<String, String> {
    runtime
        .set_workspace(&path)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn fs_home(runtime: State<'_, Arc<AgentRuntime>>) -> Result<String, String> {
    Ok(runtime.fs_home())
}

#[tauri::command]
pub async fn fs_list(
    runtime: State<'_, Arc<AgentRuntime>>,
    path: Option<String>,
) -> Result<agent_core::runtime::FsList, String> {
    runtime
        .fs_list(path.as_deref().unwrap_or_default())
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn open_session<R: Runtime>(
    app: AppHandle<R>,
    runtime: State<'_, Arc<AgentRuntime>>,
    session_id: Option<String>,
    cwd: Option<String>,
) -> Result<agent_core::runtime::Session, String> {
    let cwd = workspace(&app, cwd);
    runtime
        .open_session(session_id.as_deref().unwrap_or_default(), &cwd)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn list_sessions(
    runtime: State<'_, Arc<AgentRuntime>>,
    cwd: Option<String>,
    limit: Option<i32>,
) -> Result<Vec<agent_core::session::SessionInfo>, String> {
    runtime
        .list_sessions(cwd.as_deref().unwrap_or_default(), limit.unwrap_or(100))
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn session_history(
    runtime: State<'_, Arc<AgentRuntime>>,
    session_id: String,
    cwd: Option<String>,
) -> Result<Vec<agent_core::session::HistoryMessage>, String> {
    runtime
        .session_history(&session_id, cwd.as_deref().unwrap_or_default())
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn delete_session(
    runtime: State<'_, Arc<AgentRuntime>>,
    session_id: String,
    cwd: Option<String>,
) -> Result<bool, String> {
    runtime
        .delete_session(&session_id, cwd.as_deref().unwrap_or_default())
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn resolve_workspace<R: Runtime>(
    app: AppHandle<R>,
    cwd: Option<String>,
) -> Result<String, String> {
    Ok(workspace(&app, cwd))
}

#[tauri::command]
pub async fn chat_stream<R: Runtime>(
    app: AppHandle<R>,
    runtime: State<'_, Arc<AgentRuntime>>,
    args: ChatArgs,
) -> Result<ChatResult, String> {
    let (tx, mut rx) = mpsc::channel(128);
    let req = ChatRequest {
        session_id: args.session_id.clone().unwrap_or_default(),
        message: args.message.clone(),
        cwd: args.cwd.clone(),
        provider: args.provider.clone(),
        model: args.model.clone(),
        system_prompt: args.system_prompt.clone(),
        resume: args.resume.unwrap_or(false),
    };
    let runtime_clone = runtime.inner().clone();
    let chat = async move { runtime_clone.chat(req, tx).await };
    let pump = async move {
        let mut reply = String::new();
        let mut session_id = args.session_id.clone().unwrap_or_default();
        let mut stopped = false;
        let mut needs_continue = false;
        while let Some(chunk) = rx.recv().await {
            if let Some(error) = chunk.error {
                return Err(error.message);
            }
            if session_id.is_empty() {
                session_id = chunk.session_id.clone();
            }
            if !chunk.delta.is_empty() {
                reply.push_str(&chunk.delta);
                let _ = app.emit(CHAT_EVENT, &chunk.delta);
            }
            if let Some(tool) = chunk.tool {
                let _ = app.emit(CHAT_TOOL_EVENT, &tool);
            }
            if chunk.stopped {
                stopped = true;
            }
            if chunk.needs_continue {
                needs_continue = true;
            }
            if chunk.done {
                break;
            }
        }
        Ok(ChatResult {
            session_id,
            reply,
            stopped,
            needs_continue,
        })
    };
    let (outcome, pumped) = tokio::join!(chat, pump);
    outcome.map_err(|e| e.to_string())?;
    pumped
}

#[tauri::command]
pub async fn stop_chat(
    runtime: State<'_, Arc<AgentRuntime>>,
    session_id: Option<String>,
    cwd: Option<String>,
) -> Result<bool, String> {
    Ok(runtime
        .stop_in(
            session_id.as_deref().unwrap_or_default(),
            cwd.as_deref().unwrap_or_default(),
        )
        .await)
}

fn workspace<R: Runtime>(app: &AppHandle<R>, cwd: Option<String>) -> String {
    match cwd.filter(|c| !c.trim().is_empty()) {
        Some(cwd) => cwd.trim().to_string(),
        None => app
            .path()
            .home_dir()
            .map(|dir| dir.to_string_lossy().to_string())
            .unwrap_or_else(|_| ".".to_string()),
    }
}

#[allow(dead_code)]
fn _assert_api_type_used(_: ApiType) {}
