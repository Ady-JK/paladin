// Turn-level integration tests: tool round-trip, round-cap resume, stop.
// A fake provider scripts rounds; the real tool registry runs the calls.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use agent_core::inference::provider::{
    build_provider, Chunk, LlmMessage, ToolCall, ToolDefinition, ToolResponse,
};
use agent_core::inference::Provider;
use agent_core::tools::{builtin_registry, shell::ShellTool};
use agent_core::transcript::{Message, Role, Session, Store};
use agent_core::turn::{Agent, ChatChunk};
use async_trait::async_trait;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

struct Script {
    rounds: Mutex<Vec<Round>>,
}

struct Round {
    text: String,
    calls: Vec<ToolCall>,
}

#[async_trait]
impl Provider for Script {
    fn name(&self) -> &str {
        "script"
    }
    fn api_type(&self) -> agent_core::config::ApiType {
        agent_core::config::ApiType::Openai
    }
    fn model(&self) -> &str {
        "script-model"
    }
    async fn complete_tools(
        &self,
        _system: &str,
        _messages: &[LlmMessage],
        _tools: &[ToolDefinition],
        _session_id: &str,
    ) -> Result<ToolResponse, agent_core::error::ProviderError> {
        let mut rounds = self.rounds.lock().unwrap();
        if rounds.is_empty() {
            return Ok(ToolResponse {
                content: String::new(),
                tool_calls: Vec::new(),
            });
        }
        let round = rounds.remove(0);
        Ok(ToolResponse {
            content: round.text,
            tool_calls: round.calls,
        })
    }
    async fn stream_tools(
        &self,
        system: &str,
        messages: &[LlmMessage],
        tools: &[ToolDefinition],
        session_id: &str,
        tx: &mpsc::Sender<Chunk>,
        cancel: &CancellationToken,
    ) -> Result<(), agent_core::error::ProviderError> {
        let resp = self
            .complete_tools(system, messages, tools, session_id)
            .await?;
        if cancel.is_cancelled() {
            return Err(agent_core::error::ProviderError::Cancelled);
        }
        if !resp.content.is_empty() {
            let _ = tx.send(Chunk::text(resp.content.clone())).await;
        }
        if !resp.tool_calls.is_empty() {
            let _ = tx
                .send(Chunk {
                    tool_calls: resp.tool_calls.clone(),
                    ..Default::default()
                })
                .await;
        }
        Ok(())
    }
    async fn list_models(&self) -> Result<Vec<String>, agent_core::error::ProviderError> {
        Ok(vec![])
    }
}

fn test_session(workspace: &std::path::Path) -> (tempfile::TempDir, Session, Store) {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path().join("sessions"));
    let sess = store
        .create(&workspace.display().to_string(), "p", "m")
        .unwrap();
    (dir, sess, store)
}

fn drain(rx: &mut mpsc::Receiver<ChatChunk>) -> Vec<ChatChunk> {
    let mut out = Vec::new();
    while let Ok(chunk) = rx.try_recv() {
        out.push(chunk);
    }
    out
}

#[tokio::test]
async fn tool_round_trip_reads_file() {
    let ws = tempfile::tempdir().unwrap();
    let ws_path = ws.path().join("ws");
    std::fs::create_dir_all(&ws_path).unwrap();
    std::fs::write(ws_path.join("note.txt"), "secret recipe\n").unwrap();
    let (_dir, sess, _store) = test_session(&ws_path);
    let session_id = sess.meta.session_id.clone();

    let script = Arc::new(Script {
        rounds: Mutex::new(vec![
            Round {
                text: "looking".into(),
                calls: vec![ToolCall {
                    id: "c1".into(),
                    name: "read".into(),
                    arguments: r#"{"path":"workspace/note.txt"}"#.into(),
                }],
            },
            Round {
                text: "saw it".into(),
                calls: vec![],
            },
        ]),
    });
    let provider: Arc<dyn Provider> = script;
    let tools = Arc::new(builtin_registry());
    let shell = Arc::new(ShellTool::new());
    let cancel = CancellationToken::new();
    let history: Vec<Message> = vec![];
    let tool_defs: Vec<ToolDefinition> = tools
        .definitions()
        .into_iter()
        .map(|d| ToolDefinition {
            name: d.name,
            description: d.description,
            parameters: d.parameters,
        })
        .collect();
    let mut agent = Agent::new(
        sess,
        provider,
        tools,
        shell,
        cancel,
        "sys".into(),
        10,
        history,
        tool_defs,
    );
    let (tx, mut rx) = mpsc::channel(64);
    let outcome = agent
        .run_turn("read the file".into(), false, &tx)
        .await
        .unwrap();
    assert_eq!(outcome.session_id, session_id);
    assert!(outcome.reply.contains("saw it"));
    assert!(!outcome.stopped);
    assert!(!outcome.needs_continue);
    let chunks = drain(&mut rx);
    assert!(chunks
        .iter()
        .any(|c| c.tool.as_ref().is_some_and(|t| t.status == "running")));
    assert!(chunks
        .iter()
        .any(|c| c.tool.as_ref().is_some_and(|t| t.status == "ok")));
    // Transcript persisted: user + assistant + tool + assistant.
    let msgs = _store
        .load(&ws_path.display().to_string(), &session_id)
        .unwrap()
        .history()
        .unwrap();
    assert_eq!(msgs.len(), 4);
    assert!(msgs.iter().any(|m| m.role == Role::Tool));
}

