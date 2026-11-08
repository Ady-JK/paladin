// pluginCards.spec.ts covers a plugin's own tool-result renderer: the
// host serves it as a sandboxed plugin:// document, and a tool without a
// card keeps the normal tool row.
import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const VIEWS = {
  views: [],
  cards: [{ plugin: "board", tool: "lookup", exposed: "extension__board__lookup" }],
  errors: [],
};

async function startTurn(page: Page): Promise<void> {
  await page.fill("#promptInput", "look it up");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__e2eCardHandlers));
}

async function emitTool(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as any).__e2eCardHandlers.onTool({
      id: "lookup-1",
      name: "extension__board__lookup",
      arguments: '{"key":"PAL-1"}',
      result: '{"x":7}',
      status: "ok",
    });
  });
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((views) => {
    (window as any).__e2ePluginViews = views;
    (window as any).__e2eRunTurn = (_params: unknown, handlers: unknown) =>
      new Promise(() => {
        (window as any).__e2eCardHandlers = handlers;
      });
  }, VIEWS);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.waitForFunction(() => Boolean((window as any).__e2ePluginViewsLoaded));
});

test("a listed extension tool renders in a sandboxed plugin card", async ({ page }) => {
  await startTurn(page);
  await emitTool(page);
  const card = page.locator(".plugin-card");
  await expect(card).toBeVisible();
  const frame = card.locator("iframe.plugin-card-frame");
  await expect(frame).toHaveAttribute("sandbox", "allow-scripts");
  const src = await frame.getAttribute("src");
  expect(src).toContain("plugin://board/__card__/lookup/index.html");
  await expect(card.locator(".plugin-card-fallback")).toHaveCount(0);
});

test("a tool without a card keeps the normal tool row", async ({ page }) => {
  await startTurn(page);
  await page.evaluate(() => {
    (window as any).__e2eCardHandlers.onTool({
      id: "other-1",
      name: "extension__other__thing",
      arguments: "{}",
      result: "{}",
      status: "ok",
    });
  });
  await expect(page.locator(".plugin-card")).toHaveCount(0);
  await expect(page.locator("#messages .tool-run")).toHaveCount(1);
});

test("a listed card with no desktop bridge falls back to the result text", async ({ page }) => {
  await startTurn(page);
  await page.evaluate(() => {
    (window as any).paladin.plugins = undefined;
  });
  await emitTool(page);
  const card = page.locator(".plugin-card");
  await expect(card).toBeVisible();
  await expect(card.locator(".plugin-card-fallback")).toContainText('{"x":7}');
  await expect(card.locator("iframe")).toHaveCount(0);
});

test("a card that cannot load falls back to its result", async ({ page }) => {
  await startTurn(page);
  await emitTool(page);
  await expect(page.locator(".plugin-card-fallback")).toContainText('{"x":7}', { timeout: 6000 });
});

test("saved extension results restore their cards", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eHistory = [
      { role: "user", content: "Look it up" },
      { role: "tool", name: "extension__board__lookup", arguments: "{}", content: '{"x":7}', status: "ok" },
    ];
  });
  await showSidebar(page);
  await page.locator('button.session-pick[data-session="sess-notify-1"]').click();
  await expect(page.locator("#messages .plugin-card")).toBeVisible();
});
