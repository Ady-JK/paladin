// mcp.spec.ts covers the MCP UI: Settings → MCP (server cards, trust,
// config editor, live status), the composer's MCP chip, /mcp, MCP
// approval cards, MCP tool rows and mcp permission rules.
import { test, expect, type Page } from "@playwright/test";

const CWD = "/home/tester/project";

function dummy(over: Record<string, unknown> = {}) {
  return {
    name: "dummy",
    scope: "global",
    transport: "stdio",
    state: "ready",
    restarts: 0,
    server: { name: "mcp-dummy", version: "1.0" },
    tools: [
      { name: "echo", exposed: "mcp__dummy__echo", description: "Echo text back", readOnly: true, destructive: false },
      { name: "slow", exposed: "mcp__dummy__slow", readOnly: false, destructive: false },
      { name: "crash", exposed: "mcp__dummy__crash", readOnly: false, destructive: true },
    ],
    ...over,
  };
}

function broken(over: Record<string, unknown> = {}) {
  return {
    name: "broken",
    scope: "global",
    transport: "stdio",
    state: "failed",
    error: 'exec: "nope": executable file not found in $PATH',
    stderr: "boom: missing token",
    restarts: 0,
    tools: [],
    ...over,
  };
}

async function seed(page: Page, mcp: Record<string, unknown>) {
  await page.addInitScript((mcp) => {
    (window as any).__e2eMcp = mcp;
  }, mcp);
}

async function boot(page: Page) {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
}

async function openPane(page: Page) {
  await page.click("#settingsBtn");
  await page.click("#settingsNavMcp");
  await expect(page.locator("#settingsPaneMcp")).toBeVisible();
}

async function calls(page: Page, name: string) {
  return page.evaluate(
    (name) => ((window as any).__e2eMcpCalls || []).filter((c: any) => c.cmd === name).map((c: any) => c.args),
    name,
  );
}

test("Settings → MCP lists servers with state, tools and failures", async ({ page }) => {
  await seed(page, { servers: [dummy(), broken()] });
  await boot(page);
  await openPane(page);
  const card = page.locator('.mcp-server[data-mcp-server="dummy"]');
  await expect(card).toHaveAttribute("data-state", "ready");
  await expect(card).toContainText("mcp-dummy");
  await expect(card).toContainText("3 tools");
  await expect(card.locator('.mcp-tool[data-ro="true"]')).toHaveText("echo");
  await expect(card.locator('.mcp-tool[data-destructive="true"]')).toHaveText("crash");

  const bad = page.locator('.mcp-server[data-mcp-server="broken"]');
  await expect(bad).toHaveAttribute("data-state", "failed");
  await expect(bad.locator(".mcp-error")).toContainText("executable file not found");
  await bad.locator(".mcp-log summary").click();
  await expect(bad.locator(".mcp-log pre")).toContainText("boom: missing token");

  await bad.locator("[data-mcp-restart]").click();
  await expect(bad).toHaveAttribute("data-state", "ready");
  expect(await calls(page, "mcpRestart")).toEqual([{ scope: "global", cwd: CWD, name: "broken" }]);
});

test("the switch disables and re-enables a server", async ({ page }) => {
  await seed(page, { servers: [dummy()] });
  await boot(page);
  await openPane(page);
  const card = page.locator('.mcp-server[data-mcp-server="dummy"]');
  await card.locator("[data-mcp-toggle]").click();
  await expect(card).toHaveAttribute("data-state", "disabled");
  await card.locator("[data-mcp-toggle]").click();
  await expect(card).toHaveAttribute("data-state", "ready");
  expect(await calls(page, "mcpSetEnabled")).toEqual([
    { scope: "global", cwd: CWD, name: "dummy", enabled: false },
    { scope: "global", cwd: CWD, name: "dummy", enabled: true },
  ]);
});

