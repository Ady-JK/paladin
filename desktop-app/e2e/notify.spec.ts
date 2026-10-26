import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("a notify reminder renders in turn order", async ({ page }) => {
  await page.click('[data-session="sess-notify-1"]');

  const text = await page.locator("#messages").innerText();

  const order = [
    "Starting the slow job.",
    "sleep 60",
    "Meanwhile done.",
    "notify job-1-1",
    "It printed hi.",
  ];
  let at = -1;
  for (const part of order) {
    const next = text.indexOf(part, at + 1);
    expect(next, `expected ${part} after position ${at} in:\n${text}`).toBeGreaterThan(at);
    at = next;
  }

  await expect(
    page.locator("#messages").getByText("paladin", { exact: true }),
  ).toHaveCount(1);
});

test("expanding a tool row keeps the reading position", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 400 });
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator("#messages")).toContainText("notify job-1-1");

  await page.locator("#chatView").evaluate((el) => el.scrollTo(0, 0));
  await page.locator("#messages button").first().click();
  await page.waitForTimeout(200);
  const top = await page.locator("#chatView").evaluate((el) => el.scrollTop);
  expect(top).toBeLessThan(50);
});
