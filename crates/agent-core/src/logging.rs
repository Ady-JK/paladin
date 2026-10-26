use std::path::Path;
use std::sync::{Arc, Mutex};

/// Initialize process-wide logging: stderr + `<data_dir>/logs/paladin.log`.
/// Never logs secrets; callers must redact auth material before logging.
pub fn init(data_dir: &Path) {
    use tracing_subscriber::{fmt, prelude::*, EnvFilter};

    let logs_dir = data_dir.join("logs");
    let _ = std::fs::create_dir_all(&logs_dir);
    let file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(logs_dir.join("paladin.log"))
        .ok();

    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    let stderr_layer = fmt::layer()
        .with_writer(std::io::stderr)
        .with_filter(filter);
    // Second parse of the same filter for the file layer (EnvFilter is not Clone).
    let file_filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));

    if let Some(file) = file {
        let writer = FileWriter {
            file: Arc::new(Mutex::new(file)),
        };
        let file_layer = fmt::layer()
            .with_writer(writer)
            .with_ansi(false)
            .with_filter(file_filter);
        tracing_subscriber::registry()
            .with(stderr_layer)
            .with(file_layer)
            .try_init()
            .ok();
    } else {
        tracing_subscriber::registry()
            .with(stderr_layer)
            .try_init()
            .ok();
    }
}

#[derive(Debug, Clone)]
struct FileWriter {
    file: Arc<Mutex<std::fs::File>>,
}

struct FileGuard<'a>(std::sync::MutexGuard<'a, std::fs::File>);

impl std::io::Write for FileGuard<'_> {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.write(buf)
    }
    fn flush(&mut self) -> std::io::Result<()> {
        self.0.flush()
    }
}

impl<'a> tracing_subscriber::fmt::MakeWriter<'a> for FileWriter {
    type Writer = FileGuard<'a>;
    fn make_writer(&'a self) -> FileGuard<'a> {
        FileGuard(self.file.lock().unwrap_or_else(|e| e.into_inner()))
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn init_creates_log_dir() {
        let dir = tempfile::tempdir().unwrap();
        super::init(dir.path());
        assert!(dir.path().join("logs").is_dir());
    }
}
