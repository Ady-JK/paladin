use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use tokio::sync::Notify;

use super::{Tool, ToolCtx, ToolDefinition};
use crate::error::ToolError;

pub const DEFAULT_SHELL_TIMEOUT_MS: u64 = 10_000;
pub const MAX_SHELL_OUTPUT: usize = 32 * 1024;
const SHELL_TRUNCATED_MARKER: &str = "\n… [truncated]";

pub const SHELL_RUN: &str = "run";
pub const SHELL_STATUS: &str = "status";
pub const SHELL_CANCEL: &str = "cancel";

pub const JOB_RUNNING: &str = "running";
pub const JOB_DONE: &str = "done";
pub const JOB_FAILED: &str = "failed";
pub const JOB_CANCELLED: &str = "cancelled";
pub const JOB_TIMEOUT: &str = "timeout";

pub const MAX_NOTIFY_OUTPUT: usize = 50 * 1024;
pub const NOTIFY_TAIL_LINES: usize = 2000;

#[derive(Debug, Deserialize)]
struct ShellArgs {
    #[serde(default)]
    action: String,
    #[serde(default)]
    job_id: String,
    #[serde(default)]
    workdir: String,
    #[serde(default)]
    cmd: String,
    #[serde(default)]
    background: bool,
    #[serde(default)]
    notify: bool,
    #[serde(default)]
    timeout_ms: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShellResponse {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_id: Option<String>,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub exit_code: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub truncated: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub log: Option<String>,
}

#[derive(Debug, Clone)]
struct Job {
    id: String,
    log: PathBuf,
    status: String,
    exit_code: Option<i32>,
    finished: bool,
    notify: bool,
    notify_call_id: String,
    owner: String,
    reported: bool,
    child_id: Option<u32>,
    log_done: bool,
}

struct Jobs {
    by_id: Mutex<HashMap<String, Job>>,
    counter: AtomicU64,
    changed: Notify,
}

#[derive(Clone)]
pub struct ShellTool {
    inner: Arc<Inner>,
}

struct Inner {
    jobs: Jobs,
}

#[derive(Debug, Clone)]
pub struct NotifyResult {
    pub call_id: String,
    pub job_id: String,
    pub status: String,
    pub exit_code: Option<i32>,
    pub output: String,
    pub truncated: bool,
    pub is_error: bool,
}

impl ShellTool {
    pub fn new() -> Self {
        Self {
            inner: Arc::new(Inner {
                jobs: Jobs {
                    by_id: Mutex::new(HashMap::new()),
                    counter: AtomicU64::new(0),
                    changed: Notify::new(),
                },
            }),
        }
    }

    fn timeout_ms(ms: i64) -> Result<std::time::Duration, String> {
        if ms == 0 {
            return Ok(std::time::Duration::from_millis(DEFAULT_SHELL_TIMEOUT_MS));
        }
        if ms < 0 {
            return Err("shell: timeout_ms cannot be negative".to_string());
        }
        Ok(std::time::Duration::from_millis(ms as u64))
    }

    fn resolve_workdir(workdir: &str, workspace: &std::path::Path) -> Result<PathBuf, String> {
        let trimmed = workdir.trim();
        if trimmed.is_empty() {
            if workspace.as_os_str().is_empty() {
                return Err("shell: no workspace to run in".to_string());
            }
            return Ok(workspace.to_path_buf());
        }
        super::scope::resolve_scoped_path("shell", "workdir", trimmed, workspace)
            .map(|(p, _)| p)
            .map_err(|e| e.to_string())
    }

    fn run_sync_via_ctx(
        cmd: &str,
        dir: &std::path::Path,
        limit: std::time::Duration,
        cancel: &tokio_util::sync::CancellationToken,
    ) -> Result<String, ToolError> {
        let cancel_child = cancel.clone();
        let (tx, rx) = std::sync::mpsc::channel();
        let cmd = cmd.to_string();
        let dir = dir.to_path_buf();
        std::thread::spawn(move || {
            let result = run_sync_inner(&cmd, &dir, limit, &cancel_child);
            let _ = tx.send(result);
        });
        // The worker kills the process group on cancel/timeout; here we
        // only wait for its verdict.
        rx.recv()
            .unwrap_or_else(|_| Err(ToolError::args("shell: the command was interrupted")))
    }

