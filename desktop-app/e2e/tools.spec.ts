// tools.spec.ts covers tool switches: Settings → Tools (global), the
// composer's Tools popover (per session, session beats global), /tools,
// the "new tools" banner, and pasting a command into Settings → MCP.
import { test, expect, type Page } from "@playwright/test";

const SID = "sess-notify-1";
const CWD = "/home/tester/project";

const TOOLS = [
  { name: "read_file", description: "Read a file", source: "builtin", readOnly: true },
  { name: "shell", description: "Run a shell command", source: "builtin", destructive: true },
  { name: "web_fetch", description: "Fetch a URL", source: "builtin", readOnly: true },
  {
    name: "mcp__dummy__echo",
    description: "Echo text back",
    source: "mcp",
    server: "dummy",
    tool: "echo",
    readOnly: true,
  },
  { name: "mcp__dummy__crash", source: "mcp", server: "dummy", tool: "crash", destructive: true },
];

async function seed(page: Page, tools: Record<string, unknown> = {}) {
  await page.addInitScript(
    (tools) => {
      (window as any).__e2eTools = tools;
    },
    { tools: TOOLS, ...tools },
  );
}

async function boot(page: Page) {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
}

async function openSession(page: Page) {
  await page.locator(`button.session-pick[data-session="${SID}"]`).click();
  await expect(page.locator("#toolsChip")).toBeVisible();
}

async function calls(page: Page, name: string) {
  return page.evaluate(
    (name) => ((window as any).__e2eToolsCalls || []).filter((c: any) => c.cmd === name).map((c: any) => c.args),
    name,
  );
}

async function mcpCalls(page: Page, name: string) {
  return page.evaluate(
    (name) => ((window as any).__e2eMcpCalls || []).filter((c: any) => c.cmd === name).map((c: any) => c.args),
    name,
  );
}

test("Settings → Tools groups tools and switches them globally", async ({ page }) => {
  await seed(page);
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavTools");
  const pane = page.locator("#settingsPaneTools");
  await expect(pane).toBeVisible();
  await expect(pane.locator('.tsw-group[data-group="builtin"] .tsw-row')).toHaveCount(3);
  const mcp = pane.locator('.tsw-group[data-group="mcp:dummy"]');
  await expect(mcp.locator(".tsw-row")).toHaveCount(2);
  await expect(mcp.locator('.tsw-row[data-tool="mcp__dummy__echo"] .tsw-name')).toHaveText("echo");

  const shell = pane.locator('.tsw-row[data-tool="shell"] [data-tsw-global]');
  await expect(shell).toHaveAttribute("aria-checked", "true");
  await shell.click();
  await expect(shell).toHaveAttribute("aria-checked", "false");
  await expect(pane.locator('.tsw-row[data-tool="shell"]')).toHaveAttribute("data-enabled", "false");

  await mcp.locator("[data-tsw-group-toggle]").click();
  await expect(mcp.locator('.tsw-row[data-enabled="false"]')).toHaveCount(2);

  expect(await calls(page, "toolsSet")).toEqual([
    { scope: "global", cwd: CWD, sessionId: null, names: ["shell"], enabled: false },
    { scope: "global", cwd: CWD, sessionId: null, names: ["mcp__dummy__echo", "mcp__dummy__crash"], enabled: false },
  ]);
});

test("Settings → Tools search filters rows", async ({ page }) => {
  await seed(page);
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavTools");
  await page.fill("#toolsSearch", "echo");
  await expect(page.locator("#settingsPaneTools .tsw-row:visible")).toHaveCount(1);
  await expect(page.locator('#settingsPaneTools .tsw-group[data-group="builtin"]')).toBeHidden();
});

test("the composer Tools popover sets session switches that beat global", async ({ page }) => {
  await seed(page, { global: { shell: false } });
  await boot(page);
  await openSession(page);
  await page.click("#toolsChip");
  const menu = page.locator("#toolsMenu");
  await expect(menu).toBeVisible();
  const shell = menu.locator('.tsw-item[data-tool="shell"]');
  await expect(shell).toHaveAttribute("data-enabled", "false");
  await expect(shell.locator('[data-tsw-set="inherit"]')).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#toolsChip .tools-chip-count")).toHaveText("1");

  await shell.locator('[data-tsw-set="on"]').click();
  await expect(shell).toHaveAttribute("data-enabled", "true");
  await expect(page.locator("#toolsChip .tools-chip-count")).toHaveCount(0);

  const echo = menu.locator('.tsw-item[data-tool="mcp__dummy__echo"]');
  await echo.locator('[data-tsw-set="off"]').click();
  await expect(echo).toHaveAttribute("data-enabled", "false");
  await echo.locator('[data-tsw-set="inherit"]').click();
  await expect(echo).toHaveAttribute("data-enabled", "true");

  expect(await calls(page, "toolsSet")).toEqual([
    { scope: "session", cwd: CWD, sessionId: SID, names: ["shell"], enabled: true },
    { scope: "session", cwd: CWD, sessionId: SID, names: ["mcp__dummy__echo"], enabled: false },
    { scope: "session", cwd: CWD, sessionId: SID, names: ["mcp__dummy__echo"], enabled: null },
  ]);
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
});

test("/tools opens the Tools popover", async ({ page }) => {
  await seed(page);
  await boot(page);
  await openSession(page);
  await page.fill("#promptInput", "/tools");
  await page.keyboard.press("Enter");
  await expect(page.locator("#toolsMenu")).toBeVisible();
  await expect(page.locator("#promptInput")).toHaveValue("");
});

