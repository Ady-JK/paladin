// Electron main: window + agent-host sidecar (JSONL stdio) + WebContentsViews.
// Renderer never touches the sidecar directly; it goes through ipcHandler
// channels exposed by preload. stdout stays pure JSONL; logs go to stderr.

import path from "node:path";
import fs from "node:fs";
import { pathToFileURL } from "node:url";
import {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  nativeTheme,
  Notification,
  session,
  shell,
  WebContentsView,
  type IpcMainInvokeEvent,
} from "electron";
import { noteFor } from "./turnNotify";
import { agentHostFile, Sidecar, type HostMessage } from "./sidecar";
import { hostCommand, validateConnection, type CoreConnection } from "./remote";
import { allowedChatPopup, guestPermissionAllowed } from "./guestPolicy";
import { BrowserManager, type BrowserRequest, type BrowserState, type EgressSetup } from "./browser";
import { remoteOpenRefusal } from "./browserPolicy";
import { runBrowserSmoke } from "./browserSmoke";
import { refuseAll } from "./browserEgress";
import { appFrameNavigationAllowed, appNavigationAllowed, isMainWindowFrame } from "./mainWindowPolicy";

type ViewBounds = { x: number; y: number; width: number; height: number };

// Match Chrome's wheel behaviour: interpolate ticks instead of snapping.
// (Harmless if the platform already defaults to smooth scrolling.)
app.commandLine.appendSwitch("enable-smooth-scrolling");
// Chromium chooses an ephemeral loopback port; Playwright connects only to
// the session browser targets. The port lives for this app process.
app.commandLine.appendSwitch("remote-debugging-port", "0");
// Every renderer runs in Chromium's sandbox, whatever its webPreferences
// say (each already asks for it; this makes it the rule).
app.enableSandbox();
// Defaults for any web contents the app creates: no <webview> guests and
// no new windows. The windows and views below set their own window-open
// handlers after this, which replace the deny where they allow more.
app.on("web-contents-created", (_event, contents) => {
  contents.on("will-attach-webview", (event) => event.preventDefault());
  contents.setWindowOpenHandler(() => ({ action: "deny" }));
});
app.commandLine.appendSwitch("remote-debugging-address", "127.0.0.1");
// The browser smoke checks the web-site policy without the network: its
// made-up .test names resolve to its own loopback server, where the two
// fixture sites stand in for public ones (rebind.test does not).
const smokeEgress: EgressSetup =
  !app.isPackaged && process.argv.includes("--smoke-browser")
    ? {
        lookup: async (host) => {
          if (host.endsWith(".test") || host === "localhost") return [{ address: "127.0.0.1", family: 4 }];
          throw new Error(`getaddrinfo ENOTFOUND ${host}`);
        },
        trusted: new Set(["site-a.test", "sub.site-a.test", "site-b.test", "sub.site-b.test"]),
      }
    : {};

let win: BrowserWindow | null = null;
// CJS (tsconfig.electron): __dirname is the dist-electron dir.
const HERE = __dirname;
type ViewEntry = {
  view: WebContentsView;
  url: string;
  partition: string;
  applied?: { x: number; y: number; width: number; height: number };
};

const views = new Map<string, ViewEntry>();
let visibleView: string | null = null;
let lastBounds: ViewBounds | null = null;
// A notification click while the window was closed: the fresh renderer
// asks for it once it has booted.
let pendingOpenSession: string | null = null;
let quitting = false;

// The shell is frameless, so the renderer owns the title bar and the
// window buttons. Renderer dark/light is mirrored here because guest
// pages are native views: their background must match the shell before
// the first paint, or a dark shell flashes white on every open.
type AppTheme = "dark" | "light";
let appTheme: AppTheme = "dark";

function chromeColor(): string {
  // Matches the dark shell background in src/styles/base.css (slate-950).
  return appTheme === "dark" ? "#17171a" : "#ffffff";
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
    return path.join(process.resourcesPath, "agent-host", agentHostFile());
  }
  return path.join(HERE, "..", "..", "core", "bin", agentHostFile());
}

let connection: CoreConnection = { mode: "local" };
let agent: Sidecar;

function connectionFile(): string {
  return path.join(app.getPath("userData"), "core-connection.json");
}