    fn run_background(
        &self,
        cmd: &str,
        dir: &std::path::Path,
        limit: std::time::Duration,
        notify: bool,
        session_dir: &std::path::Path,
    ) -> Result<String, ToolError> {
        if session_dir.as_os_str().is_empty() {
            return Err(ToolError::args(
                "shell: no session to keep the background job in",
            ));
        }
        let id = format!(
            "job-{}-{}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_secs())
                .unwrap_or(0),
            self.inner.jobs.counter.fetch_add(1, Ordering::SeqCst) + 1
        );
        let tmp = session_dir.join("tmp");
        std::fs::create_dir_all(&tmp)
            .map_err(|e| ToolError::args(format!("shell: cannot keep the job log: {e}")))?;
        let log_path = tmp.join(format!("{id}.log"));
        let log_file = std::fs::File::create(&log_path)
            .map_err(|e| ToolError::args(format!("shell: cannot write the job log: {e}")))?;
        // Stream output straight to the log file, like the Go lfWriter.
        let mut child = spawn_command_with_stdio(
            cmd,
            dir,
            log_file
                .try_clone()
                .map_err(|e| ToolError::args(format!("shell: cannot write the job log: {e}")))?,
        )
        .map_err(|e| ToolError::args(format!("shell: cannot start the command: {e}")))?;
        let child_id = child.id();
        self.inner.jobs.by_id.lock().unwrap().insert(
            id.clone(),
            Job {
                id: id.clone(),
                log: log_path.clone(),
                status: JOB_RUNNING.to_string(),
                exit_code: None,
                finished: false,
                notify,
                notify_call_id: String::new(),
                owner: String::new(),
                reported: false,
                child_id: Some(child_id),
                log_done: false,
            },
        );
        let guard_this = self.clone();
        let guard_id = id.clone();
        let this = self.clone();
        let id_clone = id.clone();
        // Timeout guard: polls so it exits early when the job finishes.
        std::thread::spawn(move || {
            let start = std::time::Instant::now();
            loop {
                std::thread::sleep(std::time::Duration::from_millis(100));
                let pid = {
                    let mut jobs = guard_this.inner.jobs.by_id.lock().unwrap();
                    match jobs.get_mut(&guard_id) {
                        Some(job) if job.finished => break,
                        Some(job) if start.elapsed() >= limit => {
                            job.finished = true;
                            job.status = JOB_TIMEOUT.to_string();
                            job.child_id
                        }
                        Some(_) => continue,
                        None => break,
                    }
                };
                if let Some(pid) = pid {
                    kill_process_group(pid);
                    guard_this.inner.jobs.changed.notify_waiters();
                }
                break;
            }
        });
        std::thread::spawn(move || {
            let status = child.wait().ok().and_then(|s| s.code());
            // Log file already holds the output; just flip state.
            let mut jobs = this.inner.jobs.by_id.lock().unwrap();
            if let Some(job) = jobs.get_mut(&id_clone) {
                if !job.finished {
                    job.finished = true;
                    match status {
                        Some(0) => {
                            job.status = JOB_DONE.to_string();
                            job.exit_code = Some(0);
                        }
                        Some(c) => {
                            job.status = JOB_FAILED.to_string();
                            job.exit_code = Some(c);
                        }
                        None => {
                            job.status = JOB_FAILED.to_string();
                            job.exit_code = Some(1);
                        }
                    }
                }
                job.log_done = true;
            }
            drop(jobs);
            this.inner.jobs.changed.notify_waiters();
        });
        let out = ShellResponse {
            job_id: Some(id),
            status: JOB_RUNNING.to_string(),
            exit_code: None,
            output: None,
            truncated: None,
            log: Some(log_path.display().to_string()),
        };
        serde_json::to_string(&out)
            .map_err(|e| ToolError::args(format!("shell: encode the result: {e}")))
    }

