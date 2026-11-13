// Opt-in renderer -> real sidecar -> local LiteLLM catalog test.
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";

test("refreshing LiteLLM exposes its current models in the composer", async ({ page }) => {
  test.skip(process.env.PALADIN_LIVE_UI_TEST !== "1", "Set PALADIN_LIVE_UI_TEST=1 to use the local proxy");
  const binary = process.env.PALADIN_IDE_REAL_HOST;
  const key = process.env.LITELLM_MASTER_KEY;
  if (!binary || !key) throw new Error("PALADIN_IDE_REAL_HOST and LITELLM_MASTER_KEY are required");
  const temp = await mkdtemp(path.join(tmpdir(), "paladin-live-models-"));
  const data = path.join(temp, "data");
  await mkdir(path.join(data, "config"), { recursive: true });
  await writeFile(
    path.join(data, "config", "models.json"),
    JSON.stringify({ providers: { litellm: { allModels: ["old-model"], enabledModels: ["old-model"] } } }),
  );
  const child = spawn(binary, [], {
    env: { ...process.env, PALADIN_DATA_DIR: data, PALADIN_KEYSTORE: "memory" },
    stdio: ["pipe", "pipe", "ignore"],
  });
  const lines = createInterface({ input: child.stdout });
  let nextId = 0;
  const pending = new Map<string, { resolve: (value: any) => void; reject: (error: Error) => void }>();
  lines.on("line", (line) => {
    const message = JSON.parse(line) as { id?: string; ok?: boolean; result?: unknown; error?: string };
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
    await rpc("updateProvider", {
      name: "litellm",
      apiUrl: "http://localhost:4000/v1",
      apiType: "openai",
      apiKey: key,
    });
    await rpc("setDefaultProvider", { name: "litellm" });
    await page.exposeFunction("__liveCatalogRpc", rpc);
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript(() => {
      window.addEventListener("DOMContentLoaded", () => {
        const w = window as any;
        const fallback = w.__paladinBaseInvoke;
        w.__liveCatalogCalls = [];
        w.__paladinBaseInvoke = async (method: string, params: unknown) => {
          if (method === "refreshProviders") {
            w.__liveCatalogCalls.push(params);
            const result = await w.__liveCatalogRpc(method, params);
            w.__liveCatalogResult = result;
            return result;
          }
          if (["listProviders", "compactionGet", "compactionSet"].includes(method))
            return w.__liveCatalogRpc(method, params);
          return fallback(method, params);
        };
      });
    });
    await page.goto("/");
    await expect(page.locator("#promptInput")).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => (window as any).__liveCatalogResult?.count), { timeout: 30_000 })
      .toBeGreaterThan(0);
    expect(await page.evaluate(() => (window as any).__liveCatalogCalls)).toEqual([{ name: "litellm" }]);
    const providers = (await rpc("listProviders", {})) as Array<{
      name: string;
      models: string[];
      meta?: Record<
        string,
        { contextWindow?: number; pricing?: { tiers: Array<{ input?: number; aboveInputTokens?: number }> } }
      >;
    }>;
    const models = providers.find((provider) => provider.name === "litellm")?.models ?? [];
    const fetched = await page.evaluate(
      () => (window as any).__liveCatalogResult as { count: number; models: string[] },
    );
    expect(models.length).toBeGreaterThan(0);
    const meta = providers.find((p) => p.name === "litellm")?.meta ?? {};
    expect(Object.values(meta).some((item) => (item.contextWindow ?? 0) > 0)).toBe(true);
    expect(
      Object.values(providers.find((p) => p.name === "litellm")?.meta ?? {}).some(
        (m) => m.pricing?.tiers[0]?.input !== undefined,
      ),
    ).toBe(true);
    expect(models).toEqual(fetched.models);
    expect(models).toHaveLength(fetched.count);
    expect(models).not.toContain("old-model");
    await page.locator("#modelSelectBtn").click();
    const rows = page.locator('#modelTree [data-mpick^="litellm\t"]');
    await expect(rows).toHaveCount(models.length);
    const selected = (await rows.first().getAttribute("data-mpick"))!.split("\t")[1];
    await rows.first().click();
    const short = selected.includes("/") ? selected.slice(selected.indexOf("/") + 1) : selected;
    await expect(page.locator("#modelSelectLabel")).toHaveText(short.replace(/^~+/, ""));
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem("paladin.model") || "{}"))).toEqual({
      provider: "litellm",
      model: selected,
    });
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
