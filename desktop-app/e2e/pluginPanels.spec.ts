// pluginPanels.spec.ts covers the Plugins menu, sandboxed iframe tabs
// and the update banner. The frames are stubbed; main serves the real
// files in production.
import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const VIEWS = {
  views: [
    {
      plugin: "board",
      view: "board",
      title: "Ticket Board",
      mount: "side-pane",
      permissions: ["invokeTool", "readFile"],
    },
    {
      plugin: "danger",
      view: "run",
      title: "Danger Zone",
      mount: "side-pane",
      permissions: ["invokeToolDestructive"],
      destructive: true,
    },
  ],
  cards: [],
  errors: [],
};

async function openSession(page: Page): Promise<void> {
  await showSidebar(page);
  await page.locator('button.session-pick[data-session="sess-notify-1"]').click();
  await expect(page.locator("#sessionLabel")).not.toHaveText("—");
}

async function openMenu(page: Page): Promise<void> {
  await page.click("#panePluginsBtn");
  await expect(page.locator("#pluginsMenu")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((views) => {
    (window as any).__e2ePluginViews = views;
  }, VIEWS);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("the Plugins menu lists views with permission badges", async ({ page }) => {
  await openSession(page);
  await openMenu(page);
  await expect(page.locator("#pluginsMenu .plugin-row")).toHaveCount(2);
  await expect(page.locator("#pluginsMenu")).toContainText("Ticket Board");
  await expect(page.locator("#pluginsMenu")).toContainText("runs tools");
  await expect(page.locator("#pluginsMenu")).toContainText("reads files");
  await expect(page.locator("#pluginsMenu")).toContainText("Danger Zone");
  await expect(page.locator("#pluginsMenu .plugin-badge.is-danger")).toHaveText("changes files");
});

test("opening a view creates a sandboxed plugin tab", async ({ page }) => {
  await openSession(page);
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="board"]').click();
  const frame = page.locator("#paneBody iframe.plugin-frame");
  await expect(frame).toBeVisible();
  await expect(frame).toHaveAttribute("sandbox", "allow-scripts");
  const src = await frame.getAttribute("src");
  expect(src).toContain("plugin://board/board/ui/board.html");
  expect(src).toContain("panel=panel-1");
  await expect(page.locator('#paneTabs .pane-tab[data-plugin="board/board"]')).toBeVisible();
  const opens = await page.evaluate(() => (window as any).__e2ePluginOpens || []);
  expect(opens[0]).toMatchObject({ plugin: "board", view: "board", sessionId: "sess-notify-1" });
  // Closing the tab releases the main-process grant.
  await page.locator('#paneTabs .pane-tab[data-plugin="board/board"] .pane-tab-close').click();
  await expect(page.locator("#paneBody iframe.plugin-frame")).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).__e2ePluginCloses || [])).toContain("panel-1");
});

test("an overlay opens without a chat and expands into a canvas", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2ePluginViews = {
      views: [{ plugin: "canvas", view: "draw", title: "Canvas", mount: "overlay", permissions: [] }],
      cards: [],
      errors: [],
    };
  });
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="canvas"]').click();
  const canvas = page.locator(".plugin-overlay");
  await expect(canvas).toBeVisible();
  await expect(canvas.locator("iframe")).toHaveAttribute("sandbox", "allow-scripts");
  await canvas.getByRole("button", { name: "Expand draw canvas" }).click();
  await expect(canvas).toHaveClass(/is-maximized/);
  await canvas.getByRole("button", { name: "Restore draw canvas" }).click();
  await expect(canvas).not.toHaveClass(/is-maximized/);
  await canvas.getByRole("button", { name: "Close draw" }).click();
  await expect(canvas).toHaveCount(0);
});

test("a plugin tab opens without a chat", async ({ page }) => {
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="board"]').click();
  await expect(page.locator("#paneBody iframe.plugin-frame")).toBeVisible();
  expect(await page.evaluate(() => (window as any).__e2ePluginOpens[0].sessionId)).toBe("");
});

test("a session keeps at most two plugin panels, closing the oldest", async ({ page }) => {
  await openSession(page);
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="board"]').click();
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="danger"]').click();
  await expect(page.locator("#paneTabs .pane-tab[data-plugin]")).toHaveCount(2);
  // A third open drops the first panel before asking main for a new one.
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="board"]').click();
  await expect(page.locator("#paneTabs .pane-tab[data-plugin]")).toHaveCount(2);
  const closes = await page.evaluate(() => (window as any).__e2ePluginCloses || []);
  expect(closes).toContain("panel-1");
});

test("an SSH core explains that plugin UI is local-only", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eCoreConnection = { mode: "ssh", target: "dev@build", binary: "/usr/bin/agent-host" };
  });
  await page.reload();
  await openSession(page);
  await openMenu(page);
  await expect(page.locator("#pluginsMenu")).toContainText("Plugin UI runs local plugin files");
  await expect(page.locator("#pluginsMenu .plugin-row")).toHaveCount(0);
});

test("a plugin reload shows a banner and reopening loads the new frame", async ({ page }) => {
  await openSession(page);
  await openMenu(page);
  await page.locator('#pluginsMenu .plugin-row[data-plugin="board"]').click();
  await expect(page.locator("#paneBody iframe.plugin-frame")).toBeVisible();
  await page.evaluate(() => (window as any).__e2eHub.emit({ event: "plugin/updated", seq: 1 }));
  const banner = page.locator(".plugin-stale");
  await expect(banner).toContainText("Plugin updated");
  await banner.locator("button").click();
  await expect(page.locator(".plugin-stale")).toHaveCount(0);
  const opens = await page.evaluate(() => (window as any).__e2ePluginOpens || []);
  expect(opens).toHaveLength(2);
  await expect(page.locator("#paneBody iframe.plugin-frame")).toBeVisible();
});
