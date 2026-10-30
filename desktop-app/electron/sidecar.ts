// The agent-host sidecar: one child process speaking JSONL over stdio.
// Requests are `{id, method, params}` lines; replies carry the same id
// (in any order), and lines with an `event` field are pushed to
// onEvent. Every call has a deadline and is never retried here: a
// mutation that times out may still have happened. A host that exits
// unexpectedly is restarted with exponential backoff and announced once
// it answers a ping; calls made while it is down fail fast. No Electron
// imports, so it unit-tests in plain Node.

import { spawn, type ChildProcess } from "node:child_process";
import readline from "node:readline";
import type { HostEvent } from "./turnNotify";

// HostMessage is one line from the host: a reply ({id, ok,
// result|error}) or an event ({event, sessionId, ...}).
export type HostMessage = HostEvent & { id?: string | number; ok?: boolean; result?: unknown; error?: unknown };

type Backoff = {
  initialMs: number;
  maxMs: number;
  // A host that ran this long counts as healthy: the next crash starts
  // the backoff from scratch.
  stableMs: number;
};

// Deadlines are how long a call may wait for its reply.
type Deadlines = {
  // Reads have no side effects.
  readMs: number;
  // Anything else may have taken effect when it times out.
  writeMs: number;
  // Model and network calls.
  longMs: number;
};

type SidecarOptions = {
  command: string;
  args?: string[];
  env?: NodeJS.ProcessEnv;
  onEvent: (msg: HostMessage) => void;
  // A replacement host is up after a crash; clients should re-sync.
  onRestart: () => void;
  log?: (line: string) => void;
  backoff?: Backoff;
  deadlines?: Deadlines;
  // Calls waiting for a reply; more are refused as busy.
  maxPending?: number;
  // Request bytes buffered for a host that is not reading; more are
  // refused as busy.
  maxBufferedBytes?: number;
};

type PendingCall = {
  method: string;
  timer: NodeJS.Timeout;
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
};

const DEFAULT_BACKOFF: Backoff = { initialMs: 500, maxMs: 30_000, stableMs: 60_000 };
const DEFAULT_DEADLINES: Deadlines = { readMs: 30_000, writeMs: 120_000, longMs: 600_000 };
const DEFAULT_MAX_PENDING = 512;
const DEFAULT_MAX_BUFFERED_BYTES = 16 * 1024 * 1024;
// A restarted host must answer a ping this fast, a few times over,
// before the renderer is told to re-sync.
const READY_PING_MS = 1000;
const READY_TRIES = 30;

// READ_METHODS have no side effects; the agent-host dispatcher runs
// them as reads (plus the inline status calls).
const READ_METHODS = new Set([
  "ping",
  "turns",
  "resources",
  "getWorkspace",
  "fsHome",
  "resolveWorkspace",
  "listProviders",
  "fsList",
  "fileComplete",
  "gitInfo",
  "getPrompt",
  "listSkills",
  "listCommands",
  "memoryGet",
  "memoryProjects",
  "listSessions",
  "sessionHistory",
  "sessionTodos",
  "sessionUsage",
  "budgetGet",
  "thinkingGet",
  "overallUsage",
  "revertPreview",
  "worktreeStatus",
  "attach",
  "permissionsGet",
  "listAgents",
  "listSideChats",
  "loadSideChat",
  "hooksList",
  "mcpList",
  "mcpConfig",
  "mcpParse",
  "toolsList",
]);
const LONG_METHODS = new Set(["refreshProviders", "sideQuestion"]);

export function isReadMethod(method: string): boolean {
  return READ_METHODS.has(method);
}

// deadlineFor is how long a call to method may wait for its reply.
export function deadlineFor(method: string, deadlines: Deadlines): number {
  if (LONG_METHODS.has(method)) return deadlines.longMs;
  return isReadMethod(method) ? deadlines.readMs : deadlines.writeMs;
}

