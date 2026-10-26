use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    System,
    User,
    Assistant,
    Tool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub role: Role,
    pub content: String,
    #[serde(default)]
    pub created_at: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_calls: Option<Vec<ToolCall>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub is_error: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Meta {
    pub schema_version: u32,
    pub session_id: String,
    pub cwd: String,
    pub created_at: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub provider: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
}

pub const SCHEMA_VERSION: u32 = 1;

impl Message {
    pub fn user(content: impl Into<String>) -> Self {
        Self {
            role: Role::User,
            content: content.into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: None,
            name: None,
            is_error: false,
        }
    }

    pub fn assistant(content: impl Into<String>) -> Self {
        Self {
            role: Role::Assistant,
            content: content.into(),
            created_at: Utc::now(),
            tool_calls: None,
            tool_call_id: None,
            name: None,
            is_error: false,
        }
    }

    /// Visible in history: users always, assistants only when non-blank.
    pub fn visible(&self) -> bool {
        match self.role {
            Role::User => true,
            Role::Assistant => {
                !self.content.trim().is_empty()
                    || self.tool_calls.as_ref().is_some_and(|c| !c.is_empty())
            }
            _ => true,
        }
    }
}

/// First user message truncated to 80 chars, used as session title.
pub fn title_for(messages: &[Message]) -> String {
    for msg in messages {
        if msg.role == Role::User && !msg.content.trim().is_empty() {
            let flat: String = msg.content.split_whitespace().collect::<Vec<_>>().join(" ");
            if flat.len() <= 80 {
                return flat;
            }
            return flat.chars().take(80).collect();
        }
    }
    String::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn assistant_blank_without_tools_is_hidden() {
        let msg = Message::assistant("   ");
        assert!(!msg.visible());
    }

    #[test]
    fn title_uses_first_user_message() {
        let msgs = vec![Message::user("hello world")];
        assert_eq!(title_for(&msgs), "hello world");
    }
}
