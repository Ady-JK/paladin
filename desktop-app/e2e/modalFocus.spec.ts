// modalFocus.spec.ts checks modals behave as dialogs for the keyboard:
// announced as dialogs, focus moves in, Tab stays inside, and closing
// returns focus to the control that opened them.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("settings opens as a dialog, traps Tab and gives focus back on close", async ({ page }) => {
  await page.focus("#settingsBtn");
  await page.keyboard.press("Enter");
  const modal = page.locator("#settingsModal");
  await expect(modal).toBeVisible();
  await expect(modal).toHaveAttribute("role", "dialog");
  await expect(modal).toHaveAttribute("aria-modal", "true");
  await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest("#settingsModal"))).toBe(true);
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest("#settingsModal"))).toBe(true);
  }
  for (let i = 0; i < 5; i++) {
    await page.keyboard.press("Shift+Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest("#settingsModal"))).toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(modal).toBeHidden();
  await expect.poll(() => page.evaluate(() => document.activeElement?.id)).toBe("settingsBtn");
});

test("toasts are announced", async ({ page }) => {
  await page.evaluate(() => {
    const el = document.getElementById("toast")!;
    el.removeAttribute("role");
  });
  // A failed action raises an error toast.
  await page.evaluate(async () => {
    const path = "/src/ui/toast.ts"; // a variable keeps the type checker out of the browser module
    const mod = await import(/* @vite-ignore */ path);
    mod.toast("could not do it", "error");
  });
  await expect(page.locator("#toast")).toHaveAttribute("role", "alert");
  await expect(page.locator("#toast")).toHaveAttribute("aria-live", "assertive");
});