// agentHostFile is the sidecar's file name on a platform; Windows only
// runs executables that carry the .exe extension.
export function agentHostFile(platform: NodeJS.Platform = process.platform): string {
  return platform === "win32" ? "agent-host.exe" : "agent-host";
}

// restartDelay is the wait before restart number attempt (0-based).
export function restartDelay(attempt: number, backoff: Backoff): number {
  return Math.min(backoff.initialMs * 2 ** attempt, backoff.maxMs);
}

export class Sidecar {
  private child: ChildProcess | null = null;
  private readonly pending = new Map<string, PendingCall>();
  private nextId = 1;
  private crashes = 0;
  private startedAt = 0;
  private restartTimer: NodeJS.Timeout | null = null;
  private stopping = false;
  private readonly backoff: Backoff;
  private readonly deadlines: Deadlines;
  private readonly log: (line: string) => void;

  constructor(private readonly options: SidecarOptions) {
    this.backoff = options.backoff ?? DEFAULT_BACKOFF;
    this.deadlines = options.deadlines ?? DEFAULT_DEADLINES;
    this.log = options.log ?? ((line) => console.error(line));
  }

  get running(): boolean {
    return this.child !== null && this.child.exitCode === null && this.child.signalCode === null;
  }

  start(): void {
    this.stopping = false;
    this.spawnHost(false);
  }

