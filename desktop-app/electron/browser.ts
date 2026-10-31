// Session-scoped local browser views controlled by the Playwright CDP client.
// Hidden views stay alive and automatable while other sessions are visible.
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { app, BrowserWindow, session, WebContentsView } from "electron";
import type { Browser, Locator, Page } from "@playwright/test";
import { localBrowserRequest, localBrowserUrl } from "./browserPolicy";
import { renderBrowserAnnotationOverlay, type BrowserAnnotation } from "./browserAnnotationOverlay";

export type BrowserRequest = {
  event: "browser/request";
  id: string;
  sessionId: string;
  action: string;
  url?: string;
  selector?: string;
  role?: string;
  name?: string;
  text?: string;
  key?: string;
  value?: string;
  fullPage?: boolean;
};

export type BrowserReply = { id: string; ok: boolean; result?: unknown; imageBase64?: string; error?: string };
export type BrowserState = {
  event: "browser/opened" | "browser/closed" | "browser/navigated" | "browser/annotations";
  sessionId: string;
  url?: string;
  count?: number;
};
export type BrowserBounds = { x: number; y: number; width: number; height: number };
export type BrowserElement = { url: string; selector: string; tag: string; label: string; text: string };

type Entry = {
  view: WebContentsView;
  parking: BrowserWindow;
  page: Page | null;
  targetId: string;
  url: string;
  attached: boolean;
  binding: string;
};
type PlaywrightCore = { chromium: { connectOverCDP(endpoint: string): Promise<Browser> } };

const sessionName = /^[A-Za-z0-9_-]{1,128}$/;
const maxImageBytes = 4 << 20;

export class BrowserManager {
  private readonly entries = new Map<string, Entry>();
  private readonly lanes = new Map<string, Promise<void>>();
  private visible: string | null = null;
  private bounds: BrowserBounds | null = null;
  private browser: Browser | null = null;
  private connecting: Promise<Browser> | null = null;
  private readonly picks = new Map<
    string,
    { page: Page; cancelEvent: string; done?: Promise<BrowserElement | null> }
  >();
  private readonly annotations = new Map<string, BrowserAnnotation[]>();

  constructor(
    private readonly window: () => BrowserWindow | null,
    private readonly state: (event: BrowserState) => void,
  ) {}

  // Same-session actions stay ordered; different sessions run concurrently.
  execute(request: BrowserRequest): Promise<BrowserReply> {
    const previous = this.lanes.get(request.sessionId) ?? Promise.resolve();
    const work = previous.catch(() => undefined).then(() => this.perform(request));
    const tail = work.then(
      () => undefined,
      () => undefined,
    );
    this.lanes.set(request.sessionId, tail);
    void tail.then(() => {
      if (this.lanes.get(request.sessionId) === tail) this.lanes.delete(request.sessionId);
    });
    return work.then(
      (value) => ({ id: request.id, ok: true, ...value }),
      (error: unknown) => ({
        id: request.id,
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      }),
    );
  }

  show(sessionId: string | null): boolean {
    const win = this.window();
    if (!win) return false;
    if (sessionId && this.visible === sessionId && this.entries.get(sessionId)?.attached) {
      this.layout();
      return true;
    }
    if (this.visible) {
      const old = this.entries.get(this.visible);
      if (old?.attached) {
        try {
          win.contentView.removeChildView(old.view);
        } catch {
          /* window closing */
        }
        old.parking.contentView.addChildView(old.view);
        old.view.setBounds({ x: 0, y: 0, width: 1200, height: 800 });
        old.attached = false;
      }
    }
    this.visible = null;
    if (!sessionId) return true;
    const entry = this.entries.get(sessionId);
    if (!entry) return false;
    entry.parking.contentView.removeChildView(entry.view);
    win.contentView.addChildView(entry.view);
    entry.attached = true;
    this.visible = sessionId;
    this.layout();
    return true;
  }

  setBounds(bounds: BrowserBounds): boolean {
    const valid =
      [bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite) &&
      bounds.width >= 0 &&
      bounds.height >= 0;
    if (!valid) return false;
    this.bounds = bounds;
    this.layout();
    return true;
  }

