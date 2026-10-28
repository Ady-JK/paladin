import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("sending a message opens a session and shows the reply", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  await expect(page.locator("#messages")).toContainText("hello agent");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello agent");

  // The empty-state hero vanishes so the first user turn docks at the top.
  await expect(page.locator("#messages .greet-hero")).toHaveCount(0);
  const first = page.locator("#messages .turn-row").first();
  await expect(first).toContainText("hello agent");

  const chats = await page.evaluate(() => (window as any).__e2eChats);
  expect(chats.length).toBeGreaterThanOrEqual(1);
  expect(chats[0].message).toBe("hello agent");
  expect(chats[0].resume).toBe(false);
});

test("agent markdown renders while the user bubble stays plain", async ({ page }) => {
  await page.evaluate(() => {
    const paladin = (window as any).paladin;
    const prev = paladin.chat.bind(paladin);
    paladin.chat = async (params: any, handlers: any) => {
      const out = await prev(params, handlers);
      return {
        ...out,
        reply:
          "# Ship it\n\nRun **tests** and *lint*:\n\n- unit\n- e2e\n\n> go fast\n\n[docs](https://example.com)",
      };
    };
  });
  await page.fill("#promptInput", "explain **boldly**");
  await page.click("#sendBtn");

  const rows = page.locator("#messages .turn-row");
  await expect(rows.nth(1).locator("h1")).toHaveText("Ship it");
  await expect(rows.nth(1).locator("strong").first()).toHaveText("tests");
  await expect(rows.nth(1).locator("em").first()).toHaveText("lint");
  await expect(rows.nth(1).locator("ul li")).toHaveCount(2);
  await expect(rows.nth(1).locator("blockquote")).toContainText("go fast");
  await expect(rows.nth(1).locator('a[data-ext="https://example.com"]')).toHaveText("docs");

  // The user bubble shows exactly what was typed: no Markdown parsing.
  await expect(rows.nth(0).locator("strong")).toHaveCount(0);
  await expect(rows.nth(0)).toContainText("explain **boldly**");
});

test("a paused turn shows continue and resumes without a new message", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatPaused = true;
    (window as any).__e2eChats = [];
  });
  await page.fill("#promptInput", "do a lot of work");
  await page.click("#sendBtn");

  await expect(page.locator("#continueBtn")).toBeVisible();
  await expect(page.locator("#toastText")).toContainText("Paused at the round cap");

  await page.click("#continueBtn");
  await expect(page.locator("#messages")).toContainText("Resumed and finished.");
  await expect(page.locator("#continueBtn")).toBeHidden();

  const chats = await page.evaluate(() => (window as any).__e2eChats);
  const resumed = chats.find((c: any) => c.resume);
  expect(resumed, "expected a resume call").toBeTruthy();
  expect(resumed.message).toBe("");
});
