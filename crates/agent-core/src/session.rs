use serde::{Deserialize, Serialize};

use crate::transcript::{self, Role};

pub const TOOL_RUNNING: &str = "running";
pub const TOOL_OK: &str = "ok";
pub const TOOL_FAILED: &str = "failed";

/// Reminder rows answer no model call; older rows use the id suffix only.
pub const NOTIFY_SUFFIX: &str = "#notify";

pub fn notify_call_id(call_id: &str) -> String {
    format!("{call_id}{NOTIFY_SUFFIX}")
}

pub fn notify_args(job_id: &str) -> String {
    serde_json::json!({"action": "notify", "job_id": job_id}).to_string()
}

pub fn is_notify_message(msg: &transcript::Message) -> bool {
    msg.is_notify
        || msg
            .tool_call_id
            .as_deref()
            .is_some_and(|id| id.ends_with(NOTIFY_SUFFIX))
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionInfo {
    pub session_id: String,
    pub cwd: String,
    pub created_at: i64,
    pub provider: String,
    pub model: String,
    pub message_count: usize,
    pub title: String,
}

impl From<transcript::Info> for SessionInfo {
    fn from(info: transcript::Info) -> Self {
        Self {
            session_id: info.meta.session_id.clone(),
            cwd: info.meta.cwd.clone(),
            created_at: info.meta.created_at.timestamp(),
            provider: info.meta.provider.clone().unwrap_or_default(),
            model: info.meta.model.clone().unwrap_or_default(),
            message_count: info.message_count,
            title: info.title,
        }
    }
}

/// Client-facing history row: tool rows link back to their call; reminders
/// keep their job link instead.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HistoryMessage {
    pub role: String,
    pub content: String,
    #[serde(rename = "createdAt")]
    pub created_at: i64,
    #[serde(
        rename = "toolCallId",
        default,
        skip_serializing_if = "Option::is_none"
    )]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub arguments: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status: Option<String>,
}

pub fn history_view(msgs: &[transcript::Message]) -> Vec<HistoryMessage> {
    use std::collections::HashMap;
    let mut calls: HashMap<&str, &transcript::ToolCall> = HashMap::new();
    for msg in msgs {
        if msg.role != Role::Assistant {
            continue;
        }
        if let Some(tool_calls) = &msg.tool_calls {
            for call in tool_calls {
                if !call.id.is_empty() {
                    calls.entry(call.id.as_str()).or_insert(call);
                }
            }
        }
    }
    let mut out = Vec::new();
    for msg in msgs {
        if msg.role == Role::Tool {
            let mut args = msg
                .tool_call_id
                .as_deref()
                .and_then(|id| calls.get(id))
                .map(|c| c.arguments.clone())
                .unwrap_or_default();
            if args.is_empty() && is_notify_message(msg) {
                args = notify_history_args(&msg.content);
            }
            out.push(HistoryMessage {
                role: "tool".to_string(),
                content: msg.content.clone(),
                created_at: msg.created_at.timestamp(),
                tool_call_id: msg.tool_call_id.clone(),
                name: msg.name.clone(),
                arguments: if args.is_empty() { None } else { Some(args) },
                status: Some(if msg.is_error {
                    TOOL_FAILED.to_string()
                } else {
                    TOOL_OK.to_string()
                }),
            });
            continue;
        }
        if !msg.visible() {
            continue;
        }
        let role = match msg.role {
            Role::User => "user",
            Role::Assistant => "assistant",
            Role::System => "system",
            Role::Tool => "tool",
        };
        out.push(HistoryMessage {
            role: role.to_string(),
            content: msg.content.clone(),
            created_at: msg.created_at.timestamp(),
            tool_call_id: None,
            name: None,
            arguments: None,
            status: None,
        });
    }
    out
}

fn notify_history_args(content: &str) -> String {
    #[derive(Deserialize)]
    struct Wire {
        #[serde(default)]
        job_id: String,
    }
    match serde_json::from_str::<Wire>(content) {
        Ok(w) if !w.job_id.is_empty() => notify_args(&w.job_id),
        _ => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::transcript::{Message, Meta, Session, SCHEMA_VERSION};
    use chrono::Utc;

    fn test_session() -> (tempfile::TempDir, Session) {
        let dir = tempfile::tempdir().unwrap();
        let session_dir = dir.path().join("s");
        std::fs::create_dir_all(&session_dir).unwrap();
        let sess = Session {
            meta: Meta {
                schema_version: SCHEMA_VERSION,
                session_id: "s".into(),
                cwd: "/tmp".into(),
                created_at: Utc::now(),
                provider: None,
                model: None,
            },
            dir: session_dir,
        };
        (dir, sess)
    }

    #[test]
    fn history_links_tool_results_to_calls() {
        let (_dir, sess) = test_session();
        sess.append_message(Message {
            role: Role::Assistant,
            content: String::new(),
            created_at: Utc::now(),
            tool_calls: Some(vec![transcript::ToolCall {
                id: "c1".into(),
                name: "read".into(),
                arguments: r#"{"path":"workspace/a"}"#.into(),
            }]),
            tool_call_id: None,
            name: None,
            is_error: false,
            is_notify: false,
        })
        .unwrap();
        sess.append_message(Message {
            role: Role::Tool,
            content: "content".into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: Some("c1".into()),
            name: Some("read".into()),
            is_error: false,
            is_notify: false,
        })
        .unwrap();
        let view = history_view(&sess.history().unwrap());
        assert_eq!(view.len(), 2);
        assert_eq!(
            view[1].arguments.as_deref(),
            Some(r#"{"path":"workspace/a"}"#)
        );
        assert_eq!(view[1].status.as_deref(), Some("ok"));
    }
}
