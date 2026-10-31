import { readFileSync } from "node:fs";
import path from "node:path";
import { test, expect } from "@playwright/test";
import { agentHostFile, deadlineFor, isReadMethod, restartDelay, Sidecar, type HostMessage } from "../electron/sidecar";

// A minimal JSONL host: answers ping/echo, emits an event on "emit",
// dies on "crash", and exits cleanly when stdin closes.
const FAKE_HOST = `
const rl = require("node:readline").createInterface({ input: process.stdin });
const out = (o) => process.stdout.write(JSON.stringify(o) + "\\n");
const deaf = process.env.FAKE_DEAF_MS ? Date.now() + Number(process.env.FAKE_DEAF_MS) : 0;
rl.on("line", (line) => {
  const req = JSON.parse(line);
  if (req.method === "crash") process.exit(3);
  if (req.method === "hang") return;
  if (req.method === "ping" && Date.now() < deaf) return;
  if (req.method === "garbled") return process.stdout.write('{"id":"' + req.id + '","ok":tru\\n');
  if (req.method === "shapeless") return out({ id: req.id });
  if (req.method === "junk") { process.stdout.write("null\\n42\\n[]\\n"); return out({ id: req.id, ok: true, result: 1 }); }
  if (req.method === "fail") return out({ id: req.id, ok: false, error: "nope" });
  if (req.method === "emit") out({ event: "turn/done", sessionId: "s1" });
  out({ id: req.id, ok: true, result: { method: req.method, params: req.params, pid: process.pid } });
});
rl.on("close", () => process.exit(0));
`;

const fast = { initialMs: 20, maxMs: 200, stableMs: 10_000 };

function fakeHost(over: Partial<ConstructorParameters<typeof Sidecar>[0]> = {}) {
  const events: HostMessage[] = [];
  let restarts = 0;
  const sidecar = new Sidecar({
    command: process.execPath,
    args: ["-e", FAKE_HOST],
    onEvent: (msg) => events.push(msg),
    onRestart: () => restarts++,
    log: () => undefined,
    backoff: fast,
    ...over,
  });
  return { sidecar, events, restarts: () => restarts };
}

async function waitFor(check: () => boolean, ms = 3000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error("timed out");
    await new Promise((r) => setTimeout(r, 10));
  }
}

test("invoke resolves results, rejects host errors, and forwards events", async () => {
  const { sidecar, events } = fakeHost();
  sidecar.start();
  try {
    await expect(sidecar.invoke("echo", { a: 1 })).resolves.toMatchObject({ method: "echo", params: { a: 1 } });
    await expect(sidecar.invoke("fail")).rejects.toThrow("nope");
    await sidecar.invoke("emit");
    expect(events).toEqual([{ event: "turn/done", sessionId: "s1" }]);
  } finally {
    await sidecar.stop(1000);
  }
});

test("a crashed host fails in-flight calls, restarts, and serves again", async () => {
  const { sidecar, restarts } = fakeHost();
  sidecar.start();
  try {
    const first = (await sidecar.invoke("ping")) as { pid: number };
    const inFlight = sidecar.invoke("hang");
    await expect(sidecar.invoke("crash")).rejects.toThrow("agent-host exited");
    await expect(inFlight).rejects.toThrow("agent-host exited");
    await waitFor(() => restarts() === 1);
    const second = (await sidecar.invoke("ping")) as { pid: number };
    expect(second.pid).not.toBe(first.pid);
  } finally {
    await sidecar.stop(1000);
  }
});

test("calls while the host is down fail fast instead of hanging", async () => {
  const { sidecar } = fakeHost({ backoff: { initialMs: 5000, maxMs: 5000, stableMs: 10_000 } });
  sidecar.start();
  try {
    await expect(sidecar.invoke("crash")).rejects.toThrow();
    await expect(sidecar.invoke("ping")).rejects.toThrow("agent-host is not running");
  } finally {
    await sidecar.stop(1000);
  }
});

test("stop closes stdin, waits for exit, and never restarts", async () => {
  const { sidecar, restarts } = fakeHost();
  sidecar.start();
  await sidecar.invoke("ping");
  await sidecar.stop(1000);
  expect(sidecar.running).toBe(false);
  await new Promise((r) => setTimeout(r, 100));
  expect(restarts()).toBe(0);
  await expect(sidecar.invoke("ping")).rejects.toThrow("agent-host is not running");
});

test("a missing binary is retried with backoff, not thrown", async () => {
  const logs: string[] = [];
  const sidecar = new Sidecar({
    command: "/nonexistent/agent-host",
    onEvent: () => undefined,
    onRestart: () => undefined,
    log: (line) => logs.push(line),
    backoff: fast,
  });
  sidecar.start();
  await waitFor(() => logs.filter((l) => l.includes("spawn failed")).length >= 2);
  await sidecar.stop(100);
});

test("restart delay doubles up to the cap", () => {
  expect([0, 1, 2, 3, 4, 10].map((n) => restartDelay(n, { initialMs: 500, maxMs: 4000, stableMs: 0 }))).toEqual([
    500, 1000, 2000, 4000, 4000, 4000,
  ]);
});

