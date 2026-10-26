// lib.rs wires Tauri commands and state.
// In-process only: a single shared AgentRuntime, no daemon.

pub mod commands;

use std::sync::Arc;

use agent_core::{data_dir, AgentRuntime, Config};
use tauri::Runtime;

use commands::{
    add_provider, chat_stream, delete_session, fs_home, fs_list, get_workspace, list_providers,
    list_sessions, open_session, refresh_providers, resolve_workspace, session_history,
    set_default_provider, set_workspace, stop_chat, update_provider,
};

pub fn configure<R: Runtime>(builder: tauri::Builder<R>) -> tauri::Builder<R> {
    let data_dir = data_dir();
    let runtime = Arc::new(AgentRuntime::new(data_dir.clone(), Config::new(data_dir)));
    builder
        .manage(runtime)
        .invoke_handler(tauri::generate_handler![
            list_providers,
            refresh_providers,
            set_default_provider,
            add_provider,
            update_provider,
            get_workspace,
            set_workspace,
            fs_home,
            fs_list,
            open_session,
            resolve_workspace,
            list_sessions,
            session_history,
            delete_session,
            chat_stream,
            stop_chat,
        ])
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    configure(tauri::Builder::default())
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
