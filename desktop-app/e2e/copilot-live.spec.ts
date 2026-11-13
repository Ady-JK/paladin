// Opt-in renderer -> real sidecar -> signed-in Copilot test with temporary data.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";

test("Copilot catalog routes a selected Luna model through a real agent turn", async ({ page }) => {
  test.skip(process.env.PALADIN_COPILOT_LIVE_TEST !== "1", "Set PALADIN_COPILOT_LIVE_TEST=1 for Copilot live testing");
  test.setTimeout(90_000);
  const binary = process.env.PALADIN_IDE_REAL_HOST;
  const base = process.env.PALADIN_COPILOT_BASE_URL;
  if (!binary || !base || !/^https:\/\/api(?:\.[a-z0-9-]+)*\.githubcopilot\.com$/i.test(base)) {
    throw new Error("PALADIN_IDE_REAL_HOST and a trusted PALADIN_COPILOT_BASE_URL are required");
  }
  const temp = await mkdtemp(path.join(tmpdir(), "paladin-live-copilot-"));
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    PALADIN_DATA_DIR: path.join(temp, "data"),
    PALADIN_SNAPSHOTS: "off",
  };
  delete env.PALADIN_KEYSTORE;
  const child = spawn(binary, [], { env, stdio: ["pipe", "pipe", "ignore"] });
  const lines = createInterface({ input: child.stdout });
  const events: Array<{ event?: string; turn?: { reply?: string; state?: string } }> = [];
  let nextId = 0;
  const pending = new Map<string, { resolve: (value: any) => void; reject: (error: Error) => void }>();
  lines.on("line", (line) => {
    const message = JSON.parse(line) as { id?: string; ok?: boolean; result?: unknown; error?: string; event?: string };
    if (message.event) events.push(message);
    if (!message.id) return;
    const item = pending.get(message.id);
    if (!item) return;
    pending.delete(message.id);
    if (message.ok) item.resolve(message.result);
    else item.reject(new Error(message.error ?? "Sidecar request failed"));
  });
  const rpc = (method: string, params: unknown): Promise<any> => {
    const id = String(++nextId);
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
    });
  };
  try {
    await rpc("updateProvider", { name: "github-copilot", apiUrl: base, apiType: "github-copilot" });
    await rpc("setDefaultProvider", { name: "github-copilot" });
    await page.exposeFunction("__liveCopilotRpc", rpc);
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript(() => {
      window.addEventListener("DOMContentLoaded", () => {
        const w = window as any;
        const previous = w.__paladinBaseInvoke;
        w.__liveCopilotRefreshes = [];
        w.__paladinBaseInvoke = async (method: string, params: unknown) => {
          if (method === "refreshProviders") {
            w.__liveCopilotRefreshes.push(params);
            return w.__liveCopilotRpc(method, params);
          }
          if (["listProviders", "compactionGet", "compactionSet"].includes(method))
            return w.__liveCopilotRpc(method, params);
          return previous(method, params);
        };
      });
    });
    await page.goto("/");
    await expect(page.locator("#promptInput")).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => (window as any).__liveCopilotRefreshes), { timeout: 30_000 })
      .toEqual([{ name: "github-copilot" }]);
    await expect
      .poll(
        async () => {
          const providers = (await rpc("listProviders", {})) as Array<{
            name: string;
            meta?: Record<
              string,
              { protocol?: string; pricing?: { tiers: Array<{ input?: number; output?: number }> } }
            >;
          }>;
          return providers.find((p) => p.name === "github-copilot")?.meta?.["gpt-5.6-luna"]?.protocol;
        },
        { timeout: 30_000 },
      )
      .toBe("responses");
    const providers = (await rpc("listProviders", {})) as Array<{
      name: string;
      models: string[];
      meta?: Record<string, { protocol?: string; pricing?: { tiers: Array<{ input?: number; output?: number }> } }>;
    }>;
    const copilot = providers.find((p) => p.name === "github-copilot");
    expect(copilot?.meta?.["gpt-5.6-luna"]?.protocol).toBe("responses");
    expect(copilot?.meta?.["gpt-5.6-luna"]?.pricing?.tiers[0].input).toBe(0.2);
    expect(copilot?.models).not.toContain("gpt-6-luna-decisions");
    const lunaOptions = await rpc("compactionGet", { provider: "github-copilot", model: "gpt-5.6-luna" });
    expect(lunaOptions.window).toBe(200000);
    expect(lunaOptions.options.find((option: { window: number }) => option.window === 922000)?.surcharge).toBe(true);
    const sonnet = await rpc("compactionGet", { provider: "github-copilot", model: "claude-sonnet-5.5" });
    expect(sonnet.window).toBe(936000);
    expect(sonnet.totalWindow).toBe(1000000);
    expect(sonnet.options.map((option: { window: number }) => option.window)).toEqual([200000, 936000]);
    const shortContext = await rpc("compactionSet", {
      provider: "github-copilot",
      model: "claude-sonnet-5.5",
      percent: 90,
      window: 200000,
    });
    expect(shortContext.window).toBe(200000);
    const longContext = await rpc("compactionSet", {
      provider: "github-copilot",
      model: "claude-sonnet-5.5",
      percent: 90,
      window: 936000,
    });
    expect(longContext.window).toBe(936000);
    await page.click("#modelSelectBtn");
    const luna = page.locator('#modelTree [data-mpick="github-copilot\tgpt-5.6-luna"]');
    await luna.hover();
    await expect(page.locator("#modelPriceTooltip")).toContainText("100 AI credits = $1");
    await luna.click();
    await expect(page.locator("#modelSelectLabel")).toHaveText("gpt-5.6-luna");
    await expect(page.locator("#contextButton")).not.toHaveText("Ctx —");
    await page.locator("#contextButton").click();
    await expect(page.locator("#contextPercent")).toHaveValue("90");
    await page.locator("#contextPercent").fill("80");
    await page.locator("#contextSave").click();
    await expect(page.locator("#contextMenu")).toBeHidden();
    const context = await rpc("compactionGet", { provider: "github-copilot", model: "gpt-5.6-luna" });
    expect(context.percent).toBe(80);
    const selected = await page.evaluate(() => JSON.parse(localStorage.getItem("paladin.model") || "{}"));
    expect(selected).toEqual({ provider: "github-copilot", model: "gpt-5.6-luna" });
    await rpc("setDefaultProvider", { name: selected.provider });
    const session = await rpc("openSession", { cwd: temp });
    const otherSession = await rpc("openSession", { cwd: temp });
    const selection = { provider: "github-copilot", model: "claude-sonnet-5.5" };
    await rpc("compactionSet", { ...selection, sessionId: session.sessionId, cwd: temp, percent: 75, window: 200000 });
    const first = await rpc("compactionGet", { ...selection, sessionId: session.sessionId, cwd: temp });
    const other = await rpc("compactionGet", { ...selection, sessionId: otherSession.sessionId, cwd: temp });
    expect([first.window, first.percent]).toEqual([200000, 75]);
    expect([other.window, other.percent]).toEqual([936000, 90]);
    await rpc("openSession", { sessionId: session.sessionId, cwd: temp });
    const reopened = await rpc("compactionGet", { ...selection, sessionId: session.sessionId, cwd: temp });
    expect([reopened.window, reopened.percent]).toEqual([200000, 75]);
    await rpc("submitChat", {
      sessionId: session.sessionId,
      cwd: temp,
      provider: selected.provider,
      model: selected.model,
      message: "Reply with exactly: hello",
      permissions: "read-only",
    });
    await expect
      .poll(() => events.find((event) => event.event === "turn/done")?.turn?.state, { timeout: 60_000 })
      .toBe("done");
    expect(
      events
        .find((event) => event.event === "turn/done")
        ?.turn?.reply?.trim()
        .toLowerCase(),
    ).toBe("hello");
    const usage = await rpc("sessionUsage", { sessionId: session.sessionId, cwd: temp });
    expect(usage.hasCost).toBe(true);
    expect(usage.costEstimated).toBe(true);
    expect(usage.listCost).toBeGreaterThan(0);
    expect(await page.evaluate(() => (window as any).__liveCopilotRefreshes)).toEqual([{ name: "github-copilot" }]);
  } finally {
    child.stdin.end();
    await new Promise<void>((resolve) => {
      if (child.exitCode !== null) resolve();
      else child.once("exit", () => resolve());
    });
    lines.close();
    await rm(temp, { recursive: true, force: true });
  }
});
