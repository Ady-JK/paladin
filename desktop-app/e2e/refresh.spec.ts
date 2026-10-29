import { test, expect } from "@playwright/test";

// Provider catalogs are only fetched when the user asks: never on
// startup, never on add/edit, and a row's refresh contacts one provider.
test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRefreshCalls = [];
    const prev = w.paladin.invoke;
    w.paladin.invoke = async (cmd: string, args: any) => {
      if (cmd === "refresh_providers") {
        w.__e2eRefreshCalls.push(args ?? {});
        if (args && args.name) {
          return { name: args.name, count: 3, models: ["a", "b", "c"] };
        }
        return true;
      }
      return prev(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

const refreshCalls = (page: any) => page.evaluate(() => (window as any).__e2eRefreshCalls);

test("startup never refreshes provider catalogs", async ({ page }) => {
  await page.waitForTimeout(500);
  expect(await refreshCalls(page)).toEqual([]);
});

test("saving a provider does not fetch models", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.click("#settingsAddProviderBtn");
  await page.fill("#providerCardName", "together");
  await page.fill("#providerCardUrl", "https://api.together.xyz/v1");
  await page.fill("#providerCardKey", "sk-test");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await page.waitForTimeout(300);
  expect(await refreshCalls(page)).toEqual([]);
});

test("row refresh fetches only that provider", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const btn = page.locator('[data-refresh="openrouter"]');
  await expect(btn).toBeVisible();
  await btn.click();
  await expect.poll(() => refreshCalls(page)).toEqual([{ name: "openrouter" }]);
  await expect(page.locator("body")).toContainText("openrouter: 3 models");
});
