import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
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

// A subscription plan (OpenCode Go) charges nothing and reports the
// tokens' list price beside the zero. Showing the charge alone read as
// "$0" after millions of tokens, which is the bug this covers.
test("a subscription plan shows its list-price spend, not $0", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-sub-1",
      model: "deepseek-v4.1-flash",
      contextWindow: 200000,
      contextKnown: true,
      calls: 164,
      promptTokens: 18042705,
      completionTokens: 69216,
      reasoningTokens: 32130,
      cacheReadTokens: 17876096,
      cacheWriteTokens: 0,
      totalTokens: 18111921,
      cost: 0,
      listCost: 0.16143655,
      hasCost: true,
      costEstimated: true,
      lastInputTokens: 158495,
      fillPercent: 79.2,
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  const pill = page.locator("#usagePill");
  await expect(pill).toBeVisible();
  // The list price, not the $0 charge.
  await expect(page.locator("#usageLabel")).toContainText("$0.16");
  const title = await pill.getAttribute("title");
  expect(title).toContain("est. value");

  // And the dashboard agrees. The stub's overallUsage is a billed fleet,
  // so point it at the subscription shape too before opening the pane.
  // Arrays are populated like the real rollup: the pane renders models,
  // sessions and days, and an empty fleet is not what this covers.
  const day = new Date().toISOString().slice(0, 10);
  await page.evaluate((d) => {
    (window as any).__e2eOverall = {
      sessionCount: 1,
      sessionsWithUsage: 1,
      calls: 164,
      promptTokens: 18042705,
      completionTokens: 69216,
      reasoningTokens: 32130,
      cacheReadTokens: 17876096,
      cacheWriteTokens: 0,
      totalTokens: 18111921,
      cost: 0,
      listCost: 0.16143655,
      hasCost: true,
      costEstimated: true,
      updatedAt: Math.floor(Date.now() / 1000),
      models: [
        {
          model: "deepseek-v4.1-flash",
          contextWindow: 200000,
          contextKnown: true,
          calls: 164,
          promptTokens: 18042705,
          completionTokens: 69216,
          reasoningTokens: 32130,
          cacheReadTokens: 17876096,
          cacheWriteTokens: 0,
          totalTokens: 18111921,
          cost: 0,
          listCost: 0.16143655,
          hasCost: true,
          costEstimated: true,
          fillPercent: 79.2,
        },
      ],
      sessions: [
        {
          sessionId: "sess-sub-1",
          title: "subscription session",
          model: "deepseek-v4.1-flash",
          createdAt: Math.floor(Date.now() / 1000) - 86400,
          updatedAt: Math.floor(Date.now() / 1000),
          calls: 164,
          promptTokens: 18042705,
          completionTokens: 69216,
          reasoningTokens: 32130,
          cacheReadTokens: 17876096,
          cacheWriteTokens: 0,
          totalTokens: 18111921,
          cost: 0,
          listCost: 0.16143655,
          hasCost: true,
          costEstimated: true,
        },
      ],
      daily: [
        {
          date: d,
          calls: 164,
          promptTokens: 18042705,
          completionTokens: 69216,
          reasoningTokens: 32130,
          cacheReadTokens: 17876096,
          cacheWriteTokens: 0,
          totalTokens: 18111921,
          cost: 0,
          listCost: 0.16143655,
          hasCost: true,
          costEstimated: true,
        },
      ],
    };
  }, day);
  await page.click("#usagePill");
  // The pill opens the usage dashboard (#usageBody); #analyticsBody is
  // the Settings → Analytics pane, which the next test covers.
  const body = page.locator("#usageBody");
  await expect(body).toContainText("Est. total cost");
  await expect(body).toContainText("$0.16");
  // The removed "saved vs list" line must not come back.
  await expect(body).not.toContainText("vs list");
});

// Ledgers written before costEstimated existed have no marker. Their
// numbers alone — charged 0 beside a nonzero list price — must still
// show the price of the session instead of $0.
test("a session recorded before the estimate marker still shows its price", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-legacy-1",
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
      lastInputTokens: 83636,
      fillPercent: 41.8,
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  const pill = page.locator("#usagePill");
  await expect(pill).toBeVisible();
  await expect(page.locator("#usageLabel")).toContainText("$0.06");
  expect(await pill.getAttribute("title")).toContain("est. value");

  await page.click("#usagePill");
  const body = page.locator("#usageBody");
  await expect(body).toContainText("Est. total cost");
  await expect(body).toContainText("$0.06");
  await expect(body).not.toContainText("$0.00");
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