  layout(): void {
    const entry = this.visible ? this.entries.get(this.visible) : undefined;
    if (!entry || !this.bounds) return;
    const b = this.bounds;
    entry.view.setBounds({
      x: Math.round(b.x),
      y: Math.round(b.y),
      width: Math.max(1, Math.round(b.width)),
      height: Math.max(1, Math.round(b.height)),
    });
  }

  drop(sessionId: string): void {
    const entry = this.entries.get(sessionId);
    if (!entry) return;
    void this.cancelPick(sessionId);
    if (this.visible === sessionId) this.show(null);
    try {
      (entry.view.webContents as { destroy?: () => void }).destroy?.();
    } catch {
      /* already gone */
    }
    try {
      entry.parking.destroy();
    } catch {
      /* already gone */
    }
    this.entries.delete(sessionId);
    this.annotations.delete(sessionId);
    this.state({ event: "browser/closed", sessionId });
  }

  windowClosed(): void {
    this.visible = null;
    this.bounds = null;
    for (const id of [...this.entries.keys()]) this.drop(id);
    this.annotations.clear();
  }

  async stop(): Promise<void> {
    for (const id of [...this.entries.keys()]) this.drop(id);
    await this.connecting?.catch(() => undefined);
    await this.browser?.close().catch(() => undefined);
    this.browser = null;
  }

  getNotes(sessionId: string): BrowserAnnotation[] {
    return (this.annotations.get(sessionId) ?? []).filter((note) => note.comment.trim()).map((note) => ({ ...note }));
  }

  async clearNotes(sessionId: string): Promise<void> {
    if (!this.entries.has(sessionId)) return;
    this.annotations.delete(sessionId);
    await this.renderAnnotations(sessionId);
    this.publishAnnotationCount(sessionId);
  }

  private publishAnnotationCount(sessionId: string): void {
    this.state({ event: "browser/annotations", sessionId, count: this.getNotes(sessionId).length });
  }

  private async renderAnnotations(sessionId: string): Promise<void> {
    const entry = this.entries.get(sessionId);
    if (!entry?.page || !entry.binding) return;
    await entry.page.evaluate(renderBrowserAnnotationOverlay, {
      notes: this.annotations.get(sessionId) ?? [],
      binding: entry.binding,
    });
  }

  private async annotationMessage(sessionId: string, message: unknown): Promise<void> {
    if (!message || typeof message !== "object") return;
    const data = message as { action?: unknown; id?: unknown; comment?: unknown };
    if (typeof data.id !== "string") return;
    const notes = this.annotations.get(sessionId);
    const note = notes?.find((item) => item.id === data.id);
    if (!note) return;
    if (data.action === "save" && typeof data.comment === "string" && data.comment.trim().length <= 10000) {
      note.comment = data.comment.trim();
    } else if (data.action === "remove") {
      notes!.splice(notes!.indexOf(note), 1);
      await this.renderAnnotations(sessionId);
    } else {
      return;
    }
    this.publishAnnotationCount(sessionId);
  }

  private async addAnnotation(sessionId: string, element: BrowserElement): Promise<void> {
    const notes = this.annotations.get(sessionId) ?? [];
    if (!notes.some((note) => note.url === element.url && note.selector === element.selector)) {
      const color = notes.reduce((highest, note) => Math.max(highest, note.color), -1) + 1;
      notes.push({ ...element, id: randomUUID(), comment: "", color });
      this.annotations.set(sessionId, notes);
    }
    await this.renderAnnotations(sessionId);
  }

