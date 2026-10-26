use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};

use crate::error::ConfigError;

pub const DEFAULT_MAX_TOOL_ROUNDS: u32 = 100;
pub const MAX_MAX_TOOL_ROUNDS: u32 = 500;

pub const DEFAULT_PROVIDER_NAME: &str = "Opencode Go";
pub const OPENCODE_BASE_URL: &str = "https://opencode.ai/inference/go/openai/v1";
pub const OPENROUTER_NAME: &str = "openrouter";
pub const OPENROUTER_BASE_URL: &str = "https://openrouter.ai/api/v1";
pub const PLACEHOLDER_KEY: &str = "replace_me";

pub const KEYRING_SERVICE: &str = "paladin";
pub const OPENCODE_CATALOG_URL: &str = "https://opencode.ai/inference/go/v1/models";
pub const OPENCODE_USER_AGENT: &str = "paladin/1.0";
pub const OPENCODE_SESSION_HEADER: &str = "x-opencode-session";

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

impl ApiType {
    pub fn parse(s: &str) -> Result<Self, ConfigError> {
        match s.to_lowercase().trim() {
            "openai" => Ok(Self::Openai),
            "anthropic" => Ok(Self::Anthropic),
            "google" => Ok(Self::Google),
            other => Err(ConfigError::Parse(format!(
                "unknown api_type {other:?} (want one of openai, anthropic, google)"
            ))),
        }
    }
}

/// A provider entry. The API key is never stored here; it lives in the
/// OS keychain behind [`KeyStore`].
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
    pub providers: HashMap<String, ProviderModels>,
}

/// Secret storage for provider API keys.
pub trait KeyStore: Send + Sync {
    fn get(&self, provider: &str) -> Result<Option<String>, ConfigError>;
    fn set(&self, provider: &str, key: &str) -> Result<(), ConfigError>;
    fn delete(&self, provider: &str) -> Result<(), ConfigError>;
}

/// OS keychain-backed key store (Secret Service / Keychain / Credential Manager).
pub struct OsKeyStore;

impl KeyStore for OsKeyStore {
    fn get(&self, provider: &str) -> Result<Option<String>, ConfigError> {
        let entry = keyring::Entry::new(KEYRING_SERVICE, provider)
            .map_err(|e| ConfigError::Keyring(format!("keychain unavailable: {e}")))?;
        match entry.get_password() {
            Ok(key) => Ok(Some(key)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(e) => Err(ConfigError::Keyring(format!("keychain unavailable: {e}"))),
        }
    }

    fn set(&self, provider: &str, key: &str) -> Result<(), ConfigError> {
        let entry = keyring::Entry::new(KEYRING_SERVICE, provider)
            .map_err(|e| ConfigError::Keyring(format!("keychain unavailable: {e}")))?;
        entry
            .set_password(key)
            .map_err(|e| ConfigError::Keyring(format!("keychain unavailable: {e}")))
    }

    fn delete(&self, provider: &str) -> Result<(), ConfigError> {
        let entry = keyring::Entry::new(KEYRING_SERVICE, provider)
            .map_err(|e| ConfigError::Keyring(format!("keychain unavailable: {e}")))?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(e) => Err(ConfigError::Keyring(format!("keychain unavailable: {e}"))),
        }
    }
}

/// In-memory key store for tests, so CI needs no desktop keyring daemon.
#[derive(Default)]
pub struct MemoryKeyStore {
    keys: Mutex<HashMap<String, String>>,
}

impl MemoryKeyStore {
    pub fn new() -> Self {
        Self::default()
    }
}

impl KeyStore for MemoryKeyStore {
    fn get(&self, provider: &str) -> Result<Option<String>, ConfigError> {
        Ok(self.keys.lock().unwrap().get(provider).cloned())
    }

    fn set(&self, provider: &str, key: &str) -> Result<(), ConfigError> {
        self.keys
            .lock()
            .unwrap()
            .insert(provider.to_string(), key.to_string());
        Ok(())
    }

