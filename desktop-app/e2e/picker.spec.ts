import { showSidebar } from "./drawer";
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const bridge = (window as any).paladin;
    const original = bridge.invoke;
    bridge.invoke = (method: string, params: unknown) => {
      if (method === "listProviders")
        return Promise.resolve([
          {
            name: "primary",
            default: true,
            apiType: "openai",
            apiUrl: "",
            model: "openai/gpt-4o",
            models: ["openai/gpt-4o", "anthropic/claude-fable"],
          },
          {
            name: "alternate",
            default: false,
            apiType: "openai",
            apiUrl: "",
            model: "openai/gpt-4o",
            models: ["openai/gpt-4o"],
          },
        ]);
      return original(method, params);
    };
  });
  await page.goto("/");
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelTree [data-mpick]").first()).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#modelMenu")).toBeHidden();
});

test("picker lists models with their providers and selects one", async ({ page }) => {
  await page.click("#modelSelectBtn");
  const menu = page.locator("#modelMenu");
  await expect(menu.locator("#providerPick, #labPick, #modelDetail")).toHaveCount(0);
  const rows = menu.locator("[data-mpick]");
  await expect(rows.filter({ hasText: "gpt-4o" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "gpt-4o" }).first()).toContainText("primary");
  await rows.filter({ hasText: "claude-fable" }).click();
  await expect(menu).toBeHidden();
  await expect(page.locator("#modelSelectLabel")).toHaveText("claude-fable");
  await page.reload();
  await expect(page.locator("#modelSelectLabel")).toHaveText("claude-fable");
});

test("search ranks closest matches and keyboard selection uses provider and model", async ({ page }) => {
  await page.click("#modelSelectBtn");
  await page.selectOption("#modelProviderFilter", "*");
  await page.fill("#modelSearch", "gpt-4o");
  const rows = page.locator("#modelTree [data-mpick]");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("gpt-4o");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.locator("#modelSelectLabel")).toHaveText("gpt-4o");
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem("paladin.model") || "{}"));
  expect(saved.provider).toBeTruthy();
  expect(saved.model).toBe("openai/gpt-4o");
  await page.click("#modelSelectBtn");
  await page.fill("#modelSearch", "claude-fabl");
  await expect(rows.first()).toContainText("claude-fable");
  await page.fill("#modelSearch", "cld-fbl");
  await expect(rows.first()).toContainText("claude-fable");
  await page.fill("#modelSearch", "no-such-model-zzzz");
  await expect(page.locator("#modelTree")).toContainText("No models found");
});

test("search preserves typo matching, provider filtering and selection identity", async ({ page }) => {
  await page.click("#modelSelectBtn");
  await page.fill("#modelSearch", "claude-fable");
  await expect(page.locator("#modelTree [data-mpick]").first()).toContainText("claude-fable");
  await page.fill("#modelSearch", "clxude-fable");
  await expect(page.locator("#modelTree [data-mpick]").first()).toContainText("claude-fable");
  await page.selectOption("#modelProviderFilter", "alternate");
  await page.fill("#modelSearch", "alternate");
  const rows = page.locator("#modelTree [data-mpick]");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("gpt-4o");
  await rows.first().click();
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem("paladin.model") || "{}"));
  expect(saved).toMatchObject({ provider: "alternate", model: "openai/gpt-4o" });
});

test("new session is a single folder step that starts with the current model", async ({ page }) => {
  await showSidebar(page);
  await page.click("#newSessionBtn");
  const dialog = page.locator("#sessionModal");
  await expect(dialog).toBeVisible();

  // No model step: the dialog states which model Start will use.
  await expect(page.locator("#newSessionModelStep")).toHaveCount(0);
  await expect(page.locator("#newSessionModelHint")).toContainText("model:");

  // Breadcrumbs show the current path with a clickable root.
  await expect(page.locator("#newSessionCrumbs")).toContainText("home");
  await expect(page.locator("#newSessionCrumbs [data-fb-crumb]")).toHaveCount(3);

  // Dotfiles stay hidden by default.
  await expect(page.locator("#newSessionList")).not.toContainText(".hidden-dir");

  // The Hidden checkbox reveals them and remembers the choice.
  await page.locator("#newSessionShowHidden").check();
  await expect(page.locator("#newSessionList")).toContainText(".hidden-dir");
  expect(await page.evaluate(() => window.localStorage.getItem("paladin.showHidden"))).toBe("1");

  // Search filters the current folder.
  await page.fill("#newSessionFolderSearch", "hidden");
  await expect(page.locator("#newSessionList")).toContainText(".hidden-dir");
  await expect(page.locator("#newSessionList")).not.toContainText("project");
  await page.fill("#newSessionFolderSearch", "");
  await page.locator("#newSessionShowHidden").uncheck();
  await expect(page.locator("#newSessionList")).not.toContainText(".hidden-dir");

  // The card itself never scrolls; only the list inside does.
  const cardScroll = await page.locator("#sessionModal .modal-card").evaluate((el) => ({
    scroll: el.scrollHeight,
    client: el.clientHeight,
  }));
  expect(cardScroll.scroll).toBeLessThanOrEqual(cardScroll.client + 1);

  // Selecting a folder arms the path line; Start uses it and closes.
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await expect(page.locator("#newSessionPath")).toContainText("project");
  await page.click("#sessionStartBtn");
  await expect(dialog).toBeHidden();
  await expect(page.locator("#workspaceLabel")).toHaveText("project");
});

test("attach picker selects files and folders with crumbs and hidden toggle", async ({ page }) => {
  await page.click("#attachBtn");
  const dialog = page.locator("#fileModal");
  await expect(dialog).toBeVisible();

  // Breadcrumbs plus both confirm buttons are present.
  await expect(page.locator("#fileCrumbs")).toContainText("home");
  await expect(page.locator("#fileSelectFileBtn")).toBeVisible();
  await expect(page.locator("#fileSelectFolderBtn")).toBeVisible();

  // Hidden entries stay out until opted in.
  await expect(page.locator("#fileList")).not.toContainText(".env");
  await page.locator("#fileShowHidden").check();
  await expect(page.locator("#fileList")).toContainText(".env");
  await page.locator("#fileShowHidden").uncheck();

  // Search narrows to matching names.
  await page.fill("#fileSearch", "readme");
  await expect(page.locator("#fileList")).toContainText("README.md");
  await page.fill("#fileSearch", "");
  await expect(page.locator("#fileList")).toContainText("project");

  // Select file tags the highlighted file and closes.
  await page.locator('#fileList [data-fb-select][data-fb-kind="file"]').first().click();
  await expect(page.locator('#fileList [data-fb-select][aria-selected="true"]')).toHaveCount(1);

  // The card itself never scrolls; only the list inside does.
  const cardScroll = await page.locator("#fileModal .modal-card").evaluate((el) => ({
    scroll: el.scrollHeight,
    client: el.clientHeight,
  }));
  expect(cardScroll.scroll).toBeLessThanOrEqual(cardScroll.client + 1);

  await page.click("#fileSelectFileBtn");
  await expect(dialog).toBeHidden();
  await expect(page.locator("#tagCards .tag-card")).toHaveCount(1);

  // Select folder with nothing highlighted tags the current folder.
  await page.click("#attachBtn");
  await expect(page.locator("#fileModal")).toBeVisible();
  await page.click("#fileSelectFolderBtn");
  await expect(page.locator("#fileModal")).toBeHidden();
  await expect(page.locator("#tagCards .tag-card")).toHaveCount(2);
  await page.keyboard.press("Escape");
});