    fn status(&self, job_id: &str) -> Result<String, ToolError> {
        let jobs = self.inner.jobs.by_id.lock().unwrap();
        let job = jobs
            .get(job_id.trim())
            .ok_or_else(|| ToolError::args(format!("shell: unknown job {job_id:?}")))?;
        let out = ShellResponse {
            job_id: Some(job.id.clone()),
            status: job.status.clone(),
            exit_code: job.exit_code,
            output: None,
            truncated: None,
            log: Some(job.log.display().to_string()),
        };
        let text = serde_json::to_string(&out)
            .map_err(|e| ToolError::args(format!("shell: encode the result: {e}")))?;
        if job.finished && (job.status == JOB_FAILED || job.status == JOB_TIMEOUT) {
            return Err(ToolError::with_content(
                text,
                format!("shell: job {} {}", job.id, job.status),
            ));
        }
        Ok(text)
    }

    fn cancel(&self, job_id: &str) -> Result<String, ToolError> {
        let (child_id, already) = {
            let mut jobs = self.inner.jobs.by_id.lock().unwrap();
            let job = jobs
                .get_mut(job_id.trim())
                .ok_or_else(|| ToolError::args(format!("shell: unknown job {job_id:?}")))?;
            if job.finished {
                (None, true)
            } else {
                job.finished = true;
                job.status = JOB_CANCELLED.to_string();
                (job.child_id, false)
            }
        };
        if !already {
            if let Some(pid) = child_id {
                kill_process_group(pid);
            }
            self.inner.jobs.changed.notify_waiters();
        }
        let jobs = self.inner.jobs.by_id.lock().unwrap();
        let job = jobs
            .get(job_id.trim())
            .ok_or_else(|| ToolError::args(format!("shell: unknown job {job_id:?}")))?;
        let out = ShellResponse {
            job_id: Some(job.id.clone()),
            status: job.status.clone(),
            exit_code: job.exit_code,
            output: None,
            truncated: None,
            log: Some(job.log.display().to_string()),
        };
        serde_json::to_string(&out)
            .map_err(|e| ToolError::args(format!("shell: encode the result: {e}")))
    }

    pub fn arm_notify(&self, owner: &str, call_id: &str, job_id: &str) {
        let mut jobs = self.inner.jobs.by_id.lock().unwrap();
        if let Some(job) = jobs.get_mut(job_id.trim()) {
            job.notify = true;
            job.notify_call_id = call_id.to_string();
            job.owner = owner.to_string();
        }
    }

    pub fn pending_notify(&self, owner: &str) -> bool {
        let jobs = self.inner.jobs.by_id.lock().unwrap();
        jobs.values()
            .any(|j| j.notify && j.owner == owner && !j.finished && !j.reported)
    }

    pub fn poll_notify(&self, owner: &str) -> Vec<NotifyResult> {
        let jobs_snapshot: Vec<Job> = {
            let jobs = self.inner.jobs.by_id.lock().unwrap();
            jobs.values().cloned().collect()
        };
        let mut out = Vec::new();
        for job in jobs_snapshot {
            let mut jobs = self.inner.jobs.by_id.lock().unwrap();
            let Some(entry) = jobs.get_mut(&job.id) else {
                continue;
            };
            if !entry.notify || entry.owner != owner || !entry.finished || entry.reported {
                continue;
            }
            if !entry.log_done {
                continue;
            }
            entry.reported = true;
            let (status, exit_code, call_id, job_id, log) = (
                entry.status.clone(),
                entry.exit_code,
                entry.notify_call_id.clone(),
                entry.id.clone(),
                entry.log.clone(),
            );
            drop(jobs);
            let (tail, truncated) = tail_file(&log);
            out.push(NotifyResult {
                call_id,
                job_id,
                status: status.clone(),
                exit_code,
                output: tail,
                truncated,
                is_error: status == JOB_FAILED || status == JOB_TIMEOUT,
            });
        }
        out
    }

