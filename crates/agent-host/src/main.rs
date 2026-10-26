// agent-host is the stdio sidecar the Electron main process spawns.
// Protocol is JSONL: one JSON object per line.
//
// Electron -> Rust (stdin):
//   {"id":1,"method":"list_providers","params":{}}
//   {"id":2,"method":"chat","params":{
//     "sessionId":"","message":"hi","cwd":"/tmp/proj",
//     "provider":null,"model":null,"systemPrompt":null,"resume":false}}
//
// Rust -> Electron (stdout):
//   unary:  {"id":1,"ok":true,"result":[...]} | {"id":1,"ok":false,"error":"..."}
//   stream: {"event":"chat/delta","streamId":2,"sessionId":"...","delta":"..."}
//           {"event":"chat/tool","streamId":2,"sessionId":"...","tool":{...}}
//           {"id":2,"ok":true,"result":{"sessionId":"...","reply":"...",
//            "stopped":false,"needsContinue":false}}
//
// Logs always go to stderr so stdout stays pure JSONL.

use std::sync::Arc;

use agent_core::turn::ChatRequest;
use agent_core::{data_dir, AgentRuntime, Config};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::sync::{mpsc, Mutex};

#[derive(Debug, Deserialize)]
struct Request {
    id: serde_json::Value,
    method: String,
    #[serde(default)]
    params: serde_json::Value,
}

