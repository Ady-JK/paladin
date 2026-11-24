import { test, expect } from "@playwright/test";

test("the composer has no auto-loaded context strip", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator("#contextBox")).toBeHidden();
  await expect(page.locator("#contextButton")).toBeVisible();
});

test("the working status remains visible without the context strip", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eRunTurn = async (_params: unknown, handlers: any) => {
      handlers.onTool({ id: "t1", name: "shell", arguments: "{}", result: "", status: "running" });
      await new Promise<void>((resolve) => ((window as any).__releaseTurn = resolve));
      return { reply: "done" };
    };
  });
  await page.goto("/");
  await page.fill("#promptInput", "run a command");
  await page.click("#sendBtn");
  await expect(page.locator("#workingBar")).toContainText("Running a command");
  await expect(page.locator("#workingBar")).toBeVisible();
  expect(await page.locator("#workingBar").evaluate((el) => Boolean(el.closest(".composer-float")))).toBe(true);
  const status = (await page.locator("#workingBar").boundingBox())!;
  // The thinking level sits quietly in the toolbar beside the model; the
  // working status keeps its own line above the text.
  expect(await page.locator("#thinkingMore").evaluate((el) => Boolean(el.closest(".composer-toolbar")))).toBe(true);
  expect(status.y).toBeLessThan((await page.locator("#promptInput").boundingBox())!.y);
  const composer = (await page.locator(".composer-float").boundingBox())!;
  expect(composer.x + composer.width - (status.x + status.width)).toBeLessThan(32);
  await expect(page.locator("#contextToggle")).toBeHidden();
  await page.evaluate(() => (window as any).__releaseTurn());
  await expect(page.locator("#workingBar")).toBeHidden();
});
