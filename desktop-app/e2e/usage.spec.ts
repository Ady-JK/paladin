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

// Every LLM call bills, so the pill must move during the turn, not only
// when it ends. This runs a turn that never finishes and emits the
// chat/usage signal twice with a different reading each time.
test("the usage pill follows every LLM call inside a turn", async ({ page }) => {
  await page.evaluate(() => {
    // Keep the turn running: no turn/done can refresh the pill, so any
    // change below comes from the per-call signal alone.
    (window as any).__e2eRunTurn = () => new Promise(() => undefined);
  });

  const reading = (calls: number, total: number, fill: number, cost: number) => ({
    sessionId: "sess-notify-1",
    model: "beam-free",
    contextWindow: 200000,
    contextKnown: true,
    calls,
    promptTokens: total - 500,
    completionTokens: 500,
    reasoningTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    totalTokens: total,
    lastInputTokens: total - 500,
    cost,
    listCost: cost * 2,
    hasCost: true,
    fillPercent: fill,
    models: [],
  });
  const emitUsage = (u: unknown) =>
    page.evaluate((usage) => {
      (window as any).__e2eUsage = usage;
      const turn = (window as any).__e2eHub.list()[0];
      (window as any).__e2eHub.emit({ event: "chat/usage", sessionId: turn.sessionId, turnId: turn.turnId });
    }, u);

  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  // First call of the turn.
  await emitUsage(reading(1, 40000, 20, 0.01));
  await expect(page.locator("#usageLabel")).toHaveText("20% · $0.01");

  // Second call, same running turn: the pill must follow the ledger again.
  await emitUsage(reading(2, 90000, 45, 0.03));
  await expect(page.locator("#usageLabel")).toHaveText("45% · $0.03");

  // The turn is still running; no turn/done has repainted anything.
  expect(await page.evaluate(() => (window as any).__e2eHub.list()[0].state)).toBe("running");
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

// A harness (Codex) reports its own context: the gauge is those tokens over
// the window it really runs with, and the hover shows that, not the last
// call's input.
test("the usage pill shows the context a harness reported", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-notify-1",
      model: "gpt-5.6-luna",
      contextWindow: 258400,
      contextKnown: true,
      calls: 2,
      promptTokens: 2600,
      completionTokens: 60,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 2660,
      lastInputTokens: 1600,
      contextTokens: 18300,
      cost: 0,
      listCost: 0,
      hasCost: false,
      fillPercent: 7.08,
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  await expect(page.locator("#usageLabel")).toHaveText("7.1%");
  const title = (await page.locator("#usagePill").getAttribute("title")) ?? "";
  expect(title).toContain("18,300 context tokens / 258,400");
  expect(title).toContain("7.1%");
  expect(title).not.toContain("1,600");
});

// A Codex plan has usage windows; the pill's hover lists how much of each is used.
test("the usage pill lists the plan's limits", async ({ page }) => {
  await page.evaluate(() => {
    const at = Math.floor(Date.now() / 1000);
    (window as any).__e2eUsage = {
      sessionId: "sess-notify-1",
      model: "gpt-5.6-luna",
      contextWindow: 258400,
      contextKnown: true,
      calls: 1,
      promptTokens: 18000,
      completionTokens: 5,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 18005,
      lastInputTokens: 18000,
      contextTokens: 18005,
      cost: 0,
      listCost: 0,
      hasCost: false,
      fillPercent: 7,
      limits: [
        { label: "Session", usedPercent: 42, resetsAt: at + 7500 },
        { label: "Weekly", usedPercent: 100 },
      ],
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await expect(page.locator("#usageLabel")).toHaveText("7%");
  const title = (await page.locator("#usagePill").getAttribute("title")) ?? "";
  expect(title).toMatch(/Session: 42% used, resets in 2h \d+m/);
  expect(title).toContain("Weekly: 100% used");
  expect(title).not.toMatch(/Weekly: 100% used, resets/);
});

// A Copilot Claude session: 1.0M of input per call is nearly all cache. The cache
// hit is the average of each call's own share (two calls, 99% and 80%), not the
// pooled share, and "Input tokens" is what was sent uncached, not the whole prompt.
test("the dashboard averages the cache hit per call and splits cached input out", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-copilot-1",
      model: "claude-sonnet-5.5",
      contextWindow: 936000,
      contextKnown: true,
      calls: 2,
      promptTokens: 1_000_000 + 100_000,
      completionTokens: 2_000,
      reasoningTokens: 0,
      cacheReadTokens: 990_000 + 80_000,
      cacheWriteTokens: 8_000,
      totalTokens: 1_102_000,
      lastInputTokens: 100_000,
      cacheHitSum: 0.99 + 0.8,
      cacheHitCalls: 2,
      cost: 0,
      listCost: 1.5,
      hasCost: true,
      costEstimated: true,
      fillPercent: 10.7,
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await page.click("#usagePill");
  const body = page.locator("#usageBody");
  const hero = body.locator("div", { hasText: /^Prompt cache hit/ }).first();
  await expect(body).toContainText("Prompt cache hit");
  await expect(body).toContainText("89.5%");
  // 1,100,000 sent, 1,070,000 read and 8,000 written: 22,000 went in uncached.
  await expect(body).toContainText("22,000");
  await expect(body).toContainText("Context fill");
  await expect(hero).toBeVisible();
});

test("a ledger with no per-call cache record shows no cache hit instead of a made-up one", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eUsage = {
      sessionId: "sess-plain-1",
      model: "beam-free",
      contextWindow: 200000,
      contextKnown: true,
      calls: 3,
      promptTokens: 3000,
      completionTokens: 60,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 3060,
      lastInputTokens: 1000,
      cost: 0,
      listCost: 0,
      hasCost: false,
      fillPercent: 0.5,
      models: [],
    };
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await page.click("#usagePill");
  await expect(page.locator("#usageBody")).toContainText("no cache data");
});
