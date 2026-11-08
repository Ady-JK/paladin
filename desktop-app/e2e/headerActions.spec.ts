import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
});

test("header menu opens side chats and preserves layout controls", async ({ page }) => {
  const actions = page.locator("#titleBar #paneActions");
  await expect(actions).toBeVisible();
  await expect(page.locator("#panePluginsBtn")).toBeVisible();
  await expect(actions.locator("#paneLayoutBtn")).toBeVisible();
  await actions.locator("#paneNewChatBtn").click();
  await page.getByRole("menuitem", { name: "New side chat" }).click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
});

test("browser has one entry and new side chats and terminals share a menu", async ({ page }) => {
  await expect(page.locator("#paneBrowserBtn")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Browser", exact: true })).toHaveCount(1);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.getByRole("menuitem", { name: "New side chat" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "New terminal" })).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toContainText("No saved side chats");
  await page.getByRole("menuitem", { name: "New terminal" }).click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
});
