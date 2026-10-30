import { test, expect } from "@playwright/test";
import { agentHostFile, restartDelay, Sidecar, type HostMessage } from "../electron/sidecar";

// A minimal JSONL host: answers ping/echo, emits an event on "emit",
// dies on "crash", and exits cleanly when stdin closes.
const FAKE_HOST = `
const rl = require("node:readline").createInterface({ input: process.stdin });
const out = (o) => process.stdout.write(JSON.stringify(o) + "\\n");
rl.on("line", (line) => {
  const req = JSON.parse(line);
  if (req.method === "crash") process.exit(3);
  if (req.method === "hang") return;
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