test("an untrusted folder config asks for trust from the chip and the pane", async ({ page }) => {
  await seed(page, {
    hasFolderConfig: true,
    trusted: false,
    servers: [
      dummy(),
      dummy({ name: "local", scope: "folder", folder: CWD, state: "untrusted", tools: [], server: null }),
    ],
  });
  await boot(page);
  const chip = page.locator("#mcpChip");
  await expect(chip).toBeVisible();
  await expect(chip).toHaveAttribute("data-tone", "warn");
  await chip.click();
  await expect(page.locator("#settingsPaneMcp")).toBeVisible();
  const banner = page.locator(".mcp-trust");
  await expect(banner).toContainText("local");
  await banner.locator("[data-mcp-trust]").click();
  await expect(page.locator('.mcp-server[data-mcp-server="local"]')).toHaveAttribute("data-state", "ready");
  await expect(page.locator(".mcp-trust")).toHaveCount(0);
  expect(await calls(page, "mcpTrust")).toEqual([{ cwd: CWD, trust: true }]);
  await expect(chip).toBeHidden();
});

test("a global shadowed by a folder server is marked", async ({ page }) => {
  await seed(page, {
    hasFolderConfig: true,
    trusted: true,
    servers: [dummy({ shadowed: true }), dummy({ scope: "folder", folder: CWD })],
  });
  await boot(page);
  await openPane(page);
  await expect(page.locator('[data-mcp-section="global"] .mcp-server[data-mcp-server="dummy"]')).toContainText(
    /replaced by this folder/i,
  );
  await expect(page.locator('[data-mcp-section="folder"] .mcp-server[data-mcp-server="dummy"]')).toHaveCount(1);
});

test("the config editor loads, rejects bad JSON inline and saves", async ({ page }) => {
  await seed(page, { servers: [] });
  await boot(page);
  await openPane(page);
  await expect(page.locator("#settingsPaneMcp")).toContainText(/No MCP servers/i);
  await page.click('[data-mcp-edit="global"]');
  const editor = page.locator('.mcp-editor[data-scope="global"] textarea');
  await expect(editor).toHaveValue(/mcpServers/);
  await editor.fill("{ nope");
  await page.click('.mcp-editor[data-scope="global"] [data-mcp-save]');
  await expect(page.locator('.mcp-editor[data-scope="global"] .mcp-editor-error')).toContainText(/invalid/i);

  const text = JSON.stringify({ mcpServers: { dummy: { command: "mcp-dummy" } } }, null, 2);
  await editor.fill(text);
  await page.click('.mcp-editor[data-scope="global"] [data-mcp-save]');
  await expect(page.locator(".mcp-editor")).toHaveCount(0);
  await expect(page.locator('.mcp-server[data-mcp-server="dummy"]')).toBeVisible();
  const saved = await calls(page, "mcpSetConfig");
  expect(saved.at(-1)).toEqual({ scope: "global", cwd: CWD, text });
});

test("mcp/status events update the pane and raise the failure chip", async ({ page }) => {
  await seed(page, { servers: [dummy()] });
  await boot(page);
  await expect(page.locator("#mcpChip")).toBeHidden();
  await openPane(page);
  await page.evaluate(
    (srv) => {
      const w = window as any;
      w.__e2eMcp.servers = [srv];
      w.__e2eHub.emit({ event: "mcp/status", server: srv });
    },
    broken({ name: "dummy", error: "MCP server crashed" }),
  );
  const card = page.locator('.mcp-server[data-mcp-server="dummy"]');
  await expect(card).toHaveAttribute("data-state", "failed");
  await expect(card.locator(".mcp-error")).toContainText("crashed");
  await expect(page.locator("#settingsNavMcp .mcp-nav-dot")).toBeVisible();
  await page.click("#settingsCloseBtn");
  const chip = page.locator("#mcpChip");
  await expect(chip).toBeVisible();
  await expect(chip).toHaveAttribute("data-tone", "error");
  await expect(chip).toContainText("1 failed");
});

