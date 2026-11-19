import { expect, test } from "@playwright/test";
import type { WebContents } from "electron";
import { DebuggerHub } from "../electron/cdpBridge";

type Message = Record<string, unknown>;
type Sent = { method: string; params?: object; sessionId?: string };

// FakeContents stands in for a WebContents: it records what reaches the
// debugger and lets a test inject debugger events.
class FakeContents {
  sent: Sent[] = [];
  attached = false;
  destroyedListener: (() => void) | null = null;
  private messageListener: ((e: unknown, method: string, params: object, sessionId: string) => void) | null = null;
  private detachListener: ((e: unknown, reason: string) => void) | null = null;
  constructor(readonly targetId: string) {}
  get debugger() {
    return {
      attach: () => {
        if (this.attached) throw new Error("Another debugger is already attached");
        this.attached = true;
      },
      detach: () => {
        this.attached = false;
      },
      isAttached: () => this.attached,
      sendCommand: (method: string, params?: object, sessionId?: string) => {
        this.sent.push({ method, params, sessionId });
        if (method === "Target.getTargetInfo") {
          return Promise.resolve({
            targetInfo: { targetId: this.targetId, type: "page", url: "about:blank", browserContextId: "ctx" },
          });
        }
        if (method === "Boom") return Promise.reject(new Error("no such thing"));
        return Promise.resolve({ ok: method });
      },
      on: (event: string, listener: never) => {
        if (event === "message") this.messageListener = listener;
        else this.detachListener = listener;
      },
      removeListener: () => undefined,
    };
  }
  once(_event: string, listener: () => void) {
    this.destroyedListener = listener;
  }
  isDestroyed() {
    return false;
  }
  emit(method: string, params: object, sessionId = "") {
    this.messageListener?.({}, method, params, sessionId);
  }
  detach(reason: string) {
    this.attached = false;
    this.detachListener?.({}, reason);
  }
  asContents(): WebContents {
    return this as unknown as WebContents;
  }
}

// connect opens a transport and returns helpers to call it like Playwright.
function connect(hub: DebuggerHub) {
  const transport = hub.transport();
  const events: Message[] = [];
  const replies = new Map<number, (m: Message) => void>();
  transport.onmessage = (m: object) => {
    const msg = m as Message;
    if (typeof msg.id === "number") replies.get(msg.id)?.(msg);
    else events.push(msg);
  };
  let id = 0;
  const call = (method: string, params?: object, sessionId?: string): Promise<Message> =>
    new Promise((resolve) => {
      const n = ++id;
      replies.set(n, resolve);
      transport.send({ id: n, method, params, sessionId });
    });
  const settle = () => new Promise((resolve) => setTimeout(resolve, 5));
  return { transport, events, call, settle };
}

test("handshake answers browser-level calls itself, without touching a page", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("T1");
  await hub.register(page.asContents());
  const { call } = connect(hub);
  const version = (await call("Browser.getVersion")).result as { product: string; userAgent: string };
  expect(version.product).toMatch(/^Chrome\//);
  expect(typeof version.userAgent).toBe("string");
  const info = (await call("Target.getTargetInfo")).result as { targetInfo: { type: string } };
  expect(info.targetInfo.type).toBe("browser");
  expect(page.sent.map((s) => s.method)).toEqual(["Target.getTargetInfo"]); // only the registration lookup
});

