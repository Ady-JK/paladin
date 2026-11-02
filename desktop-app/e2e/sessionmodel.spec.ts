import { test, expect } from "@playwright/test";
import { showSidebar } from "./drawer";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

// Opening an old session adopts the model it last used (persisted in
// the session meta by the core) and records it as the global default,
// so a later provider refresh or app restart restores this model
// instead of falling back to another provider's.
test("opening a session adopts and persists its last-used model", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eOpenExtra = {
      "sess-notify-1": { provider: "openrouter", model: "openai/gpt-4o" },
    };
  });
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#modelSelectLabel")).toHaveText("openrouter/gpt-4o");
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem("paladin.model") || "{}"));
  expect(saved).toMatchObject({ provider: "openrouter", model: "openai/gpt-4o" });
});
