// Opt-in real renderer -> JSONL agent-host -> installed Claude Code test.
// Inference is hard-limited to Haiku 5.5; no TypeScript Claude SDK is involved.
import { test, expect } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { showSidebar } from "./drawer";

test("Claude Haiku streams from the real Go sidecar into the renderer", async ({ page }, testInfo) => {
  test.setTimeout(120000);
  const binary = process.env.PALADIN_CLAUDE_REAL_HOST;
  test.skip(!binary, "Set PALADIN_CLAUDE_REAL_HOST to a freshly built agent-host; this bills Haiku 5.5");
  const root = await mkdtemp(path.join(tmpdir(), "paladin-claude-ui-"));
  const child = spawn(binary!, [], {
    cwd: root,
    env: {
      ...process.env,
      PALADIN_DATA_DIR: path.join(root, "data"),
      PALADIN_KEYSTORE: "memory",
      PALADIN_SNAPSHOTS: "off",
    },
    stdio: ["pipe", "pipe", "ignore"],
  });
  const lines = createInterface({ input: child.stdout });
  page.on("pageerror", (error) => console.log("Claude renderer error:", error.message));
  const calls: string[] = [];
  let next = 0;
  let ready = false;
  const buffered: unknown[] = [];
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
  lines.on("line", (line) => {
    const message = JSON.parse(line) as { id?: string; event?: string; ok?: boolean; result?: unknown; error?: string };
    if (message.event) {
      if (!ready) buffered.push(message);
      else void page.evaluate((event) => (window as any).__e2eHub.emit(event), message).catch(() => undefined);
      return;
    }
    if (!message.id) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    if (message.ok) request.resolve(message.result);
    else request.reject(new Error(message.error ?? "sidecar request failed"));
  });
  const invoke = (method: string, params: Record<string, unknown> = {}): Promise<unknown> => {
    calls.push(method);
    if (method === "submitChat" && (params.provider !== "claude" || params.model !== "claude-haiku-5-5"))
      return Promise.reject(new Error("Live testing permits only Claude Haiku 5.5"));
    const id = `claude-ui-${++next}`;
    return new Promise((resolve, reject) => {
      pending.set(id, {
        resolve: (value) => {
          if (method === "claudeCommands") console.log("Claude command catalog received for", params.cwd);
          resolve(value);
        },
        reject: (error) => {
          console.log("Claude real RPC failed:", method, error.message);
          reject(error);
        },
      });
      child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
    });
  };
  try {
    await invoke("setWorkspace", { path: root });
    await invoke("budgetSet", { scope: "global", budgetUsd: 0.15 });
    const status = (await invoke("claudeStatus", { deep: true })) as {
      installed: boolean;
      loggedIn: boolean;
      error?: string;
    };
    expect(status.installed, status.error).toBe(true);
    expect(status.loggedIn, status.error).toBe(true);
    await page.exposeFunction("__claudeRealInvoke", invoke);
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript(() => {
      localStorage.setItem("paladin.claudeModel", JSON.stringify("claude-haiku-5-5"));
      const bridge = (window as any).paladin;
      const original = bridge.invoke;
      const real = new Set([
        "getWorkspace",
        "resolveWorkspace",
        "fsList",
        "claudeStatus",
        "claudeCommands",
        "openSession",
        "submitChat",
        "attach",
        "sessionHistoryPage",
        "sessionHistory",
        "sessionImages",
        "sessionUsage",
        "listSessions",
        "thinkingGet",
        "thinkingSet",
        "compactionGet",
        "budgetGet",
        "stopChat",
        "syncSession",
      ]);
      bridge.invoke = (method: string, params: Record<string, unknown>) => {
        if (["thinkingGet", "thinkingSet", "compactionGet"].includes(method) && params?.provider !== "claude")
          return original(method, params);
        return real.has(method) ? (window as any).__claudeRealInvoke(method, params || {}) : original(method, params);
      };
    });
    await page.goto("/");
    await expect(page.locator("#promptInput")).toBeVisible();
    ready = true;
    for (const event of buffered) await page.evaluate((value) => (window as any).__e2eHub.emit(value), event);
    await showSidebar(page);
    await page.click("#newSessionBtn");
    await expect(page.locator("#sessionStartClaudeBtn")).toBeEnabled();
    await page.click("#sessionStartClaudeBtn");
    await expect(page.locator("body")).toHaveClass(/harness-claude/, { timeout: 45000 });
    await expect(page.locator("#modelSelectLabel")).toContainText("claude-haiku-5-5");
    await page.fill("#promptInput", "Reply exactly UI_CLAUDE_HAIKU_OK. Do not use tools.");
    await page.click("#sendBtn");
    await expect(page.locator("#messages .agent-text")).toContainText("UI_CLAUDE_HAIKU_OK", { timeout: 45000 });
    await expect(page.locator("#pauseBtn")).toBeHidden({ timeout: 45000 });
    await page.screenshot({ path: testInfo.outputPath("claude-real-core.png") });
  } finally {
    console.log("Claude real RPC calls:", calls.join(", "));
    ready = false;
    lines.close();
    const exited = new Promise<void>((resolve) => {
      if (child.exitCode !== null) resolve();
      else child.once("exit", () => resolve());
    });
    child.stdin.end();
    const timer = setTimeout(() => child.kill("SIGKILL"), 10000);
    await exited;
    clearTimeout(timer);
    await rm(root, { recursive: true, force: true });
  }
});
