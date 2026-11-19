// Opt-in live renderer -> real sidecar -> local LiteLLM test. All workspace
// and Paladin data are temporary; the proxy key stays in the process environment.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { showSidebar } from "./drawer";

test("switches sessions while a live local-proxy turn is running", async ({ page }) => {
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
    const first = (await rpc("openSession", { cwd: root })) as { sessionId: string };
    const second = (await rpc("openSession", { cwd: root })) as { sessionId: string };
    await page.goto("/");
    await expect(page.locator("#promptInput")).toBeVisible();
    await showSidebar(page);
    await page.locator(`#sessionList [data-session="${first.sessionId}"]`).first().click();
    await page.locator("#promptInput").fill("Write 60 short numbered facts about the color red. Do not use any tools.");
    await page.locator("#promptInput").press("Enter");
    await expect(page.locator("#pauseBtn")).toBeVisible();
    await expect
      .poll(async () => ((await rpc("turns", {})) as Array<{ state: string }>).some((turn) => turn.state === "running"))
      .toBe(true);
    await writeFile(path.join(root, "external.txt"), "outside change\n");
    const activeChanges = (await rpc("sessionChanges", { sessionId: first.sessionId, cwd: root })) as {
      files: unknown[];
    };
    expect(activeChanges.files).toHaveLength(0);
    await showSidebar(page);
    const start = Date.now();
    await page.locator(`#sessionList [data-session="${second.sessionId}"]`).first().click();
    await expect(page.locator("#sessionLabel")).toHaveText(second.sessionId.slice(0, 8));
    await expect(page.locator("#pauseBtn")).toBeHidden();
    await expect(page.locator("#messages")).not.toContainText("Write 60 short numbered facts");
    const elapsed = Date.now() - start;
    console.log(`Live session switch: ${elapsed} ms`);
    expect(elapsed).toBeLessThan(5000);
    await expect.poll(() => outcomes.length, { timeout: 90000 }).toBeGreaterThanOrEqual(1);
    expect(outcomes.at(-1)?.state).toBe("done");
    for (const session of [first, second]) {
      const changes = (await rpc("sessionChanges", { sessionId: session.sessionId, cwd: root })) as {
        files: unknown[];
      };
      expect(changes.files).toHaveLength(0);
    }
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