    fn delete(&self, provider: &str) -> Result<(), ConfigError> {
        self.keys.lock().unwrap().remove(provider);
        Ok(())
    }
}

/// On-disk catalog: `config/providers.json` + `config/models.json` under
/// the data dir, with secrets in the [`KeyStore`].
pub struct Store {
    data_dir: PathBuf,
    pub keys: Arc<dyn KeyStore>,
}

impl Store {
    pub fn new(data_dir: PathBuf, keys: Arc<dyn KeyStore>) -> Self {
        Self { data_dir, keys }
    }

    pub fn providers_path(&self) -> PathBuf {
        self.data_dir.join("config").join("providers.json")
    }

    pub fn models_path(&self) -> PathBuf {
        self.data_dir.join("config").join("models.json")
    }

    pub fn load_providers(&self) -> Result<ProvidersFile, ConfigError> {
        let path = self.providers_path();
        match std::fs::read_to_string(&path) {
            Ok(data) => {
                if data.trim().is_empty() {
                    return Ok(ProvidersFile::default());
                }
                let mut file: ProvidersFile = serde_json::from_str(&data)
                    .map_err(|e| ConfigError::Parse(format!("{}: {e}", path.display())))?;
                validate_providers(&file.providers)?;
                let before = (file.providers.len(), file.default_provider.clone());
                file = ensure_seeded(file);
                if (file.providers.len(), file.default_provider.clone()) != before {
                    self.save_providers(&file)?;
                }
                Ok(file)
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let seeded = ensure_seeded(default_providers_file());
                self.save_providers(&seeded)?;
                Ok(seeded)
            }
            Err(e) => Err(ConfigError::Io(format!("{}: {e}", path.display()))),
        }
    }

    pub fn save_providers(&self, file: &ProvidersFile) -> Result<(), ConfigError> {
        validate_providers(&file.providers)?;
        let data =
            serde_json::to_string_pretty(file).map_err(|e| ConfigError::Parse(e.to_string()))?;
        atomic_write(&self.providers_path(), &(data + "\n"))
    }

    pub fn load_models(&self) -> Result<ModelsFile, ConfigError> {
        let path = self.models_path();
        match std::fs::read_to_string(&path) {
            Ok(data) => {
                if data.trim().is_empty() {
                    return Ok(ModelsFile::default());
                }
                let mut file: ModelsFile = serde_json::from_str(&data)
                    .map_err(|e| ConfigError::Parse(format!("{}: {e}", path.display())))?;
                if file.providers.is_empty() {
                    file.providers = HashMap::new();
                }
                Ok(file)
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let empty = ModelsFile::default();
                self.save_models(&empty)?;
                Ok(empty)
            }
            Err(e) => Err(ConfigError::Io(format!("{}: {e}", path.display()))),
        }
    }

    pub fn save_models(&self, file: &ModelsFile) -> Result<(), ConfigError> {
        let data =
            serde_json::to_string_pretty(file).map_err(|e| ConfigError::Parse(e.to_string()))?;
        atomic_write(&self.models_path(), &(data + "\n"))
    }

