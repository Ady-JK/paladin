import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

// A submit dropped before it started (the user stopped a queued turn)
// is an intentional stop, not a failure: no red error row, the draft
// stays so the message can be resent, and the pill reads stopped.
test("a queued turn stopped before starting shows no error", async ({ page }) => {
  await page.evaluate(() => {
    const base = (window as any).__paladinBaseInvoke;
    (window as any).paladin.invoke = (method: string, params: any) =>
      method === "submitChat" ? Promise.reject(new Error("stopped before it started")) : base(method, params);
  });
  await page.fill("#promptInput", "do it later");
  await page.click("#sendBtn");
  await expect(page.locator("#daemonLabel")).toHaveText("stopped");
  await expect(page.locator("#messages")).not.toContainText("stopped before it started");
  await expect(page.locator("#promptInput")).toHaveValue("do it later");
});