    pub async fn wait_pending_notify(
        &self,
        owner: &str,
        cancel: &tokio_util::sync::CancellationToken,
    ) -> bool {
        loop {
            {
                let jobs = self.inner.jobs.by_id.lock().unwrap();
                let pending = jobs
                    .values()
                    .any(|j| j.notify && j.owner == owner && !j.finished && !j.reported);
                if !pending {
                    return true;
                }
            }
            tokio::select! {
                _ = self.inner.jobs.changed.notified() => {}
                _ = cancel.cancelled() => return false,
            }
        }
    }
}

impl Default for ShellTool {
    fn default() -> Self {
        Self::new()
    }
}

fn spawn_command_with_stdio(
    cmd: &str,
    dir: &std::path::Path,
    log: std::fs::File,
) -> Result<std::process::Child, std::io::Error> {
    use std::process::Stdio;
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let mut command = std::process::Command::new("sh");
        command.arg("-c").arg(cmd).current_dir(dir);
        command.env("TERM", "dumb");
        command.env("NO_COLOR", "1");
        command.env("CLICOLOR", "0");
        unsafe {
            command.pre_exec(|| {
                libc::setsid();
                Ok(())
            });
        }
        // Merge stderr into the same log stream.
        let err_log = log.try_clone()?;
        command.stdout(log).stderr(err_log);
        let _ = Stdio::piped();
        command.spawn()
    }
    #[cfg(not(unix))]
    {
        let mut command = std::process::Command::new("sh");
        command.arg("-c").arg(cmd).current_dir(dir);
        command.env("TERM", "dumb");
        let err_log = log.try_clone()?;
        command.stdout(log).stderr(err_log);
        command.spawn()
    }
}

fn spawn_command(cmd: &str, dir: &std::path::Path) -> Result<std::process::Child, std::io::Error> {
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let mut command = std::process::Command::new("sh");
        command.arg("-c").arg(cmd).current_dir(dir);
        command.env("TERM", "dumb");
        command.env("NO_COLOR", "1");
        command.env("CLICOLOR", "0");
        unsafe {
            command.pre_exec(|| {
                libc::setsid();
                Ok(())
            });
        }
        command
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped());
        command.spawn()
    }
    #[cfg(not(unix))]
    {
        let mut command = std::process::Command::new("sh");
        command.arg("-c").arg(cmd).current_dir(dir);
        command.env("TERM", "dumb");
        command
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped());
        command.spawn()
    }
}

fn kill_process_group(pid: u32) {
    #[cfg(unix)]
    unsafe {
        libc::kill(-(pid as i32), libc::SIGKILL);
    }
    #[cfg(not(unix))]
    {
        let _ = pid;
    }
}

fn run_sync_inner(
    cmd: &str,
    dir: &std::path::Path,
    limit: std::time::Duration,
    cancel: &tokio_util::sync::CancellationToken,
) -> Result<String, ToolError> {
    let mut child = spawn_command(cmd, dir)
        .map_err(|e| ToolError::args(format!("shell: cannot start the command: {e}")))?;
    let pid = child.id();
    use std::io::Read;
    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut combined = Vec::new();
        if let Some(mut out) = stdout {
            let mut buf = Vec::new();
            let _ = out.read_to_end(&mut buf);
            combined.extend_from_slice(&buf);
        }
        if let Some(mut err) = stderr {
            let mut buf = Vec::new();
            let _ = err.read_to_end(&mut buf);
            combined.extend_from_slice(&buf);
        }
        let _ = tx.send(combined);
    });
    let start = std::time::Instant::now();
    loop {
        if cancel.is_cancelled() {
            kill_process_group(pid);
            let _ = child.wait();
            return Err(ToolError::args("shell: the command was interrupted"));
        }
        match child.try_wait() {
            Ok(Some(status)) => {
                let output = rx
                    .recv_timeout(std::time::Duration::from_secs(10))
                    .unwrap_or_default();
                return finish_sync(output, status.code());
            }
            Ok(None) => {
                if start.elapsed() >= limit {
                    kill_process_group(pid);
                    let _ = child.wait();
                    return Err(ToolError::args(format!(
                        "shell: the command ran past its timeout of {}ms",
                        limit.as_millis()
                    )));
                }
                std::thread::sleep(std::time::Duration::from_millis(20));
            }
            Err(e) => return Err(ToolError::args(format!("shell: {e}"))),
        }
    }
}