#[tokio::test]
async fn round_cap_sets_needs_continue_and_resumes() {
    let ws = tempfile::tempdir().unwrap();
    let ws_path = ws.path().join("ws");
    std::fs::create_dir_all(&ws_path).unwrap();
    let (_dir, sess, store) = test_session(&ws_path);
    let session_id = sess.meta.session_id.clone();

    let script = Arc::new(Script {
        rounds: Mutex::new(vec![
            Round {
                text: String::new(),
                calls: vec![ToolCall {
                    id: "c1".into(),
                    name: "read".into(),
                    arguments: r#"{"path":"workspace/missing.txt"}"#.into(),
                }],
            },
            Round {
                text: "finished".into(),
                calls: vec![],
            },
        ]),
    });
    let tools = Arc::new(builtin_registry());
    let shell = Arc::new(ShellTool::new());
    let tool_defs: Vec<ToolDefinition> = tools
        .definitions()
        .into_iter()
        .map(|d| ToolDefinition {
            name: d.name,
            description: d.description,
            parameters: d.parameters,
        })
        .collect();

    let provider: Arc<dyn Provider> = script.clone();
    let mut agent = Agent::new(
        sess,
        provider,
        tools.clone(),
        shell.clone(),
        CancellationToken::new(),
        "sys".into(),
        1,
        vec![],
        tool_defs.clone(),
    );
    let (tx, _rx) = mpsc::channel(64);
    let outcome = agent.run_turn("go".into(), false, &tx).await.unwrap();
    assert!(outcome.needs_continue);

    // Resume from the persisted transcript.
    let sess = store
        .load(&ws_path.display().to_string(), &session_id)
        .unwrap();
    let history = sess.history().unwrap();
    assert!(!history.is_empty());
    let provider: Arc<dyn Provider> = script;
    let mut agent = Agent::new(
        sess,
        provider,
        tools,
        shell,
        CancellationToken::new(),
        "sys".into(),
        10,
        history,
        tool_defs,
    );
    let (tx, _rx) = mpsc::channel(64);
    let outcome = agent.run_turn(String::new(), true, &tx).await.unwrap();
    assert!(!outcome.needs_continue);
    assert!(outcome.reply.contains("finished"));
}

#[tokio::test]
async fn stop_persists_partial_transcript() {
    struct Blocker;
    #[async_trait]
    impl Provider for Blocker {
        fn name(&self) -> &str {
            "blocker"
        }
        fn api_type(&self) -> agent_core::config::ApiType {
            agent_core::config::ApiType::Openai
        }
        fn model(&self) -> &str {
            "m"
        }
        async fn complete_tools(
            &self,
            _s: &str,
            _m: &[LlmMessage],
            _t: &[ToolDefinition],
            _s2: &str,
        ) -> Result<ToolResponse, agent_core::error::ProviderError> {
            Ok(ToolResponse::default())
        }
        async fn stream_tools(
            &self,
            _s: &str,
            _m: &[LlmMessage],
            _t: &[ToolDefinition],
            _s2: &str,
            tx: &mpsc::Sender<Chunk>,
            cancel: &CancellationToken,
        ) -> Result<(), agent_core::error::ProviderError> {
            let _ = tx.send(Chunk::text("partial")).await;
            cancel.cancelled().await;
            Err(agent_core::error::ProviderError::Cancelled)
        }
        async fn list_models(&self) -> Result<Vec<String>, agent_core::error::ProviderError> {
            Ok(vec![])
        }
    }

    let ws = tempfile::tempdir().unwrap();
    let ws_path = ws.path().join("ws");
    std::fs::create_dir_all(&ws_path).unwrap();
    let (_dir, sess, store) = test_session(&ws_path);
    let session_id = sess.meta.session_id.clone();
    let tools = Arc::new(builtin_registry());
    let shell = Arc::new(ShellTool::new());
    let tool_defs: Vec<ToolDefinition> = vec![];
    let cancel = CancellationToken::new();
    let cancel_clone = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(200)).await;
        cancel_clone.cancel();
    });
    let provider: Arc<dyn Provider> = Arc::new(Blocker);
    let mut agent = Agent::new(
        sess,
        provider,
        tools,
        shell,
        cancel,
        "sys".into(),
        10,
        vec![],
        tool_defs,
    );
    let (tx, _rx) = mpsc::channel(64);
    let outcome = agent.run_turn("go".into(), false, &tx).await.unwrap();
    assert!(outcome.stopped);
    assert!(outcome.reply.contains("partial"));
    let history = store
        .load(&ws_path.display().to_string(), &session_id)
        .unwrap()
        .history()
        .unwrap();
    assert!(history.iter().any(|m| m.content.contains("partial")));
}

#[test]
fn placeholder_keys_rejected_by_factory() {
    let result = build_provider(
        agent_core::config::ApiType::Openai,
        "openrouter".into(),
        "m".into(),
        "https://openrouter.ai/api/v1".into(),
        "replace_me".into(),
        Duration::from_secs(5),
    );
    assert!(result.is_err());
}
