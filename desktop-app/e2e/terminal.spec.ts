import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
});

test("bang runs a workspace command then starts a turn with its result", async ({ page }) => {
  await page.locator("#promptInput").fill("! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalRuns?.length ?? 0)).toBe(1);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(1);
  const result = await page.evaluate(() => ({
    run: (window as any).__e2eTerminalRuns[0],
    submit: (window as any).__e2eSubmits[0],
  }));
  expect(result.run.command).toBe("pwd");
  expect(result.run.cwd).toBe("/home/tester/project");
  expect(result.submit.message).toContain("/home/tester/project");
  expect(result.submit.message).toContain("pwd");
});

test("double bang shows output without adding agent history", async ({ page }) => {
  await page.locator("#promptInput").fill("!! pwd");
  await page.locator("#promptInput").press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalRuns?.length ?? 0)).toBe(1);
  await expect(page.locator(".pane-terminal .xterm-rows")).toContainText("/home/tester/project");
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(0);
});

test("terminal button opens a full terminal tab beside chat", async ({ page }) => {
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
  await page.locator("#sidePaneBtn").click();
  await page.locator("#paneTerminalBtn").click();
  await expect(page.locator(".pane-terminal")).toBeVisible();
  await expect(page.locator(".xterm")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalWrites?.length ?? 0)).toBeGreaterThan(0);
});