test("agent-host binary name carries .exe only on Windows", () => {
  expect(agentHostFile("win32")).toBe("agent-host.exe");
  expect(agentHostFile("darwin")).toBe("agent-host");
  expect(agentHostFile("linux")).toBe("agent-host");
});

test("a call past its deadline rejects; a mutation says it may have completed", async () => {
  const { sidecar } = fakeHost({ deadlines: { readMs: 80, writeMs: 120, longMs: 1000 } });
  sidecar.start();
  try {
    await sidecar.invoke("ping", {}, 5000); // wait out start-up
    const started = Date.now();
    await expect(sidecar.invoke("listSessions", {})).resolves.toBeTruthy();
    // "hang" is unknown, so it is treated as a mutation.
    await expect(sidecar.invoke("hang")).rejects.toThrow(
      /did not answer hang within 0\.12s; it may still have completed/,
    );
    expect(Date.now() - started).toBeLessThan(1000);
    // The host is still usable after a timeout.
    await expect(sidecar.invoke("ping")).resolves.toBeTruthy();
  } finally {
    await sidecar.stop(1000);
  }
});

test("deadlines are picked per method", () => {
  const d = { readMs: 1, writeMs: 2, longMs: 3 };
  expect(deadlineFor("listSessions", d)).toBe(1);
  expect(deadlineFor("attach", d)).toBe(1);
  expect(deadlineFor("deleteSession", d)).toBe(2);
  expect(deadlineFor("somethingNew", d)).toBe(2);
  expect(deadlineFor("sideQuestion", d)).toBe(3);
  expect(isReadMethod("listSessions")).toBe(true);
  expect(isReadMethod("submitChat")).toBe(false);
});

test("too many pending calls are refused instead of queued forever", async () => {
  const { sidecar } = fakeHost({ maxPending: 3 });
  sidecar.start();
  try {
    const hung = [sidecar.invoke("hang"), sidecar.invoke("hang"), sidecar.invoke("hang")];
    await expect(sidecar.invoke("ping")).rejects.toThrow("agent-host is busy");
    await expect(sidecar.invoke("crash")).rejects.toThrow("agent-host is busy");
    hung.forEach((p) => p.catch(() => undefined));
  } finally {
    await sidecar.stop(1000);
  }
});

test("one oversized request is refused before it reaches the host", async () => {
  const { sidecar } = fakeHost({ maxBufferedBytes: 128 });
  sidecar.start();
  try {
    await expect(sidecar.invoke("ping")).resolves.toBeTruthy();
    await expect(sidecar.invoke("echo", { text: "x".repeat(256) })).rejects.toThrow("agent-host is busy");
    await expect(sidecar.invoke("ping")).resolves.toBeTruthy();
  } finally {
    await sidecar.stop(1000);
  }
});

test("a malformed reply rejects its call; stray JSON values do not break the bridge", async () => {
  const logs: string[] = [];
  const { sidecar } = fakeHost({ log: (line) => logs.push(line) });
  sidecar.start();
  try {
    await expect(sidecar.invoke("garbled")).rejects.toThrow("malformed reply to garbled");
    await expect(sidecar.invoke("shapeless")).rejects.toThrow("malformed reply to shapeless");
    await expect(sidecar.invoke("junk")).resolves.toBe(1);
    await expect(sidecar.invoke("ping")).resolves.toBeTruthy();
    expect(logs.some((l) => l.includes("unexpected line"))).toBe(true);
  } finally {
    await sidecar.stop(1000);
  }
});

test("a restarted host is announced only once it answers a ping", async () => {
  // The fake ignores pings for its first 300ms of life.
  const { sidecar, restarts } = fakeHost({ env: { ...process.env, FAKE_DEAF_MS: "300" } });
  sidecar.start();
  try {
    await expect(sidecar.invoke("crash")).rejects.toThrow("agent-host exited");
    const crashedAt = Date.now();
    await waitFor(() => restarts() === 1, 5000);
    expect(Date.now() - crashedAt).toBeGreaterThanOrEqual(280);
    await expect(sidecar.invoke("echo")).resolves.toBeTruthy();
  } finally {
    await sidecar.stop(1000);
  }
});

test("the read list matches the host's dispatcher classes", () => {
  const src = readFileSync(path.join(__dirname, "../../core/cmd/agent-host/dispatcher.go"), "utf8");
  const classOf = new Map([...src.matchAll(/"(\w+)":\s*(run\w+)/g)].map((m) => [m[1], m[2]]));
  // Runs on the read pool but stops a child: a mutation for deadlines.
  const sideEffects = new Set(["interruptAgent"]);
  for (const [method, cls] of classOf) {
    if (cls === "runRead" && !sideEffects.has(method)) expect(isReadMethod(method), method).toBe(true);
    if (isReadMethod(method)) expect(["runRead", "runInline"], method).toContain(cls);
  }
  expect(classOf.size).toBeGreaterThan(50);
});
