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
