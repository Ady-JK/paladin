// In-process stand-in for Chromium's remote-debugging port.
//
// Playwright drives the agent's browser views over CDP. Instead of a TCP
// port (which exposes every window in the app, privileged ones included, to
// any local process), it is handed a transport object (connectOverCDP(
// transport)) that talks to Electron's webContents.debugger. Nothing
// listens anywhere, and only the pages registered here exist for it: the
// main window and the personal web tabs are never registered, so they
// cannot be attached to, listed or created.
import type { WebContents } from "electron";

export type CdpTransport = {
  open?(): void;
  send(message: object): void;
  close(): void;
  onmessage?: (message: object) => void;
  onclose?: (reason?: string) => void;
};

type Json = Record<string, unknown>;
type Request = { id?: number; method?: string; params?: Json; sessionId?: string };
type Target = {
  contents: WebContents;
  targetId: string;
  sessionId: string;
  info: Json;
  listeners: { message: (...args: never[]) => void; detach: (...args: never[]) => void };
};

const DEFAULT_CONTEXT = "paladin-default";

const BROWSER_TARGET: Json = {
  targetId: "paladin-browser",
  type: "browser",
  title: "",
  url: "",
  attached: true,
  canAccessOpener: false,
};

class CdpError extends Error {
  constructor(
    message: string,
    readonly code = -32000,
  ) {
    super(message);
  }
}

export class DebuggerHub {
  private readonly targets = new Map<string, Target>();
  private readonly byContents = new Map<WebContents, Target>();
  // Child sessions (iframes, workers) a page announced, by owner.
  private readonly children = new Map<string, Target>();
  private readonly transports = new Set<HubTransport>();
  private nextSession = 0;

  // register attaches the debugger to a page and makes it visible to
  // Playwright; it resolves to the page's CDP target id.
  async register(contents: WebContents): Promise<string> {
    const existing = this.byContents.get(contents);
    if (existing) return existing.targetId;
    const session = contents.debugger;
    session.attach("1.3");
    let info: Json;
    try {
      const reply = (await session.sendCommand("Target.getTargetInfo")) as { targetInfo: Json };
      info = { ...reply.targetInfo };
    } catch (error) {
      session.detach();
      throw error;
    }
    // Playwright asserts a context id on every page and treats one it did not
    // create as the default context, so every page reports the same one.
    info.browserContextId = DEFAULT_CONTEXT;
    const targetId = String(info.targetId);
    const target: Target = {
      contents,
      targetId,
      sessionId: `paladin-page-${++this.nextSession}`,
      info,
      listeners: {
        message: (_event: unknown, method: string, params: Json, sessionId: string) =>
          this.fromPage(target, method, params, sessionId),
        detach: () => this.unregister(contents),
      },
    };
    this.targets.set(targetId, target);
    this.byContents.set(contents, target);
    session.on("message", target.listeners.message as never);
    session.on("detach", target.listeners.detach as never);
    contents.once("destroyed", () => this.unregister(contents));
    for (const transport of this.transports) transport.announce(target);
    return targetId;
  }

  targetIdOf(contents: WebContents): string | undefined {
    return this.byContents.get(contents)?.targetId;
  }

  unregister(contents: WebContents): void {
    const target = this.byContents.get(contents);
    if (!target) return;
    this.byContents.delete(contents);
    this.targets.delete(target.targetId);
    for (const [id, owner] of this.children) if (owner === target) this.children.delete(id);
    for (const transport of this.transports) transport.retire(target);
    try {
      contents.debugger.removeListener("message", target.listeners.message as never);
      contents.debugger.removeListener("detach", target.listeners.detach as never);
      if (contents.debugger.isAttached()) contents.debugger.detach();
    } catch {
      // The page is already gone.
    }
  }

  // transport opens one Playwright connection. Closing it leaves the pages
  // registered, so a later connection finds them again.
  transport(): CdpTransport {
    const transport = new HubTransport(this);
    this.transports.add(transport);
    return transport;
  }

  /** @internal */
  forget(transport: HubTransport): void {
    this.transports.delete(transport);
  }

  /** @internal */
  listTargets(): Target[] {
    return [...this.targets.values()];
  }

  /** @internal */
  target(targetId: unknown): Target | undefined {
    return typeof targetId === "string" ? this.targets.get(targetId) : undefined;
  }

  /** @internal */
  childOwner(sessionId: string): Target | undefined {
    return this.children.get(sessionId);
  }

  private fromPage(target: Target, method: string, params: Json, sessionId: string): void {
    if (method === "Target.attachedToTarget" && typeof params?.sessionId === "string") {
      this.children.set(params.sessionId, target);
    }
    for (const transport of this.transports) transport.event(target, method, params, sessionId);
    if (method === "Target.detachedFromTarget" && typeof params?.sessionId === "string") {
      this.children.delete(params.sessionId);
    }
  }
}

export class HubTransport implements CdpTransport {
  onmessage?: (message: object) => void;
  onclose?: (reason?: string) => void;
  private closed = false;
  private autoAttach = false;
  // Pages announced to this client, and the target-info-only sessions it
  // opened with Target.attachToTarget (what page.context().newCDPSession
  // uses to learn a page's target id).
  private readonly pages = new Map<string, Target>();
  private readonly aux = new Map<string, Target>();
  // Sessions from Target.attachToBrowserTarget: they may attach registered
  // pages to an aux session and nothing else.
  private readonly browserSessions = new Set<string>();
  private auxCount = 0;

