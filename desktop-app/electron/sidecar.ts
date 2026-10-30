// The agent-host sidecar: one child process speaking JSONL over stdio.
// Requests are `{id, method, params}` lines; replies carry the same id,
// and lines with an `event` field are pushed to onEvent. A host that
// exits unexpectedly is restarted with exponential backoff; calls made
// while it is down fail fast. No Electron imports, so it unit-tests in
// plain Node.

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

type SidecarOptions = {
  command: string;
  args?: string[];
  onEvent: (msg: HostMessage) => void;
  // A replacement host is up after a crash; clients should re-sync.
  onRestart: () => void;
  log?: (line: string) => void;
  backoff?: Backoff;
};

type PendingCall = { resolve: (value: unknown) => void; reject: (error: Error) => void };

const DEFAULT_BACKOFF: Backoff = { initialMs: 500, maxMs: 30_000, stableMs: 60_000 };

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
  private readonly log: (line: string) => void;

  constructor(private readonly options: SidecarOptions) {
    this.backoff = options.backoff ?? DEFAULT_BACKOFF;
    this.log = options.log ?? ((line) => console.error(line));
  }

  get running(): boolean {
    return this.child !== null && this.child.exitCode === null && this.child.signalCode === null;
  }

  start(): void {
    this.stopping = false;
    this.spawnHost(false);
  }

  invoke(method: string, params: unknown = {}): Promise<unknown> {
    const stdin = this.running ? this.child?.stdin : null;
    if (!stdin?.writable) {
      return Promise.reject(new Error("agent-host is not running"));
    }
    const id = `m${this.nextId++}`;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      stdin.write(JSON.stringify({ id, method, params }) + "\n", (error) => {
        if (error) this.settle(id, new Error(`agent-host write failed: ${error.message}`));
      });
    });
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
    const child = spawn(command, args, { stdio: ["pipe", "pipe", "inherit"] });
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
      this.options.onRestart();
    }
  }

  private handleLine(line: string): void {
    if (!line.trim()) return;
    let msg: HostMessage;
    try {
      msg = JSON.parse(line) as HostMessage;
    } catch {
      this.log(`[agent-host] non-JSON line: ${line.slice(0, 200)}`);
      return;
    }
    if (typeof msg.event === "string") {
      this.options.onEvent(msg);
      return;
    }
    if (msg.id === undefined) return;
    const error = msg.ok ? null : new Error(typeof msg.error === "string" ? msg.error : "agent error");
    this.settle(String(msg.id), error, msg.result);
  }

  private settle(id: string, error: Error | null, result?: unknown): void {
    const call = this.pending.get(id);
    if (!call) return;
    this.pending.delete(id);
    if (error) call.reject(error);
    else call.resolve(result);
  }

  private handleExit(child: ChildProcess, reason: string): void {
    if (this.child !== child) return;
    this.child = null;
    for (const call of this.pending.values()) call.reject(new Error(reason));
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
