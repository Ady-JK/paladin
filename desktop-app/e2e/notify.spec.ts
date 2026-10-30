import { test, expect } from "@playwright/test";
import { showSidebar } from "./drawer";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("a notify reminder renders in turn order", async ({ page }) => {
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator("#messages")).toContainText("It printed hi.");

  const text = (await page.locator("#messages").textContent()) ?? "";

  const order = ["Starting the slow job.", "sleep 60", "Meanwhile done.", "notify job-1-1", "It printed hi."];
  let at = -1;
  for (const part of order) {
    const next = text.indexOf(part, at + 1);
    expect(next, `expected ${part} after position ${at} in:\n${text}`).toBeGreaterThan(at);
    at = next;
  }

  // No name labels: sides alone say who is talking, so the agent
  // name must not appear as a row header.
  await expect(page.locator("#messages").getByText("paladin", { exact: true })).toHaveCount(0);
});

test("expanding a tool row keeps the reading position", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 400 });
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator("#messages")).toContainText("notify job-1-1");

  // Seat the row in view first: the assertion below measures the
  // expand itself, not the click's scroll-into-view.
  const btn = page.locator("#messages button").first();
  await btn.scrollIntoViewIfNeeded();
  await page.waitForTimeout(100);
  const before = await page.locator("#chatView").evaluate((el) => el.scrollTop);
  await btn.click();
  await page.waitForTimeout(200);
  const after = await page.locator("#chatView").evaluate((el) => el.scrollTop);
  expect(Math.abs(after - before)).toBeLessThan(50);
});
