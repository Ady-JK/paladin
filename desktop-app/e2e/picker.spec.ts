import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.click("#modelSelectBtn");
  await expect(page.locator("#modelTree [data-mpick]").first()).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#modelMenu")).toBeHidden();
});

test("composer picker stays open while toggling and picking", async ({ page }) => {
  const menu = page.locator("#modelMenu");
  await expect(menu).toBeHidden();

  await page.click("#modelSelectBtn");
  await expect(menu).toBeVisible();

  await expect(menu.locator("[data-ltoggle]")).toHaveCount(4);
  await menu.locator('[data-ptoggle="Opencode Go"]').click();
  await expect(menu).toBeVisible();
  await expect(menu.locator("[data-ltoggle]")).toHaveCount(2);

  await menu.locator('[data-ptoggle="Opencode Go"]').click();
  await expect(menu).toBeVisible();
  await expect(menu.locator("[data-ltoggle]")).toHaveCount(4);

  await menu.locator("[data-mpick]").first().click();
  await expect(menu).toBeVisible();
  await expect(page.locator("#modelSelectLabel")).toHaveText("local/llama3.2");

  await page.click("#sidebar h1");
  await expect(menu).toBeHidden();
});

test("the picked model survives a reload", async ({ page }) => {
  await page.click("#modelSelectBtn");
  const menu = page.locator("#modelMenu");
  await menu.locator("[data-mpick]").filter({ hasText: "claude-fable" }).click();
  await expect(page.locator("#modelSelectLabel")).toHaveText("Opencode Go/claude-fable");

  await page.reload();
  await expect(page.locator("#modelSelectLabel")).toHaveText("Opencode Go/claude-fable");
});

test("the picked model survives a catalog refresh", async ({ page }) => {
  await page.click("#modelSelectBtn");
  const menu = page.locator("#modelMenu");
  await menu.locator("[data-mpick]").filter({ hasText: "claude-fable" }).click();
  await expect(page.locator("#modelSelectLabel")).toHaveText("Opencode Go/claude-fable");

  await expect(page.locator("#modelSelectLabel")).toHaveText(
    "Opencode Go/claude-fable-20990101",
    { timeout: 20000 },
  );
});

test("picker groups the ~ labs and shows no tilde", async ({ page }) => {
  await page.click("#modelSelectBtn");
  const tree = page.locator("#modelTree");

  await expect(tree.getByRole("button", { name: "anthropic" })).toHaveCount(1);
  await expect(tree).not.toContainText("~");
});

test("dropdowns filter the tree and clear shows everything", async ({ page }) => {
  await page.click("#modelSelectBtn");
  const menu = page.locator("#modelMenu");
  const tree = page.locator("#modelTree");

  await expect(tree.locator("[data-ptoggle]")).toHaveCount(3);

  await page.locator("#providerPick").selectOption("Opencode Go");
  await expect(tree.locator("[data-ptoggle]")).toHaveCount(1);
  await expect(page.locator("#filterBar")).toBeVisible();
  await expect(page.locator("#filterLabel")).toHaveText("Showing Opencode Go");

  await page.locator("#labPick").selectOption("anthropic");
  await expect(tree.locator("[data-ltoggle]")).toHaveCount(1);
  await expect(tree.locator("[data-mpick]")).toHaveCount(2);
  await expect(page.locator("#filterLabel")).toHaveText("Showing Opencode Go / anthropic");

  await page.locator("#filterClearBtn").click();
  await expect(tree.locator("[data-ptoggle]")).toHaveCount(3);
  await expect(page.locator("#filterBar")).toBeHidden();
  await expect(menu).toBeVisible();
});