  invoke(method: string, params: unknown = {}, deadlineMs = deadlineFor(method, this.deadlines)): Promise<unknown> {
    const stdin = this.running ? this.child?.stdin : null;
    if (!stdin?.writable) {
      return Promise.reject(new Error("agent-host is not running"));
    }
    const maxPending = this.options.maxPending ?? DEFAULT_MAX_PENDING;
    const maxBuffered = this.options.maxBufferedBytes ?? DEFAULT_MAX_BUFFERED_BYTES;
    if (this.pending.size >= maxPending || stdin.writableLength >= maxBuffered) {
      return Promise.reject(new Error("agent-host is busy, try again"));
    }
    const id = `m${this.nextId++}`;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => this.settle(id, this.timeoutError(method, deadlineMs)), deadlineMs);
      this.pending.set(id, { method, timer, resolve, reject });
      stdin.write(JSON.stringify({ id, method, params }) + "\n", (error) => {
        if (error) this.settle(id, new Error(`agent-host write failed: ${error.message}`));
      });
    });
  }

  private timeoutError(method: string, deadlineMs: number): Error {
    const after = `agent-host did not answer ${method} within ${deadlineMs / 1000}s`;
    if (isReadMethod(method)) return new Error(after);
    return new Error(`${after}; it may still have completed, so check before trying again`);
  }

  // stop closes the host's stdin (it persists running turns and exits)
  // and kills it if it is still alive after graceMs. No restart follows.
  stop(graceMs: number): Promise<void> {
    this.stopping = true;
    if (this.restartTimer) {
      clearTimeout(this.restartTimer);
      this.restartTimer = null;
    }
    const child = this.child;
    if (!child || !this.running) return Promise.resolve();
    return new Promise((resolve) => {
      const hardKill = setTimeout(() => child.kill("SIGKILL"), graceMs);
      child.once("exit", () => {
        clearTimeout(hardKill);
        resolve();
      });
      child.stdin?.end();
    });
  }

  private spawnHost(isRestart: boolean): void {
    const { command, args = [] } = this.options;
    const child = spawn(command, args, { stdio: ["pipe", "pipe", "inherit"], env: this.options.env });
    this.child = child;
    this.startedAt = Date.now();
    let gone = false;
    // Spawn failures emit only "error"; crashes emit "exit". Either way
    // this runs once per child.
    const onGone = (reason: string): void => {
      if (gone) return;
      gone = true;
      this.handleExit(child, reason);
    };
    child.on("error", (error) => {
      this.log(`[agent-host] spawn failed (${command}): ${error.message}`);
      onGone("agent-host failed to start");
    });
    child.on("exit", (code, signal) => {
      this.log(`[agent-host] exited with ${signal ?? `code ${code}`}`);
      onGone("agent-host exited");
    });
    // Writes to a host that just died fail per call (see invoke); this
    // keeps the stream error from crashing the main process.
    child.stdin?.on("error", (error) => this.log(`[agent-host] stdin: ${error.message}`));
    if (child.stdout) {
      readline.createInterface({ input: child.stdout }).on("line", (line) => this.handleLine(line));
    }
    if (isRestart && child.pid !== undefined) {
      void this.announceWhenReady(child);
    }
  }

  // announceWhenReady tells the client about a restarted host only once
  // it answers, so the client's re-sync is not lost to a host that is
  // still starting. A host that dies first is never announced.
  private async announceWhenReady(child: ChildProcess): Promise<void> {
    for (let tries = 0; tries < READY_TRIES && this.child === child; tries++) {
      try {
        await this.invoke("ping", {}, READY_PING_MS);
        if (this.child === child) this.options.onRestart();
        return;
      } catch {
        // Not up yet, or gone: the loop condition decides.
      }
    }
    if (this.child === child) {
      this.log("[agent-host] restarted host never answered a ping; announcing anyway");
      this.options.onRestart();
    }
  }

  private handleLine(line: string): void {
    if (!line.trim()) return;
    let msg: unknown;
    try {
      msg = JSON.parse(line);
    } catch {
      this.rejectMalformed(line);
      return;
    }
    if (typeof msg !== "object" || msg === null || Array.isArray(msg)) {
      this.log(`[agent-host] unexpected line: ${line.slice(0, 200)}`);
      return;
    }
    const reply = msg as HostMessage;
    if (typeof reply.event === "string") {
      this.options.onEvent(reply);
      return;
    }
    if (reply.id === undefined) return;
    const id = String(reply.id);
    if (typeof reply.ok !== "boolean") {
      this.settle(id, this.malformed(id));
      return;
    }
    const error = reply.ok ? null : new Error(errorText(reply.error));
    this.settle(id, error, reply.result);
  }

  // rejectMalformed fails the call a broken line was meant for, when its
  // id can still be read, so the caller does not wait out its deadline.
  private rejectMalformed(line: string): void {
    this.log(`[agent-host] non-JSON line: ${line.slice(0, 200)}`);
    const id = /"id"\s*:\s*"([^"]+)"/.exec(line)?.[1];
    if (id && this.pending.has(id)) this.settle(id, this.malformed(id));
  }

  private malformed(id: string): Error {
    return new Error(`agent-host sent a malformed reply to ${this.pending.get(id)?.method ?? "a call"}`);
  }

  private settle(id: string, error: Error | null, result?: unknown): void {
    const call = this.pending.get(id);
    if (!call) return;
    this.pending.delete(id);
    clearTimeout(call.timer);
    if (error) call.reject(error);
    else call.resolve(result);
  }

  private handleExit(child: ChildProcess, reason: string): void {
    if (this.child !== child) return;
    this.child = null;
    for (const call of this.pending.values()) {
      clearTimeout(call.timer);
      call.reject(new Error(reason));
    }
    this.pending.clear();
    if (this.stopping) return;
    if (Date.now() - this.startedAt >= this.backoff.stableMs) this.crashes = 0;
    const delay = restartDelay(this.crashes++, this.backoff);
    this.log(`[agent-host] restarting in ${delay}ms`);
    this.restartTimer = setTimeout(() => {
      this.restartTimer = null;
      if (!this.stopping) this.spawnHost(true);
    }, delay);
  }
}

function errorText(error: unknown): string {
  if (typeof error === "string" && error) return error;
  if (typeof error === "object" && error !== null && typeof (error as { message?: unknown }).message === "string") {
    return (error as { message: string }).message;
  }
  return "agent error";
}
