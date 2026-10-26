use std::path::PathBuf;

use serde::{Deserialize, Serialize};

pub const DEFAULT_MAX_TOOL_ROUNDS: u32 = 100;
pub const MAX_MAX_TOOL_ROUNDS: u32 = 500;

/// Resolves the shared data directory:
/// `$PALADIN_DATA_DIR` override, else OS-specific default.
pub fn data_dir() -> PathBuf {
    if let Ok(dir) = std::env::var("PALADIN_DATA_DIR") {
        if !dir.trim().is_empty() {
            return PathBuf::from(dir);
        }
    }
    directories::ProjectDirs::from("", "", "paladin")
        .map(|d| d.config_dir().to_path_buf())
        .unwrap_or_else(|| PathBuf::from(".paladin"))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum ApiType {
    #[default]
    Openai,
    Anthropic,
    Google,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Provider {
    pub name: String,
    #[serde(default)]
    pub api_url: String,
    #[serde(default)]
    pub api_type: ApiType,
    #[serde(default)]
    pub model: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProviderInfo {
    pub name: String,
    #[serde(rename = "apiType")]
    pub api_type: ApiType,
    #[serde(rename = "apiUrl")]
    pub api_url: String,
    pub model: String,
    pub models: Vec<String>,
    pub default: bool,
    #[serde(rename = "hasKey", default)]
    pub has_key: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProviderInput {
    pub name: String,
    #[serde(default, rename = "apiUrl")]
    pub api_url: String,
    #[serde(default, rename = "apiKey")]
    pub api_key: String,
    #[serde(default, rename = "apiType")]
    pub api_type: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ProvidersFile {
    #[serde(default)]
    pub default_provider: String,
    #[serde(default)]
    pub providers: Vec<Provider>,
    #[serde(default)]
    pub workspace: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ProviderModels {
    #[serde(default)]
    pub all_models: Vec<String>,
    #[serde(default)]
    pub enabled_models: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ModelsFile {
    #[serde(default)]
    pub providers: std::collections::HashMap<String, ProviderModels>,
}

#[derive(Debug, Clone)]
pub struct Config {
    pub data_dir: PathBuf,
    pub max_tool_rounds: u32,
    pub system_prompt: String,
}

impl Config {
    pub fn new(data_dir: PathBuf) -> Self {
        Self {
            data_dir,
            max_tool_rounds: DEFAULT_MAX_TOOL_ROUNDS,
            system_prompt: String::new(),
        }
    }

    pub fn providers_path(&self) -> PathBuf {
        self.data_dir.join("config").join("providers.json")
    }

    pub fn models_path(&self) -> PathBuf {
        self.data_dir.join("config").join("models.json")
    }

    pub fn sessions_root(&self) -> PathBuf {
        self.data_dir.join("sessions")
    }
}

pub fn clamp_max_tool_rounds(value: u32) -> u32 {
    if value == 0 {
        DEFAULT_MAX_TOOL_ROUNDS
    } else {
        value.min(MAX_MAX_TOOL_ROUNDS)
    }
}

pub fn is_placeholder_key(key: &str) -> bool {
    let lower = key.to_lowercase();
    key.is_empty()
        || lower.contains("replace")
        || lower.contains("changeme")
        || lower.contains("dummy")
        || lower.contains("your-api-key")
        || lower.contains("example")
        || lower.contains("xxx")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn max_tool_rounds_zero_defaults_to_100() {
        assert_eq!(clamp_max_tool_rounds(0), 100);
    }

    #[test]
    fn max_tool_rounds_clamps_to_500() {
        assert_eq!(clamp_max_tool_rounds(999), 500);
    }

    #[test]
    fn detects_placeholder_keys() {
        assert!(is_placeholder_key("replace_me"));
        assert!(!is_placeholder_key("sk-live-abc123"));
    }
}
