import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
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

test("remote core settings keep their values and filter navigation", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.fill("#settingsSearch", "connection");
  await expect(page.locator("#settingsNavConnection")).toBeVisible();
  await expect(page.locator("#settingsNavWorkspace")).toBeHidden();
  await page.click("#settingsNavConnection");
  await page.selectOption("#settingsCoreMode", "ssh");
  await page.fill("#settingsRemoteTarget", "dev@example.internal");
  await page.fill("#settingsRemoteBinary", "/opt/paladin/agent-host");
  await page.click("#settingsRemoteSave");
  await expect(page.locator("#settingsRemoteError")).toBeEmpty();
  expect(await page.evaluate(() => (window as any).__e2eCoreConnection)).toEqual({
    mode: "ssh",
    target: "dev@example.internal",
    binary: "/opt/paladin/agent-host",
  });
});

test("providers pane lists shipped providers and lets LiteLLM use a custom URL", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavProviders");
  const list = page.locator("#settingsProviderList");
  await expect(list).toContainText("Opencode Go");
  await expect(list).toContainText("openrouter");
  await expect(list).toContainText("litellm");
  const opencodeRow = list.locator("div", { hasText: "Opencode Go" }).first();
  await expect(opencodeRow).toContainText("default");
  const routerRow = list.locator("div", { hasText: "openrouter" }).first();
  await expect(routerRow.locator("[data-setdefault]")).toBeVisible();
  await expect(routerRow.locator("[data-connect]")).toBeVisible();
  const liteRow = list.locator("div", { hasText: "litellm" }).first();
  await liteRow.locator("[data-connect]").click();
  await expect(page.locator("#providerCardUrl")).toHaveValue("http://127.0.0.1:4000/v1");
  await page.fill("#providerCardUrl", "http://127.0.0.1:14054/v1");
  await page.fill("#providerCardKey", "local-test-key");
  await page.click("#providerCardSave");
  await expect(list).toContainText("http://127.0.0.1:14054/v1");
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
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.click("#settingsBtn");
  await expect(page.locator("#settingsDefaultWorkspace")).toHaveValue("/home/tester/project");
});

test("prompt pane loads, saves, and resets the agent prompt", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavPrompt");
  await expect(page.locator("#settingsNavPrompt")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#settingsPaneTitle")).toHaveText("Prompt");
  await expect(page.locator("#settingsPromptPreview")).toContainText("stub system prompt");
  await page.click("#settingsPromptEditTab");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("stub system prompt");

  await page.fill("#settingsSystemPrompt", "custom sys");
  await page.click("#settingsPromptSaveBtn");
  await expect(page.locator("#toast")).toContainText("Prompt saved");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("custom sys");

  await page.click("#settingsPromptResetBtn");
  await expect(page.locator("#toast")).toContainText("Prompt reset to default");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("stub system prompt");
});

test("prompt helper proposes a reviewable edit without saving it", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavPrompt");
  await page.click("#settingsPromptEditTab");
  await page.fill("#settingsPromptInstruction", "Make it more concise");
  await page.click("#settingsPromptSuggestBtn");
  await expect(page.locator("#settingsPromptProposal")).toContainText("Make it more concise");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue("stub system prompt");
  await page.click("#settingsPromptApplyBtn");
  await expect(page.locator("#settingsSystemPrompt")).toHaveValue(/Make it more concise/);
});

test("settings content uses its width and tools search is one field", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavTools");
  const pane = await page.locator("#settingsPaneTools").boundingBox();
  const body = await page.locator("#settingsContentBody").boundingBox();
  expect(pane!.width).toBeGreaterThan(body!.width * 0.85);
  const search = page.locator(".tsw-search");
  await expect(search).toBeVisible();
  expect(await search.locator("input").evaluate((input) => getComputedStyle(input).backgroundColor)).toBe(
    "rgba(0, 0, 0, 0)",
  );
});

test("every settings section keeps the shared layout", async ({ page }) => {
  await page.click("#settingsBtn");
  const sections = [
    ["Workspace", "Workspace"],
    ["Providers", "Providers"],
    ["Prompt", "Prompt"],
    ["Connection", "Connection"],
    ["Analytics", "Analytics"],
    ["Appearance", "Appearance"],
    ["Resources", "Resources"],
    ["Memory", "Memory"],
    ["Permissions", "Permissions"],
    ["Mcp", "MCP servers"],
    ["Hooks", "Hooks"],
    ["Tools", "Tools"],
  ] as const;
  for (const [id, title] of sections) {
    await page.locator(`#settingsNav${id}`).click();
    await expect(page.locator("#settingsPaneTitle")).toHaveText(title);
    await expect(page.locator(`#settingsPane${id}`)).toBeVisible();
    await expect(page.locator("#settingsPaneSubtitle")).not.toBeEmpty();
    const pane = await page.locator(`#settingsPane${id}`).boundingBox();
    const body = await page.locator("#settingsContentBody").boundingBox();
    expect(pane!.width).toBeGreaterThan(body!.width * 0.85);
  }
});

test("appearance theme and text size persist", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavAppearance");
  await page.selectOption("#settingsColorMode", "light");
  await page.selectOption("#settingsFontSize", "large");
  await expect(page.locator("html")).toHaveAttribute("data-font-size", "large");
  await expect(page.locator("html")).toHaveClass(/light/);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-font-size", "large");
  await expect(page.locator("html")).toHaveClass(/light/);
});