test("auto-attach announces each registered page once, with a session id and the default browser context", async () => {
  const hub = new DebuggerHub();
  const a = new FakeContents("TA");
  await hub.register(a.asContents());
  const { call, events, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  const attached = events.filter((e) => e.method === "Target.attachedToTarget");
  expect(attached).toHaveLength(1);
  const params = attached[0].params as {
    sessionId: string;
    waitingForDebugger: boolean;
    targetInfo: { targetId: string; browserContextId?: string; attached: boolean };
  };
  expect(params.targetInfo.targetId).toBe("TA");
  expect(params.targetInfo.browserContextId).toBe("paladin-default"); // Playwright asserts one is present
  expect(params.targetInfo.attached).toBe(true);
  expect(params.waitingForDebugger).toBe(false);
  // A page registered later is announced too.
  const b = new FakeContents("TB");
  await hub.register(b.asContents());
  await settle();
  expect(events.filter((e) => e.method === "Target.attachedToTarget")).toHaveLength(2);
});

test("page commands reach only that page's debugger, and its events come back tagged", async () => {
  const hub = new DebuggerHub();
  const a = new FakeContents("TA");
  const b = new FakeContents("TB");
  await hub.register(a.asContents());
  await hub.register(b.asContents());
  const { call, events, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  const sessions = new Map(
    events
      .filter((e) => e.method === "Target.attachedToTarget")
      .map((e) => {
        const p = e.params as { sessionId: string; targetInfo: { targetId: string } };
        return [p.targetInfo.targetId, p.sessionId] as const;
      }),
  );
  a.sent.length = 0;
  b.sent.length = 0;
  const reply = await call("Page.navigate", { url: "https://x.example/" }, sessions.get("TB"));
  expect(reply.result).toEqual({ ok: "Page.navigate" });
  expect(reply.sessionId).toBe(sessions.get("TB")); // Playwright routes replies by session
  expect((await call("Browser.getVersion")).sessionId).toBeUndefined();
  expect(a.sent).toEqual([]);
  expect(b.sent).toEqual([{ method: "Page.navigate", params: { url: "https://x.example/" }, sessionId: undefined }]);
  events.length = 0;
  a.emit("Page.loadEventFired", { timestamp: 1 });
  await settle();
  expect(events).toEqual([{ method: "Page.loadEventFired", params: { timestamp: 1 }, sessionId: sessions.get("TA") }]);
});

test("child sessions announced by a page route back to that page only", async () => {
  const hub = new DebuggerHub();
  const a = new FakeContents("TA");
  const b = new FakeContents("TB");
  await hub.register(a.asContents());
  await hub.register(b.asContents());
  const { call, events, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  a.emit("Target.attachedToTarget", { sessionId: "CHILD1", targetInfo: { type: "iframe" } });
  await settle();
  const forwarded = events.find((e) => (e.params as { sessionId?: string })?.sessionId === "CHILD1");
  expect(forwarded?.method).toBe("Target.attachedToTarget");
  a.sent.length = 0;
  b.sent.length = 0;
  await call("DOM.enable", {}, "CHILD1");
  expect(a.sent).toEqual([{ method: "DOM.enable", params: {}, sessionId: "CHILD1" }]);
  expect(b.sent).toEqual([]);
  a.emit("Target.detachedFromTarget", { sessionId: "CHILD1" });
  await settle();
  const after = await call("DOM.enable", {}, "CHILD1");
  expect(String((after.error as { message: string }).message)).toContain("Session");
});

test("unknown targets and sessions are refused, so nothing else in the app is reachable", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call } = connect(hub);
  for (const [method, params, sessionId] of [
    ["Target.attachToTarget", { targetId: "MAIN-WINDOW", flatten: true }, undefined],
    ["Target.createTarget", { url: "https://x.example/" }, undefined],
    ["Target.createBrowserContext", {}, undefined],
    ["Target.closeTarget", { targetId: "TA" }, undefined],
    ["Runtime.evaluate", { expression: "1" }, "not-a-session"],
  ] as const) {
    const reply = await call(method, params, sessionId);
    expect(reply.error, method).toBeTruthy();
  }
  expect(page.sent.map((s) => s.method)).toEqual(["Target.getTargetInfo"]);
});

test("an aux session (newCDPSession) may only ask for target info and detach", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call } = connect(hub);
  const attach = (await call("Target.attachToTarget", { targetId: "TA", flatten: true })).result as {
    sessionId: string;
  };
  const info = (await call("Target.getTargetInfo", {}, attach.sessionId)).result as {
    targetInfo: { targetId: string };
  };
  expect(info.targetInfo.targetId).toBe("TA");
  const blocked = await call("Runtime.evaluate", { expression: "document.cookie" }, attach.sessionId);
  expect(blocked.error).toBeTruthy();
  expect(page.sent.map((s) => s.method)).toEqual(["Target.getTargetInfo"]); // registration only
  await call("Target.detachFromTarget", { sessionId: attach.sessionId });
  const gone = await call("Target.getTargetInfo", {}, attach.sessionId);
  expect(gone.error).toBeTruthy();
});