  constructor(private readonly hub: DebuggerHub) {}

  open(): void {
    // Nothing to open: the hub is in-process.
  }

  send(message: object): void {
    if (this.closed) return;
    const request = message as Request;
    // A reply belongs to the session that asked; Playwright routes by it.
    const session = request.sessionId === undefined ? {} : { sessionId: request.sessionId };
    void this.dispatch(request).then(
      (result) => this.emit({ id: request.id, result, ...session }),
      (error: unknown) =>
        this.emit({
          id: request.id,
          ...session,
          error: {
            code: error instanceof CdpError ? error.code : -32000,
            message: error instanceof Error ? error.message : String(error),
          },
        }),
    );
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.hub.forget(this);
    this.pages.clear();
    this.aux.clear();
    this.browserSessions.clear();
    // Playwright treats the browser as closed only when onclose fires.
    queueMicrotask(() => this.onclose?.());
  }

  announce(target: Target): void {
    if (this.closed || !this.autoAttach || this.pages.has(target.sessionId)) return;
    this.pages.set(target.sessionId, target);
    this.emit({
      method: "Target.attachedToTarget",
      params: {
        sessionId: target.sessionId,
        targetInfo: { ...target.info, attached: true },
        waitingForDebugger: false,
      },
    });
  }

  retire(target: Target): void {
    for (const [id, owner] of this.aux) if (owner === target) this.aux.delete(id);
    if (!this.pages.delete(target.sessionId)) return;
    this.emit({
      method: "Target.detachedFromTarget",
      params: { sessionId: target.sessionId, targetId: target.targetId },
    });
  }

  event(target: Target, method: string, params: Json, sessionId: string): void {
    if (!this.pages.has(target.sessionId)) return;
    this.emit({ method, params, sessionId: sessionId || target.sessionId });
  }

  private emit(message: object): void {
    if (this.closed) return;
    queueMicrotask(() => {
      if (!this.closed) this.onmessage?.(message);
    });
  }

  private async dispatch(request: Request): Promise<unknown> {
    const { method = "", params = {}, sessionId } = request;
    if (sessionId === undefined) return this.browserCommand(method, params);
    if (this.browserSessions.has(sessionId)) return this.browserSessionCommand(method, params);
    const aux = this.aux.get(sessionId);
    if (aux) return this.auxCommand(aux, method, params);
    const page = this.pages.get(sessionId);
    if (page) return this.pageCommand(page, method, params);
    const owner = this.hub.childOwner(sessionId);
    if (owner && this.pages.has(owner.sessionId)) return this.pageCommand(owner, method, params, sessionId);
    throw new CdpError("Session with given id not found.", -32001);
  }

  private async pageCommand(page: Target, method: string, params: Json, childSession?: string): Promise<unknown> {
    // Pages are announced as not waiting for a debugger, so there is nothing
    // to resume; Electron's debugger does not implement the call.
    if (method === "Runtime.runIfWaitingForDebugger") return {};
    return page.contents.debugger.sendCommand(method, params, childSession);
  }

  private async browserCommand(method: string, params: Json): Promise<unknown> {
    switch (method) {
      case "Browser.getVersion":
        return {
          protocolVersion: "1.3",
          product: `Chrome/${process.versions.chrome}`,
          revision: "",
          userAgent: `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${process.versions.chrome} Safari/537.36`,
          jsVersion: process.versions.v8,
        };
      case "Target.getTargetInfo":
        return { targetInfo: BROWSER_TARGET };
      case "Target.setAutoAttach":
        this.autoAttach = params.autoAttach === true;
        if (this.autoAttach) {
          queueMicrotask(() => {
            for (const target of this.hub.listTargets()) this.announce(target);
          });
        }
        return {};
      case "Target.setDiscoverTargets":
      case "Browser.setDownloadBehavior":
        return {};
      case "Target.getTargets":
        return {
          targetInfos: this.hub.listTargets().map((t) => ({ ...t.info, attached: this.pages.has(t.sessionId) })),
        };
      case "Target.attachToTarget": {
        const target = this.hub.target(params.targetId);
        if (!target) throw new CdpError("No target with given id found");
        const sessionId = `paladin-aux-${++this.auxCount}`;
        this.aux.set(sessionId, target);
        return { sessionId };
      }
      case "Target.attachToBrowserTarget": {
        const sessionId = `paladin-browser-session-${++this.auxCount}`;
        this.browserSessions.add(sessionId);
        return { sessionId };
      }
      case "Target.detachFromTarget":
        if (typeof params.sessionId === "string") {
          this.aux.delete(params.sessionId);
          this.browserSessions.delete(params.sessionId);
        }
        return {};
      default:
        // Creating targets or contexts, closing the browser, storage and
        // the like would act beyond the registered pages.
        throw new CdpError(`'${method}' wasn't found`, -32601);
    }
  }

  private async browserSessionCommand(method: string, params: Json): Promise<unknown> {
    if (method === "Target.attachToTarget" || method === "Target.detachFromTarget") {
      return this.browserCommand(method, params);
    }
    throw new CdpError(`'${method}' wasn't found`, -32601);
  }

  private async auxCommand(target: Target, method: string, params: Json): Promise<unknown> {
    if (method === "Target.getTargetInfo") return { targetInfo: { ...target.info, attached: true } };
    void params;
    throw new CdpError(`'${method}' wasn't found`, -32601);
  }
}
