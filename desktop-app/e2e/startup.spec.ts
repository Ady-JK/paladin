import { expect, test } from "@playwright/test";

test("startup waits for initialization before revealing the app", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const api = (window as any).paladin;
    const invoke = api.invoke.bind(api);
    api.invoke = (method: string, args: unknown) => {
      if (method === "getWorkspace") {
        return new Promise((resolve) => {
          (window as any).__finishStartup = () => resolve("");
        });
      }
      return invoke(method, args);
    };
  });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Loading Paladin" })).toBeVisible();
  await expect(page.locator("#appShell")).toHaveAttribute("inert", "");
  await page.waitForFunction(() => Boolean((window as any).__finishStartup));
  await page.evaluate(() => (window as any).__finishStartup());
  await expect(page.getByRole("status", { name: "Loading Paladin" })).toHaveCount(0);
  await expect(page.locator("#appShell")).not.toHaveAttribute("inert", "");
  await expect(page.locator("#promptInput")).toBeFocused();
});

test("a stalled connection cannot trap the startup screen", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const api = (window as any).paladin;
    const invoke = api.invoke.bind(api);
    api.invoke = (method: string, args: unknown) =>
      method === "getWorkspace" ? new Promise(() => {}) : invoke(method, args);
  });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Loading Paladin" })).toHaveCount(0, { timeout: 4000 });
  await expect(page.locator("#promptInput")).toBeVisible();
});
