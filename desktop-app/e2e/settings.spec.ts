import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("settings fills most of the screen with a sidebar", async ({ page }) => {
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsModal")).toBeVisible();
  const modal = await page.locator("#settingsModal > div").first().boundingBox();
  const viewport = page.viewportSize()!;
  expect(modal!.width).toBeGreaterThan(viewport.width * 0.8);
  expect(modal!.height).toBeGreaterThan(viewport.height * 0.8);
  await expect(page.locator("#settingsNavWorkspace")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#settingsPaneTitle")).toHaveText("Workspace");
});

test("settings nav switches panes", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await expect(page.locator("#settingsNavProviders")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#settingsPaneTitle")).toHaveText("Providers");
  await expect(page.locator("#settingsPaneProviders")).toBeVisible();
  await page.click("#settingsNavConnection");
  await expect(page.locator("#settingsPaneConnection")).toBeVisible();
  await expect(page.locator("#settingsPaneProviders")).toBeHidden();
});

test("providers pane lists opencode and openrouter rows", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const list = page.locator("#settingsProviderList");
  await expect(list).toContainText("Opencode Go");
  await expect(list).toContainText("openrouter");
  const opencodeRow = list.locator("div", { hasText: "Opencode Go" }).first();
  await expect(opencodeRow).toContainText("default");
  const routerRow = list.locator("div", { hasText: "openrouter" }).first();
  await expect(routerRow.locator("[data-setdefault]")).toBeVisible();
  await expect(routerRow.locator("[data-connect]")).toBeVisible();
});

test("add provider card creates an entry", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  await page.click("#settingsAddProviderBtn");
  await expect(page.locator("#providerCard")).toBeVisible();
  await expect(page.locator("#providerCardTitle")).toHaveText("Add provider");
  await page.fill("#providerCardName", "together");
  await page.fill("#providerCardUrl", "https://api.together.xyz/v1");
  await page.fill("#providerCardKey", "live-together-1");
  await page.locator("#providerCardType").selectOption("openai");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await expect(page.locator("#settingsProviderList")).toContainText("together");
});

test("connect card updates a provider without repeating the key", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const row = page.locator("#settingsProviderList div", { hasText: "openrouter" }).first();
  await row.locator("[data-connect]").click();
  await expect(page.locator("#providerCardTitle")).toHaveText("Connect openrouter");
  await expect(page.locator("#providerCardUrl")).toHaveValue("https://openrouter.ai/api/v1");
  await page.fill("#providerCardKey", "live-router-1");
  await page.click("#providerCardSave");
  await expect(page.locator("#providerCard")).toBeHidden();
  await expect(page.locator("#settingsProviderList")).toContainText("key stored");
});

test("set default flips the default badge", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const row = page.locator("#settingsProviderList div", { hasText: "openrouter" }).first();
  await row.locator("[data-setdefault]").click();
  await expect(page.locator("#settingsProviderList")).toContainText("openrouter");
  const updated = page.locator("#settingsProviderList div", { hasText: "openrouter" }).first();
  await expect(updated).toContainText("default");
});

test("workspace pane saves the default workspace", async ({ page }) => {
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsDefaultWorkspace")).toBeVisible();
  await page.fill("#settingsDefaultWorkspace", "/home/tester/project");
  await page.click("#settingsWorkspaceSaveBtn");
  await expect(page.locator("#toast")).toContainText("Default workspace saved");
  await page.reload();
  await expect(page.locator("#sessionList")).toBeVisible();
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsDefaultWorkspace")).toHaveValue("/home/tester/project");
});

test("prompt pane loads, saves, and resets the agent prompt", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavPrompt");
  await expect(page.locator("#settingsNavPrompt")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#settingsPaneTitle")).toHaveText("Prompt");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("stub system prompt");

  await page.fill("#settingsSystemPrompt", "custom sys");
  await page.click("#settingsPromptSaveBtn");
  await expect(page.locator("#toast")).toContainText("Prompt saved");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("custom sys");

  await page.click("#settingsPromptResetBtn");
  await expect(page.locator("#toast")).toContainText("Prompt reset to default");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("stub system prompt");
});
