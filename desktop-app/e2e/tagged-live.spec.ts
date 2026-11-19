// Opt-in live renderer -> real sidecar -> local LiteLLM test. All workspace
// and Paladin data are temporary; the proxy key stays in the process environment.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { showSidebar } from "./drawer";

test("tagged images and files are visible during a live local-proxy turn", async ({ page }) => {
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
                  model: "claude-haiku-4-5-20251001",
                  models: ["claude-haiku-4-5-20251001"],
                  default: true,
                  hasKey: true,
                  meta: metadata,
                },
              ]);
            if (method === "submitChat") {
              w.__liveSessionId = params.sessionId;
              return w.__liveIdeRpc(method, { ...params, provider: "litellm", model: "claude-haiku-4-5-20251001" });
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
    await expect(page.locator("#promptInput")).toBeVisible();
    const image = await page.evaluate(() => {
      const canvas = document.createElement("canvas");
      canvas.width = 64;
      canvas.height = 64;
      const ctx = canvas.getContext("2d")!;
      ctx.fillStyle = "#e64c5b";
      ctx.fillRect(0, 0, 64, 64);
      return canvas.toDataURL("image/png").split(",")[1];
    });
    await writeFile(path.join(root, "red-square.png"), Buffer.from(image, "base64"));
    await page.locator("#promptInput").fill("@red-square");
    await expect(page.locator("#mentionPopup")).toContainText("red-square.png");
    await page.keyboard.press("Enter");
    await expect(page.locator("#tagCards")).toContainText("red-square.png");
    await page.locator("#promptInput").fill("@result");
    await expect(page.locator("#mentionPopup")).toContainText("result.txt");
    await page.keyboard.press("Enter");
    await expect(page.locator("#tagCards")).toContainText("result.txt");
    await page
      .locator("#promptInput")
      .fill("What color is the image? Then write 60 short numbered lines about that color.");
    await page.locator("#promptInput").press("Enter");
    const row = page.locator("#messages [data-turn-id]").filter({ hasText: "What color is the image?" });
    await expect(page.locator("#pauseBtn")).toBeVisible();
    await expect(row.locator('.session-image-thumb img[alt="red-square.png"]')).toBeVisible();
    await expect(row.locator(".session-file-card")).toContainText("result.txt");
    if (process.env.PALADIN_LIVE_SCREENSHOT)
      await page.screenshot({ path: process.env.PALADIN_LIVE_SCREENSHOT, animations: "disabled" });
    await expect(page.locator("#pauseBtn")).toBeVisible();
    const sessionId = await page.evaluate(() => (window as any).__liveSessionId as string);
    await page.reload();
    await showSidebar(page);
    await page.locator(`#sessionList [data-session="${sessionId}"]`).first().click();
    await expect(page.locator("#pauseBtn")).toBeVisible();
    await expect(page.locator('#messages .session-image-thumb img[alt="red-square.png"]')).toBeVisible();
    await expect(page.locator("#messages .session-file-card")).toContainText("result.txt");
    await expect.poll(() => outcomes.length, { timeout: 90000 }).toBeGreaterThanOrEqual(1);
    expect(outcomes.at(-1)?.state).toBe("done");
    expect(outcomes.at(-1)?.reply).toMatch(/\bred\b/i);
    const history = (await rpc("sessionHistory", { sessionId, cwd: root })) as Array<{
      role: string;
      attachments?: Array<{ name: string; image?: unknown }>;
    }>;
    const sent = history.find((message) => message.role === "user");
    expect(sent?.attachments?.some((attachment) => attachment.name === "red-square.png" && attachment.image)).toBe(
      true,
    );
    expect(sent?.attachments?.some((attachment) => attachment.name === "result.txt")).toBe(true);
    await page.reload();
    await showSidebar(page);
    await page.locator(`#sessionList [data-session="${sessionId}"]`).first().click();
    const restored = page.locator("#messages").filter({ hasText: "What color is the image?" });
    await expect(restored.locator('.session-image-thumb img[alt="red-square.png"]')).toBeVisible();
    await expect(restored.locator(".session-file-card")).toContainText("result.txt");
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
