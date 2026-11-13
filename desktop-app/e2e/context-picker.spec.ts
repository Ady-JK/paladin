import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test("context window and compaction threshold accept percentages and token amounts", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const bridge = (window as any).paladin;
    const original = bridge.invoke;
    let percent = 90;
    let windowSize = 200000;
    bridge.invoke = (method: string, params: any) => {
      if (method === "compactionGet" || method === "compactionSet") {
        if (method === "compactionSet") {
          percent = params.percent;
          windowSize = params.window;
        }
        return Promise.resolve({
          provider: params.provider,
          model: params.model,
          window: windowSize,
          totalWindow: 1000000,
          options: [{ window: 200000 }, { window: 936000, totalWindow: 1000000 }],
          known: true,
          percent,
        });
      }
      return original(method, params);
    };
  });
  await page.goto("/");
  await expect(page.locator("#contextButton")).toContainText("200k");
  await page.click("#contextButton");
  await expect(page.locator("#contextPercent")).toHaveValue("90");
  await expect(page.locator("#contextSize")).toContainText("1M context · 936k input");
  await expect(page.locator("#contextTokens")).toHaveValue("180k");
  await page.fill("#contextPercent", "80");
  await expect(page.locator("#contextTokens")).toHaveValue("160k");
  await page.fill("#contextTokens", "50k");
  await expect(page.locator("#contextPercent")).toHaveValue("25");
  await page.click("#contextSave");
  await page.click("#contextButton");
  await expect(page.locator("#contextPercent")).toHaveValue("25");
  await page.fill("#contextPercent", "100");
  await expect(page.locator("#contextTokens")).toHaveValue("200k");
  await page.click("#contextSave");
  await page.click("#contextButton");
  await expect(page.locator("#contextPercent")).toHaveValue("100");
  await page.selectOption("#contextSize", "936000");
  await expect(page.locator("#contextTokens")).toHaveValue("936k");
  await page.click("#contextSave");
  await expect(page.locator("#contextButton")).toContainText("1M");
  await page.click("#contextButton");
  await expect(page.locator("#contextSize")).toHaveValue("936000");
});

test("context choices follow each session when switching and reopening", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const bridge = (window as any).paladin;
    const original = bridge.invoke;
    const saved: Record<string, { percent: number; window: number }> = {};
    bridge.invoke = (method: string, params: any) => {
      if (method === "listProviders")
        return Promise.resolve([
          { name: "litellm", default: true, apiType: "openai", apiUrl: "", model: "sonnet", models: ["sonnet"] },
        ]);
      if (method === "listSessions")
        return Promise.resolve(
          ["session-a", "session-b"].map((id) => ({
            sessionId: id,
            cwd: "/home/tester/project",
            provider: "litellm",
            model: "sonnet",
            title: id,
            createdAt: 100,
            messageCount: 1,
          })),
        );
      if (method === "openSession")
        return Promise.resolve({
          sessionId: params.sessionId,
          cwd: "/home/tester/project",
          provider: "litellm",
          model: "sonnet",
        });
      if (method === "compactionGet" || method === "compactionSet") {
        const id = params.sessionId || "draft";
        if (method === "compactionSet") saved[id] = { percent: params.percent, window: params.window };
        const choice = saved[id] || { percent: 90, window: 936000 };
        return Promise.resolve({
          provider: params.provider,
          model: params.model,
          ...choice,
          totalWindow: 1000000,
          known: true,
          options: [{ window: 200000 }, { window: 936000, totalWindow: 1000000 }],
        });
      }
      return original(method, params);
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('[data-session="session-a"]').click();
  await expect(page.locator("#contextButton")).toContainText("1M");
  await page.click("#contextButton");
  await page.selectOption("#contextSize", "200000");
  await page.fill("#contextPercent", "75");
  await page.click("#contextSave");
  await expect(page.locator("#contextButton")).toContainText("200k");
  await showSidebar(page);
  await page.locator('[data-session="session-b"]').click();
  await expect(page.locator("#contextButton")).toContainText("1M");
  await page.click("#contextButton");
  await expect(page.locator("#contextPercent")).toHaveValue("90");
  await page.click("#contextButton");
  await showSidebar(page);
  await page.locator('[data-session="session-a"]').click();
  await expect(page.locator("#contextButton")).toContainText("200k");
  await page.click("#contextButton");
  await expect(page.locator("#contextPercent")).toHaveValue("75");
});
