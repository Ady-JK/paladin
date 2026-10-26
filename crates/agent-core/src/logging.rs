use std::path::Path;

/// Initialize process-wide logging: stderr + `<data_dir>/logs/paladin.log`.
/// Never logs secrets; callers must redact auth material before logging.
pub fn init(data_dir: &Path) {
    use tracing_subscriber::{fmt, EnvFilter};
    let logs_dir = data_dir.join("logs");
    let _ = std::fs::create_dir_all(&logs_dir);
    let file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(logs_dir.join("paladin.log"))
        .ok();
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    if let Some(_file) = file {
        // Keep it simple for the scaffold: stderr layer only.
        // File logging lands in a later phase.
        fmt()
            .with_env_filter(filter)
            .with_writer(std::io::stderr)
            .try_init()
            .ok();
    } else {
        fmt()
            .with_env_filter(filter)
            .with_writer(std::io::stderr)
            .try_init()
            .ok();
    }
}
