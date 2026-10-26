// IPC contract tests: drive the real Tauri command layer with a mock
// runtime (temp data dir + in-memory key store). No network, no keychain.

use std::sync::Arc;

use agent_core::config::MemoryKeyStore;
use agent_core::{AgentRuntime, Config};

fn test_app() -> (
    tempfile::TempDir,
    tauri::App<tauri::test::MockRuntime>,
    tauri::WebviewWindow<tauri::test::MockRuntime>,
) {
    let dir = tempfile::tempdir().unwrap();
    let keys = Arc::new(MemoryKeyStore::new());
    let runtime = Arc::new(AgentRuntime::with_key_store(
        dir.path().to_path_buf(),
        Config::new(dir.path().to_path_buf()),
        keys,
    ));
    let app = desktop_app_lib::configure_with_runtime(tauri::test::mock_builder(), runtime)
        .build(tauri::test::mock_context(tauri::test::noop_assets()))
        .unwrap();
    let window = tauri::WebviewWindowBuilder::new(&app, "main", Default::default())
        .build()
        .unwrap();
    (dir, app, window)
}

fn call(
    window: &tauri::WebviewWindow<tauri::test::MockRuntime>,
    cmd: &str,
    body: serde_json::Value,
) -> Result<serde_json::Value, serde_json::Value> {
    tauri::test::get_ipc_response(
        window,
        tauri::webview::InvokeRequest {
            cmd: cmd.to_string(),
            callback: tauri::ipc::CallbackFn(0),
            error: tauri::ipc::CallbackFn(1),
            url: "tauri://localhost".parse().unwrap(),
            body: tauri::ipc::InvokeBody::Json(body),
            headers: Default::default(),
            invoke_key: tauri::test::INVOKE_KEY.to_string(),
        },
    )
    .map(|b| b.deserialize::<serde_json::Value>().unwrap())
}

#[test]
fn providers_seed_and_accept_keys() {
    let (_dir, _app, window) = test_app();
    let providers = call(&window, "list_providers", serde_json::json!({})).unwrap();
    let names: Vec<&str> = providers
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p["name"].as_str().unwrap())
        .collect();
    assert!(names.contains(&"Opencode Go"));
    assert!(names.contains(&"openrouter"));
    assert!(providers
        .as_array()
        .unwrap()
        .iter()
        .all(|p| p["hasKey"] == false));

    let added = call(
        &window,
        "add_provider",
        serde_json::json!({
            "name": "local",
            "apiUrl": "http://localhost:11434/v1",
            "apiKey": "ollama",
            "apiType": "openai",
        }),
    )
    .unwrap();
    assert_eq!(added["hasKey"], true);
}

#[test]
fn session_crud_round_trip() {
    let (_dir, _app, window) = test_app();
    // add_provider stores the key; then open_session can resolve.
    call(
        &window,
        "add_provider",
        serde_json::json!({
            "name": "openrouter",
            "apiUrl": "https://openrouter.ai/api/v1",
            "apiKey": "sk-test",
            "apiType": "openai",
        }),
    )
    .unwrap_err(); // already exists (seeded) -> duplicate error
    let updated = call(
        &window,
        "update_provider",
        serde_json::json!({
            "name": "openrouter",
            "apiUrl": "https://openrouter.ai/api/v1",
            "apiKey": "sk-test",
            "apiType": "openai",
        }),
    )
    .unwrap();
    assert_eq!(updated["hasKey"], true);

    let opened = call(
        &window,
        "open_session",
        serde_json::json!({"sessionId": null, "cwd": "/tmp/paladin-ipc"}),
    )
    .unwrap();
    let session_id = opened["sessionId"].as_str().unwrap().to_string();
    assert!(!session_id.is_empty());

    let list = call(
        &window,
        "list_sessions",
        serde_json::json!({"cwd": "/tmp/paladin-ipc", "limit": 100}),
    )
    .unwrap();
    assert_eq!(list.as_array().unwrap().len(), 1);

    let history = call(
        &window,
        "session_history",
        serde_json::json!({"sessionId": session_id, "cwd": "/tmp/paladin-ipc"}),
    )
    .unwrap();
    assert!(history.as_array().unwrap().is_empty());

    let deleted = call(
        &window,
        "delete_session",
        serde_json::json!({"sessionId": session_id, "cwd": "/tmp/paladin-ipc"}),
    )
    .unwrap();
    assert_eq!(deleted, true);
}

#[test]
fn fs_list_and_workspace_validation() {
    let (_dir, _app, window) = test_app();
    let home = call(&window, "fs_home", serde_json::json!({})).unwrap();
    assert!(!home.as_str().unwrap().is_empty());

    let err = call(
        &window,
        "set_workspace",
        serde_json::json!({"path": "/no/such/dir-xyz"}),
    )
    .unwrap_err();
    assert!(err.as_str().unwrap().contains("not reachable"));

    let list = call(&window, "fs_list", serde_json::json!({"path": null})).unwrap();
    assert!(!list["currentPath"].as_str().unwrap().is_empty());
}
