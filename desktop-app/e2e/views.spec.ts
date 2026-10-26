import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("chat tab swaps the sidebar and the main view", async ({ page }) => {
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#externalView")).toBeHidden();

  await page.click("#chatTab");

  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#searchWrap")).toBeHidden();
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator('[data-pick="gemini"]')).toBeVisible();

  await expect(page.locator("#agentView")).toBeHidden();
  await expect(page.locator("#externalView")).toBeVisible();

  await page.click('[data-pick="gemini"]');
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");

  await page.click('[data-pick="chatgpt"]');
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");

  await expect(page.locator("#pageReloadBtn")).toBeVisible();
  await expect(page.locator('[data-open="gemini"]')).toBeVisible();
  await expect(page.locator('[data-open="chatgpt"]')).toBeVisible();

  await page.click("#agentTab");
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#externalView")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#pageReloadBtn")).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("Agent Chat");
});

test("window buttons fill the top-right corner", async ({ page }) => {
  await expect(page.locator("#windowMinimizeBtn")).toBeVisible();
  await expect(page.locator("#windowCloseBtn")).toBeVisible();
  const header = await page.locator("#titleBar").boundingBox();
  const minimize = await page.locator("#windowMinimizeBtn").boundingBox();
  const close = await page.locator("#windowCloseBtn").boundingBox();
  expect(header).toBeTruthy();
  expect(minimize).toBeTruthy();
  expect(close).toBeTruthy();
  expect(Math.abs(header!.y - minimize!.y)).toBeLessThanOrEqual(1);
  expect(
    Math.abs(header!.x + header!.width - (close!.x + close!.width)),
  ).toBeLessThanOrEqual(1);
  expect(
    Math.abs(header!.y + header!.height - (close!.y + close!.height)),
  ).toBeLessThanOrEqual(2);
  expect(Math.abs(minimize!.height - close!.height)).toBeLessThanOrEqual(1);
});

// Verifies the picked chat side survives a reload.
// It checks the external view and title persist.
test("the picked side survives a reload", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="gemini"]');
  await page.reload();
  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("sidebar stays fixed open in chat mode", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="gemini"]');

  await expect(page.locator("#sidebarToggleBtn")).toBeDisabled();
  // Clicking the locked toggle is a no-op: the page list stays put.
  await page.click("#sidebarToggleBtn", { force: true });
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#searchWrap")).toBeHidden();
  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("collapsing in agent keeps the sessions and hides the chats", async ({ page }) => {
  await page.click("#sidebarToggleBtn");
  await expect(page.locator("#sessionList")).toBeHidden();
  await page.click("#sidebarToggleBtn");
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#searchWrap")).toBeVisible();
  await expect(page.locator("#chatList")).toBeHidden();
  await expect(page.locator("#agentView")).toBeVisible();
});

test("collapse, expand, then switch sides stays clean", async ({ page }) => {
  await page.click("#sidebarToggleBtn");
  await expect(page.locator("#sessionList")).toBeHidden();
  await page.click("#sidebarToggleBtn");
  await expect(page.locator("#sessionList")).toBeVisible();

  await page.click("#chatTab");
  await page.click('[data-pick="chatgpt"]');
  await expect(page.locator("#sidebarToggleBtn")).toBeDisabled();

  await page.click("#agentTab");
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#chatList")).toBeHidden();

  await page.click("#chatTab");
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");
});

test("rapid tab switching ends in one clean side", async ({ page }) => {  await page.click("#chatTab");
  await page.click("#agentTab");
  await page.click("#chatTab");
  await page.click('[data-pick="chatgpt"]');
  await page.click("#agentTab");
  await page.click("#chatTab");

  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#agentView")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#searchWrap")).toBeHidden();
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");
});

test("chat mode never moves the page", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="gemini"]');
  // Let the initial show settle first: the show itself moves once.
  await page.waitForTimeout(900);

  await page.evaluate(() => {
    window.__e2eMoves = 0;
  });
  // The toggle is locked in chat mode: fixed sidebar, zero moves.
  await page.click("#sidebarToggleBtn", { force: true });
  await page.waitForTimeout(900);
  const moves = await page.evaluate(() => window.__e2eMoves || 0);
  expect(moves).toBe(0);
});

test("a site can be added, kept, and removed", async ({ page }) => {
  await page.click("#chatTab");

  await page.click("#newSessionBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await page.fill("#siteName", "Docs");
  await page.fill("#siteUrl", "example.com");
  await page.click("#siteAddBtn");

  const entry = page.locator('[data-pick="site:Docs"]');
  await expect(entry).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Docs");
  await expect(entry).toContainText("example.com");

  await page.reload();
  await expect(page.locator('[data-pick="site:Docs"]')).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Docs");

  await page.locator('[data-del="site:Docs"]').click();
  await expect(page.locator('[data-pick="site:Docs"]')).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("bad site URLs are rejected", async ({ page }) => {
  await page.click("#chatTab");
  await page.click("#newSessionBtn");

  await page.fill("#siteName", "Evil");
  await page.fill("#siteUrl", "javascript:alert(1)");
  await page.click("#siteAddBtn");

  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-pick="site:Evil"]')).toBeHidden();

  await page.fill("#siteName", "Gemini");
  await page.fill("#siteUrl", "https://gemini.google.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator("#chatEntries")).not.toContainText("site:");

  await page.fill("#siteName", "Insecure");
  await page.fill("#siteUrl", "http://example.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-pick="site:Insecure"]')).toBeHidden();

  await page.keyboard.press("Escape");
  await expect(page.locator("#siteModal")).toBeHidden();
});

test("the action button reads New Site in chat, New Session in agent", async ({ page }) => {
  await expect(page.locator("#newSessionBtnLabel")).toHaveText("New Session");

  await page.click("#chatTab");
  await expect(page.locator("#newSessionBtnLabel")).toHaveText("New Site");

  await page.click("#newSessionBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await page.keyboard.press("Escape");

  await page.click("#agentTab");
  await expect(page.locator("#newSessionBtnLabel")).toHaveText("New Session");

  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionModal")).toBeVisible();
});