    /// Resolve the provider to use: explicit name wins, else the default
    /// when it has a real key, else the first provider with a real key.
    /// Returns the entry plus its keychain key (empty when unset).
    pub fn resolve(&self, name: &str) -> Result<(Provider, String), ConfigError> {
        let file = self.load_providers()?;
        if !name.trim().is_empty() {
            for p in &file.providers {
                if p.name == name {
                    let key = self.keys.get(&p.name)?.unwrap_or_default();
                    return Ok((p.clone(), key));
                }
            }
            return Err(ConfigError::Parse(format!("provider {name:?} not found")));
        }
        if !file.default_provider.is_empty() {
            if let Some(p) = file
                .providers
                .iter()
                .find(|p| p.name == file.default_provider)
            {
                let key = self.keys.get(&p.name)?.unwrap_or_default();
                if !is_placeholder_key(&key) {
                    return Ok((p.clone(), key));
                }
                // Fall through to first usable when the default still
                // holds a placeholder key.
                for p in &file.providers {
                    let key = self.keys.get(&p.name)?.unwrap_or_default();
                    if !is_placeholder_key(&key) {
                        return Ok((p.clone(), key));
                    }
                }
                return Err(ConfigError::Parse(format!(
                    "no usable provider: {:?} still has a placeholder api key; add one in settings",
                    p.name
                )));
            }
        }
        for p in &file.providers {
            let key = self.keys.get(&p.name)?.unwrap_or_default();
            if !is_placeholder_key(&key) {
                return Ok((p.clone(), key));
            }
        }
        if let Some(first) = file.providers.first() {
            return Err(ConfigError::Parse(format!(
                "no usable provider: {:?} still has a placeholder api key; add one in settings",
                first.name
            )));
        }
        Err(ConfigError::Parse("no providers configured".to_string()))
    }

    pub fn models_for(&self, name: &str) -> Vec<String> {
        let file = self.load_models().unwrap_or_default();
        if let Some(entry) = file.providers.get(name) {
            if !entry.enabled_models.is_empty() {
                return entry.enabled_models.clone();
            }
            return entry.all_models.clone();
        }
        Vec::new()
    }

    pub fn provider_infos(&self) -> Result<Vec<ProviderInfo>, ConfigError> {
        let file = self.load_providers()?;
        let models = self.load_models().unwrap_or_default();
        Ok(file
            .providers
            .iter()
            .map(|p| {
                let entry = models.providers.get(&p.name);
                let list = match entry {
                    Some(e) if !e.enabled_models.is_empty() => e.enabled_models.clone(),
                    Some(e) => e.all_models.clone(),
                    None => Vec::new(),
                };
                let key = self.keys.get(&p.name).unwrap_or(None).unwrap_or_default();
                ProviderInfo {
                    name: p.name.clone(),
                    api_type: p.api_type,
                    api_url: p.api_url.clone(),
                    model: p.model.clone(),
                    models: list,
                    default: p.name == file.default_provider,
                    has_key: !is_placeholder_key(&key),
                }
            })
            .collect())
    }

    pub fn workspace(&self) -> String {
        self.load_providers()
            .map(|f| f.workspace)
            .unwrap_or_default()
    }
}

pub fn default_providers_file() -> ProvidersFile {
    ProvidersFile {
        default_provider: DEFAULT_PROVIDER_NAME.to_string(),
        providers: vec![
            Provider {
                name: DEFAULT_PROVIDER_NAME.to_string(),
                api_url: OPENCODE_BASE_URL.to_string(),
                api_type: ApiType::Openai,
                model: String::new(),
            },
            Provider {
                name: OPENROUTER_NAME.to_string(),
                api_url: OPENROUTER_BASE_URL.to_string(),
                api_type: ApiType::Openai,
                model: String::new(),
            },
        ],
        workspace: String::new(),
    }
}

/// Add the shipped entries when missing, leaving existing entries and the
/// default untouched.
pub fn ensure_seeded(mut file: ProvidersFile) -> ProvidersFile {
    for seed in default_providers_file().providers {
        if !file.providers.iter().any(|p| p.name == seed.name) {
            file.providers.push(seed);
        }
    }
    if file.default_provider.is_empty() {
        file.default_provider = DEFAULT_PROVIDER_NAME.to_string();
    }
    file
}

pub fn validate_providers(providers: &[Provider]) -> Result<(), ConfigError> {
    let mut seen = std::collections::HashSet::new();
    for p in providers {
        if p.name.trim().is_empty() {
            return Err(ConfigError::Parse(
                "provider name must not be empty".to_string(),
            ));
        }
        if !seen.insert(p.name.clone()) {
            return Err(ConfigError::Parse(format!(
                "duplicate provider name {:?}",
                p.name
            )));
        }
    }
    Ok(())
}

