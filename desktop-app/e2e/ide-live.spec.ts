// Opt-in live renderer -> real sidecar -> local LiteLLM test. All workspace
// and Paladin data are temporary; the proxy key stays in the process environment.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";

test("IDE shows changes from a live local-proxy agent", async ({ page }) => {
  test.skip(process.env.PALADIN_LIVE_UI_TEST !== "1", "Set PALADIN_LIVE_UI_TEST=1 to use the local proxy");
  test.setTimeout(120_000);
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
  lines.on("line", (line) => {
    const msg = JSON.parse(line) as { id?: string; event?: string; ok?: boolean; result?: unknown; error?: string };
    if (msg.event) {
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
    await rpc("addProvider", {
      name: "local-test",
      apiUrl: "http://localhost:4000/v1",
      apiType: "openai",
      apiKey: key,
    });
    await rpc("setDefaultProvider", { name: "local-test" });
    await page.exposeFunction("__liveIdeRpc", rpc);
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript((workspace) => {
      window.addEventListener("DOMContentLoaded", () => {
        const w = window as any;
        const fallback = w.__paladinBaseInvoke;
        w.__paladinBaseInvoke = (method: string, params: any) => {
          if (method === "getWorkspace") return Promise.resolve(workspace);
          if (method === "listProviders")
            return Promise.resolve([
              {
                name: "local-test",
                apiType: "openai",
                apiUrl: "http://localhost:4000/v1",
                model: "claude-sonnet-5",
                models: ["claude-sonnet-5"],
                default: true,
                hasKey: true,
              },
            ]);
          if (method === "submitChat")
            return w.__liveIdeRpc(method, { ...params, provider: "local-test", model: "claude-sonnet-5" });
          if (
            [
              "openSession",
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
    }, root);
    await page.goto("/");
    await expect(page.locator("#workspaceLabel")).not.toHaveText("workspace");
    await page.locator("#ideModeBtn").click();
    await expect(page.locator("#ideWorkspace")).toBeVisible();
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
