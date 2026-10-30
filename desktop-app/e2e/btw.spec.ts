// btw.spec.ts covers /btw side chats: each opens as a tab in the side
// pane with its own composer, is saved per session, and can be reopened
// or deleted from the pane's history menu.
import { test, expect, type Page } from "@playwright/test";

const sideCalls = (page: Page) => page.evaluate(() => ((window as any).__e2eSideCalls || []) as any[]);

async function ask(page: Page, line: string): Promise<void> {
  await page.fill("#promptInput", line);
  await page.press("#promptInput", "Enter");
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("a bare /btw opens a new side chat ready for a question", async ({ page }) => {
  await ask(page, "start");
  await expect(page.locator("#messages")).toContainText("start");

  await ask(page, "/btw");
  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(page.locator("#promptInput")).toHaveValue("");
  await expect(pane.locator(".pane-tab")).toHaveCount(1);
  const input = pane.locator(".side-input");
  await expect(input).toBeFocused();
  expect(await sideCalls(page)).toEqual([]);

  // A second bare /btw reuses the empty chat rather than piling up tabs.
  await ask(page, "/btw");
  await expect(pane.locator(".pane-tab")).toHaveCount(1);
});

test("picking /btw from the slash menu opens a side chat at once", async ({ page }) => {
  await ask(page, "start");
  await page.fill("#promptInput", "/bt");
  await expect(page.locator('.slash-row.is-active[data-name="btw"]')).toBeVisible();
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#sidePane .side-input")).toBeFocused();
  await expect(page.locator("#promptInput")).toHaveValue("");
});

test("/btw opens a side chat tab while the turn keeps running", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__finish = () => resolve({ sessionId: "", reply: "", stopped: false });
        handlers.onDelta("still working");
      });
    w.__e2eSideQuestion = (args: any) =>
      new Promise((resolve) => {
        w.__answer = () =>
          resolve({ chatId: args.chatId || "chat-1", answer: "It is **refactoring** `auth.go`.", model: "gpt-x" });
      });
  });
  await ask(page, "refactor auth");
  await expect(page.locator("#messages")).toContainText("still working");

  await ask(page, "/btw what are you doing?");
  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(pane.locator(".pane-tab")).toHaveCount(1);
  await expect(pane.locator(".pane-tab.is-active")).toContainText("what are you doing?");
  const chat = pane.locator(".side-chat:visible");
  await expect(chat.locator('.side-msg[data-role="user"]')).toHaveText("what are you doing?");
  await expect(chat.locator('.side-msg[data-role="assistant"]')).toHaveAttribute("data-state", "pending");
  await expect(chat.locator(".side-send")).toBeDisabled();
  await expect(page.locator("#promptInput")).toHaveValue("");

  const call = (await sideCalls(page))[0];
  expect(call.question).toBe("what are you doing?");
  expect(call.chatId ?? null).toBeNull();
  expect(call.sessionId).toBeTruthy();

  await page.evaluate(() => (window as any).__answer());
  const answer = chat.locator('.side-msg[data-role="assistant"]');
  await expect(answer).toHaveAttribute("data-state", "done");
  await expect(answer.locator("strong")).toHaveText("refactoring");
  await expect(answer.locator(".side-model")).toHaveText("gpt-x");
  // A side chat is not a turn: nothing lands in the transcript.
  await expect(page.locator("#messages")).not.toContainText("what are you doing?");

  // Follow-ups go to the same chat from the tab's own composer.
  await chat.locator(".side-input").fill("and then?");
  await chat.locator(".side-input").press("Enter");
  await expect.poll(async () => (await sideCalls(page))[1]?.chatId).toBe("chat-1");
  expect((await sideCalls(page))[1].question).toBe("and then?");
  await page.evaluate(() => (window as any).__answer());
  await expect(chat.locator('.side-msg[data-role="assistant"]')).toHaveCount(2);

  await pane.locator(".pane-tab.is-active .pane-tab-close").click();
  await expect(pane.locator(".pane-tab")).toHaveCount(0);
  await page.evaluate(() => (window as any).__finish());
});

test("/btw shows the error inline and the chat can retry", async ({ page }) => {
  await ask(page, "hello");
  await expect(page.locator("#messages")).toContainText("hello");
  await page.evaluate(() => {
    (window as any).__e2eSideQuestion = () => Promise.reject(new Error("model said nothing"));
  });
  await ask(page, "/btw anything?");
  const chat = page.locator("#sidePane .side-chat:visible");
  await expect(chat.locator(".side-error")).toHaveText("model said nothing");
  await expect(chat.locator('.side-msg[data-role="user"]')).toHaveText("anything?");
  await page.evaluate(() => {
    (window as any).__e2eSideQuestion = () => ({ chatId: "c-ok", answer: "now fine", model: "m" });
  });
  await chat.locator(".side-input").fill("again?");
  await chat.locator(".side-send").click();
  await expect(chat.locator('.side-msg[data-role="assistant"]')).toContainText("now fine");
});

test("/btw needs a session", async ({ page }) => {
  await ask(page, "/btw");
  await expect(page.locator(".pane-tab")).toHaveCount(0);
  await ask(page, "/btw is anyone there?");
  await expect(page.locator(".pane-tab")).toHaveCount(0);
  expect(await sideCalls(page)).toHaveLength(0);
});

test("the history menu reopens and deletes saved side chats", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eSideChats = [
      { id: "c-old", title: "what is x", updatedAt: Date.now() - 60000, messageCount: 2 },
      { id: "c-gone", title: "drop me", updatedAt: Date.now() - 120000, messageCount: 2 },
    ];
    w.__e2eSideChatBodies = {
      "c-old": {
        id: "c-old",
        title: "what is x",
        createdAt: 1,
        updatedAt: 2,
        messages: [
          { role: "user", content: "what is x", createdAt: 1 },
          { role: "assistant", content: "x is **42**", model: "m1", createdAt: 2 },
        ],
      },
    };
  });
  await ask(page, "hello");
  await expect(page.locator("#messages")).toContainText("hello");

  // The pane opens on an empty side chat.
  await page.locator("#sidePaneBtn").click();
  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(pane.locator(".side-empty:visible")).toBeVisible();

  await pane.locator("#paneHistoryBtn").click();
  const menu = page.locator("#paneHistoryMenu");
  await expect(menu.locator(".pane-hist-item")).toHaveCount(2);
  await menu.locator('.pane-hist-item[data-chat="c-old"] .pane-hist-open').click();
  await expect(menu).toBeHidden();
  await expect(pane.locator(".pane-tab.is-active")).toContainText("what is x");
  const chat = pane.locator(".side-chat:visible");
  await expect(chat.locator('.side-msg[data-role="assistant"] strong')).toHaveText("42");

  // Reopening an open chat focuses its tab instead of duplicating it.
  await pane.locator("#paneHistoryBtn").click();
  await menu.locator('.pane-hist-item[data-chat="c-old"] .pane-hist-open').click();
  await expect(pane.locator('.pane-tab[data-chat="c-old"]')).toHaveCount(1);

  await pane.locator("#paneHistoryBtn").click();
  await menu.locator('.pane-hist-item[data-chat="c-gone"] .pane-hist-del').click();
  await expect(menu.locator(".pane-hist-item")).toHaveCount(1);
  expect(await page.evaluate(() => (window as any).__e2eSideDeletes.map((d: any) => d.chatId))).toEqual(["c-gone"]);

  await page.keyboard.press("Escape");
  await pane.locator("#paneCloseBtn").click();
  await expect(pane).toBeHidden();
});