pub fn validate_provider_input(
    input: &ProviderInput,
    is_new: bool,
) -> Result<(String, String, ApiType), ConfigError> {
    let name = input.name.trim().to_string();
    if name.is_empty() {
        return Err(ConfigError::Parse("provider name is required".to_string()));
    }
    if name.contains(['\t', '\n', '/', '\\']) {
        return Err(ConfigError::Parse(
            "provider name must be one word".to_string(),
        ));
    }
    let url = input.api_url.trim().to_string();
    if url.is_empty() {
        return Err(ConfigError::Parse("api url is required".to_string()));
    }
    if !url.starts_with("http://") && !url.starts_with("https://") {
        return Err(ConfigError::Parse(
            "api url must start with http:// or https://".to_string(),
        ));
    }
    if is_new && input.api_key.trim().is_empty() {
        return Err(ConfigError::Parse("api key is required".to_string()));
    }
    let api_type = ApiType::parse(&input.api_type)?;
    Ok((name, url, api_type))
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

pub fn is_opencode_endpoint(api_url: &str) -> bool {
    api_url.to_lowercase().contains("opencode.ai/inference")
}

pub fn atomic_write(path: &Path, data: &str) -> Result<(), ConfigError> {
    let dir = path.parent().unwrap_or_else(|| Path::new("."));
    std::fs::create_dir_all(dir).map_err(|e| ConfigError::Io(format!("{}: {e}", dir.display())))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        let mut builder = std::fs::DirBuilder::new();
        builder.mode(0o700);
        let _ = builder.create(dir);
    }
    let mut tmp = tempfile::NamedTempFile::new_in(dir)
        .map_err(|e| ConfigError::Io(format!("temp file in {}: {e}", dir.display())))?;
    use std::io::Write;
    tmp.write_all(data.as_bytes())
        .map_err(|e| ConfigError::Io(format!("{}: {e}", path.display())))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(tmp.path(), std::fs::Permissions::from_mode(0o600)).ok();
    }
    tmp.persist(path)
        .map_err(|e| ConfigError::Io(format!("{}: {e}", path.display())))?;
    Ok(())
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
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_store() -> (tempfile::TempDir, Store) {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().to_path_buf(), Arc::new(MemoryKeyStore::new()));
        (dir, store)
    }

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
        assert!(is_placeholder_key(""));
        assert!(!is_placeholder_key("sk-live-abc123"));
    }

    #[test]
    fn seeds_default_providers_on_first_load() {
        let (_dir, store) = test_store();
        let file = store.load_providers().unwrap();
        assert_eq!(file.default_provider, DEFAULT_PROVIDER_NAME);
        assert!(file.providers.iter().any(|p| p.name == OPENROUTER_NAME));
        // No keys in the JSON file.
        let raw = std::fs::read_to_string(store.providers_path()).unwrap();
        assert!(!raw.contains("api_key"));
    }

    #[test]
    fn resolve_prefers_default_with_real_key() {
        let (_dir, store) = test_store();
        store.load_providers().unwrap();
        store.keys.set(DEFAULT_PROVIDER_NAME, "sk-real").unwrap();
        let (p, key) = store.resolve("").unwrap();
        assert_eq!(p.name, DEFAULT_PROVIDER_NAME);
        assert_eq!(key, "sk-real");
    }

    #[test]
    fn resolve_rejects_placeholder_only_catalog() {
        let (_dir, store) = test_store();
        store.load_providers().unwrap();
        assert!(store.resolve("").is_err());
    }

    #[test]
    fn validate_rejects_bad_input() {
        assert!(validate_provider_input(
            &ProviderInput {
                name: "x".into(),
                api_url: "ftp://x".into(),
                api_key: "k".into(),
                api_type: "openai".into()
            },
            true
        )
        .is_err());
        assert!(validate_provider_input(
            &ProviderInput {
                name: "x".into(),
                api_url: "https://x/v1".into(),
                api_key: "".into(),
                api_type: "openai".into()
            },
            true
        )
        .is_err());
    }
}
