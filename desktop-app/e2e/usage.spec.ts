import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the usage pill stays hidden until a session is active", async ({ page }) => {
  await expect(page.locator("#usagePill")).toBeHidden();
});

test("the usage pill shows fill and spend after a turn", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  const pill = page.locator("#usagePill");
  await expect(pill).toBeVisible();
  await expect(page.locator("#usageLabel")).toHaveText("12.5% · $0.0042");

  const title = await pill.getAttribute("title");
  expect(title).toContain("beam-free");
  expect(title).toContain("25,000");
  expect(title).toContain("12.5%");
  expect(title).toContain("3 calls");
});

test("the usage pill falls back to tokens when the window is unknown", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-notify-1",
      model: "omen-alpha",
      contextWindow: 200000,
      contextKnown: false,
      calls: 1,
      promptTokens: 3000,
      completionTokens: 66,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 3066,
      cost: 0,
      listCost: 0,
      hasCost: true,
      models: [],
      // fillPercent absent: unknown window.
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  await expect(page.locator("#usagePill")).toBeVisible();
  await expect(page.locator("#usageLabel")).toHaveText("3.1k tok · $0");

  const title = await page.locator("#usagePill").getAttribute("title");
  expect(title).toContain("window unknown");
});

test("clicking the pill opens the analytics dashboard", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await expect(page.locator("#usagePill")).toBeVisible();

  await page.click("#usagePill");
  const modal = page.locator("#usageModal");
  await expect(modal).toBeVisible();
  await expect(page.locator("#usageBody")).toContainText("Total tokens");
  await expect(page.locator("#usageBody")).toContainText("25,000");
  await expect(page.locator("#usageBody")).toContainText("Context fill");
  await expect(page.locator("#usageBody svg").first()).toBeVisible();
  await expect(page.locator("#usageBody")).toContainText("Share by model");

  await page.click("#usageTabModels");
  await expect(page.locator("#usageBody")).toContainText("beam-free");
  await expect(page.locator("#usageBody")).toContainText("kimi-k2.6");
  await expect(page.locator("#usageBody")).toContainText("19,000");

  await page.click("#usageCloseBtn");
  await expect(modal).toBeHidden();
});

test("escape closes the analytics dashboard", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await page.click("#usagePill");
  await expect(page.locator("#usageModal")).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(page.locator("#usageModal")).toBeHidden();
});
