// thinking.spec.ts covers the per-model thinking level (the composer
// chip and its slider card) and the model's streamed thinking, live and
// from history.
import { test, expect, type Page } from "@playwright/test";

async function hold(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as any;
    w.__held = {};
    w.__e2eRunTurn = (params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__held[params.message] = { handlers, resolve };
      });
  });
}

const sets = (page: Page) => page.evaluate(() => ((window as any).__e2eThinkingSets || []) as any[]);

async function pickModel(page: Page, query: string): Promise<void> {
  await page.click("#modelSelectBtn");
  await page.fill("#modelSearch", query);
  await page.waitForTimeout(250);
  await page.press("#modelSearch", "Enter");
  await expect(page.locator("#modelMenu")).toBeHidden();
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the thinking chip opens a slider card that sets the model's level", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  const chip = page.locator("#thinkingChip");
  await expect(chip).toBeVisible();
  await expect(chip).toContainText("Default");

  await chip.click();
  const card = page.locator("#thinkingMenu");
  await expect(card).toBeVisible();
  await expect(card.locator(".think-level")).toHaveText("Default");
  await expect(card.locator(".think-model")).toContainText("claude-3-opus");
  const slider = card.locator('input[type="range"]');
  await expect(slider).toHaveAttribute("aria-valuetext", "Default");
  await expect(slider).toHaveAttribute("max", "6");

  await slider.focus();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect(card.locator(".think-level")).toHaveText("Low");
  await expect(chip).toContainText("Low");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("low");
  const last = (await sets(page)).at(-1);
  expect(last.model).toBe("anthropic/claude-3-opus");

  await page.keyboard.press("End");
  await expect(card.locator(".think-level")).toHaveText("Max");
  await expect(card).toHaveAttribute("data-level", "max");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("max");

  await card.locator(".think-reset").click();
  await expect(card.locator(".think-level")).toHaveText("Default");
  await expect(chip).toContainText("Default");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("");

  await page.keyboard.press("Escape");
  await expect(card).toBeHidden();
});

test("the card's model name opens the model picker", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  await page.click("#thinkingChip");
  await page.click("#thinkingMenu .think-model");
  await expect(page.locator("#thinkingMenu")).toBeHidden();
  await expect(page.locator("#modelMenu")).toBeVisible();
});

test("the chip hides for a model without adjustable thinking and remembers levels per model", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  await page.click("#thinkingChip");
  await page.locator('#thinkingMenu input[type="range"]').focus();
  await page.keyboard.press("End");
  await expect(page.locator("#thinkingChip")).toContainText("Max");
  await page.keyboard.press("Escape");

  await pickModel(page, "gpt-4o");
  await expect(page.locator("#thinkingChip")).toBeHidden();

  await pickModel(page, "claude-3-opus");
  await expect(page.locator("#thinkingChip")).toContainText("Max");
});

test("thinking streams in a block that folds away once the answer starts", async ({ page }) => {
  await hold(page);
  await page.fill("#promptInput", "ponder");
  await page.click("#sendBtn");
  await expect.poll(() => page.evaluate(() => !!(window as any).__held?.ponder)).toBe(true);
  const h = (fn: string, arg: string) =>
    page.evaluate(([f, a]) => (window as any).__held.ponder.handlers[f](a), [fn, arg]);

  await h("onThinkingDropped", "max");
  await expect(page.locator('#messages [data-notice="thinking"]')).toContainText("Max");

  await h("onReasoning", "Let me look ");
  await h("onReasoning", "at the files.");
  const block = page.locator("#messages .think-block").first();
  await expect(block).toHaveAttribute("data-state", "streaming");
  await expect(block).toHaveAttribute("open", "");
  await expect(block.locator(".think-body")).toHaveText("Let me look at the files.");
  await expect(block.locator(".think-title")).toHaveText("Thinking");

  await h("onDelta", "Here is the answer.");
  await expect(block).toHaveAttribute("data-state", "done");
  await expect(block).not.toHaveAttribute("open", "");
  await expect(block.locator(".think-title")).toHaveText("Thought");
  await expect(page.locator("#messages")).toContainText("Here is the answer.");

  // A new round's thinking gets its own block.
  await h("onReasoning", "One more check.");
  await expect(page.locator("#messages .think-block")).toHaveCount(2);
  await page.evaluate(() => (window as any).__held.ponder.resolve({ sessionId: "", reply: "", stopped: false }));
  await expect(page.locator("#messages .think-block").nth(1)).toHaveAttribute("data-state", "done");
});

test("saved thinking shows as a folded block from history", async ({ page }) => {
  await page.evaluate(() => {
    const at = Math.floor(Date.now() / 1000);
    (window as any).__e2eHistory = [
      { role: "user", content: "why?", createdAt: at, index: 0 },
      { role: "assistant", kind: "thinking", content: "Weighing the **options**.", createdAt: at },
      { role: "assistant", content: "Because.", createdAt: at },
    ];
  });
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator("#messages")).toContainText("Because.");
  const block = page.locator("#messages .think-block");
  await expect(block).toHaveCount(1);
  await expect(block).toHaveAttribute("data-state", "done");
  await expect(block).not.toHaveAttribute("open", "");
  await block.locator("summary").click();
  await expect(block.locator(".think-body")).toContainText("Weighing the options.");
  const text = (await page.locator("#messages").textContent()) ?? "";
  expect(text.indexOf("Weighing")).toBeLessThan(text.indexOf("Because."));
});
