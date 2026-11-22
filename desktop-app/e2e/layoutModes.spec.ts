// layoutModes.spec.ts pins the app's two views. Agent mode is the conversation
// (with or without the side pane); Browser mode gives the pages the whole
// window and its own bar. The old split/tabs layout toggle is gone.
import { expect, test } from "@playwright/test";

import { hidePane, showPane } from "./modes";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
});

test("a fresh start opens in Agent mode with the pane closed", async ({ page }) => {
  await expect(page.locator("#modeAgentBtn")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#browserView")).toBeHidden();
  // The conversation is the whole card until something opens the pane.
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane")).toBeHidden();
});

test("the pane narrows the conversation and gives it back when closed", async ({ page }) => {
  const chat = page.locator("#agentMain");
  const wide = (await chat.boundingBox())!.width;
  await showPane(page);
  const narrow = (await chat.boundingBox())!.width;
  expect(narrow).toBeLessThan(wide - 100);
  await hidePane(page);
  await expect.poll(async () => Math.round((await chat.boundingBox())!.width)).toBeGreaterThan(narrow + 100);
});

test("a side chat keeps working across open and close", async ({ page }) => {
  await showPane(page);
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();
  const splitWidth = (await page.locator("#sidePane").boundingBox())!.width;
  const fullWidth = (await page.locator("#agentSplit").boundingBox())!.width;
  expect(splitWidth).toBeLessThan(fullWidth * 0.7);

  await hidePane(page);
  await showPane(page);
  // The tab outlived the pane being closed: it is a tab, not the pane's state.
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();
});

test("Browser mode owns the window: no readings, no pane, its own strip", async ({ page }) => {
  await page.locator("#modeBrowserBtn").click();
  await expect(page.locator("#browserView")).toBeVisible();
  await expect(page.locator("#agentView")).toBeHidden();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#sidePane")).toBeHidden();
  // The agent header gives way to the browser's own bar.
  await expect(page.locator("#titleBar")).toBeHidden();
  await expect(page.locator("#browserBar")).toBeVisible();

  await page.locator("#modeAgentBtn").click();
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#titleBar")).toBeVisible();
  await expect(page.locator("#browserView")).toBeHidden();
});

test("the mode survives a reload", async ({ page }) => {
  await page.locator("#modeBrowserBtn").click();
  await expect(page.locator("#browserView")).toBeVisible();
  await page.reload();
  await expect(page.locator("#modeBrowserBtn")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#browserView")).toBeVisible();
});

test("the saved-side-chat menu still works from the header +", async ({ page }) => {
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#paneHistoryMenu")).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toContainText("New side chat");
  await expect(page.locator("#paneHistoryMenu")).toContainText("No saved side chats");
});