#[derive(Debug, Serialize)]
struct Response {
    id: serde_json::Value,
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    result: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct ChatParams {
    #[serde(default)]
    session_id: Option<String>,
    #[serde(default)]
    message: String,
    #[serde(default)]
    cwd: String,
    #[serde(default)]
    provider: Option<String>,
    #[serde(default)]
    model: Option<String>,
    #[serde(default)]
    system_prompt: Option<String>,
    #[serde(default)]
    resume: Option<bool>,
}

fn param<T: Default + for<'de> Deserialize<'de>>(v: &serde_json::Value) -> T {
    serde_json::from_value(v.clone()).unwrap_or_default()
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct StrField {
    #[serde(default)]
    name: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    cwd: Option<String>,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let data_dir = data_dir();
    agent_core::logging::init(&data_dir);
    let runtime = Arc::new(AgentRuntime::new(data_dir.clone(), Config::new(data_dir)));

    // Ready banner on stderr (never stdout).
    eprintln!("agent-host ready dir={}", runtime.get_workspace());

    let stdin = tokio::io::stdin();
    let mut lines = BufReader::new(stdin).lines();
    let stdout = Arc::new(Mutex::new(tokio::io::stdout()));
    // In-flight chat streams: on stdin EOF we drain these before exit so
    // every request gets its response line even when the pipe closes.
    let mut chats = tokio::task::JoinSet::new();

    while let Some(line) = lines.next_line().await? {
        let line = line.trim().to_string();
        if line.is_empty() {
            continue;
        }
        let req: Request = match serde_json::from_str(&line) {
            Ok(r) => r,
            Err(e) => {
                tracing::warn!("bad request line: {e}");
                continue;
            }
        };
        let rt = runtime.clone();
        let out = stdout.clone();
        if req.method == "chat" {
            // Streaming: run in background so deltas flow while other
            // requests (e.g. stop_chat) are still served.
            chats.spawn(async move {
                run_chat(rt, out, req).await;
            });
        } else {
            let resp = run_unary(&rt, &req).await;
            emit(&out, &resp).await;
        }
    }
    // Reap chat tasks so their final response lines always flush.
    while chats.join_next().await.is_some() {}
    Ok(())
}

async fn emit(out: &Arc<Mutex<tokio::io::Stdout>>, value: &impl Serialize) {
    let mut line = serde_json::to_string(value).unwrap_or_else(|_| "{}".to_string());
    line.push('\n');
    let mut w = out.lock().await;
    let _ = w.write_all(line.as_bytes()).await;
    let _ = w.flush().await;
}

async fn run_unary(rt: &Arc<AgentRuntime>, req: &Request) -> Response {
    let ok = |result: serde_json::Value| Response {
        id: req.id.clone(),
        ok: true,
        result: Some(result),
        error: None,
    };
    let err = |e: String| Response {
        id: req.id.clone(),
        ok: false,
        result: None,
        error: Some(e),
    };
    match req.method.as_str() {
        "ping" => ok(serde_json::json!({"ok": true})),
        "list_providers" => ok(serde_json::to_value(rt.list_providers()).unwrap_or_default()),
        "refresh_providers" => ok(serde_json::json!(rt.refresh_providers().await)),
        "set_default_provider" => {
            let p: StrField = param(&req.params);
            match rt.set_default_provider(&p.name).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "add_provider" => {
            let v = &req.params;
            let input = agent_core::config::ProviderInput {
                name: v
                    .get("name")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_url: v
                    .get("apiUrl")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_key: v
                    .get("apiKey")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_type: v
                    .get("apiType")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
            };
            match rt.add_provider(input).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "update_provider" => {
            let v = &req.params;
            let input = agent_core::config::ProviderInput {
                name: v
                    .get("name")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_url: v
                    .get("apiUrl")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_key: v
                    .get("apiKey")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
                api_type: v
                    .get("apiType")
                    .and_then(|s| s.as_str())
                    .unwrap_or_default()
                    .to_string(),
            };
            match rt.update_provider(input).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "get_workspace" => ok(serde_json::json!(rt.get_workspace())),
        "set_workspace" => {
            let p: StrField = param(&req.params);
            match rt.set_workspace(&p.path).await {
                Ok(v) => ok(serde_json::json!(v)),
                Err(e) => err(e),
            }
        }
        "resolve_workspace" => {
            let p: StrField = param(&req.params);
            let cwd = p.cwd.unwrap_or_default();
            if !cwd.trim().is_empty() {
                ok(serde_json::json!(cwd.trim()))
            } else {
                ok(serde_json::json!(rt.fs_home()))
            }
        }
        "fs_home" => ok(serde_json::json!(rt.fs_home())),
        "fs_list" => {
            let path = req
                .params
                .get("path")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            match rt.fs_list(path).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "open_session" => {
            let sid = req
                .params
                .get("sessionId")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            let cwd = req
                .params
                .get("cwd")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            match rt.open_session(sid, cwd).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "list_sessions" => {
            let cwd = req
                .params
                .get("cwd")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            let limit = req
                .params
                .get("limit")
                .and_then(|n| n.as_i64())
                .unwrap_or(100) as i32;
            match rt.list_sessions(cwd, limit).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "session_history" => {
            let sid = req
                .params
                .get("sessionId")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            let cwd = req
                .params
                .get("cwd")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            match rt.session_history(sid, cwd).await {
                Ok(v) => ok(serde_json::to_value(v).unwrap_or_default()),
                Err(e) => err(e),
            }
        }
        "delete_session" => {
            let sid = req
                .params
                .get("sessionId")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            let cwd = req
                .params
                .get("cwd")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            match rt.delete_session(sid, cwd).await {
                Ok(v) => ok(serde_json::json!(v)),
                Err(e) => err(e),
            }
        }
        "stop_chat" => {
            let sid = req
                .params
                .get("sessionId")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            let cwd = req
                .params
                .get("cwd")
                .and_then(|s| s.as_str())
                .unwrap_or_default();
            ok(serde_json::json!(rt.stop_in(sid, cwd).await))
        }
        other => err(format!("unknown method {other:?}")),
    }
}

async fn run_chat(rt: Arc<AgentRuntime>, out: Arc<Mutex<tokio::io::Stdout>>, req: Request) {
    let stream_id = req.id.clone();
    let p: ChatParams = param(&req.params);
    let (tx, mut rx) = mpsc::channel(128);
    let chat_req = ChatRequest {
        session_id: p.session_id.clone().unwrap_or_default(),
        message: p.message.clone(),
        cwd: p.cwd.clone(),
        provider: p.provider.clone(),
        model: p.model.clone(),
        system_prompt: p.system_prompt.clone(),
        resume: p.resume.unwrap_or(false),
    };
    let runner = rt.clone();
    let chat = tokio::spawn(async move { runner.chat(chat_req, tx).await });
    let mut reply = String::new();
    let mut session_id = p.session_id.unwrap_or_default();
    let mut stopped = false;
    let mut needs_continue = false;

    while let Some(chunk) = rx.recv().await {
        if let Some(error) = chunk.error {
            emit(
                &out,
                &Response {
                    id: stream_id.clone(),
                    ok: false,
                    result: None,
                    error: Some(error.message),
                },
            )
            .await;
            return;
        }
        if session_id.is_empty() {
            session_id = chunk.session_id.clone();
        }
        if !chunk.delta.is_empty() {
            reply.push_str(&chunk.delta);
            emit(
                &out,
                &serde_json::json!({
                    "event": "chat/delta",
                    "streamId": stream_id,
                    "sessionId": session_id,
                    "delta": chunk.delta,
                }),
            )
            .await;
        }
        if let Some(tool) = chunk.tool {
            emit(
                &out,
                &serde_json::json!({
                    "event": "chat/tool",
                    "streamId": stream_id,
                    "sessionId": session_id,
                    "tool": tool,
                }),
            )
            .await;
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
    match chat.await {
        Ok(Ok(_)) => {
            emit(
                &out,
                &Response {
                    id: stream_id,
                    ok: true,
                    result: Some(serde_json::json!({
                        "sessionId": session_id,
                        "reply": reply,
                        "stopped": stopped,
                        "needsContinue": needs_continue,
                    })),
                    error: None,
                },
            )
            .await;
        }
        Ok(Err(e)) => {
            emit(
                &out,
                &Response {
                    id: stream_id,
                    ok: false,
                    result: None,
                    error: Some(e),
                },
            )
            .await;
        }
        Err(e) => {
            emit(
                &out,
                &Response {
                    id: stream_id,
                    ok: false,
                    result: None,
                    error: Some(format!("chat task failed: {e}")),
                },
            )
            .await;
        }
    }
}