test("dialog picker toggles and picks without closing", async ({ page }) => {  await page.click("#newSessionBtn");
  const dialog = page.locator("#sessionModal");
  await expect(dialog).toBeVisible();

  // Wizard opens on the folder step; confirm it to reach the model step.
  await expect(page.locator("#newSessionFolderStep")).toBeVisible();
  await expect(page.locator("#newSessionModelStep")).toBeHidden();
  await page.click("#sessionSelectFolderBtn");
  await expect(page.locator("#newSessionFolderStep")).toBeHidden();
  await expect(page.locator("#newSessionModelStep")).toBeVisible();

  // Back returns to the folder step for reselecting.
  await page.click("#sessionBackBtn");
  await expect(page.locator("#newSessionFolderStep")).toBeVisible();
  await page.click("#sessionSelectFolderBtn");

  const tree = page.locator("#newSessionTree");
  await expect(tree.locator("[data-ltoggle]")).toHaveCount(4);
  await tree.locator('[data-ptoggle="Opencode Go"]').click();
  await expect(dialog).toBeVisible();
  await expect(tree.locator("[data-ltoggle]")).toHaveCount(2);

  await tree.locator('[data-ptoggle="Opencode Go"]').click();
  await tree.locator("[data-mpick]").first().click();
  await expect(dialog).toBeVisible();
  await expect(page.locator("#newSessionCrumb")).toHaveText("local / other / llama3.2");
});

test("new-session folder step browses with crumbs, search, and hidden toggle", async ({ page }) => {
  await page.click("#newSessionBtn");
  const dialog = page.locator("#sessionModal");
  await expect(dialog).toBeVisible();

  // Breadcrumbs show the current path with a clickable root.
  await expect(page.locator("#newSessionCrumbs")).toContainText("home");
  await expect(page.locator("#newSessionCrumbs [data-fb-crumb]")).toHaveCount(3);

  // Dotfiles stay hidden by default.
  await expect(page.locator("#newSessionList")).not.toContainText(".hidden-dir");

  // The Hidden checkbox reveals them and remembers the choice.
  await page.locator("#newSessionShowHidden").check();
  await expect(page.locator("#newSessionList")).toContainText(".hidden-dir");
  await expect(await page.evaluate(() => window.localStorage.getItem("paladin.showHidden"))).toBe("1");
  await page.locator("#newSessionShowHidden").uncheck();
  await expect(page.locator("#newSessionList")).not.toContainText(".hidden-dir");

  // Search filters the current folder (folders only in this step).
  await page.locator("#newSessionShowHidden").check();
  await page.fill("#newSessionFolderSearch", "hidden");
  await expect(page.locator("#newSessionList")).toContainText(".hidden-dir");
  await expect(page.locator("#newSessionList")).not.toContainText("project");
  await page.fill("#newSessionFolderSearch", "");
  await page.locator("#newSessionShowHidden").uncheck();
  await expect(page.locator("#newSessionList")).not.toContainText(".hidden-dir");

  // Selecting a folder arms the pending workspace shown in the path.
  await page.locator('#newSessionList button[data-fb-select$="/project"]').first().click();
  await expect(page.locator("#newSessionPath")).toContainText("project");

  // Confirming advances to the model step with a way back.
  await page.click("#sessionSelectFolderBtn");
  await expect(page.locator("#newSessionModelStep")).toBeVisible();
  await expect(page.locator("#sessionFolderLabel")).toContainText("project");

  // The card itself never scrolls; only the list inside does.
  const cardScroll = await page.locator("#sessionModal .modal-card").evaluate((el) => ({
    scroll: el.scrollHeight,
    client: el.clientHeight,
  }));
  expect(cardScroll.scroll).toBeLessThanOrEqual(cardScroll.client + 1);

  await page.click("#sessionBackBtn");
  await expect(page.locator("#newSessionFolderStep")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
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

  // Select file inserts the highlighted file and closes.
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
  await expect(page.locator("#promptInput")).not.toBeEmpty();

  // Select folder with nothing highlighted inserts the current folder.
  await page.fill("#promptInput", "");
  await page.click("#attachBtn");
  await expect(page.locator("#fileModal")).toBeVisible();
  await page.click("#fileSelectFolderBtn");
  await expect(page.locator("#fileModal")).toBeHidden();
  await expect(page.locator("#promptInput")).not.toBeEmpty();
  await page.keyboard.press("Escape");
});
