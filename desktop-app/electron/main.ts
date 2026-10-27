// Electron main: window + agent-host sidecar (JSONL stdio) + WebContentsViews.
// Renderer never touches the sidecar directly; it goes through ipcHandler
// channels exposed by preload. stdout stays pure JSONL; logs go to stderr.

import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import { app, BrowserWindow, ipcMain, nativeTheme, shell, WebContentsView } from "electron";
import readline from "node:readline";

type Json = any;

// Match Chrome's wheel behaviour: interpolate ticks instead of snapping.
// (Harmless if the platform already defaults to smooth scrolling.)
app.commandLine.appendSwitch("enable-smooth-scrolling");

let win: BrowserWindow | null = null;
let agent: ChildProcess | null = null;
let nextId = 1;
// CJS (tsconfig.electron): __dirname is the dist-electron dir.
const HERE = __dirname;
const pending = new Map<string, { resolve: (v: any) => void; reject: (e: Error) => void }>();
type ViewEntry = {
  view: WebContentsView;
  url: string;
  partition: string;
  applied?: { x: number; y: number; width: number; height: number };
};

const views = new Map<string, ViewEntry>();
let visibleView: string | null = null;
let lastBounds: { x: number; y: number; width: number; height: number } | null = null;

// The shell is frameless, so the renderer owns the title bar and the
// window buttons. Renderer dark/light is mirrored here because guest
// pages are native views: their background must match the shell before
// the first paint, or a dark shell flashes white on every open.
type AppTheme = "dark" | "light";
let appTheme: AppTheme = "dark";

function chromeColor(): string {
  // Matches the palette in styles.css: slate-950 is #06090e.
  return appTheme === "dark" ? "#06090e" : "#ffffff";
}

function paintViewBackground(entry: ViewEntry): void {
  try {
    entry.view.setBackgroundColor(chromeColor());
  } catch {
    // Window is closing; ignore.
  }
}

function applyTheme(theme: AppTheme): void {
  appTheme = theme;
  const color = chromeColor();
  // Guest pages read prefers-color-scheme, not the shell's CSS class. Tell
  // them the same theme so a site with a system theme loads dark instead
  // of flashing its white default in a dark window.
  nativeTheme.themeSource = theme;
  try {
    win?.setBackgroundColor(color);
  } catch {
    // Window is closing; ignore.
  }
  for (const entry of views.values()) {
    paintViewBackground(entry);
  }
}

function agentBin(): string {
  // Dev: go build output next to the repo. Prod: bundled resource.
  if (app.isPackaged) {
    return path.join(process.resourcesPath, "agent-host", "agent-host");
  }
  return path.join(HERE, "..", "..", "core", "bin", "agent-host");
}

function sendLine(obj: Json): void {
  if (!agent?.stdin) throw new Error("agent-host is not running");
  agent.stdin.write(JSON.stringify(obj) + "\n");
}

function invokeAgent(method: string, params: Json = {}): Promise<any> {
  const id = `m${nextId++}`;
  return new Promise((resolve, reject) => {
    pending.set(String(id), { resolve, reject });
    try {
      sendLine({ id, method, params });
    } catch (e) {
      pending.delete(String(id));
      reject(e as Error);
    }
  });
}

function startAgent(): void {
  const bin = agentBin();
  agent = spawn(bin, [], { stdio: ["pipe", "pipe", "inherit"] });
  agent.on("error", (e) => {
    console.error(`[agent-host] spawn failed (${bin}):`, e.message);
  });
  agent.on("exit", (code) => {
    console.error(`[agent-host] exited with code ${code}`);
    for (const { reject } of pending.values()) reject(new Error("agent-host exited"));
    pending.clear();
  });
  const rl = readline.createInterface({ input: agent.stdout! });
  rl.on("line", (line) => {
    if (!line.trim()) return;
    let msg: Json;
    try {
      msg = JSON.parse(line);
    } catch {
      console.error("[agent-host] non-JSON line:", line.slice(0, 200));
      return;
    }
    if (msg.event === "chat/delta" || msg.event === "chat/tool") {
      win?.webContents.send("agent:chat-event", msg);
      return;
    }
    if (msg.id !== undefined) {
      const key = String(msg.id);
      const p = pending.get(key);
      if (!p) return;
      pending.delete(key);
      if (msg.ok) p.resolve(msg.result);
      else p.reject(new Error(typeof msg.error === "string" ? msg.error : "agent error"));
    }
  });
}

// Guest pages get a plain Chrome UA (no Electron token): heavy sites
// UA-sniff for bundles and bot checks, and the Electron token buys us
// the degraded path.
let guestUA = "";
function userAgentForView(): string {
  if (!guestUA && win) {
    try {
      guestUA = win.webContents.getUserAgent().replace(/ Electron\/\S+/, "");
    } catch {
      guestUA = "";
    }
  }
  return guestUA;
}

