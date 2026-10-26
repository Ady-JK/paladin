use std::path::PathBuf;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::error::ApiError;
use crate::inference::provider::{Chunk, LlmMessage, ToolCall, ToolDefinition, ToolResponse};
use crate::inference::Provider;
use crate::session::{
    is_notify_message, notify_args, notify_call_id, TOOL_FAILED, TOOL_OK, TOOL_RUNNING,
};
use crate::tools::shell::ShellTool;
use crate::tools::{ToolCall as ToolCallArgs, ToolCtx, ToolRegistry, ToolResult};
use crate::transcript::{Message, Role, Session, ToolCall as StoredCall};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatRequest {
    pub session_id: String,
    pub message: String,
    pub cwd: String,
    #[serde(default)]
    pub provider: Option<String>,
    #[serde(default)]
    pub model: Option<String>,
    #[serde(default)]
    pub system_prompt: Option<String>,
    #[serde(default)]
    pub resume: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolActivity {
    pub id: String,
    pub name: String,
    pub arguments: String,
    pub result: String,
    pub status: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatChunk {
    pub session_id: String,
    pub delta: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool: Option<ToolActivity>,
    #[serde(default)]
    pub done: bool,
    #[serde(default)]
    pub stopped: bool,
    #[serde(default)]
    pub needs_continue: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ApiError>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChatOutcome {
    pub session_id: String,
    pub reply: String,
    pub stopped: bool,
    pub needs_continue: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Phase {
    Created,
    Running,
    Done,
    Stopped,
    NeedsContinue,
}

#[derive(Debug)]
enum TurnError {
    Stopped,
    Failed(String),
}

/// One turn of one session. Built per `chat()` call, dropped at exit;
/// nothing survives between turns except the on-disk transcript.
pub struct Agent {
    session: Session,
    provider: Arc<dyn Provider>,
    tools: Arc<ToolRegistry>,
    shell: Arc<ShellTool>,
    cancel: CancellationToken,
    tool_ctx: ToolCtx,
    system: String,
    max_rounds: u32,
    history: Vec<Message>,
    tool_defs: Vec<ToolDefinition>,
    phase: Phase,
    round: u32,
    messages: Vec<LlmMessage>,
    records: Vec<Message>,
    reply: String,
}

impl Agent {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        session: Session,
        provider: Arc<dyn Provider>,
        tools: Arc<ToolRegistry>,
        shell: Arc<ShellTool>,
        cancel: CancellationToken,
        system: String,
        max_rounds: u32,
        history: Vec<Message>,
        tool_defs: Vec<ToolDefinition>,
    ) -> Self {
        let tool_ctx = ToolCtx {
            workspace: PathBuf::from(session.meta.cwd.clone()),
            session_dir: session.dir.clone(),
            cancel: cancel.clone(),
        };
        Self {
            session,
            provider,
            tools,
            shell,
            cancel,
            tool_ctx,
            system,
            max_rounds,
            history,
            tool_defs,
            phase: Phase::Created,
            round: 0,
            messages: Vec::new(),
            records: Vec::new(),
            reply: String::new(),
        }
    }

    pub async fn run_turn(
        &mut self,
        message: String,
        resume: bool,
        tx: &mpsc::Sender<ChatChunk>,
    ) -> Result<ChatOutcome, String> {
        if message.is_empty() && !resume {
            return Err("message is required".to_string());
        }
        self.phase = Phase::Running;
        self.messages = self.model_messages(&message, resume);

        let session_id = self.session.meta.session_id.clone();
        let err = self.run_loop(tx).await;
        let mut stopped = false;
        match err {
            None => {}
            Some(TurnError::Stopped) => stopped = true,
            Some(TurnError::Failed(e)) => {
                let _ = self.persist(&message, resume, true);
                return Err(e);
            }
        }
        self.finalize(stopped);
        self.persist(&message, resume, stopped)?;
        Ok(ChatOutcome {
            session_id,
            reply: self.reply.clone(),
            stopped,
            needs_continue: self.phase == Phase::NeedsContinue,
        })
    }

    fn model_messages(&self, message: &str, resume: bool) -> Vec<LlmMessage> {
        let with_tools = !self.tool_defs.is_empty();
        let mut messages = llm_messages(&self.history, with_tools);
        if with_tools && !resume {
            messages.push(LlmMessage {
                role: "user".to_string(),
                content: message.to_string(),
                ..Default::default()
            });
        }
        messages
    }

    fn stop_requested(&self) -> bool {
        self.cancel.is_cancelled()
    }

    fn needs_continue(&self) -> bool {
        self.round >= self.max_rounds
    }

    fn finish(&mut self, phase: Phase) {
        self.phase = phase;
    }

    fn finalize(&mut self, stopped: bool) {
        if self.phase == Phase::NeedsContinue {
            return;
        }
        if stopped {
            self.finish(Phase::Stopped);
        } else {
            self.finish(Phase::Done);
        }
    }

    fn persist(&self, message: &str, resume: bool, _stopped: bool) -> Result<(), String> {
        if !resume {
            self.session
                .append(Role::User, message)
                .map_err(|e| format!("persist user message: {e}"))?;
        }
        for record in &self.records {
            self.session
                .append_message(record.clone())
                .map_err(|e| format!("persist turn: {e}"))?;
        }
        if self.tool_defs.is_empty() && (!self.reply.is_empty() || resume) {
            self.session
                .append(Role::Assistant, &self.reply)
                .map_err(|e| format!("persist reply: {e}"))?;
        }
        Ok(())
    }

    async fn emit(&self, tx: &mpsc::Sender<ChatChunk>, chunk: ChatChunk) -> Result<(), TurnError> {
        let mut chunk = chunk;
        chunk.session_id = self.session.meta.session_id.clone();
        tx.send(chunk).await.map_err(|_| TurnError::Stopped)
    }

    fn append_delta(&mut self, text: &str) {
        self.reply.push_str(text);
    }

    fn append_assistant(&mut self, content: String, calls: Vec<ToolCall>) {
        self.messages.push(LlmMessage {
            role: "assistant".to_string(),
            content: content.clone(),
            tool_calls: calls.clone(),
            ..Default::default()
        });
        self.records.push(Message {
            role: Role::Assistant,
            content,
            created_at: chrono::Utc::now(),
            tool_calls: Some(
                calls
                    .into_iter()
                    .map(|c| StoredCall {
                        id: c.id,
                        name: c.name,
                        arguments: c.arguments,
                    })
                    .collect(),
            ),
            tool_call_id: None,
            name: None,
            is_error: false,
            is_notify: false,
        });
    }

    fn append_tool_result(&mut self, call: &ToolCall, result: &ToolResult) {
        self.messages.push(LlmMessage {
            role: "tool".to_string(),
            content: result.content.clone(),
            tool_call_id: Some(call.id.clone()),
            name: Some(call.name.clone()),
            ..Default::default()
        });
        self.records.push(Message {
            role: Role::Tool,
            content: result.content.clone(),
            created_at: chrono::Utc::now(),
            tool_calls: None,
            tool_call_id: Some(result.call_id.clone()),
            name: Some(result.name.clone()),
            is_error: result.is_error,
            is_notify: false,
        });
    }

    fn append_notify(&mut self, content: String, call_id: String, is_error: bool) {
        self.messages.push(LlmMessage {
            role: "user".to_string(),
            content: content.clone(),
            ..Default::default()
        });
        self.records.push(Message {
            role: Role::Tool,
            content,
            created_at: chrono::Utc::now(),
            tool_calls: None,
            tool_call_id: Some(call_id),
            name: Some("shell".to_string()),
            is_error,
            is_notify: true,
        });
    }

    async fn run_loop(&mut self, tx: &mpsc::Sender<ChatChunk>) -> Option<TurnError> {
        if self.tool_defs.is_empty() {
            return self.run_plain(tx).await;
        }
        while !self.needs_continue() {
            self.round += 1;
            if self.stop_requested() {
                return Some(TurnError::Stopped);
            }
            if let Err(e) = self.drain_notifications(tx).await {
                return Some(e);
            }
            // Stream one round through a local channel, forwarding to the UI.
            let (ptx, mut prx) = mpsc::channel::<Chunk>(64);
            let provider = self.provider.clone();
            let system = self.system.clone();
            let messages = self.messages.clone();
            let tool_defs = self.tool_defs.clone();
            let session_id = self.session.meta.session_id.clone();
            let cancel = self.cancel.clone();
            let task_session_id = session_id.clone();
            let stream_task = tokio::spawn(async move {
                provider
                    .stream_tools(
                        &system,
                        &messages,
                        &tool_defs,
                        &task_session_id,
                        &ptx,
                        &cancel,
                    )
                    .await
            });
            let mut round_text = String::new();
            let mut calls: Vec<ToolCall> = Vec::new();
            let mut stream_error: Option<String> = None;
            loop {
                tokio::select! {
                    next = prx.recv() => {
                        let Some(chunk) = next else { break };
                        if let Some(err) = chunk.err {
                            stream_error = Some(err);
                            break;
                        }
                        if let Some(notice) = chunk.tool_notice {
                            let activity = ToolActivity {
                                id: notice.id,
                                name: if notice.name.is_empty() { "tool".to_string() } else { notice.name },
                                arguments: "{}".to_string(),
                                result: String::new(),
                                status: TOOL_RUNNING.to_string(),
                            };
                            if self.emit(tx, ChatChunk {
                                session_id: String::new(),
                                delta: String::new(),
                                tool: Some(activity),
                                done: false,
                                stopped: false,
                                needs_continue: false,
                                error: None,
                            }).await.is_err() {
                                stream_error = Some("client went away".to_string());
                                break;
                            }
                        }
                        if !chunk.text.is_empty() {
                            round_text.push_str(&chunk.text);
                            self.append_delta(&chunk.text);
                            if self.emit(tx, ChatChunk {
                                session_id: String::new(),
                                delta: chunk.text,
                                tool: None,
                                done: false,
                                stopped: false,
                                needs_continue: false,
                                error: None,
                            }).await.is_err() {
                                stream_error = Some("client went away".to_string());
                                break;
                            }
                        }
                        if !chunk.tool_calls.is_empty() {
                            calls = chunk.tool_calls;
                        }
                        if self.stop_requested() {
                            break;
                        }
                    }
                    _ = self.cancel.cancelled() => {
                        break;
                    }
                }
            }
            // Settle the provider task; a cancel maps to a stopped turn.
            let task_result = stream_task.await;
            if self.stop_requested() {
                return self.stop_with_partial(&round_text);
            }
            match task_result {
                Ok(Ok(())) => {}
                Ok(Err(e)) => {
                    if matches!(e, crate::error::ProviderError::Cancelled) || self.stop_requested()
                    {
                        return self.stop_with_partial(&round_text);
                    }
                    return Some(TurnError::Failed(format!("provider unavailable: {e}")));
                }
                Err(e) => return Some(TurnError::Failed(format!("provider task failed: {e}"))),
            }
            if let Some(err) = stream_error {
                if err == "client went away" {
                    return self.stop_with_partial(&round_text);
                }
                return Some(TurnError::Failed(format!("provider unavailable: {err}")));
            }
            if self.stop_requested() {
                return self.stop_with_partial(&round_text);
            }
            // Empty stream fallback: one buffered completion.
            if round_text.is_empty() && calls.is_empty() {
                match self
                    .provider
                    .complete_tools(&self.system, &self.messages, &self.tool_defs, &session_id)
                    .await
                {
                    Ok(resp) => {
                        round_text.push_str(&resp.content);
                        self.append_delta(&resp.content);
                        calls = resp.tool_calls;
                        if !resp.content.is_empty()
                            && self
                                .emit(
                                    tx,
                                    ChatChunk {
                                        session_id: String::new(),
                                        delta: resp.content,
                                        tool: None,
                                        done: false,
                                        stopped: false,
                                        needs_continue: false,
                                        error: None,
                                    },
                                )
                                .await
                                .is_err()
                        {
                            return self.stop_with_partial(&round_text);
                        }
                    }
                    Err(e) => {
                        if self.stop_requested() {
                            return self.stop_with_partial(&round_text);
                        }
                        return Some(TurnError::Failed(format!("provider unavailable: {e}")));
                    }
                }
            }
            if calls.is_empty() {
                if !round_text.is_empty() {
                    self.records.push(Message {
                        role: Role::Assistant,
                        content: round_text.clone(),
                        created_at: chrono::Utc::now(),
                        tool_calls: None,
                        tool_call_id: None,
                        name: None,
                        is_error: false,
                        is_notify: false,
                    });
                }
                if self.shell.pending_notify(&session_id)
                    && !self
                        .shell
                        .wait_pending_notify(&session_id, &self.cancel)
                        .await
                {
                    if self.stop_requested() {
                        return Some(TurnError::Stopped);
                    }
                    return self.stop_with_partial(&round_text);
                }
                let drained = self.records.len();
                if let Err(e) = self.drain_notifications(tx).await {
                    return Some(e);
                }
                if self.records.len() != drained {
                    continue;
                }
                if self.stop_requested() {
                    return Some(TurnError::Stopped);
                }
                self.finish(Phase::Done);
                return None;
            }
            let text = std::mem::take(&mut round_text);
            self.append_assistant(text, calls.clone());
            if let Some(e) = self.run_tools_streaming(tx, &calls).await {
                return Some(e);
            }
        }
        self.finish(Phase::NeedsContinue);
        None
    }

    fn stop_with_partial(&mut self, round_text: &str) -> Option<TurnError> {
        if !round_text.is_empty() {
            self.records.push(Message {
                role: Role::Assistant,
                content: round_text.to_string(),
                created_at: chrono::Utc::now(),
                tool_calls: None,
                tool_call_id: None,
                name: None,
                is_error: false,
                is_notify: false,
            });
        }
        Some(TurnError::Stopped)
    }

    async fn run_tools_streaming(
        &mut self,
        tx: &mpsc::Sender<ChatChunk>,
        calls: &[ToolCall],
    ) -> Option<TurnError> {
        for call in calls {
            let activity = ToolActivity {
                id: call.id.clone(),
                name: call.name.clone(),
                arguments: call.arguments.clone(),
                result: String::new(),
                status: TOOL_RUNNING.to_string(),
            };
            if self
                .emit(
                    tx,
                    ChatChunk {
                        session_id: String::new(),
                        delta: String::new(),
                        tool: Some(activity),
                        done: false,
                        stopped: false,
                        needs_continue: false,
                        error: None,
                    },
                )
                .await
                .is_err()
            {
                return Some(TurnError::Stopped);
            }
        }
        for call in calls {
            if self.stop_requested() {
                return Some(TurnError::Stopped);
            }
            let args = ToolCallArgs {
                id: call.id.clone(),
                name: call.name.clone(),
                arguments: call.arguments.clone(),
            };
            let result = self.tools.call(&args, &self.tool_ctx).await;
            self.arm_notify(call, &result);
            let status = if result.is_error {
                TOOL_FAILED
            } else {
                TOOL_OK
            };
            let activity = ToolActivity {
                id: result.call_id.clone(),
                name: result.name.clone(),
                arguments: call.arguments.clone(),
                result: result.content.clone(),
                status: status.to_string(),
            };
            if self
                .emit(
                    tx,
                    ChatChunk {
                        session_id: String::new(),
                        delta: String::new(),
                        tool: Some(activity),
                        done: false,
                        stopped: false,
                        needs_continue: false,
                        error: None,
                    },
                )
                .await
                .is_err()
            {
                return Some(TurnError::Stopped);
            }
            self.append_tool_result(call, &result);
        }
        None
    }

    fn arm_notify(&self, call: &ToolCall, result: &ToolResult) {
        if call.name != "shell" || result.is_error {
            return;
        }
        #[derive(serde::Deserialize)]
        struct Args {
            #[serde(default)]
            action: String,
            #[serde(default)]
            background: bool,
            #[serde(default)]
            notify: bool,
        }
        let args: Args = match serde_json::from_str(&call.arguments) {
            Ok(a) => a,
            Err(_) => return,
        };
        if (!args.action.is_empty() && args.action != "run") || !args.background || !args.notify {
            return;
        }
        #[derive(serde::Deserialize)]
        struct Receipt {
            #[serde(default)]
            job_id: String,
            #[serde(default)]
            status: String,
        }
        let receipt: Receipt = match serde_json::from_str(&result.content) {
            Ok(r) => r,
            Err(_) => return,
        };
        if receipt.job_id.is_empty() || receipt.status != crate::tools::shell::JOB_RUNNING {
            return;
        }
        self.shell
            .arm_notify(&self.session.meta.session_id, &call.id, &receipt.job_id);
    }

    async fn drain_notifications(&mut self, tx: &mpsc::Sender<ChatChunk>) -> Result<(), TurnError> {
        for notice in self.shell.poll_notify(&self.session.meta.session_id) {
            let content = notify_content(&notice);
            let call_id = notify_call_id(&notice.call_id);
            self.append_notify(content.clone(), call_id.clone(), notice.is_error);
            let status = if notice.is_error {
                TOOL_FAILED
            } else {
                TOOL_OK
            };
            if self
                .emit(
                    tx,
                    ChatChunk {
                        session_id: String::new(),
                        delta: String::new(),
                        tool: Some(ToolActivity {
                            id: call_id,
                            name: "shell".to_string(),
                            arguments: notify_args(&notice.job_id),
                            result: content,
                            status: status.to_string(),
                        }),
                        done: false,
                        stopped: false,
                        needs_continue: false,
                        error: None,
                    },
                )
                .await
                .is_err()
            {
                return Err(TurnError::Stopped);
            }
        }
        Ok(())
    }

    /// Plain completion path for a provider with no tool definitions.
    async fn run_plain(&mut self, tx: &mpsc::Sender<ChatChunk>) -> Option<TurnError> {
        let (ptx, mut prx) = mpsc::channel::<Chunk>(64);
        let provider = self.provider.clone();
        let system = self.system.clone();
        let messages = self.messages.clone();
        let session_id = self.session.meta.session_id.clone();
        let cancel = self.cancel.clone();
        let empty_tools: Vec<ToolDefinition> = Vec::new();
        let task = tokio::spawn(async move {
            provider
                .stream_tools(&system, &messages, &empty_tools, &session_id, &ptx, &cancel)
                .await
        });
        let mut streamed = false;
        loop {
            tokio::select! {
                next = prx.recv() => {
                    let Some(chunk) = next else { break };
                    if let Some(err) = chunk.err {
                        if self.stop_requested() {
                            return Some(TurnError::Stopped);
                        }
                        return Some(TurnError::Failed(format!("provider unavailable: {err}")));
                    }
                    if !chunk.text.is_empty() {
                        streamed = true;
                        self.append_delta(&chunk.text);
                        if self.emit(tx, ChatChunk {
                            session_id: String::new(),
                            delta: chunk.text,
                            tool: None,
                            done: false,
                            stopped: false,
                            needs_continue: false,
                            error: None,
                        }).await.is_err() {
                            return Some(TurnError::Stopped);
                        }
                    }
                    if self.stop_requested() {
                        return Some(TurnError::Stopped);
                    }
                }
                _ = self.cancel.cancelled() => {
                    return Some(TurnError::Stopped);
                }
            }
        }
        match task.await {
            Ok(Ok(())) => {}
            Ok(Err(e)) => {
                if self.stop_requested() {
                    return Some(TurnError::Stopped);
                }
                let _ = streamed;
                return Some(TurnError::Failed(format!("provider unavailable: {e}")));
            }
            Err(e) => return Some(TurnError::Failed(format!("provider task failed: {e}"))),
        }
        if self.stop_requested() {
            return Some(TurnError::Stopped);
        }
        if !streamed {
            match self
                .provider
                .complete_tools(
                    &self.system,
                    &self.messages,
                    &[],
                    &self.session.meta.session_id,
                )
                .await
            {
                Ok(resp) => {
                    self.append_delta(&resp.content);
                    let content = std::mem::take(&mut self.reply);
                    let reply = content.clone();
                    self.reply = reply.clone();
                    if self
                        .emit(
                            tx,
                            ChatChunk {
                                session_id: String::new(),
                                delta: content,
                                tool: None,
                                done: false,
                                stopped: false,
                                needs_continue: false,
                                error: None,
                            },
                        )
                        .await
                        .is_err()
                    {
                        return Some(TurnError::Stopped);
                    }
                }
                Err(e) => {
                    if self.stop_requested() {
                        return Some(TurnError::Stopped);
                    }
                    return Some(TurnError::Failed(format!("provider unavailable: {e}")));
                }
            }
        }
        self.finish(Phase::Done);
        None
    }
}

fn notify_content(notice: &crate::tools::shell::NotifyResult) -> String {
    serde_json::json!({
        "notice": "background job finished",
        "job_id": notice.job_id,
        "status": notice.status,
        "exit_code": notice.exit_code,
        "output": notice.output,
        "truncated": notice.truncated,
    })
    .to_string()
}

/// Build the LLM view of the stored history. Tool traffic only travels
/// with tool-capable providers; anything that cannot link (reminders,
/// orphans, empty ids) becomes plain user context instead of breaking
/// the turn.
pub fn llm_messages(history: &[Message], with_tools: bool) -> Vec<LlmMessage> {
    use std::collections::HashSet;
    let mut known: HashSet<&str> = HashSet::new();
    for msg in history {
        if msg.role != Role::Assistant {
            continue;
        }
        if let Some(calls) = &msg.tool_calls {
            for call in calls {
                if !call.id.is_empty() {
                    known.insert(call.id.as_str());
                }
            }
        }
    }
    let mut out = Vec::new();
    for msg in history {
        match msg.role {
            Role::User => out.push(LlmMessage {
                role: "user".to_string(),
                content: msg.content.clone(),
                ..Default::default()
            }),
            Role::Assistant => {
                let mut calls = Vec::new();
                if with_tools {
                    if let Some(tool_calls) = &msg.tool_calls {
                        for call in tool_calls {
                            if call.id.is_empty() {
                                continue;
                            }
                            calls.push(ToolCall {
                                id: call.id.clone(),
                                name: call.name.clone(),
                                arguments: call.arguments.clone(),
                            });
                        }
                    }
                }
                if msg.content.trim().is_empty() && calls.is_empty() {
                    continue;
                }
                out.push(LlmMessage {
                    role: "assistant".to_string(),
                    content: msg.content.clone(),
                    tool_calls: calls,
                    ..Default::default()
                });
            }
            Role::Tool => {
                if !with_tools {
                    continue;
                }
                let linked = msg
                    .tool_call_id
                    .as_deref()
                    .is_some_and(|id| !id.is_empty() && known.contains(id));
                if msg.tool_call_id.as_deref().unwrap_or("").is_empty()
                    || is_notify_message(msg)
                    || !linked
                {
                    out.push(LlmMessage {
                        role: "user".to_string(),
                        content: msg.content.clone(),
                        ..Default::default()
                    });
                    continue;
                }
                out.push(LlmMessage {
                    role: "tool".to_string(),
                    content: msg.content.clone(),
                    tool_call_id: msg.tool_call_id.clone(),
                    name: msg.name.clone(),
                    ..Default::default()
                });
            }
            Role::System => {}
        }
    }
    out
}

#[allow(dead_code)]
fn _tool_response_used(_: &ToolResponse) {}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::transcript::Message;

    #[test]
    fn tool_results_demote_to_user_without_tools() {
        let history = vec![
            Message::user("hi"),
            Message {
                role: Role::Tool,
                content: "out".into(),
                created_at: chrono::Utc::now(),
                tool_calls: None,
                tool_call_id: Some("c1".into()),
                name: Some("shell".into()),
                is_error: false,
                is_notify: false,
            },
        ];
        let plain = llm_messages(&history, false);
        assert!(plain.iter().all(|m| m.role != "tool"));
        let with = llm_messages(&history, true);
        // Orphan tool result (no known call) demotes to user context.
        assert!(with.iter().all(|m| m.role != "tool"));
    }

    #[test]
    fn linked_tool_results_keep_tool_role() {
        let history = vec![
            Message {
                role: Role::Assistant,
                content: String::new(),
                created_at: chrono::Utc::now(),
                tool_calls: Some(vec![StoredCall {
                    id: "c1".into(),
                    name: "read".into(),
                    arguments: "{}".into(),
                }]),
                tool_call_id: None,
                name: None,
                is_error: false,
                is_notify: false,
            },
            Message {
                role: Role::Tool,
                content: "out".into(),
                created_at: chrono::Utc::now(),
                tool_calls: None,
                tool_call_id: Some("c1".into()),
                name: Some("read".into()),
                is_error: false,
                is_notify: false,
            },
        ];
        let with = llm_messages(&history, true);
        assert!(with.iter().any(|m| m.role == "tool"));
    }
}
