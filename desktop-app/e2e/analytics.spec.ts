import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("the analytics pane shows the fleet dashboard", async ({ page }) => {
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsModal")).toBeVisible();
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  // The stub is a billed session (costEstimated absent), so the label is
  // "Total cost"; "Est." appears only for a subscription's list price.
  await expect(body).toContainText("Total cost");
  await expect(body).toContainText("Total tokens");
  await expect(body).toContainText("Sessions");
  await expect(body).toContainText("Prompt cache hit");
  await expect(body).toContainText("Input tokens");
  await expect(body).toContainText("beam-free");
  await expect(body).toContainText("Recent sessions");
  await expect(body.locator("svg").first()).toBeVisible();
  await page.click("#settingsCloseBtn");
  await expect(page.locator("#settingsModal")).toBeHidden();
});

test("the analytics model filter narrows the table", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  await expect(body).toContainText("kimi-k2.6");
  await body.locator("#analyticsModelFilter").selectOption("kimi-k2.6");
  await expect(body).toContainText("kimi-k2.6");
  await expect(body.locator("table")).not.toContainText("beam-free");
  await page.click("#settingsCloseBtn");
});

// A ledger written before costEstimated existed has no marker. The
// fleet page must infer it from "charged 0, priced above 0" and show
// the list price as the session's cost, not $0.
test("a legacy subscription ledger shows its list price, not $0", async ({ page }) => {
  const day = new Date().toISOString().slice(0, 10);
  await page.evaluate((d) => {
    (window as any).__e2eOverall = {
      sessionCount: 1,
      sessionsWithUsage: 1,
      calls: 55,
      promptTokens: 3271142,
      completionTokens: 17981,
      reasoningTokens: 4892,
      cacheReadTokens: 3194112,
      cacheWriteTokens: 0,
      totalTokens: 3289123,
      cost: 0,
      listCost: 0.06385086,
      hasCost: true,
      updatedAt: Math.floor(Date.now() / 1000),
      models: [
        {
          model: "deepseek-v4.1-flash",
          contextWindow: 200000,
          contextKnown: true,
          calls: 55,
          promptTokens: 3271142,
          completionTokens: 17981,
          reasoningTokens: 4892,
          cacheReadTokens: 3194112,
          cacheWriteTokens: 0,
          totalTokens: 3289123,
          cost: 0,
          listCost: 0.06385086,
          hasCost: true,
          fillPercent: 41.8,
        },
      ],
      sessions: [
        {
          sessionId: "sess-legacy-1",
          title: "legacy subscription",
          model: "deepseek-v4.1-flash",
          createdAt: Math.floor(Date.now() / 1000) - 86400,
          updatedAt: Math.floor(Date.now() / 1000),
          calls: 55,
          promptTokens: 3271142,
          completionTokens: 17981,
          reasoningTokens: 4892,
          cacheReadTokens: 3194112,
          cacheWriteTokens: 0,
          totalTokens: 3289123,
          cost: 0,
          listCost: 0.06385086,
          hasCost: true,
        },
      ],
      daily: [
        {
          date: d,
          calls: 55,
          promptTokens: 3271142,
          completionTokens: 17981,
          reasoningTokens: 4892,
          cacheReadTokens: 3194112,
          cacheWriteTokens: 0,
          totalTokens: 3289123,
          cost: 0,
          listCost: 0.06385086,
          hasCost: true,
        },
      ],
    };
  }, day);
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  await expect(body).toContainText("Est. total cost");
  await expect(body).toContainText("$0.06");
  await expect(body).not.toContainText("$0.00");
  await expect(body.locator("table")).toContainText("EST.");
});
