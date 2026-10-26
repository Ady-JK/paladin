// Live validation against OpenRouter (ignored by default).
// Run: OPENROUTER_KEY=... cargo test -p agent-core --test live_openrouter -- --ignored --nocapture
// The key stays in the environment; nothing is written to the repo.

use std::sync::Arc;

use agent_core::config::{Config, KeyStore, MemoryKeyStore};
use agent_core::runtime::AgentRuntime;
use agent_core::turn::ChatRequest;
use tokio::sync::mpsc;

#[tokio::test]
#[ignore]
async fn live_openrouter_gemma() {
    let key = std::env::var("OPENROUTER_KEY").expect("OPENROUTER_KEY must be set");
    assert!(!key.trim().is_empty(), "OPENROUTER_KEY must not be empty");
    let dir = tempfile::tempdir().unwrap();
    let keys = Arc::new(MemoryKeyStore::new());
    keys.set("openrouter", key.trim()).unwrap();
    let rt = AgentRuntime::with_key_store(
        dir.path().to_path_buf(),
        Config::new(dir.path().to_path_buf()),
        keys,
    );
    let ws = dir.path().join("ws");
    std::fs::create_dir_all(&ws).unwrap();
    let cwd = ws.display().to_string();
    let sess = rt.open_session("", &cwd).await.expect("open session");
    assert_eq!(sess.provider, "openrouter");
    let (tx, rx) = mpsc::channel(128);
    let req = ChatRequest {
        session_id: sess.session_id.clone(),
        message: "Reply with exactly: paladin-live-ok".to_string(),
        cwd: cwd.clone(),
        provider: None,
        model: Some("google/gemma-4-31b-it:free".to_string()),
        system_prompt: None,
        resume: false,
    };
    let outcome = rt.chat(req, tx).await.expect("live chat");
    drop(rx);
    assert!(!outcome.stopped, "turn was stopped");
    assert!(
        outcome.reply.to_lowercase().contains("paladin-live-ok"),
        "unexpected reply: {}",
        outcome.reply.chars().take(200).collect::<String>()
    );
    let history = rt
        .session_history(&sess.session_id, &cwd)
        .await
        .expect("history");
    assert!(history.len() >= 2, "history has {} messages", history.len());
}
