// Voice dictation: a Moonshine WASM transcriber in a utility process.
//
// The renderer captures microphone audio (getUserMedia) and sends 16 kHz
// mono float32 chunks over IPC. This module forwards them to a utility
// process that owns the WebAssembly module and the streaming transcriber,
// then forwards the line events to the window as `voice:event`. The model
// stays warm for five minutes after a dictation, then the process exits so
// an idle Paladin holds no voice memory at all.
//
// The WebAssembly runtime and the model are the same files on Linux, macOS
// and Windows, so there is nothing native to compile and nothing to ship per
// platform.

import { existsSync, mkdirSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { app, shell, utilityProcess, type UtilityProcess } from "electron";
import { paladinDataDir } from "./pluginHost";

export type VoiceModel = {
  id: string;
  label: string;
  arch: number;
  language: string;
  description?: string;
  source: "bundled" | "user";
};

export type VoiceOverview = {
  available: boolean;
  models: VoiceModel[];
  runtime?: string;
};

export type VoiceEvent = {
  event: "ready" | "line" | "stopped" | "cancelled" | "error";
  index?: number;
  id?: string;
  text?: string;
  complete?: boolean;
  latencyMs?: number;
  message?: string;
  arch?: number;
  dir?: string;
};

const MODEL_ID = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const STREAMING_ARCHS = new Set([2, 4, 5]);
const MAX_AUDIO_SAMPLES = 16000 * 2; // two seconds; the renderer sends 100 ms
const IDLE_UNLOAD_MS = 5 * 60 * 1000;
const LOAD_TIMEOUT_MS = 30_000;

type ResolvedModel = VoiceModel & { dir: string };

export class VoiceManager {
  private child: UtilityProcess | null = null;
  private running = false;
  private starting = false;
  private idleTimer: NodeJS.Timeout | null = null;
  private pending: { resolve: () => void; reject: (error: Error) => void; timer: NodeJS.Timeout } | null = null;
  private disposed = false;
  private queue: Promise<unknown> = Promise.resolve();

  onEvent: ((event: VoiceEvent) => void) | null = null;

  private voiceRoot(): string {
    return app.isPackaged ? path.join(process.resourcesPath, "voice") : path.join(__dirname, "..", "voice");
  }

  workerPath(): string {
    return path.join(this.voiceRoot(), "worker.mjs");
  }

  wasmPath(): string {
    return path.join(this.voiceRoot(), "wasm", "dist", "transcriber.js");
  }

  private modelRoots(): string[] {
    return [path.join(this.voiceRoot(), "models"), path.join(paladinDataDir(), "voice", "models")];
  }

  // overview lists the models found in the app bundle and in the user's
  // data directory, and whether the WASM runtime is present at all.
  overview(): VoiceOverview {
    return {
      available: existsSync(this.workerPath()) && existsSync(this.wasmPath()),
      models: this.models(),
      runtime: this.wasmPath(),
    };
  }

  models(): VoiceModel[] {
    const found = new Map<string, VoiceModel>();
    for (const [index, root] of this.modelRoots().entries()) {
      let names: string[];
      try {
        names = readdirSync(root);
      } catch {
        continue;
      }
      for (const name of names) {
        const dir = path.join(root, name);
        try {
          if (!statSync(dir).isDirectory()) continue;
        } catch {
          continue;
        }
        const model = this.readModel(dir, index === 0 ? "bundled" : "user");
        if (!model) continue;
        // A bundled model wins over a user copy with the same id.
        if (!found.has(model.id)) found.set(model.id, model);
      }
    }
    return [...found.values()].sort((a, b) => a.arch - b.arch || a.label.localeCompare(b.label));
  }

  private readModel(dir: string, source: "bundled" | "user"): VoiceModel | null {
    let raw: unknown;
    try {
      const text = readFileSync(path.join(dir, "model.json"), "utf8");
      if (text.length > 64 << 10) return null;
      raw = JSON.parse(text);
    } catch {
      return null;
    }
    if (!raw || typeof raw !== "object") return null;
    const record = raw as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id : "";
    const label = typeof record.label === "string" ? record.label.trim() : "";
    const arch = typeof record.arch === "number" ? record.arch : -1;
    if (!MODEL_ID.test(id) || !label || label.length > 120 || !STREAMING_ARCHS.has(arch)) return null;
    return {
      id,
      label,
      arch,
      language: typeof record.language === "string" && record.language ? record.language : "en",
      description: typeof record.description === "string" ? record.description.slice(0, 240) : undefined,
      source,
    };
  }

  private resolve(modelId: string): ResolvedModel | null {
    const model = this.models().find((item) => item.id === modelId);
    if (!model) return null;
    for (const root of this.modelRoots()) {
      const dir = path.join(root, model.id);
      if (existsSync(path.join(dir, "model.json"))) return { ...model, dir };
    }
    return null;
  }

  // start loads the chosen model (or reuses the warm one) and opens a
  // fresh stream. Calls are serialized so a quick second press cannot
  // interleave audio with the first load.
  start(modelId: string): Promise<void> {
    const run = this.queue.then(() => this.startNow(modelId));
    this.queue = run.catch(() => undefined);
    return run;
  }

  private async startNow(modelId: string): Promise<void> {
    if (this.disposed) throw new Error("voice is shutting down");
    const model = this.resolve(modelId);
    if (!model) throw new Error(`voice: model "${modelId}" is not installed`);
    if (!existsSync(this.workerPath()) || !existsSync(this.wasmPath())) {
      throw new Error("voice: the dictation runtime is missing; run `make voice` and restart Paladin");
    }
    this.clearIdle();
    this.starting = true;
    try {
      this.ensureChild();
      // Register the wait before posting: a warm model can answer `ready`
      // in the same tick.
      const ready = this.awaitReady();
      this.post({ type: "load", dir: model.dir, arch: model.arch });
      await ready;
      this.running = true;
    } finally {
      this.starting = false;
    }
  }

  stop(): void {
    if (!this.running) return;
    this.post({ type: "stop" });
  }

  cancel(): void {
    if (!this.running) return;
    this.post({ type: "cancel" });
  }

  // audio accepts one chunk of 16 kHz mono float32 samples. The renderer
  // is the only caller, and main verifies the frame before this point.
  audio(samples: Float32Array): void {
    if (!this.running || samples.length === 0 || samples.length > MAX_AUDIO_SAMPLES) return;
    this.post({ type: "audio", audio: samples });
  }

  openModelsFolder(): string {
    const dir = path.join(paladinDataDir(), "voice", "models");
    mkdirSync(dir, { recursive: true });
    void shell.openPath(dir);
    return dir;
  }

  dispose(): void {
    this.disposed = true;
    this.clearIdle();
    this.rejectPending(new Error("voice is shutting down"));
    const child = this.child;
    this.child = null;
    this.running = false;
    if (!child) return;
    try {
      child.postMessage({ type: "shutdown" });
    } catch {
      // Already gone.
    }
    child.kill();
  }

  private ensureChild(): void {
    if (this.child) return;
    const child = utilityProcess.fork(this.workerPath(), [], {
      serviceName: "paladin-voice",
      stdio: "pipe",
    });
    this.child = child;
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", (chunk: string) => {
      const text = chunk.trim();
      if (text) console.log(`[voice] ${text}`);
    });
    child.stderr?.setEncoding("utf8");
    child.stderr?.on("data", (chunk: string) => {
      const text = chunk.trim();
      if (text) console.log(`[voice] ${text}`);
    });
    child.on("message", (message: VoiceEvent) => this.handleEvent(message));
    child.on("exit", (code) => {
      if (this.child !== child) return;
      this.child = null;
      this.running = false;
      this.rejectPending(new Error("voice: the dictation process exited"));
      if (!this.disposed) {
        console.log(`[voice] process exited (${code})`);
        this.emit({ event: "error", message: "the dictation process stopped unexpectedly" });
      }
    });
  }

  private handleEvent(event: VoiceEvent): void {
    if (!event || typeof event !== "object") return;
    switch (event.event) {
      case "ready":
        this.running = true;
        this.resolvePending();
        break;
      case "stopped":
      case "cancelled":
        this.running = false;
        this.scheduleIdle();
        break;
      case "error":
        this.running = false;
        this.rejectPending(new Error(event.message || "voice: unknown error"));
        break;
      default:
        break;
    }
    this.emit(event);
  }

  private emit(event: VoiceEvent): void {
    this.onEvent?.(event);
  }

  private awaitReady(): Promise<void> {
    if (this.pending) return Promise.reject(new Error("voice is already loading"));
    return new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending = null;
        reject(new Error("voice: the model took too long to load"));
      }, LOAD_TIMEOUT_MS);
      this.pending = { resolve, reject, timer };
    });
  }

  private resolvePending(): void {
    const pending = this.pending;
    if (!pending) return;
    this.pending = null;
    clearTimeout(pending.timer);
    pending.resolve();
  }

  private rejectPending(error: Error): void {
    const pending = this.pending;
    if (!pending) return;
    this.pending = null;
    clearTimeout(pending.timer);
    pending.reject(error);
  }

  private post(message: unknown): void {
    const child = this.child;
    if (!child) return;
    try {
      child.postMessage(message);
    } catch (error) {
      console.log(`[voice] post: ${String(error)}`);
    }
  }

  private scheduleIdle(): void {
    this.clearIdle();
    this.idleTimer = setTimeout(() => {
      this.idleTimer = null;
      this.unloadIdle();
    }, IDLE_UNLOAD_MS);
  }

  private clearIdle(): void {
    if (!this.idleTimer) return;
    clearTimeout(this.idleTimer);
    this.idleTimer = null;
  }

  private unloadIdle(): void {
    // No dictation for five minutes: drop the model and its memory. The
    // next press respawns the process and reloads it.
    if (this.running || this.starting || !this.child) return;
    const child = this.child;
    this.child = null;
    try {
      child.postMessage({ type: "shutdown" });
    } catch {
      // Already gone.
    }
    child.kill();
    console.log("[voice] unloaded the idle model");
  }
}