function ensureView(name: string, url: string, partition: string): WebContentsView {
  const existing = views.get(name);
  if (existing) {
    if (existing.url !== url) {
      existing.url = url;
      existing.view.webContents.loadURL(url).catch(() => undefined);
    }
    return existing.view;
  }
  const view = new WebContentsView({
    webPreferences: {
      partition,
      sandbox: true,
      // Guest pages are the content of this app: a visible one must not be
      // throttled to 1fps when the window loses focus. Hidden guests get
      // throttling back in showOnly so they stop burning a core.
      backgroundThrottling: false,
      // Spelling underlines on every keystroke in a chat composer are pure
      // overhead; the pages run their own editors anyway.
      spellcheck: false,
    },
  });
  // Paint the view's surface in the shell colour from the first frame.
  // Without this the compositor shows its default white until the page
  // commits, which is jarring (and blinding) on a dark theme.
  try {
    view.setBackgroundColor(chromeColor());
  } catch {
    // Older Electron; the window background still covers the gap.
  }
  const ua = userAgentForView();
  if (ua) {
    view.webContents.setUserAgent(ua);
  }
  view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  views.set(name, { view, url, partition });
  view.webContents.loadURL(url).catch(() => undefined);
  return view;
}

function layoutVisible(): void {
  if (!win || win.isDestroyed() || !visibleView || !lastBounds) return;
  const entry = views.get(visibleView);
  if (!entry) return;
  const b = lastBounds;
  const next = {
    x: Math.max(0, Math.round(b.x)),
    y: Math.max(0, Math.round(b.y)),
    width: Math.max(1, Math.round(b.width)),
    height: Math.max(1, Math.round(b.height)),
  };
  const prev = entry.applied;
  // setBounds recreates the guest surface: skip no-ops so resizes and
  // repeat shows don't stutter an already-placed page.
  if (
    prev &&
    prev.x === next.x &&
    prev.y === next.y &&
    prev.width === next.width &&
    prev.height === next.height
  ) {
    return;
  }
  try {
    entry.view.setBounds(next);
    entry.applied = next;
  } catch {
    // Window is closing; ignore.
  }
}

function showOnly(name: string | null): void {
  if (!win || win.isDestroyed()) return;
  // Same page already up: no detach/reattach, just confirm bounds.
  if (name && name !== "agent" && visibleView === name && views.has(name)) {
    layoutVisible();
    return;
  }
  for (const [, entry] of views) {
    try {
      win.contentView.removeChildView(entry.view);
    } catch {
      // Already removed.
    }
    // Hidden pages may throttle: they keep their JS state but stop
    // compositing/timers at full speed in the background.
    try {
      entry.view.webContents.setBackgroundThrottling(true);
    } catch {
      // Destroyed.
    }
  }
  visibleView = null;
  if (name && name !== "agent") {
    const entry = views.get(name);
    if (entry) {
      win.contentView.addChildView(entry.view);
      visibleView = name;
      // The page on screen is the app's foreground: never throttle it.
      try {
        entry.view.webContents.setBackgroundThrottling(false);
      } catch {
        // Destroyed.
      }
      layoutVisible();
    }
  }
}

function createWindow(): void {
  win = new BrowserWindow({
    width: 1400,
    height: 900,
    autoHideMenuBar: true,
    // No OS title bar: the renderer draws the header and the window
    // buttons, so there is exactly one set of controls.
    frame: false,
    // Painted before the first frame: no white flash between window
    // creation and the renderer's dark background.
    backgroundColor: chromeColor(),
    // Start on the whole screen, not a small floating rectangle. Wait
    // for the first frame before showing so the maximize is not a jump.
    show: false,
    webPreferences: {
      preload: path.join(HERE, "preload.js"),
      contextIsolation: true,
      sandbox: true,
    },
  });

  win.once("ready-to-show", () => {
    win?.maximize();
    win?.show();
  });

  if (!app.isPackaged && process.env.ELECTRON_START_URL) {
    win.loadURL(process.env.ELECTRON_START_URL).catch(console.error);
  } else if (!app.isPackaged) {
    win.loadURL("http://localhost:5173").catch(console.error);
  } else {
    win.loadFile(path.join(HERE, "..", "dist", "index.html"));
  }

  win.on("resized", layoutVisible);
  win.on("moved", layoutVisible);
  // GPU status is only meaningful once a window (and thus Viz) exists:
  // logged a moment after the first load. If this says software, the
  // guest pages are rasterising on the CPU and will feel sluggish no
  // matter what the shell does.
  win.webContents.once("did-finish-load", () => {
    setTimeout(() => {
      try {
        console.error(`[gpu] ${JSON.stringify(app.getGPUFeatureStatus())}`);
      } catch {
        // Not available.
      }
    }, 2000);
  });
  const notifyResized = (): void => {
    win?.webContents.send("window:resized");
  };
  win.on("resize", notifyResized);
  win.on("maximize", notifyResized);
  win.on("unmaximize", notifyResized);
  win.contentView.on("bounds-changed", layoutVisible);
}