function loadConnection(): CoreConnection {
  try {
    return validateConnection(JSON.parse(fs.readFileSync(connectionFile(), "utf8")));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") console.error("invalid core connection", error);
    return { mode: "local" };
  }
}

function createAgent(selected: CoreConnection): Sidecar {
  const sidecar = new Sidecar({
    ...hostCommand(selected, agentBin()),
    onEvent: forwardEvent,
    // The renderer re-reads sessions and ends any interrupted turns.
    onRestart: () => {
      if (agent === sidecar) liveWindow()?.webContents.send("agent:event", { event: "host/restarted" });
    },
  });
  return sidecar;
}

async function switchConnection(next: CoreConnection): Promise<CoreConnection> {
  const checked = validateConnection(next);
  const candidate = createAgent(checked);
  candidate.start();
  try {
    const ping = await candidate.invoke("ping", {}, 12_000);
    if (!ping || typeof ping !== "object" || (ping as { ok?: unknown }).ok !== true) {
      throw new Error("remote core did not answer ping");
    }
  } catch (error) {
    await candidate.stop(1_000);
    throw error;
  }
  const file = connectionFile();
  const pending = `${file}.tmp`;
  try {
    fs.writeFileSync(pending, JSON.stringify(checked), { mode: 0o600 });
    fs.renameSync(pending, file);
  } catch (error) {
    await candidate.stop(1_000);
    throw error;
  }
  await agent.stop(10_000);
  connection = checked;
  agent = candidate;
  liveWindow()?.webContents.send("agent:event", { event: "host/restarted" });
  return checked;
}

function forwardBrowserState(event: BrowserState): void {
  try {
    liveWindow()?.webContents.send("agent:event", event);
  } catch {
    /* renderer reloading */
  }
}

const browsers = new BrowserManager(liveWindow, forwardBrowserState, smokeEgress);
let nextUIBrowserID = 0;

// isWebUrl admits only http(s) URLs: guest views and the OS browser
// must never be handed file:, javascript: or custom-scheme URLs.
function isWebUrl(value: unknown): value is string {
  if (typeof value !== "string") return false;
  try {
    const { protocol } = new URL(value);
    return protocol === "https:" || protocol === "http:";
  } catch {
    return false;
  }
}

function isViewBounds(value: unknown): value is ViewBounds {
  if (typeof value !== "object" || value === null) return false;
  const { x, y, width, height } = value as Record<string, unknown>;
  const finite = (n: unknown): n is number => typeof n === "number" && Number.isFinite(n);
  return finite(x) && finite(y) && finite(width) && finite(height) && width >= 0 && height >= 0;
}

function liveWindow(): BrowserWindow | null {
  return win && !win.isDestroyed() ? win : null;
}

function forwardEvent(msg: HostMessage): void {
  if (msg.event === "browser/request") {
    if (typeof msg.id !== "string" || typeof msg.sessionId !== "string" || typeof msg.action !== "string") return;
    const req = msg as BrowserRequest;
    if (connection.mode === "ssh") {
      // A remote core does not decide this computer's network access.
      req.privateNetwork = false;
      const refusal = req.action === "open" && typeof req.url === "string" ? remoteOpenRefusal(req.url) : "";
      if (refusal) {
        void agent
          .invoke("browserReply", { id: req.id, ok: false, error: refusal })
          .catch((error: unknown) => console.error("browser reply failed", error));
        return;
      }
    }
    void browsers
      .execute(req)
      .then((reply) =>
        agent.invoke("browserReply", reply).catch((error: unknown) => console.error("browser reply failed", error)),
      );
    return;
  }
  const w = liveWindow();
  if (w) {
    try {
      w.webContents.send("agent:event", msg);
    } catch {
      // Renderer is reloading; it re-attaches on boot.
    }
  }
  if (w && w.isFocused()) return;
  const note = noteFor(msg);
  if (!note || !Notification.isSupported()) return;
  const n = new Notification({ title: note.title, body: note.body, silent: false });
  n.on("click", () => openSessionFromNotification(note.sessionId));
  n.show();
}