  // Pick through the page's own surface: the renderer cannot receive
  // pointer events from an Electron WebContentsView layered above it.
  async pick(sessionId: string): Promise<BrowserElement | null> {
    const entry = this.entries.get(sessionId);
    if (!entry?.page) throw new Error("Open a local page before selecting an element");
    if (this.picks.has(sessionId)) throw new Error("Element selection is already active");
    const page = entry.page;
    const cancelEvent = `paladin-cancel-pick-${randomUUID()}`;
    const active: { page: Page; cancelEvent: string; done?: Promise<BrowserElement | null> } = { page, cancelEvent };
    this.picks.set(sessionId, active);
    try {
      const done = page.evaluate((eventName): Promise<BrowserElement | null> => {
        return new Promise((resolve) => {
          const cover = document.createElement("div");
          cover.dataset.paladinPicker = "";
          Object.assign(cover.style, {
            position: "fixed",
            inset: "0",
            zIndex: "2147483646",
            cursor: "crosshair",
            background: "transparent",
          });
          const outline = document.createElement("div");
          Object.assign(outline.style, {
            position: "fixed",
            zIndex: "2147483647",
            pointerEvents: "none",
            border: "2px solid #8b7bd2",
            borderRadius: "4px",
            background: "rgba(139, 123, 210, 0.12)",
            display: "none",
          });
          document.documentElement.append(cover, outline);
          let hovered: Element | null = null;
          const at = (x: number, y: number): Element | null => {
            cover.style.pointerEvents = "none";
            const found = document.elementFromPoint(x, y);
            cover.style.pointerEvents = "auto";
            return found === outline ? null : found;
          };
          const move = (event: PointerEvent): void => {
            hovered = at(event.clientX, event.clientY);
            if (!hovered) {
              outline.style.display = "none";
              return;
            }
            const rect = hovered.getBoundingClientRect();
            Object.assign(outline.style, {
              display: "block",
              left: `${rect.left}px`,
              top: `${rect.top}px`,
              width: `${rect.width}px`,
              height: `${rect.height}px`,
            });
          };
          const selector = (element: Element): string => {
            const parts: string[] = [];
            for (let node: Element | null = element; node; node = node.parentElement) {
              if (node.id && document.querySelectorAll(`#${CSS.escape(node.id)}`).length === 1) {
                parts.unshift(`#${CSS.escape(node.id)}`);
                break;
              }
              const tag = node.tagName.toLowerCase();
              const parent: Element | null = node.parentElement;
              const peers = parent
                ? Array.from(parent.children).filter((child) => child.tagName.toLowerCase() === tag)
                : [];
              const position = peers.length > 1 ? `:nth-of-type(${peers.indexOf(node) + 1})` : "";
              parts.unshift(tag + position);
            }
            return parts.join(" > ");
          };
          const finish = (value: BrowserElement | null): void => {
            cover.removeEventListener("pointermove", move);
            cover.removeEventListener("click", click);
            window.removeEventListener(eventName, cancel);
            window.removeEventListener("keydown", keydown, true);
            cover.remove();
            outline.remove();
            resolve(value);
          };
          const cancel = (): void => finish(null);
          const keydown = (event: KeyboardEvent): void => {
            if (
              event
                .composedPath()
                .some((node) => node instanceof HTMLElement && node.dataset.paladinAnnotationRoot !== undefined)
            )
              return;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopImmediatePropagation();
              finish(null);
            }
          };
          const click = (event: MouseEvent): void => {
            event.preventDefault();
            event.stopImmediatePropagation();
            const element = at(event.clientX, event.clientY) ?? hovered;
            if (!element) return;
            const rawText = element instanceof HTMLElement ? element.innerText : element.textContent;
            const text = (rawText ?? "").replace(/\s+/g, " ").trim().slice(0, 160);
            const fieldLabel =
              element instanceof HTMLInputElement ||
              element instanceof HTMLTextAreaElement ||
              element instanceof HTMLSelectElement
                ? Array.from(element.labels ?? [])
                    .map((label) => label.innerText)
                    .join(" ")
                : "";
            finish({
              url: location.href,
              selector: selector(element),
              tag: element.tagName.toLowerCase(),
              label: (
                element.getAttribute("aria-label") ||
                fieldLabel ||
                element.getAttribute("alt") ||
                element.getAttribute("title") ||
                element.getAttribute("placeholder") ||
                ""
              ).slice(0, 160),
              text,
            });
          };
          cover.addEventListener("pointermove", move);
          cover.addEventListener("click", click);
          window.addEventListener(eventName, cancel, { once: true });
          window.addEventListener("keydown", keydown, true);
        });
      }, cancelEvent);
      active.done = done;
      const element = await done;
      if (element) await this.addAnnotation(sessionId, element);
      return element;
    } finally {
      this.picks.delete(sessionId);
    }
  }

  async cancelPick(sessionId: string): Promise<void> {
    const active = this.picks.get(sessionId);
    if (!active) return;
    // A rapid toggle can arrive before the page has installed its
    // overlay and cancel listener. Wait for that setup before signalling.
    await active.page
      .locator("[data-paladin-picker]")
      .waitFor({ state: "attached", timeout: 5000 })
      .catch(() => undefined);
    await active.page
      .evaluate((eventName) => window.dispatchEvent(new Event(eventName)), active.cancelEvent)
      .catch(() => undefined);
    await active.done?.catch(() => undefined);
  }

  private async perform(req: BrowserRequest): Promise<{ result?: unknown; imageBase64?: string }> {
    if (!sessionName.test(req.sessionId)) throw new Error("invalid browser session id");
    if (req.action === "close") {
      this.drop(req.sessionId);
      return { result: { closed: true } };
    }
    if (req.action === "open") {
      if (!req.url || !localBrowserUrl(req.url))
        throw new Error("browser only opens http://localhost or http://127.0.0.1");
      const entry = this.ensure(req.sessionId);
      this.state({ event: "browser/opened", sessionId: req.sessionId, url: req.url });
      await entry.view.webContents.loadURL(req.url);
      entry.url = entry.view.webContents.getURL();
      entry.targetId = await this.targetId(entry.view);
      entry.page = await this.findPage(entry.targetId);
      if (!entry.binding) {
        entry.binding = `__paladinAnnotation_${randomUUID().replace(/-/g, "")}`;
        await entry.page.exposeBinding(entry.binding, (_source, message: unknown) =>
          this.annotationMessage(req.sessionId, message),
        );
      }
      await this.renderAnnotations(req.sessionId);
      this.publishAnnotationCount(req.sessionId);
      this.state({ event: "browser/navigated", sessionId: req.sessionId, url: entry.url });
      return { result: { url: entry.url, title: await entry.page.title() } };
    }
    const entry = this.entries.get(req.sessionId);
    if (!entry?.page) throw new Error("browser has no page; use open first");
    const page = entry.page;
    switch (req.action) {
      case "snapshot": {
        const snapshot = await page.locator("body").ariaSnapshot({ timeout: 15_000 });
        return {
          result: {
            url: page.url(),
            title: await page.title(),
            snapshot: snapshot.slice(0, 30_000),
            truncated: snapshot.length > 30_000,
          },
        };
      }
      case "screenshot": {
        let bytes = await page.screenshot({ type: "jpeg", quality: 75, fullPage: !!req.fullPage, timeout: 20_000 });
        if (bytes.length > maxImageBytes)
          bytes = await page.screenshot({ type: "jpeg", quality: 45, fullPage: !!req.fullPage, timeout: 20_000 });
        if (bytes.length > maxImageBytes) throw new Error("screenshot exceeds 4 MB; use a viewport screenshot");
        return {
          result: { url: page.url(), width: page.viewportSize()?.width, height: page.viewportSize()?.height },
          imageBase64: bytes.toString("base64"),
        };
      }
      case "reload":
        await page.reload({ waitUntil: "domcontentloaded", timeout: 20_000 });
        break;
      case "back":
        await page.goBack({ waitUntil: "domcontentloaded", timeout: 20_000 });
        break;
      case "forward":
        await page.goForward({ waitUntil: "domcontentloaded", timeout: 20_000 });
        break;
      case "click":
        await this.locator(page, req).click({ timeout: 15_000 });
        break;
      case "fill":
        await this.locator(page, req).fill(req.text ?? "", { timeout: 15_000 });
        break;
      case "press":
        await this.locator(page, req).press(req.key ?? "Enter", { timeout: 15_000 });
        break;
      case "hover":
        await this.locator(page, req).hover({ timeout: 15_000 });
        break;
      case "select":
        await this.locator(page, req).selectOption(req.value ?? "", { timeout: 15_000 });
        break;
      case "wait":
        await this.locator(page, req).waitFor({ state: "visible", timeout: 20_000 });
        break;
      default:
        throw new Error(`unknown browser action ${req.action}`);
    }
    entry.url = page.url();
    if (req.action === "reload" || req.action === "back" || req.action === "forward") {
      await this.renderAnnotations(req.sessionId);
    }
    this.state({ event: "browser/navigated", sessionId: req.sessionId, url: entry.url });
    return { result: { url: entry.url, title: await page.title(), action: req.action } };
  }

  private locator(page: Page, req: BrowserRequest): Locator {
    if (req.role)
      return page.getByRole(req.role as Parameters<Page["getByRole"]>[0], req.name ? { name: req.name } : {});
    if (req.selector) return page.locator(req.selector);
    throw new Error("selector or role is required");
  }

  private ensure(sessionId: string): Entry {
    const existing = this.entries.get(sessionId);
    if (existing) return existing;
    if (!this.window()) throw new Error("Paladin window is closed");
    const partition = `paladin-browser-${sessionId}`;
    const jar = session.fromPartition(partition);
    jar.webRequest.onBeforeRequest((details, callback) => callback({ cancel: !localBrowserRequest(details.url) }));
    jar.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    jar.setPermissionCheckHandler(() => false);
    const view = new WebContentsView({
      webPreferences: { partition, sandbox: true, contextIsolation: true, nodeIntegration: false },
    });
    const parking = new BrowserWindow({
      show: false,
      width: 1200,
      height: 800,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false },
    });
    view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    view.webContents.on("will-navigate", (details) => {
      if (!localBrowserUrl(details.url)) details.preventDefault();
    });
    view.webContents.on("will-redirect", (details) => {
      if (!localBrowserUrl(details.url)) details.preventDefault();
    });
    view.webContents.on("did-navigate", (_event, url) => this.state({ event: "browser/navigated", sessionId, url }));
    const restoreAnnotations = () => {
      // A navigation event can arrive before the new document accepts
      // Playwright evaluation. Retry once after its first layout frame.
      void this.renderAnnotations(sessionId).catch(() => undefined);
      setTimeout(() => {
        void this.renderAnnotations(sessionId).catch(() => undefined);
      }, 100);
    };
    view.webContents.on("did-finish-load", restoreAnnotations);
    view.webContents.on("did-navigate-in-page", restoreAnnotations);
    // A detached view cannot receive real CDP pointer clicks. Give each
    // background session a hidden window with a full-size surface instead.
    parking.contentView.addChildView(view);
    view.setBounds({ x: 0, y: 0, width: 1200, height: 800 });
    view.webContents.setBackgroundThrottling(false);
    const entry: Entry = { view, parking, page: null, targetId: "", url: "", attached: false, binding: "" };
    this.entries.set(sessionId, entry);
    return entry;
  }

  private async targetId(view: WebContentsView): Promise<string> {
    const debuggerSession = view.webContents.debugger;
    debuggerSession.attach("1.3");
    try {
      const data = (await debuggerSession.sendCommand("Target.getTargetInfo")) as { targetInfo: { targetId: string } };
      return data.targetInfo.targetId;
    } finally {
      debuggerSession.detach();
    }
  }

  private connect(): Promise<Browser> {
    if (this.browser?.isConnected()) return Promise.resolve(this.browser);
    if (!this.connecting) {
      this.connecting = this.connectFresh()
        .then((browser) => {
          this.browser = browser;
          return browser;
        })
        .finally(() => {
          this.connecting = null;
        });
    }
    return this.connecting;
  }

  private async connectFresh(): Promise<Browser> {
    const activePort = path.join(app.getPath("userData"), "DevToolsActivePort");
    let port = 0;
    for (let attempt = 0; attempt < 20; attempt++) {
      try {
        port = Number((await readFile(activePort, "utf8")).split("\n")[0]);
      } catch {
        /* starting */
      }
      if (Number.isInteger(port) && port > 0 && port <= 65535) break;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    if (!port) throw new Error("Chromium debugging endpoint is unavailable");
    const client = createRequire(__filename)(path.join(__dirname, "vendor", "playwright-core")) as PlaywrightCore;
    return client.chromium.connectOverCDP(`http://127.0.0.1:${port}`);
  }

  private async findPage(targetId: string): Promise<Page> {
    const browser = await this.connect();
    for (let attempt = 0; attempt < 30; attempt++) {
      for (const context of browser.contexts()) {
        for (const page of context.pages()) {
          const cdp = await context.newCDPSession(page);
          try {
            const info = (await cdp.send("Target.getTargetInfo")) as { targetInfo: { targetId: string } };
            if (info.targetInfo.targetId === targetId) return page;
          } finally {
            await cdp.detach();
          }
        }
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error("Playwright did not find the local browser page");
  }
}
