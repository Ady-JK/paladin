// Opt-in live renderer -> real sidecar -> local LiteLLM test. All workspace
// and Paladin data are temporary; the proxy key stays in the process environment.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { showSidebar } from "./drawer";

test("IDE and image turns work with a live local-proxy agent", async ({ page }) => {
  test.skip(process.env.PALADIN_LIVE_UI_TEST !== "1", "Set PALADIN_LIVE_UI_TEST=1 to use the local proxy");
  test.setTimeout(180_000);
  const binary = process.env.PALADIN_IDE_REAL_HOST;
  const key = process.env.LITELLM_MASTER_KEY;
  if (!binary || !key) throw new Error("PALADIN_IDE_REAL_HOST and LITELLM_MASTER_KEY are required");
  const temp = await mkdtemp(path.join(tmpdir(), "paladin-live-ide-"));
  const root = path.join(temp, "project-under-test");
  await mkdir(root);
  await writeFile(path.join(root, "result.txt"), "before-live\n");
  const child = spawn(binary, [], {
    env: { ...process.env, PALADIN_DATA_DIR: path.join(temp, "data"), PALADIN_KEYSTORE: "memory" },
    stdio: ["pipe", "pipe", "ignore"],
  });
  const lines = createInterface({ input: child.stdout });
  let nextId = 0;
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
  const outcomes: Array<{ state?: string; reply?: string }> = [];
  lines.on("line", (line) => {
    const msg = JSON.parse(line) as {
      id?: string;
      event?: string;
      ok?: boolean;
      result?: unknown;
      error?: string;
      turn?: { state?: string; reply?: string };
    };
    if (msg.event) {
      if (msg.event === "turn/done" && msg.turn) outcomes.push(msg.turn);
      void page.evaluate((event) => (window as any).__e2eHub?.emit(event), msg).catch(() => undefined);
      return;
    }
    if (!msg.id) return;
    const item = pending.get(msg.id);
    if (!item) return;
    pending.delete(msg.id);
    if (msg.ok) item.resolve(msg.result);
    else item.reject(new Error(msg.error ?? "Sidecar request failed"));
  });
  const rpc = (method: string, params: unknown): Promise<unknown> => {
    const id = String(++nextId);
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
    });
  };
  try {
    await rpc("updateProvider", {
      name: "litellm",
      apiUrl: "http://localhost:4000/v1",
      apiType: "openai",
      apiKey: key,
    });
    await rpc("setDefaultProvider", { name: "litellm" });
    const refresh = (await rpc("refreshProviders", { name: "litellm" })) as { error?: string };
    expect(refresh.error).toBeUndefined();
    const providers = (await rpc("listProviders", {})) as Array<{ name: string; meta?: Record<string, unknown> }>;
    const metadata = providers.find((provider) => provider.name === "litellm")?.meta ?? {};
    await page.exposeFunction("__liveIdeRpc", rpc);
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript(
      ({ workspace, metadata }) => {
        window.addEventListener("DOMContentLoaded", () => {
          const w = window as any;
          const fallback = w.__paladinBaseInvoke;
          w.__paladinBaseInvoke = (method: string, params: any) => {
            if (method === "getWorkspace") return Promise.resolve(workspace);
            if (method === "listProviders")
              return Promise.resolve([
                {
                  name: "litellm",
                  apiType: "openai",
                  apiUrl: "http://localhost:4000/v1",
                  model: "claude-sonnet-5",
                  models: ["claude-sonnet-5"],
                  default: true,
                  hasKey: true,
                  meta: metadata,
                },
              ]);
            if (method === "submitChat") {
              w.__liveSessionId = params.sessionId;
              return w.__liveIdeRpc(method, { ...params, provider: "litellm", model: "claude-sonnet-5" });
            }
            if (
              [
                "openSession",
                "saveAttachment",
                "saveTaggedImage",
                "sessionImage",
                "fileComplete",
                "filePreview",
                "sessionUsage",
                "compactionGet",
                "thinkingGet",
                "thinkingSet",
                "attach",
                "turns",
                "listSessions",
                "ideList",
                "ideSearch",
                "ideReadFile",
                "ideFileStatuses",
                "ideSaveFile",
                "ideLayoutGet",
                "ideLayoutSet",
                "ideExplorerSettingsGet",
                "ideExplorerSettingsSet",
                "sessionChanges",
                "sessionDiff",
                "sessionImages",
                "answerApproval",
              ].includes(method)
            )
              return w.__liveIdeRpc(method, params);
            return fallback(method, params);
          };
        });
      },
      { workspace: root, metadata },
    );
    await page.goto("/");
    await expect(page.locator("#workspaceLabel")).not.toHaveText("workspace");
    await page.locator("#ideModeBtn").click();
    await expect(page.locator("#ideWorkspace")).toBeVisible();
    await expect(page.locator("#contextBox")).toBeVisible();
    await page.locator("#thinkingMore").click();
    const thinkingSlider = page.locator('#thinkingMenu input[type="range"]');
    await expect(thinkingSlider).toBeVisible();
    await thinkingSlider.focus();
    await page.keyboard.press("End");
    await expect(page.locator("html")).toHaveAttribute("data-think-level", "max");
    if (process.env.PALADIN_LIVE_SCREENSHOT)
      await page.screenshot({ path: process.env.PALADIN_LIVE_SCREENSHOT, animations: "disabled" });
    await page.locator("#thinkingMenu .think-reset").click();
    await expect(page.locator("html")).toHaveAttribute("data-think-level", "default");
    await page.keyboard.press("Escape");
    if (await page.locator(".ide-explorer.is-closed").count()) await page.locator("#ideExplorerToggle").click();
    await expect(page.getByRole("button", { name: "result.txt", exact: true })).toBeVisible({ timeout: 15_000 });
    await page.getByRole("button", { name: "result.txt", exact: true }).click();
    await expect(page.locator("#ideEditor .cm-content")).toHaveText("before-live");
    await page
      .locator("#promptInput")
      .fill(
        "Use the write tool to replace result.txt in this workspace with exactly orbit-live-ok, then reply briefly.",
      );
    await page.locator("#promptInput").press("Enter");
    await expect
      .poll(async () => readFile(path.join(root, "result.txt"), "utf8").catch(() => ""), { timeout: 90_000 })
      .toContain("orbit-live-ok");
    await expect(page.getByText("This file changed on disk.")).toBeVisible({ timeout: 15_000 });
    await page.getByRole("button", { name: "Compare changes" }).click();
    await expect(page.locator("#ideDiskCompare")).toContainText("orbit-live-ok");
    await expect(page.locator("#ideDiskCompare")).toContainText("before-live");
    await page.getByRole("button", { name: "Close comparison" }).click();
    await expect.poll(async () => ((await rpc("turns", {})) as unknown[]).length, { timeout: 60_000 }).toBe(0);
    await expect(page.locator("#messages")).toContainText("result.txt", { timeout: 30_000 });
    await page.getByRole("button", { name: "Show changes" }).click();
    const panel = page.locator("#ideChangesPanel");
    await expect(panel.getByRole("button", { name: /result.txt/ })).toBeVisible({ timeout: 30_000 });
    await panel.getByRole("button", { name: /result.txt/ }).click();
    await expect(panel.locator(".change-diff")).toContainText("orbit-live-ok", { timeout: 30_000 });
    await expect(page.locator("#contextBox")).toBeVisible();
    const context = await page.locator("#contextBox").boundingBox();
    const composer = await page.locator(".composer-float").boundingBox();
    expect(context && composer && context.y + context.height <= composer.y + 1).toBe(true);
    const image = await page.evaluate(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 32;
      canvas.height = 32;
      const context = canvas.getContext("2d")!;
      context.fillStyle = "#ff0000";
      context.fillRect(0, 0, 32, 32);
      return canvas.toDataURL("image/png").split(",")[1];
    });
    await writeFile(path.join(root, "red-square.png"), Buffer.from(image, "base64"));
    await page.locator("#promptInput").fill("@red-square");
    await expect(page.locator("#mentionPopup")).toContainText("red-square.png");
    await page.keyboard.press("Enter");
    await expect(page.locator("#tagCards")).toContainText("red-square.png");
    await page.locator("#promptInput").fill("What is the solid color of this image? Answer with one color name.");
    await page.locator("#promptInput").press("Enter");
    await expect(page.locator("#messages .session-image-thumb")).toBeVisible();
    await expect.poll(() => outcomes.length, { timeout: 90000 }).toBeGreaterThanOrEqual(2);
    expect(outcomes.at(-1)?.state).toBe("done");
    expect(outcomes.at(-1)?.reply).toMatch(/\bred\b/i);
    const sessionId = await page.evaluate(() => (window as any).__liveSessionId as string);
    await page.locator("#promptInput").fill("/compact");
    await page.locator("#promptInput").press("Enter");
    await expect.poll(() => outcomes.length, { timeout: 90000 }).toBeGreaterThanOrEqual(3);
    expect(outcomes.at(-1)?.state).toBe("done");
    await expect(page.locator('#messages [data-notice="compaction"]')).toContainText("→");
    const history = (await rpc("sessionHistory", { sessionId, cwd: root })) as Array<{
      role: string;
      attachments?: Array<{ name: string; image?: unknown }>;
      compaction?: { tokensBefore: number; tokensAfter?: number };
    }>;
    expect(
      history.some(
        (row) => row.role === "user" && row.attachments?.some((a) => a.name === "red-square.png" && a.image),
      ),
    ).toBe(true);
    const checkpoint = history.find((row) => row.compaction)?.compaction;
    expect(checkpoint?.tokensBefore).toBeGreaterThan(0);
    expect(checkpoint?.tokensAfter).toBeGreaterThan(0);
    await page.reload();
    await showSidebar(page);
    await page.locator(`#sessionList [data-session="${sessionId}"]`).first().click();
    await expect(page.locator("#messages .session-image-thumb")).toBeVisible();
    await expect(page.locator('#messages [data-notice="compaction"]')).toContainText("→");
    const usage = (await rpc("sessionUsage", { sessionId, cwd: root })) as {
      calls: number;
      hasCost: boolean;
      trackedSpend?: number;
      cost: number;
      listCost: number;
    };
    expect(usage.calls).toBeGreaterThanOrEqual(2);
    expect(usage.hasCost).toBe(true);
    expect(usage.trackedSpend ?? Math.max(usage.cost, usage.listCost)).toBeGreaterThan(0);
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
