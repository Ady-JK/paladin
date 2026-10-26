import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/tauri-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("sending a message opens a session and shows the reply", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");

  await expect(page.locator("#messages")).toContainText("hello agent");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello agent");

  const chats = await page.evaluate(() => (window as any).__e2eChats);
  expect(chats.length).toBeGreaterThanOrEqual(1);
  expect(chats[0].message).toBe("hello agent");
  expect(chats[0].resume).toBe(false);
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