test("/mcp opens Settings → MCP", async ({ page }) => {
  await seed(page, { servers: [dummy()] });
  await boot(page);
  await page.fill("#promptInput", "/mcp");
  await page.keyboard.press("Enter");
  await expect(page.locator("#settingsPaneMcp")).toBeVisible();
  await expect(page.locator('.mcp-server[data-mcp-server="dummy"]')).toBeVisible();
});

test("an MCP approval card shows server, tool and arguments", async ({ page }) => {
  await boot(page);
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/approval",
        approval: {
          id: "m1",
          tool: "mcp__dummy__slow",
          kind: "mcp",
          server: "dummy",
          mcpTool: "slow",
          args: JSON.stringify({ ms: 250, label: "nap" }),
          reasons: ["MCP tool without a read-only hint"],
          choices: ["once", "session", "folder", "deny"],
          remember: [{ tool: "mcp", pattern: "dummy/slow", action: "allow" }],
          state: "pending",
        },
      },
    ];
  });
  await page.fill("#promptInput", "go slow");
  await page.click("#sendBtn");
  const card = page.locator('.approval-card[data-approval="m1"]');
  await expect(card).toBeVisible();
  await expect(card).toHaveClass(/is-mcp/);
  await expect(card.locator(".approval-title")).toContainText(/MCP tool/i);
  await expect(card.locator(".approval-mcp-server")).toHaveText("dummy");
  await expect(card.locator(".approval-mcp-tool")).toHaveText("slow");
  await expect(card.locator(".approval-args .kv-key").first()).toHaveText("ms");
  await expect(card.locator(".approval-args")).toContainText("250");
  await expect(card.locator('[data-approve="folder"]')).toContainText("dummy/slow");
  await card.locator('[data-approve="once"]').click();
  await expect(card).toContainText(/Allowed once/);
  await expect(card.locator(".approval-receipt-cmd")).toContainText("dummy › slow");
});

test("an MCP tool row names the server and renders args and output", async ({ page }) => {
  await boot(page);
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/tool",
        tool: {
          id: "t1",
          name: "mcp__dummy__echo",
          arguments: JSON.stringify({ text: "hi there" }),
          result: "",
          status: "running",
        },
      },
      {
        event: "chat/tool",
        tool: {
          id: "t1",
          name: "mcp__dummy__echo",
          arguments: JSON.stringify({ text: "hi there" }),
          result: "echo: hi there",
          status: "ok",
        },
      },
    ];
  });
  await page.fill("#promptInput", "echo");
  await page.click("#sendBtn");
  const row = page.locator('.tool-row[data-tool="mcp__dummy__echo"]');
  await expect(row).toBeVisible();
  await expect(row.locator(".tool-server")).toHaveText("dummy");
  await expect(row.locator(".tool-detail")).toHaveText("echo");
  await expect(row.locator(".tool-row-head")).not.toContainText("mcp__");
  await row.locator(".tool-row-head").click();
  await expect(row.locator('[data-body="args"] .kv-key')).toHaveText("text");
  await expect(row.locator('[data-body="result"]')).toContainText("echo: hi there");
});

test("the permissions pane adds and labels mcp rules", async ({ page }) => {
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavPermissions");
  const global = page.locator('[data-rules="global"]');
  await global.locator("[data-rule-tool]").selectOption("mcp");
  await expect(global.locator("[data-rule-pattern]")).toHaveAttribute("placeholder", /server\/tool/);
  await global.locator("[data-rule-pattern]").fill("dummy / slow");
  await global.locator("[data-rule-add]").click();
  await expect(global.locator(".perm-rule")).toHaveCount(1);
  await expect(global.locator(".perm-rule-tool")).toHaveText("mcp");
  await expect(global.locator(".perm-rule-pattern")).toHaveText("dummy/slow");
});
