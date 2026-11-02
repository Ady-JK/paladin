// thinking.spec.ts covers the per-model thinking level (the composer
// chip and its slider card) and the model's streamed thinking, live and
// from history.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

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
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("the toggle above the composer shows the current level and opens the slider", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  const toggle = page.locator("#thinkingMore");
  await expect(toggle).toBeVisible();
  await expect(toggle).toContainText("Thinking: Default");

  await toggle.click();
  const card = page.locator("#thinkingMenu");
  await expect(card).toBeVisible();
  await expect(card.locator(".think-level")).toHaveText("Default");
  await expect(card.locator(".think-model")).toContainText("claude-3-opus");
  const slider = card.locator('input[type="range"]');
  await expect(slider).toHaveAttribute("aria-valuetext", "Default");
  // Five levels plus the provider default.
  await expect(slider).toHaveAttribute("max", "5");

  await slider.focus();
  // Stop 0 is the provider default, so two steps in is Medium.
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect(card.locator(".think-level")).toHaveText("Medium");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("medium");
  // The toggle mirrors the card's choice.
  await expect(toggle).toContainText("Thinking: Medium");

  await page.keyboard.press("End");
  await expect(card).toHaveAttribute("data-level", "max");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("max");

  await card.locator(".think-reset").click();
  await expect(card.locator(".think-level")).toHaveText("Default");
  await expect(toggle).toContainText("Thinking: Default");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("");

  await page.keyboard.press("Escape");
  await expect(card).toBeHidden();
});

test("the toggle says n/a for a model with no adjustable thinking", async ({ page }) => {
  await pickModel(page, "gpt-4o");
  const toggle = page.locator("#thinkingMore");
  // Visible and disabled rather than hidden: a hidden control reads as
  // broken, and the composer layout stays stable.
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeDisabled();
  await expect(toggle).toContainText("Thinking: n/a");
  await toggle.click({ force: true });
  await expect(page.locator("#thinkingMenu")).toBeHidden();
});

test("levels are remembered per model", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  await page.click("#thinkingMore");
  const slider = page.locator('#thinkingMenu input[type="range"]');
  await slider.focus();
  await page.keyboard.press("End");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("max");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: Max");
  await page.keyboard.press("Escape");

  await pickModel(page, "gpt-4o");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: n/a");

  await pickModel(page, "claude-3-opus");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: Max");
});

test("the card's model name opens the model picker", async ({ page }) => {
  await pickModel(page, "claude-3-opus");
  await page.click("#thinkingMore");
  await page.click("#thinkingMenu .think-model");
  await expect(page.locator("#thinkingMenu")).toBeHidden();
  await expect(page.locator("#modelMenu")).toBeVisible();
});

test("thinking levels appear after a catalog refresh, without switching models", async ({ page }) => {
  // The pick is a model the first listing describes with no levels to
  // pick. The second listing (a refresh) teaches the app three.
  await pickModel(page, "claude-fable");
  await expect(page.locator(".think-inline")).toBeVisible();
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: n/a");

  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.locator('[data-refresh="Opencode Go"]').click();
  await page.click("#settingsCloseBtn");
  // Same model id, new metadata: the control must follow the catalog,
  // not wait for the user to switch models.
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: Default");
  await expect(page.locator("#thinkingMore")).toBeEnabled();
  await page.click("#thinkingMore");
  await expect(page.locator("#thinkingMenu")).toBeVisible();
  await page.keyboard.press("Escape");
});

test("/thinking opens the options and /thinking <level> sets one", async ({ page }) => {
  await pickModel(page, "claude-3-opus");

  await page.fill("#promptInput", "/thinking");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#thinkingMenu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#thinkingMenu")).toBeHidden();

  // The toggle also opens the card for pointer users.
  await page.click("#thinkingMore");
  await expect(page.locator("#thinkingMenu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#thinkingMenu")).toBeHidden();

  await page.fill("#promptInput", "/thinking high");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: High");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("high");
  // A local command never becomes a turn.
  const chats = await page.evaluate(() => ((window as any).__e2eChats || []).length);
  expect(chats).toBe(0);

  // A level the model does not take is refused, naming what it does take.
  await page.fill("#promptInput", "/thinking minimal");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#toast")).toContainText("claude-3-opus: Low, Medium, High, Extra High, Max");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: High");

  // And `default` clears it back to the provider's own.
  await page.fill("#promptInput", "/thinking default");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#thinkingMore")).toContainText("Thinking: Default");
  await expect.poll(async () => (await sets(page)).at(-1)?.level).toBe("");
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
  await showSidebar(page);
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