fn finish_sync(output: Vec<u8>, code: Option<i32>) -> Result<String, ToolError> {
    let mut text = to_lf(&strip_ansi(&String::from_utf8_lossy(&output)));
    let mut truncated = false;
    if text.len() > MAX_SHELL_OUTPUT {
        text.truncate(MAX_SHELL_OUTPUT);
        text.push_str(SHELL_TRUNCATED_MARKER);
        truncated = true;
    }
    match code {
        Some(0) => {
            let out = ShellResponse {
                job_id: None,
                status: JOB_DONE.to_string(),
                exit_code: Some(0),
                output: Some(text),
                truncated: Some(truncated).filter(|t| *t),
                log: None,
            };
            serde_json::to_string(&out)
                .map_err(|e| ToolError::args(format!("shell: encode the result: {e}")))
        }
        Some(c) => {
            let out = ShellResponse {
                job_id: None,
                status: JOB_FAILED.to_string(),
                exit_code: Some(c),
                output: Some(text),
                truncated: Some(truncated).filter(|t| *t),
                log: None,
            };
            let content = serde_json::to_string(&out)
                .map_err(|e| ToolError::args(format!("shell: encode the result: {e}")))?;
            Err(ToolError::with_content(
                content,
                format!("shell: the command exited with status {c}"),
            ))
        }
        None => Err(ToolError::args("shell: the command exited with status 1")),
    }
}

pub fn to_lf(s: &str) -> String {
    let s = strip_ansi(s);
    s.replace("\r\n", "\n").replace('\r', "\n")
}

pub fn strip_ansi(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != 0x1b {
            out.push(bytes[i] as char);
            i += 1;
            continue;
        }
        i += 1;
        if i >= bytes.len() {
            break;
        }
        if bytes[i] == b'[' {
            i += 1;
            while i < bytes.len() && !(bytes[i] >= 0x40 && bytes[i] <= 0x7e) {
                i += 1;
            }
            i += 1;
        }
    }
    out
}

fn tail_file(path: &std::path::Path) -> (String, bool) {
    let data = match std::fs::read(path) {
        Ok(d) => d,
        Err(_) => return (String::new(), false),
    };
    let mut truncated = false;
    let mut slice = data.as_slice();
    if slice.len() > MAX_NOTIFY_OUTPUT {
        slice = &slice[slice.len() - MAX_NOTIFY_OUTPUT..];
        truncated = true;
        if let Some(i) = slice.iter().position(|&b| b == b'\n') {
            slice = &slice[i + 1..];
        } else {
            slice = b"";
        }
    }
    let mut text = to_lf(&strip_ansi(&String::from_utf8_lossy(slice)));
    let mut lines: Vec<&str> = text.lines().collect();
    if lines.len() > NOTIFY_TAIL_LINES {
        lines = lines[lines.len() - NOTIFY_TAIL_LINES..].to_vec();
        truncated = true;
    }
    text = lines.join("\n");
    (text, truncated)
}

