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

// The dashboard is the machine's ledger: every workspace's sessions,
// not just the open one. Pin the wire call's cwd being null.
test("the analytics rollup pulls every workspace", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.paladin.invoke.bind(w.paladin);
    w.__overallCalls = [];
    w.paladin = {
      ...w.paladin,
      invoke: async (cmd: string, args: any) => {
        if (cmd === "overallUsage") w.__overallCalls.push(args);
        return base(cmd, args);
      },
    };
  });
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  await expect(page.locator("#analyticsBody")).toContainText("Total tokens");
  const calls = await page.evaluate(() => (window as any).__overallCalls);
  expect(calls.length).toBeGreaterThan(0);
  for (const call of calls) {
    expect(call.cwd).toBeNull();
  }
});

test("the analytics model filter narrows the table", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  await expect(body).toContainText("kimi-k2.6");
  await body.locator("#analyticsModelFilter").selectOption("kimi-k2.6");
  await expect(body).toContainText("kimi-k2.6");
  // The sessions audit narrows with the filter; the model-pricing table
  // is fleet-wide by design and keeps showing every model.
  const sessionsAudit = body.locator(".glass-panel").filter({ hasText: "Recent sessions" });
  await expect(sessionsAudit).not.toContainText("beam-free");
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
  // The hero must show the list price, never a bare $0 for a subscription:
  // the guard is the KPI's own value, since legitimate sub-cent figures
  // (avg $/call) also begin with "$0.00".
  const hero = body.locator(".settings-stat").filter({ hasText: "Est. total cost" });
  await expect(hero).toContainText("$0.06");
  // Both model views must mark the list price: the sessions audit's
  // BILLED/EST. badge and the pricing table's EST tag.
  const sessionsAudit = body.locator(".glass-panel").filter({ hasText: "Recent sessions" });
  await expect(sessionsAudit).toContainText("EST.");
});
