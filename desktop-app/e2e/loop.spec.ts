// loop.spec.ts covers the uncapped agent loop's controls: steering a
// running turn, the per-session spend budget (chip, /budget, the
// settings default) and the notices a turn shows when it hits it.
import { test, expect, type Page } from "@playwright/test";

// hold makes every turn wait until the spec releases it, so steering
// and budget events land while the turn is running.
async function hold(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as any;
    w.__held = {};
    w.__e2eRunTurn = (params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__held[params.message] = { handlers, resolve };
        handlers.onDelta(`working on ${params.message}`);
      });
    w.__release = (message: string, reply = "") => w.__held[message].resolve({ sessionId: "", reply, stopped: false });
  });
}

async function startRunning(page: Page, text: string): Promise<void> {
  await hold(page);
  await page.fill("#promptInput", text);
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText(`working on ${text}`);
}

const calls = (page: Page, name: string) => page.evaluate((n) => ((window as any)[n] || []) as any[], name);

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the round-cap continue button is gone", async ({ page }) => {
  await expect(page.locator("#continueBtn")).toHaveCount(0);
});

test("steer sends a message into the running turn and shows it inline", async ({ page }) => {
  await expect(page.locator("#steerBtn")).toBeHidden();
  await startRunning(page, "big job");
  await expect(page.locator("#steerBtn")).toBeVisible();

  await page.fill("#promptInput", "also update the docs");
  await page.click("#steerBtn");
  await expect(page.locator("#promptInput")).toHaveValue("");

  const steers = await calls(page, "__e2eSteers");
  expect(steers).toHaveLength(1);
  expect(steers[0].message).toBe("also update the docs");
  expect(steers[0].sessionId).toBeTruthy();
  const submits = await calls(page, "__e2eSubmits");
  expect(submits).toHaveLength(1);

  const bubble = page.locator("#messages .steer-bubble");
  await expect(bubble).toContainText("also update the docs");
  // The steer lands inside the running turn, and later text streams below it.
  await page.evaluate(() => (window as any).__held["big job"].handlers.onDelta(" — and docs done"));
  const agent = page.locator("#messages .turn-row").nth(1);
  await expect(agent.locator(".steer-bubble")).toHaveCount(1);
  await expect(agent.locator(".agent-text").last()).toContainText("and docs done");

  await page.evaluate(() => (window as any).__release("big job", "all done"));
  await expect(page.locator("#steerBtn")).toBeHidden();
});

test("Cmd/Ctrl+Enter steers and Enter still queues", async ({ page }) => {
  await startRunning(page, "first");
  await page.fill("#promptInput", "steer me");
  await page.locator("#promptInput").press("ControlOrMeta+Enter");
  await expect(page.locator("#messages .steer-bubble")).toContainText("steer me");

  await page.fill("#promptInput", "queue me");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#messages .turn-queued")).toContainText("queue me");
  expect((await calls(page, "__e2eSteers")).map((s) => s.message)).toEqual(["steer me"]);
});

test("a steer that misses the turn is sent as a new message", async ({ page }) => {
  await startRunning(page, "racing");
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "steerChat") throw new Error("session has no running turn to steer");
      return base(cmd, args);
    };
  });
  await page.fill("#promptInput", "late steer");
  await page.click("#steerBtn");
  await expect(page.locator("#messages")).toContainText("late steer");
  const submits = await calls(page, "__e2eSubmits");
  expect(submits.map((s) => s.message)).toEqual(["racing", "late steer"]);
});

test("budget notices show in the turn", async ({ page }) => {
  await startRunning(page, "spendy");
  await page.evaluate(() =>
    (window as any).__held["spendy"].handlers.onBudget({ state: "unknownCost", spent: 0, limit: 1 }),
  );
  await expect(page.locator('#messages [data-notice="budget"]')).toContainText("can't be enforced");
  await page.evaluate(() =>
    (window as any).__held["spendy"].handlers.onBudget({ state: "reached", spent: 1.2, limit: 1 }),
  );
  const reached = page.locator('#messages [data-notice="budget"][data-state="reached"]');
  await expect(reached).toContainText("$1.20 of $1.00");
  await expect(reached).toContainText("summary");
});

test("the budget chip shows spend and sets the session budget", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eBudget = { defaultUsd: 2, sessions: {}, spentUsd: 0.4, hasCost: true };
  });
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");

  const chip = page.locator("#budgetChip");
  await expect(chip).toContainText("$0.40 / $2.00");
  await chip.click();
  const menu = page.locator("#budgetMenu");
  await expect(menu).toBeVisible();
  await expect(menu).toContainText("app default");
  await menu.locator("input").fill("5");
  await menu.locator('[data-budget="save"]').click();
  await expect(chip).toContainText("$0.40 / $5.00");

  await chip.click();
  await menu.locator('[data-budget="off"]').click();
  await expect(chip).toContainText("$0.40");
  await expect(chip).not.toContainText("/");

  await chip.click();
  await menu.locator('[data-budget="inherit"]').click();
  await expect(chip).toContainText("$0.40 / $2.00");

  const sets = await calls(page, "__e2eBudgetSets");
  expect(sets.map((s) => [s.scope, s.budgetUsd])).toEqual([
    ["session", 5],
    ["session", 0],
    ["session", null],
  ]);
});

test("the budget chip warns when the provider reports no cost", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eBudget = { defaultUsd: 1, sessions: {}, spentUsd: 0, hasCost: false };
  });
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  await expect(page.locator("#budgetChip")).toHaveAttribute("data-level", "unknown");
  await page.click("#budgetChip");
  await expect(page.locator("#budgetMenu")).toContainText("no cost");
});

test("/budget sets, turns off and resets the session budget", async ({ page }) => {
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");

  for (const line of ["/budget 3.5", "/budget off", "/budget default"]) {
    await page.fill("#promptInput", line);
    await page.locator("#promptInput").press("Escape");
    await page.locator("#promptInput").press("Enter");
  }
  await expect
    .poll(async () => (await calls(page, "__e2eBudgetSets")).map((s) => [s.scope, s.budgetUsd]))
    .toEqual([
      ["session", 3.5],
      ["session", 0],
      ["session", null],
    ]);
  expect(await calls(page, "__e2eSubmits")).toHaveLength(1);

  await page.fill("#promptInput", "/budget");
  await page.locator("#promptInput").press("Escape");
  await page.locator("#promptInput").press("Enter");
  await expect(page.locator("#budgetMenu")).toBeVisible();
});

test("settings set the default budget for every session", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  const input = page.locator("#settingsBudgetDefault");
  await expect(input).toBeVisible();
  await input.fill("10");
  await page.click("#settingsBudgetSaveBtn");
  await expect(page.locator("#toastText")).toContainText("Default budget");
  const sets = await calls(page, "__e2eBudgetSets");
  expect(sets.map((s) => [s.scope, s.budgetUsd])).toEqual([["global", 10]]);
  await input.fill("");
  await page.click("#settingsBudgetSaveBtn");
  await expect.poll(async () => (await calls(page, "__e2eBudgetSets")).length).toBe(2);
  expect((await calls(page, "__e2eBudgetSets"))[1].budgetUsd).toBeNull();
});