#[async_trait]
impl Tool for ShellTool {
    fn name(&self) -> &str {
        "shell"
    }

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: "shell".to_string(),
            description: "Run a shell command in a working directory. \
                Pass workdir as workspace/<path> for a directory inside the \
                workspace or as a full absolute path elsewhere; bare relative \
                workdirs are rejected. \
                With background false (the default) the call waits for the command and \
                returns its output; with background true it returns at once with a \
                job_id in running state and writes everything to a log file. \
                Status checks a background job, cancel stops one. Prefer \
                synchronous runs and wait, raising timeout_ms for slow commands; \
                use the background only for commands over about 60 seconds when \
                other tool calls can run meanwhile, then poll status and read \
                the log file."
                .to_string(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "action": {"type": "string", "enum": ["run", "status", "cancel"], "description": "What to do: run a command, check a job, or stop a job. Defaults to run."},
                    "job_id": {"type": "string", "description": "The job to check or stop. Empty or missing for a new run."},
                    "workdir": {"type": "string", "description": "Where to run: workspace/<path> for a directory inside the session workspace, or a full absolute path elsewhere. Empty means the workspace root."},
                    "cmd": {"type": "string", "description": "The shell command to run. Required for action run. Relative paths inside the command resolve against workdir."},
                    "background": {"type": "boolean", "description": "When true the call returns at once with a running job_id. Defaults to false."},
                    "notify": {"type": "boolean", "description": "With background true, remind the agent when the job finishes: its final state and output tail arrive on their own in a later round. Defaults to false; ignored without background."},
                    "timeout_ms": {"type": "integer", "description": "How long a command may run, in milliseconds. Defaults to 10000.", "minimum": 1}
                }
            }),
        }
    }

    async fn call(&self, args: serde_json::Value, ctx: &ToolCtx) -> Result<String, ToolError> {
        let args: ShellArgs = serde_json::from_value(args).map_err(|e| {
            ToolError::args(format!("shell: the arguments are not valid JSON: {e}"))
        })?;
        match args.action.as_str() {
            "" | "run" => {
                if args.cmd.trim().is_empty() {
                    return Err(ToolError::args("shell: cmd is required for action run"));
                }
                let limit = Self::timeout_ms(args.timeout_ms).map_err(ToolError::args)?;
                let dir = Self::resolve_workdir(&args.workdir, &ctx.workspace)
                    .map_err(ToolError::args)?;
                let info = std::fs::metadata(&dir).map_err(|e| {
                    if e.kind() == std::io::ErrorKind::NotFound {
                        ToolError::args(format!("shell: workdir {:?} does not exist", args.workdir))
                    } else {
                        ToolError::args(format!(
                            "shell: workdir {:?} cannot be used: {e}",
                            args.workdir
                        ))
                    }
                })?;
                if !info.is_dir() {
                    return Err(ToolError::args(format!(
                        "shell: workdir {:?} is not a directory",
                        args.workdir
                    )));
                }
                if args.background {
                    // Background runs detach; notify arming happens per-turn.
                    self.run_background(&args.cmd, &dir, limit, args.notify, &ctx.session_dir)
                } else {
                    Self::run_sync_via_ctx(&args.cmd, &dir, limit, &ctx.cancel)
                }
            }
            "status" => {
                if args.job_id.trim().is_empty() {
                    return Err(ToolError::args("shell: job_id is required for this action"));
                }
                self.status(&args.job_id)
            }
            "cancel" => {
                if args.job_id.trim().is_empty() {
                    return Err(ToolError::args("shell: job_id is required for this action"));
                }
                self.cancel(&args.job_id)
            }
            other => Err(ToolError::args(format!(
                "shell: action must be one of run, status or cancel, got {other:?}"
            ))),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx() -> (tempfile::TempDir, ToolCtx) {
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::create_dir_all(&ws).unwrap();
        let sess = dir.path().join("sess");
        std::fs::create_dir_all(&sess).unwrap();
        let ctx = ToolCtx {
            workspace: ws,
            session_dir: sess,
            cancel: tokio_util::sync::CancellationToken::new(),
        };
        // dir is moved out to keep paths alive for the test.
        (dir, ctx)
    }

    #[tokio::test]
    async fn sync_run_returns_output() {
        let (_dir, ctx) = ctx();
        let tool = ShellTool::new();
        let out: ShellResponse = serde_json::from_str(
            &tool
                .call(serde_json::json!({"cmd": "echo hi"}), &ctx)
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(out.status, JOB_DONE);
        assert_eq!(out.output.as_deref(), Some("hi\n"));
    }

    #[tokio::test]
    async fn failing_command_is_tool_error() {
        let (_dir, ctx) = ctx();
        let tool = ShellTool::new();
        let err = tool
            .call(serde_json::json!({"cmd": "exit 3"}), &ctx)
            .await
            .unwrap_err();
        assert!(err.to_string().contains("status 3"));
    }

    #[tokio::test]
    async fn background_status_cancel() {
        let (_dir, ctx) = ctx();
        let tool = ShellTool::new();
        let out: ShellResponse = serde_json::from_str(
            &tool
                .call(
                    serde_json::json!({"cmd": "sleep 30", "background": true}),
                    &ctx,
                )
                .await
                .unwrap(),
        )
        .unwrap();
        let job_id = out.job_id.unwrap();
        let status: ShellResponse = serde_json::from_str(
            &tool
                .call(
                    serde_json::json!({"action": "status", "job_id": job_id}),
                    &ctx,
                )
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(status.status, JOB_RUNNING);
        let cancelled: ShellResponse = serde_json::from_str(
            &tool
                .call(
                    serde_json::json!({"action": "cancel", "job_id": job_id}),
                    &ctx,
                )
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(cancelled.status, JOB_CANCELLED);
    }
}