app.whenReady().then(() => {
  nativeTheme.themeSource = appTheme;
  startAgent();

  ipcMain.handle("agent:invoke", (_e, method: string, params: Json) => invokeAgent(method, params));

  ipcMain.handle("agent:chat", (e, params: Json) => {
    const streamId = `s${nextId++}`;
    pending.set(streamId, {
      resolve: () => undefined,
      reject: () => undefined,
    });
    // Completion arrives as {id: streamId, ok, result|error}; forward it
    // to this sender only, then forget the stream.
    const orig = pending.get(streamId)!;
    pending.set(streamId, {
      resolve: (result) => {
        e.sender.send("agent:chat-done", { streamId, ok: true, result });
        orig.resolve(result);
      },
      reject: (err) => {
        e.sender.send("agent:chat-done", { streamId, ok: false, error: String(err?.message ?? err) });
        orig.reject(err);
      },
    });
    try {
      sendLine({ id: streamId, method: "chat", params });
    } catch (err) {
      pending.delete(streamId);
      throw err;
    }
    return streamId;
  });

  ipcMain.handle("views:ensure", (_e, name: string, url: string, partition: string) => {
    if (!win) return false;
    ensureView(name, url, partition);
    return true;
  });
  ipcMain.handle("views:show", (_e, name: string) => {
    showOnly(name);
    return true;
  });
  ipcMain.handle("views:setBounds", (_e, bounds: Json) => {
    lastBounds = bounds;
    layoutVisible();
    return true;
  });
  ipcMain.handle("views:navigate", (_e, name: string, url: string) => {
    const entry = views.get(name);
    if (!entry) return false;
    entry.url = url;
    entry.view.webContents.loadURL(url).catch(() => undefined);
    return true;
  });
  ipcMain.handle("views:reload", (_e, name: string) => {
    views.get(name)?.view.webContents.reload();
    return true;
  });
  ipcMain.handle("shell:openExternal", (_e, url: string) => shell.openExternal(url));

  ipcMain.handle("window:minimize", () => win?.minimize());
  ipcMain.handle("window:toggleMaximize", () => {
    if (!win) return;
    if (win.isMaximized()) win.unmaximize();
    else win.maximize();
  });
  ipcMain.handle("window:close", () => win?.close());
  ipcMain.handle("window:isMaximized", () => win?.isMaximized() ?? false);

  ipcMain.handle("views:setTheme", (_e, theme: string) => {
    applyTheme(theme === "light" ? "light" : "dark");
    return true;
  });
  ipcMain.handle("views:drop", (_e, name: string) => {
    const entry = views.get(name);
    if (!entry) return true;
    if (win) {
      try {
        win.contentView.removeChildView(entry.view);
      } catch {
        // Ignore.
      }
    }
    try {
      (entry.view.webContents as any).destroy?.();
    } catch {
      // Ignore.
    }
    views.delete(name);
    if (visibleView === name) visibleView = null;
    return true;
  });

  createWindow();
  // Headless smoke: --smoke-test loads the window, pings the sidecar
  // over JSONL, reports, and exits (non-zero on any failure).
  if (process.argv.includes("--smoke-test")) {
    const fail = (msg: string): never => {
      console.error(`[smoke] FAIL: ${msg}`);
      process.exit(1);
    };
    win!.webContents.on("did-fail-load", (_e, code, desc) => fail(`renderer load failed ${code}: ${desc}`));
    win!.webContents.on("console-message", (_e, _level, message) => {
      if (/preload bridge missing|Uncaught/.test(message)) fail(`renderer console: ${message}`);
    });
    const timer = setTimeout(() => fail("timed out waiting for load + ping"), 25000);
    win!.webContents.on("did-finish-load", () => {
      // The frame can be momentarily undisposed-ready right after load
      // (worse headless); poll briefly instead of failing on one shot.
      const probeBridge = (tries: number): void => {
        win!.webContents
          .executeJavaScript("typeof window.paladin")
          .then((t) => {
            if (t !== "object") {
              if (tries > 0) {
                setTimeout(() => probeBridge(tries - 1), 250);
              } else {
                fail(`window.paladin is ${String(t)} (preload bridge missing)`);
              }
            }
          })
          .catch((e) => {
            if (tries > 0) {
              setTimeout(() => probeBridge(tries - 1), 250);
            } else {
              fail(`bridge probe failed: ${e.message}`);
            }
          });
      };
      probeBridge(12);
      invokeAgent("ping")
        .then((r) => {
          clearTimeout(timer);
          if (r?.ok === true) {
            console.log("[smoke] PASS: renderer loaded, agent-host ping ok");
            process.exit(0);
          } else fail(`unexpected ping result: ${JSON.stringify(r)}`);
        })
        .catch((e) => fail(`ping failed: ${e.message}`));
    });
  }
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", () => {
  try {
    agent?.kill();
  } catch {
    // Ignore.
  }
});
