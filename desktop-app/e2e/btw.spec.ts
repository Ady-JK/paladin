// btw.spec.ts covers /btw: a side question answered in a dismissible
// card, including while a turn is still running.
import { test, expect, type Page } from "@playwright/test";

const sideCalls = (page: Page) => page.evaluate(() => ((window as any).__e2eSideCalls || []) as any[]);

async function ask(page: Page, line: string): Promise<void> {
  await page.fill("#promptInput", line);
  await page.press("#promptInput", "Enter");
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("/btw answers in a card while the turn keeps running", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eRunTurn = (params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__finish = () => resolve({ sessionId: "", reply: "", stopped: false });
        handlers.onDelta("still working");
      });
    w.__e2eSideQuestion = (args: any) =>
      new Promise((resolve) => {
        w.__answer = () => resolve({ answer: "It is **refactoring** `auth.go`.", model: "gpt-x" });
      });
  });
  await ask(page, "refactor auth");
  await expect(page.locator("#messages")).toContainText("still working");

  await ask(page, "/btw what are you doing?");
  const card = page.locator(".btw-card");
  await expect(card).toHaveAttribute("data-state", "pending");
  await expect(card.locator(".btw-question")).toHaveText("what are you doing?");
  await expect(page.locator("#promptInput")).toHaveValue("");

  const call = (await sideCalls(page))[0];
  expect(call.question).toBe("what are you doing?");
  expect(call.sessionId).toBeTruthy();

  await page.evaluate(() => (window as any).__answer());
  await expect(card).toHaveAttribute("data-state", "done");
  await expect(card.locator(".btw-answer strong")).toHaveText("refactoring");
  await expect(card.locator(".btw-model")).toHaveText("gpt-x");
  // The side question is not a turn: no user bubble for it.
  await expect(page.locator("#messages")).not.toContainText("/btw");

  await card.locator(".btw-close").click();
  await expect(page.locator(".btw-card")).toHaveCount(0);
  await page.evaluate(() => (window as any).__finish());
});

test("/btw shows the error in the card", async ({ page }) => {
  await ask(page, "hello");
  await expect(page.locator("#messages")).toContainText("hello");
  await page.evaluate(() => {
    (window as any).__e2eSideQuestion = () => Promise.reject(new Error("model said nothing"));
  });
  await ask(page, "/btw anything?");
  await expect(page.locator(".btw-card")).toHaveAttribute("data-state", "error");
  await expect(page.locator(".btw-error")).toHaveText("model said nothing");
});

test("/btw needs a question and a session", async ({ page }) => {
  await ask(page, "/btw");
  await expect(page.locator("#promptInput")).toHaveValue("/btw ");
  await expect(page.locator(".btw-card")).toHaveCount(0);
  await ask(page, "/btw is anyone there?");
  await expect(page.locator(".btw-card")).toHaveCount(0);
  expect(await sideCalls(page)).toHaveLength(0);
});
