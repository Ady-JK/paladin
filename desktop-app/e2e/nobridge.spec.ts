// nobridge.spec.ts pins that the renderer survives without the Electron bridge.
//
// The dev server opened in a plain browser (or in Paladin's own agent browser)
// has no `window.paladin`. That used to abort DOMContentLoaded partway through
// boot, leaving the window on "Opening Paladin" forever with the shell inert —
// the most inspectable place in the app was un-inspectable, and layout work
// meant booting Electron. Boot must fail open: reveal the app, and say what is
// missing instead of hanging.
import { expect, test } from "@playwright/test";

// Deliberately no paladin-stub.js.
test("the app boots without the native bridge instead of hanging on the splash", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");

  // The shell is revealed: the startup screen goes away and the app is usable.
  await expect(page.locator("#appShell")).not.toHaveAttribute("inert", "");
  await expect(page.locator("#promptInput")).toBeVisible({ timeout: 10_000 });
  await expect(page.locator("#startup")).toHaveCount(0);

  // And the mode switch works, so this is genuinely the app, not a painted card.
  await expect(page.locator("#modeAgentBtn")).toHaveAttribute("aria-selected", "true");
  await page.locator("#modeBrowserBtn").click();
  await expect(page.locator("#browserView")).toBeVisible();
  await expect(page.locator("#browserToAgentBtn")).toBeVisible();
  await page.locator("#browserToAgentBtn").click();
  await expect(page.locator("#agentView")).toBeVisible();

  // No unhandled error escaped boot. Rejections are expected — there is nothing
  // to connect to — but they must be caught, never thrown synchronously.
  expect(errors).toEqual([]);
});

test("a missing bridge never becomes a dead window control", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible({ timeout: 10_000 });
  // There is nothing to minimise in a browser tab, so the buttons say so
  // rather than looking live and doing nothing.
  await expect(page.locator("#windowCloseBtn")).toBeDisabled();
  await expect(page.locator("#windowCloseBtn")).toHaveAttribute("title", /desktop only/);
});

test("the sites menu opens under its anchor without a bridge", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible({ timeout: 10_000 });
  // The menu hangs off the card, so its old container-relative offset put it a
  // whole card-height down. It is placed from its anchor now.
  await page.locator("#modeBrowserBtn").click();
  await page.locator("#browserNewTab").click();
  const menu = page.locator("#webMenu");
  await expect(menu).toBeVisible();

  const anchor = (await page.locator("#browserNewTab").boundingBox())!;
  const box = (await menu.boundingBox())!;
  // Directly below the + that opened it, not pinned to the window's bottom.
  expect(box.y).toBeGreaterThan(anchor.y);
  expect(box.y - (anchor.y + anchor.height)).toBeLessThan(24);
});
