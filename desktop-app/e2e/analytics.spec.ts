import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the analytics pane shows the fleet dashboard", async ({ page }) => {
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsModal")).toBeVisible();
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  await expect(body).toContainText("Est. total cost");
  await expect(body).toContainText("Total tokens");
  await expect(body).toContainText("Sessions");
  await expect(body).toContainText("Prompt cache hit");
  await expect(body).toContainText("Input Tokens");
  await expect(body).toContainText("beam-free");
  await expect(body).toContainText("Recent sessions");
  await expect(body.locator("svg").first()).toBeVisible();
  await page.click("#settingsCloseBtn");
  await expect(page.locator("#settingsModal")).toBeHidden();
});

test("the analytics model filter narrows the table", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavAnalytics");
  const body = page.locator("#analyticsBody");
  await expect(body).toContainText("kimi-k2.6");
  await body.locator("#analyticsModelFilter").selectOption("kimi-k2.6");
  await expect(body).toContainText("kimi-k2.6");
  await expect(body.locator("table")).not.toContainText("beam-free");
  await page.click("#settingsCloseBtn");
});
