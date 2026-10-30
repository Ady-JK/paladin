import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("/help lists the slash commands without calling the agent", async ({ page }) => {
  await page.fill("#promptInput", "/help");
  await page.click("#sendBtn");
  const chats = await page.evaluate(() => (window as any).__e2eChats || []);
  expect(chats).toHaveLength(0);
  await expect(page.locator("#messages")).toContainText("/new");
  await expect(page.locator("#messages")).toContainText("/help");
});

test("an unknown slash command toasts instead of sending", async ({ page }) => {
  await page.fill("#promptInput", "/frobnicate");
  await page.click("#sendBtn");
  const chats = await page.evaluate(() => (window as any).__e2eChats || []);
  expect(chats).toHaveLength(0);
  await expect(page.locator("#toastText")).toContainText("Unknown command");
});

test("/settings opens the settings modal", async ({ page }) => {
  await page.fill("#promptInput", "/settings");
  await page.click("#sendBtn");
  await expect(page.locator("#settingsModal")).toBeVisible();
  await page.click("#settingsCloseBtn");
});

test("/new resets the session and keeps follow-up text", async ({ page }) => {
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("hello agent");
  await page.fill("#promptInput", "/new fresh start");
  await page.click("#sendBtn");
  await expect(page.locator("#promptInput")).toHaveValue("fresh start");
});

test("@ popup completes to the OS-specific path", async ({ page }) => {
  await page.click("#promptInput");
  await page.fill("#promptInput", "@src/ma");
  const popup = page.locator("#mentionPopup");
  await expect(popup).toBeVisible();
  await expect(popup).toContainText("src/main.go");
  await page.keyboard.press("Enter");
  await expect(page.locator("#promptInput")).toHaveValue("/home/tester/project/src/main.go");
});

test("a hand-typed @path expands to the OS path on send", async ({ page }) => {
  await page.fill("#promptInput", "look at @src/main.go please");
  await page.click("#sendBtn");
  const chats = await page.evaluate(() => (window as any).__e2eChats || []);
  expect(chats).toHaveLength(1);
  expect(chats[0].message).toBe("look at /home/tester/project/src/main.go please");
  // The user turn keeps the original @ text.
  await expect(page.locator("#messages")).toContainText("@src/main.go");
});

test("@../ tags a file outside the workspace", async ({ page }) => {
  await page.click("#promptInput");
  await page.fill("#promptInput", "@../sib");
  const popup = page.locator("#mentionPopup");
  await expect(popup).toBeVisible();
  await expect(popup).toContainText("../sibling/outside.txt");
  await page.keyboard.press("Enter");
  await expect(page.locator("#promptInput")).toHaveValue("/home/tester/sibling/outside.txt");
});

test("the slash popup completes /he to /help with Tab", async ({ page }) => {
  await page.click("#promptInput");
  await page.fill("#promptInput", "/he");
  const popup = page.locator("#slashPopup");
  await expect(popup).toBeVisible();
  await expect(popup).toContainText("/help");
  await page.keyboard.press("Tab");
  await expect(page.locator("#promptInput")).toHaveValue("/help ");
});