function openSessionFromNotification(sessionId: string): void {
  const w = liveWindow();
  if (!w) {
    pendingOpenSession = sessionId;
    createWindow();
    return;
  }
  if (w.isMinimized()) w.restore();
  w.show();
  w.focus();
  w.webContents.send("app:open-session", sessionId);
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

// dropView destroys a guest view outright and forgets it. The session
// partition lives on disk, so a re-shown page reloads still logged in —
// this is what keeps N visited chat pages from pinning N heavy renderers
// (~300MB each) in RAM at once.
function dropView(name: string): void {
  const entry = views.get(name);
  if (!entry) {
    return;
  }
  if (win) {
    try {
      win.contentView.removeChildView(entry.view);
    } catch {
      // Already removed.
    }
  }
  try {
    (entry.view.webContents as { destroy?: () => void }).destroy?.();
  } catch {
    // Ignore.
  }
  views.delete(name);
  if (visibleView === name) {
    visibleView = null;
  }
}

// NO_GUEST_DRAG_CSS neutralizes page-declared `-webkit-app-region: drag`
// regions inside guest views. ChatGPT's logged-in shell marks its 52px
// fixed header draggable (PWA window-controls-overlay), and Chromium
// honors that browser-side: real mouse events in the strip are consumed
// for window dragging and never reach the page, so the sidebar toggle and
// search buttons go dead (CDP-injected input still works, which is why it
// looks like the buttons themselves are broken). Our own titleBar stays
// the drag handle, so guests lose nothing.
const NO_GUEST_DRAG_CSS = "html * { -webkit-app-region: no-drag !important; }";

// defuseGuestDrag injects the no-drag override into a guest view. dom-ready
// fires per navigation, so SPA route changes keep working and full loads
// re-arm automatically. Failures (view destroyed mid-load) are ignored.
function defuseGuestDrag(view: WebContentsView): void {
  view.webContents.on("dom-ready", () => {
    try {
      view.webContents.insertCSS(NO_GUEST_DRAG_CSS).catch(() => undefined);
    } catch {
      // Destroyed.
    }
  });
}

// lockGuestSession applies the site-pane permission policy to a
// partition once (its popups share it).
const lockedGuests = new WeakSet<Electron.Session>();
function lockGuestSession(partition: string): void {
  const jar = session.fromPartition(partition);
  if (lockedGuests.has(jar)) return;
  lockedGuests.add(jar);
  jar.setPermissionRequestHandler((_contents, permission, callback) => callback(guestPermissionAllowed(permission)));
  jar.setPermissionCheckHandler((_contents, permission) => guestPermissionAllowed(permission));
  jar.setDevicePermissionHandler(() => false);
}

function ensureView(name: string, url: string, partition: string): WebContentsView {
  const existing = views.get(name);
  if (existing) {
    // The renderer moved every site page to one shared partition so
    // Google SSO cookies carry across pages. A view created under an
    // old per-page partition keeps its old session (the partition is
    // fixed at creation), so rebuild it once on the shared jar.
    if (existing.partition !== partition) {
      dropView(name);
    } else {
      if (existing.url !== url) {
        existing.url = url;
        existing.view.webContents.loadURL(url).catch(() => undefined);
      }
      return existing.view;
    }
  }
  lockGuestSession(partition);
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
  // Sign-in windows for the embedded chat providers need their shared
  // cookie partition. Other destinations keep the existing deny policy.
  view.webContents.setWindowOpenHandler(({ url }) =>
    allowedChatPopup(view.webContents.getURL()) && allowedChatPopup(url)
      ? {
          action: "allow",
          overrideBrowserWindowOptions: {
            autoHideMenuBar: true,
            width: 650,
            height: 760,
            webPreferences: { partition, sandbox: true, contextIsolation: true, nodeIntegration: false },
          },
        }
      : { action: "deny" },
  );
  view.webContents.on("did-create-window", (popup) => {
    popup.webContents.on("will-navigate", (event, nextURL) => {
      if (!allowedChatPopup(nextURL)) event.preventDefault();
    });
    popup.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  });
  defuseGuestDrag(view);
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
  if (prev && prev.x === next.x && prev.y === next.y && prev.width === next.width && prev.height === next.height) {
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
  if (name && name !== "agent") browsers.show(null);
  // Same page already up: no detach/reattach, just confirm bounds.
  if (name && name !== "agent" && visibleView === name && views.has(name)) {
    layoutVisible();
    return;
  }
  // Single live guest: showing a chat page destroys every other guest
  // outright. Each hidden SPA pins a full renderer (~hundreds of MB), so
  // keeping visited pages alive is what ballooned the app past 2GB.
  // Reloads stay logged in (partitions persist on disk); the Agent tab
  // keeps its 60s idle drop instead, so flipping to the agent and back
  // does not nuke the reference page immediately.
  if (name && name !== "agent") {
    for (const other of [...views.keys()]) {
      if (other !== name) {
        dropView(other);
      }
    }
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

// lockAppSession: the app window never needs the network (the core does
// that work), so its session goes through a proxy that refuses
// everything; only the dev server is reached directly. With WebRTC
// limited to that proxy (createWindow), code in a visual cannot send
// data out over WebRTC either, which CSP does not cover.
async function lockAppSession(): Promise<void> {
  const port = await refuseAll();
  let bypass = "localhost;127.0.0.1;[::1]";
  if (!app.isPackaged && process.env.ELECTRON_START_URL) {
    try {
      bypass += `;${new URL(process.env.ELECTRON_START_URL).host}`;
    } catch {
      /* no dev server override */
    }
  }
  await session.defaultSession.setProxy({
    mode: "fixed_servers",
    proxyRules: `http://127.0.0.1:${port}`,
    proxyBypassRules: bypass,
  });
  // The window uses no web permissions (notifications come from the main
  // process): Electron would otherwise grant every request, including to
  // code in a visual.
  const allowed = (permission: string) => permission === "clipboard-sanitized-write";
  session.defaultSession.setPermissionRequestHandler((_contents, permission, callback) =>
    callback(allowed(permission)),
  );
  session.defaultSession.setPermissionCheckHandler((_contents, permission) => allowed(permission));
  session.defaultSession.setDevicePermissionHandler(() => false);
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

  win.webContents.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");

  win.once("ready-to-show", () => {
    // The browser smoke drives a hidden window; it never needs the screen.
    if (process.argv.includes("--smoke-browser")) return;
    win?.maximize();
    win?.show();
  });

  const appURL =
    !app.isPackaged && !process.argv.includes("--smoke-browser")
      ? (process.env.ELECTRON_START_URL ?? "http://localhost:5173")
      : pathToFileURL(path.join(HERE, "..", "dist", "index.html")).href;
  const keepAppPage = (event: Electron.Event, navigation: string | { url: string }): void => {
    const nextURL = typeof navigation === "string" ? navigation : navigation.url;
    if (!appNavigationAllowed(appURL, nextURL)) event.preventDefault();
  };
  win.webContents.on("will-navigate", keepAppPage);
  win.webContents.on("will-redirect", keepAppPage);
  win.webContents.on("will-frame-navigate", (details) => {
    if (!details.isMainFrame && !appFrameNavigationAllowed(appURL, details.url)) details.preventDefault();
  });
  win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));

  if (!app.isPackaged && process.argv.includes("--smoke-browser")) {
    win.loadFile(path.join(HERE, "..", "dist", "index.html")).catch(console.error);
  } else if (!app.isPackaged && process.env.ELECTRON_START_URL) {
    win.loadURL(process.env.ELECTRON_START_URL).catch(console.error);
  } else if (!app.isPackaged) {
    win.loadURL("http://localhost:5173").catch(console.error);
  } else {
    win.loadFile(path.join(HERE, "..", "dist", "index.html")).catch(console.error);
  }

  win.on("resized", layoutVisible);
  win.on("resized", () => browsers.layout());
  win.on("moved", layoutVisible);
  // Closing the window keeps the app (and every running turn) alive on
  // macOS; guest views die with the window, so forget them.
  win.on("closed", () => {
    browsers.windowClosed();
    win = null;
    views.clear();
    visibleView = null;
  });
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

app
  .whenReady()
  .then(async () => {
    nativeTheme.themeSource = appTheme;
    connection = loadConnection();
    agent = createAgent(connection);
    if (!process.argv.includes("--smoke-browser")) agent.start();

    function handleTrusted<T extends unknown[], R>(
      channel: string,
      handler: (event: IpcMainInvokeEvent, ...args: T) => R,
    ): void {
      ipcMain.handle(channel, (event, ...args: unknown[]) => {
        if (!isMainWindowFrame(event.sender, event.senderFrame, win?.webContents, win?.webContents.mainFrame)) {
          throw new Error("privileged IPC requires the Paladin main frame");
        }
        return handler(event, ...(args as T));
      });
    }

    handleTrusted("core:connection", () => connection);
    handleTrusted("core:setConnection", (_e, value: unknown) => switchConnection(validateConnection(value)));

    handleTrusted("agent:invoke", (_e, method: unknown, params: unknown) => {
      if (typeof method !== "string" || !method) throw new Error("agent:invoke needs a method name");
      return agent.invoke(method, params);
    });
    handleTrusted("app:metrics", () => {
      let cpu = 0;
      let rss = 0;
      const procs = app.getAppMetrics();
      for (const m of procs) {
        cpu += m.cpu?.percentCPUUsage ?? 0;
        rss += (m.memory?.workingSetSize ?? 0) * 1024;
      }
      return { cpu, rss, procs: procs.length };
    });
    handleTrusted("app:takePendingOpen", () => {
      const id = pendingOpenSession;
      pendingOpenSession = null;
      return id;
    });

    // Native OS folder picker for workspace selection. Null means the
    // user cancelled (or no window yet).
    handleTrusted("dialog:pickDirectory", async (_e, defaultPath: string | null) => {
      if (!win) return null;
      const res = await dialog.showOpenDialog(win, {
        properties: ["openDirectory"],
        ...(defaultPath ? { defaultPath } : {}),
      });
      return res.canceled ? null : (res.filePaths[0] ?? null);
    });

    handleTrusted("views:ensure", (_e, name: string, url: unknown, partition: string) => {
      if (!win || !isWebUrl(url)) return false;
      ensureView(name, url, partition);
      return true;
    });
    handleTrusted("views:show", (_e, name: string) => {
      showOnly(name);
      return true;
    });
    handleTrusted("views:setBounds", (_e, bounds: unknown) => {
      if (!isViewBounds(bounds)) return false;
      lastBounds = bounds;
      layoutVisible();
      return true;
    });
    handleTrusted("views:navigate", (_e, name: string, url: unknown) => {
      const entry = views.get(name);
      if (!entry || !isWebUrl(url)) return false;
      entry.url = url;
      entry.view.webContents.loadURL(url).catch(() => undefined);
      return true;
    });
    handleTrusted("views:reload", (_e, name: string) => {
      views.get(name)?.view.webContents.reload();
      return true;
    });
    handleTrusted("shell:openExternal", (_e, url: unknown) => {
      if (!isWebUrl(url) && !(typeof url === "string" && url.startsWith("mailto:"))) {
        throw new Error("openExternal only opens web and mailto links");
      }
      return shell.openExternal(url);
    });

    handleTrusted("window:minimize", () => win?.minimize());
    handleTrusted("window:toggleMaximize", () => {
      if (!win) return;
      if (win.isMaximized()) win.unmaximize();
      else win.maximize();
    });
    handleTrusted("window:close", () => win?.close());
    handleTrusted("window:isMaximized", () => win?.isMaximized() ?? false);

    handleTrusted("views:setTheme", (_e, theme: string) => {
      applyTheme(theme === "light" ? "light" : "dark");
      return true;
    });
    handleTrusted("views:drop", (_e, name: string) => {
      dropView(name);
      return true;
    });

    handleTrusted("browser:open", async (_e, sessionId: string, url: string) => {
      // A URL typed in the pane follows the same private-network switch
      // as the agent's requests; without an answer the last one stands.
      const tools = (await agent.invoke("toolsList", {}, 3000).catch(() => null)) as {
        behavior?: { webPrivateNetwork?: boolean };
      } | null;
      if (tools?.behavior && connection.mode === "local") {
        browsers.setPrivateNetwork(tools.behavior.webPrivateNetwork === true);
      }
      const reply = await browsers.execute({
        event: "browser/request",
        id: `ui-${++nextUIBrowserID}`,
        sessionId,
        action: "open",
        url,
      });
      if (!reply.ok) throw new Error(reply.error ?? "browser open failed");
      return reply.result;
    });
    handleTrusted("browser:show", (_e, sessionId: string | null) => browsers.show(sessionId));
    handleTrusted("browser:setBounds", (_e, bounds: ViewBounds) => browsers.setBounds(bounds));
    handleTrusted("browser:drop", (_e, sessionId: string) => {
      browsers.drop(sessionId);
      return true;
    });
    handleTrusted("browser:pick", (_e, sessionId: string) => browsers.pick(sessionId));
    handleTrusted("browser:cancelPick", (_e, sessionId: string) => browsers.cancelPick(sessionId));
    handleTrusted("browser:getNotes", (_e, sessionId: string) => browsers.getNotes(sessionId));
    handleTrusted("browser:clearNotes", (_e, sessionId: string) => browsers.clearNotes(sessionId));

    await lockAppSession();
    createWindow();
    if (process.argv.includes("--smoke-browser")) {
      void runBrowserSmoke(browsers, liveWindow, smokeEgress).then(
        () => {
          console.log(
            "[browser smoke] PASS: two sessions, Playwright actions, screenshot, element picker and web-site policy",
          );
          app.exit(0);
        },
        (error: unknown) => {
          console.error("[browser smoke] FAIL:", error);
          app.exit(1);
        },
      );
    }
    // Headless smoke: --smoke-test loads the window, pings the sidecar
    // over JSONL, reports, and exits (non-zero on any failure).
    if (process.argv.includes("--smoke-test")) {
      // Never dereference the mutable global `win` from a delayed
      // callback: closing the window during a retry used to throw
      // "Cannot read properties of null (reading 'webContents')".
      const smokeWin = win;
      if (!smokeWin) {
        console.error("[smoke] FAIL: no window to test");
        process.exit(1);
      }
      // Electron emits "closed" while process.exit tears the window down,
      // so a passing run must not be turned into a failure by its own
      // shutdown.
      let finished = false;
      const fail = (msg: string): never => {
        finished = true;
        console.error(`[smoke] FAIL: ${msg}`);
        process.exit(1);
      };
      const timer = setTimeout(() => fail("timed out waiting for load + ping"), 25000);
      const gone = (): boolean => smokeWin.isDestroyed();
      const execute = (code: string): Promise<unknown> => {
        if (gone()) return Promise.reject(new Error("window closed"));
        return smokeWin.webContents.executeJavaScript(code);
      };
      smokeWin.once("closed", () => {
        clearTimeout(timer);
        if (!finished) fail("window closed before the smoke test finished");
      });
      smokeWin.webContents.on("did-fail-load", (_e, code, desc) => fail(`renderer load failed ${code}: ${desc}`));
      smokeWin.webContents.on("console-message", (_e, _level, message) => {
        if (/preload bridge missing|Uncaught/.test(message)) fail(`renderer console: ${message}`);
      });
      smokeWin.webContents.on("did-finish-load", () => {
        // The frame can be momentarily undisposed-ready right after load
        // (worse headless); poll briefly instead of failing on one shot.
        const probeBridge = (tries: number): void => {
          if (gone()) return;
          execute("typeof window.paladin")
            .then((t) => {
              if (gone()) return;
              if (t !== "object") {
                if (tries > 0) {
                  setTimeout(() => probeBridge(tries - 1), 250);
                } else {
                  fail(`window.paladin is ${String(t)} (preload bridge missing)`);
                }
              }
            })
            .catch((e) => {
              if (gone()) return;
              if (tries > 0) {
                setTimeout(() => probeBridge(tries - 1), 250);
              } else {
                fail(`bridge probe failed: ${e.message}`);
              }
            });
        };
        probeBridge(12);
        agent
          .invoke("ping")
          .then(async (r) => {
            if ((r as { ok?: unknown } | null)?.ok !== true) {
              fail(`unexpected ping result: ${JSON.stringify(r)}`);
            }
            const bridged = await execute('window.paladin.invoke("ping")');
            if ((bridged as { ok?: unknown } | null)?.ok !== true) fail("preload bridge ping failed");
            clearTimeout(timer);
            finished = true;
            console.log("[smoke] PASS: renderer loaded, preload bridge and agent-host ping ok");
            process.exit(0);
          })
          .catch((e) => fail(`ping failed: ${e.message}`));
      });
    }
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  })
  .catch((error: unknown) => {
    // Startup failed before a usable window existed: quit instead of
    // leaving an invisible app running.
    console.error("[startup] failed:", error);
    app.quit();
  });

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

// Quit closes the sidecar's stdin: it stops every turn, persists what
// they did, and exits. A sidecar that hangs is killed after a grace.
app.on("before-quit", (event) => {
  void browsers.stop();
  if (quitting || !agent.running) return;
  quitting = true;
  event.preventDefault();
  void agent.stop(8000).then(() => app.quit());
});