test("debugger errors come back as CDP errors, and a closed transport rejects further calls", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call, events, settle, transport } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  const sessionId = (events.find((e) => e.method === "Target.attachedToTarget")!.params as { sessionId: string })
    .sessionId;
  const failed = await call("Boom", {}, sessionId);
  expect((failed.error as { message: string }).message).toContain("no such thing");
  expect(failed.sessionId).toBe(sessionId);
  let closed = false;
  transport.onclose = () => {
    closed = true;
  };
  transport.close();
  await settle();
  expect(closed).toBe(true); // Playwright waits for onclose before it treats the browser as closed
  transport.send({ id: 99, method: "Browser.getVersion" });
  await settle();
  expect(events.some((e) => e.id === 99)).toBe(false);
});

test("a page that detaches or is unregistered is announced as detached, and its debugger is released", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call, events, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  hub.unregister(page.asContents());
  await settle();
  const detached = events.find((e) => e.method === "Target.detachedFromTarget");
  expect((detached?.params as { targetId?: string }).targetId).toBe("TA"); // Playwright keys page teardown on it
  expect(page.attached).toBe(false);
  expect(hub.targetIdOf(page.asContents())).toBeUndefined();
});

test("registering a page returns its target id and is idempotent", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  expect(await hub.register(page.asContents())).toBe("TA");
  expect(await hub.register(page.asContents())).toBe("TA");
  expect(hub.targetIdOf(page.asContents())).toBe("TA");
});

test("newCDPSession's browser session can attach registered pages and nothing more", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call } = connect(hub);
  const browser = (await call("Target.attachToBrowserTarget")).result as { sessionId: string };
  expect(browser.sessionId).toBeTruthy();
  const aux = await call("Target.attachToTarget", { targetId: "TA", flatten: true }, browser.sessionId);
  expect(aux.sessionId).toBe(browser.sessionId);
  const auxId = (aux.result as { sessionId: string }).sessionId;
  const info = (await call("Target.getTargetInfo", {}, auxId)).result as { targetInfo: { targetId: string } };
  expect(info.targetInfo.targetId).toBe("TA");
  for (const method of [
    "Target.createTarget",
    "Target.createBrowserContext",
    "Runtime.evaluate",
    "Storage.getCookies",
  ]) {
    expect((await call(method, {}, browser.sessionId)).error, method).toBeTruthy();
  }
  expect((await call("Target.attachToTarget", { targetId: "MAIN" }, browser.sessionId)).error).toBeTruthy();
  expect(page.sent.map((s) => s.method)).toEqual(["Target.getTargetInfo"]); // registration only
});

test("runIfWaitingForDebugger is acknowledged without reaching the debugger (nothing ever waits)", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call, events, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  const sessionId = (events.find((e) => e.method === "Target.attachedToTarget")!.params as { sessionId: string })
    .sessionId;
  page.sent.length = 0;
  const reply = await call("Runtime.runIfWaitingForDebugger", {}, sessionId);
  expect(reply.error).toBeUndefined();
  expect(page.sent).toEqual([]);
});

test("runIfWaitingForDebugger on a child session is acknowledged too", async () => {
  const hub = new DebuggerHub();
  const page = new FakeContents("TA");
  await hub.register(page.asContents());
  const { call, settle } = connect(hub);
  await call("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  await settle();
  page.emit("Target.attachedToTarget", { sessionId: "CHILD1", targetInfo: { type: "iframe" } });
  await settle();
  page.sent.length = 0;
  expect((await call("Runtime.runIfWaitingForDebugger", {}, "CHILD1")).error).toBeUndefined();
  expect(page.sent).toEqual([]);
});
