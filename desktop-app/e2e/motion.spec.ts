// motion.spec.ts covers the animation level: System (follows the OS
// reduce-motion setting), Full, Reduced and Off, persisted locally.
import { test, expect, type Page } from "@playwright/test";

async function boot(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
}

const motion = (page: Page) => page.evaluate(() => document.documentElement.dataset.motion);

test("system default follows the OS: full normally", async ({ page }) => {
  await boot(page);
  expect(await motion(page)).toBe("full");
});

test("system default follows the OS: reduced when the OS asks", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await boot(page);
  expect(await motion(page)).toBe("reduced");
});

test("off stops transitions and persists across reloads", async ({ page }) => {
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavAppearance");
  await expect(page.locator('#motionOptions input[value="system"]')).toBeChecked();
  await page.check('#motionOptions input[value="off"]');
  expect(await motion(page)).toBe("off");
  const duration = await page.locator("#sidebar").evaluate((el) => getComputedStyle(el).transitionDuration);
  expect(duration.split(",").every((d) => parseFloat(d) === 0)).toBe(true);
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  expect(await motion(page)).toBe("off");
});

test("off closes modals at once", async ({ page }) => {
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavAppearance");
  await page.check('#motionOptions input[value="off"]');
  await page.click("#settingsCloseBtn");
  // No fade-out timer: hidden on the same tick.
  expect(await page.locator("#settingsModal").evaluate((el) => el.classList.contains("hidden"))).toBe(true);
});

test("full overrides an OS reduce-motion preference", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavAppearance");
  await page.check('#motionOptions input[value="full"]');
  expect(await motion(page)).toBe("full");
  const duration = await page.locator("#sidebar").evaluate((el) => getComputedStyle(el).transitionDuration);
  expect(duration.split(",").some((d) => parseFloat(d) > 0.05)).toBe(true);
});