test("switches made before the first message apply to the new session", async ({ page }) => {
  await seed(page);
  await boot(page);
  await page.click("#toolsChip");
  await page.locator('#toolsMenu .tsw-item[data-tool="web_fetch"] [data-tsw-set="off"]').click();
  await expect(page.locator('#toolsMenu .tsw-item[data-tool="web_fetch"]')).toHaveAttribute("data-enabled", "false");
  expect(await calls(page, "toolsSet")).toEqual([]);
  await page.keyboard.press("Escape");
  await page.fill("#promptInput", "hello");
  await page.click("#sendBtn");
  await expect
    .poll(() => calls(page, "toolsSet"))
    .toEqual([{ scope: "session", cwd: CWD, sessionId: expect.any(String), names: ["web_fetch"], enabled: false }]);
});

test("new tools raise a banner: Add now accepts them from the next turn", async ({ page }) => {
  await seed(page, { pending: { [SID]: ["mcp__dummy__echo", "mcp__dummy__crash"] } });
  await boot(page);
  await openSession(page);
  const banner = page.locator("#toolsBanner");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("2 new tools");
  await expect(banner).toContainText("dummy");
  await expect(banner).toContainText(/cache/i);
  await banner.locator("[data-tools-accept]").click();
  await expect(banner).toBeHidden();
  await expect(page.locator("#toolsPendingChip")).toBeHidden();
  await expect(page.locator("#toastText")).toContainText(/next turn/i);
  expect(await calls(page, "toolsAccept")).toEqual([
    { cwd: CWD, sessionId: SID, names: ["mcp__dummy__echo", "mcp__dummy__crash"] },
  ]);
});

test("Later hides the banner behind a chip that brings it back", async ({ page }) => {
  await seed(page, { pending: { [SID]: ["mcp__dummy__echo"] } });
  await boot(page);
  await openSession(page);
  const banner = page.locator("#toolsBanner");
  await expect(banner).toContainText("1 new tool");
  await banner.locator("[data-tools-later]").click();
  await expect(banner).toBeHidden();
  const chip = page.locator("#toolsPendingChip");
  await expect(chip).toBeVisible();
  await expect(chip).toContainText("1 new");
  await chip.click();
  await expect(banner).toBeVisible();
  expect(await calls(page, "toolsAccept")).toEqual([]);
});

test("a tools/pending event raises the banner for the open session only", async ({ page }) => {
  await seed(page);
  await boot(page);
  await openSession(page);
  await expect(page.locator("#toolsBanner")).toBeHidden();
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eHub.emit({ event: "tools/pending", sessionId: "sess-other-2", pending: ["mcp__dummy__echo"] });
  });
  await page.waitForTimeout(100);
  await expect(page.locator("#toolsBanner")).toBeHidden();
  await page.evaluate((sid) => {
    const w = window as any;
    w.__e2eTools.pending = { [sid]: ["mcp__dummy__echo"] };
    w.__e2eHub.emit({ event: "tools/pending", sessionId: sid, pending: ["mcp__dummy__echo"] });
  }, SID);
  await expect(page.locator("#toolsBanner")).toContainText("1 new tool");
});

test("pasting a command into Settings → MCP previews and adds the server", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eMcp = { servers: [] };
  });
  await seed(page);
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavMcp");
  await page.click("[data-mcp-paste]");
  const box = page.locator(".mcp-paste");
  await box.locator("textarea").fill("npx -y @modelcontextprotocol/server-github");
  await box.locator("[data-mcp-parse]").click();
  const row = box.locator('.mcp-preview[data-index="0"]');
  await expect(row.locator("[data-field=name]")).toHaveValue("github");
  await expect(row).toContainText("npx -y @modelcontextprotocol/server-github");
  await row.locator("[data-field=name]").fill("gh");
  await box.locator("[data-mcp-add]").click();
  await expect(page.locator('.mcp-server[data-mcp-server="gh"]')).toBeVisible();
  await expect(page.locator(".mcp-paste")).toHaveCount(0);
  expect(await mcpCalls(page, "mcpAdd")).toEqual([
    {
      scope: "global",
      cwd: CWD,
      servers: [{ name: "gh", config: { command: "npx", args: ["-y", "@modelcontextprotocol/server-github"] } }],
      replace: false,
    },
  ]);
});

test("adding a server whose name exists offers Replace", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eMcp = {
      servers: [{ name: "github", scope: "folder", transport: "stdio", state: "ready", restarts: 0, tools: [] }],
      hasFolderConfig: true,
      trusted: true,
    };
  });
  await seed(page);
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavMcp");
  await page.click("[data-mcp-paste]");
  const box = page.locator(".mcp-paste");
  await box.locator("textarea").fill("npx -y @modelcontextprotocol/server-github");
  await box.locator("[data-mcp-parse]").click();
  await box.locator("[data-field=scope]").selectOption("folder");
  await box.locator("[data-mcp-add]").click();
  await expect(box.locator(".mcp-editor-error")).toContainText("already exists");
  await box.locator("[data-mcp-replace]").click();
  await expect(page.locator(".mcp-paste")).toHaveCount(0);
  const adds = await mcpCalls(page, "mcpAdd");
  expect(adds.map((a: any) => [a.scope, a.replace])).toEqual([
    ["folder", false],
    ["folder", true],
  ]);
});

test("a bad paste shows the parse error inline", async ({ page }) => {
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavMcp");
  await page.click("[data-mcp-paste]");
  const box = page.locator(".mcp-paste");
  await box.locator("textarea").fill("{ nope");
  await box.locator("[data-mcp-parse]").click();
  await expect(box.locator(".mcp-editor-error")).toContainText("nothing to add");
  await expect(box.locator(".mcp-preview")).toHaveCount(0);
});
