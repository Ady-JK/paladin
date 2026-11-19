import { test, expect } from "@playwright/test";

// mcpRemove.spec.ts covers removing a server from Settings → MCP, the
// scope label the pane uses, and the editor's height. A server is removed
// from its mcp.json, so the action asks once in place rather than acting
// on a single click on the trash icon.
test("a server can be removed from the global list, and the label says Global", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eMcp = {
      servers: [
        {
          name: "graphify",
          scope: "global",
          transport: "stdio",
          state: "ready",
          tools: [{ name: "query_graph", exposed: "query_graph", readOnly: true, destructive: false }],
          restarts: 0,
        },
        { name: "other", scope: "global", transport: "stdio", state: "ready", tools: [], restarts: 0 },
      ],
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "/mcp");
  await page.keyboard.press("Enter");
  const pane = page.locator("#settingsPaneMcp");
  await expect(pane).toBeVisible();
  await page.waitForTimeout(500);

  const section = pane.locator('[data-mcp-section="global"]');
  await expect(section.locator(".mem-title").first()).toHaveText("Global");
  await expect(pane.locator("text=Everywhere")).toHaveCount(0);

  // Editor is bigger now.
  await page.click('[data-mcp-edit="global"]');
  const ta = page.locator('.mcp-editor[data-scope="global"] textarea');
  await expect(ta).toBeVisible();
  const box = await ta.boundingBox();
  expect(Math.round(box?.height ?? 0), "the editor must be usable").toBeGreaterThan(200);
  await page.click("[data-mcp-cancel]");
  await page.waitForTimeout(200);

  // Trash asks once, then deletes.
  const card = section.locator('.mcp-server[data-mcp-server="graphify"]');
  await expect(card.locator("[data-mcp-remove-ask]")).toBeVisible();
  await card.locator("[data-mcp-remove-ask]").click();
  await page.waitForTimeout(250);
  await expect(section.locator(".mcp-remove-confirm")).toHaveCount(1);
  await page.screenshot({ path: "/tmp/mcp-confirm.png" });
  await section.locator("[data-mcp-remove]").click();
  await page.waitForTimeout(600);
  await expect(section.locator('.mcp-server[data-mcp-server="graphify"]')).toHaveCount(0);
  // Only the chosen server goes.
  await expect(section.locator('.mcp-server[data-mcp-server="other"]')).toHaveCount(1);
  const calls = await page.evaluate(() => (window as any).__e2eMcpCalls || []);
  const removed = calls.filter((c: any) => c.cmd === "mcpRemove").at(-1);
  expect(removed.args).toMatchObject({ scope: "global", name: "graphify" });
});

// Cancel leaves the server in place and puts the trash icon back.
test("cancelling the remove keeps the server", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eMcp = {
      servers: [{ name: "graphify", scope: "global", transport: "stdio", state: "ready", tools: [], restarts: 0 }],
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "/mcp");
  await page.keyboard.press("Enter");
  const section = page.locator('[data-mcp-section="global"]');
  const card = section.locator('.mcp-server[data-mcp-server="graphify"]');
  await card.locator("[data-mcp-remove-ask]").click();
  await expect(section.locator(".mcp-remove-confirm")).toHaveCount(1);
  await section.locator("[data-mcp-remove-cancel]").click();
  await expect(section.locator(".mcp-remove-confirm")).toHaveCount(0);
  await expect(card.locator("[data-mcp-remove-ask]")).toBeVisible();
  const calls = await page.evaluate(() => (window as any).__e2eMcpCalls || []);
  expect(calls.filter((c: any) => c.cmd === "mcpRemove")).toHaveLength(0);
});
