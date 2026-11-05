// extensions.spec.ts covers local extension plugins in the renderer:
// Settings → Tools lists each enabled plugin with its warm runner and its
// load error, "Reload plugins" calls toolsReload, an extension rule can be
// picked from the installed plugins, and an extension approval card names
// the plugin and its tool.
import { test, expect, type Page } from "@playwright/test";

const CWD = "/home/tester/project";

const TOOLS = [
  { name: "read_file", description: "Read a file", source: "builtin", readOnly: true },
  { name: "extension__jira__lookup", description: "Look up an issue", source: "extension" },
  { name: "extension__jira__create", description: "Create an issue", source: "extension" },
  { name: "extension__notes__save", description: "Save a note", source: "extension" },
];

const EXTENSIONS = [
  {
    name: "jira",
    kind: "plugin",
    path: "/data/config/extensions/jira",
    permissions: ["network"],
    tools: ["extension__jira__lookup", "extension__jira__create"],
  },
  { name: "notes", kind: "plugin", path: "/data/config/extensions/notes", tools: ["extension__notes__save"] },
  { name: "legacy", kind: "file", path: "/data/config/extensions/legacy.ts", error: "cannot find module 'left-pad'" },
];

async function boot(page: Page, seed: Record<string, unknown> = {}) {
  await page.addInitScript(
    (seed) => {
      (window as any).__e2eTools = seed;
    },
    { tools: TOOLS, extensions: EXTENSIONS, ...seed },
  );
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
}

async function openToolsPane(page: Page) {
  await page.click("#settingsBtn");
  await page.click("#settingsNavTools");
  await expect(page.locator("#settingsPaneTools")).toBeVisible();
}

async function calls(page: Page, name: string) {
  return page.evaluate(
    (name) => ((window as any).__e2eToolsCalls || []).filter((c: any) => c.cmd === name).map((c: any) => c.args),
    name,
  );
}

test("Settings → Tools lists each extension plugin with its runner and its error", async ({ page }) => {
  await boot(page, {
    runners: [{ plugin: "jira", warm: true, pid: 4242, node: "/usr/bin/node", inflight: 1, generation: 1 }],
  });
  await openToolsPane(page);
  const section = page.locator('#settingsPaneTools .tsw-group[data-group="plugins"]');
  await expect(section).toContainText("Extensions");
  const jira = section.locator('.tsw-ext[data-plugin="jira"]');
  await expect(jira).toContainText("jira");
  await expect(jira).toContainText("uses the network");
  await expect(jira.locator(".tsw-ext-state")).toContainText("running");
  await expect(jira.locator(".tsw-ext-state")).toContainText("1 call");
  // No runner yet: it starts on the first call.
  await expect(section.locator('.tsw-ext[data-plugin="notes"] .tsw-ext-state')).toHaveText("idle");
  // A plugin that could not load shows why, and is not switched on.
  const legacy = section.locator('.tsw-ext[data-plugin="legacy"]');
  await expect(legacy).toHaveAttribute("data-error", "true");
  await expect(legacy.locator(".tsw-ext-error")).toContainText("left-pad");
  await expect(legacy).toContainText("legacy file");
  // The extension tools keep their own group below.
  const group = page.locator('#settingsPaneTools .tsw-group[data-group="extension"]');
  await expect(group.locator(".tsw-row")).toHaveCount(3);
});

test("Reload plugins re-reads the files and drops the warm runners", async ({ page }) => {
  await boot(page, {
    dropsRunners: true,
    runners: [{ plugin: "jira", warm: true, pid: 4242, node: "/usr/bin/node" }],
  });
  await openToolsPane(page);
  const jira = page.locator('#settingsPaneTools .tsw-ext[data-plugin="jira"]');
  await expect(jira.locator(".tsw-ext-state")).toContainText("running");
  await page.click("[data-tsw-reload]");
  await expect(jira.locator(".tsw-ext-state")).toHaveText("idle");
  await expect(page.locator("#toastText")).toContainText("Reloaded 3 extensions");
  await expect(page.locator("#toastText")).toContainText("1 could not load");
  expect(await calls(page, "toolsReload")).toEqual([{ cwd: CWD, sessionId: null }]);
});

test("an extension rule offers the installed plugins as patterns", async ({ page }) => {
  await boot(page);
  await page.click("#settingsBtn");
  await page.click("#settingsNavPermissions");
  const section = page.locator('#settingsPanePermissions [data-rules="global"]');
  await section.locator("[data-rule-tool]").selectOption("extension");
  await expect(section.locator("[data-rule-pattern]")).toHaveAttribute("placeholder", /plugin {3}or {3}plugin\/tool/);
  // The datalist is filled from toolsList once extension is picked: one
  // entry per plugin, plus one per tool of that plugin.
  const options = section.locator("#permRuleExt option");
  await expect(options).toHaveCount(5);
  await expect(options.nth(0)).toHaveAttribute("value", "jira/*");
  await expect(options.nth(1)).toHaveAttribute("value", "jira/lookup");
  await expect(options.nth(2)).toHaveAttribute("value", "jira/create");
  await expect(options.nth(3)).toHaveAttribute("value", "notes/*");
  await expect(options.nth(4)).toHaveAttribute("value", "notes/save");

  await section.locator("[data-rule-pattern]").fill("jira/lookup");
  await section.locator("[data-rule-add]").click();
  await expect(section.locator(".perm-rule-pattern")).toHaveText("jira/lookup");
});

test("a plugin that loads late shows up after a reload", async ({ page }) => {
  await boot(page, {
    extensions: EXTENSIONS.filter((e) => e.name !== "notes"),
    // The reload picks up the plugin that was added on disk meanwhile.
    onReload: true,
  });
  await openToolsPane(page);
  await expect(page.locator('#settingsPaneTools .tsw-ext[data-plugin="notes"]')).toHaveCount(0);
  await page.evaluate((all) => {
    (window as any).__e2eTools.extensions = all;
  }, EXTENSIONS);
  await page.click("[data-tsw-reload]");
  await expect(page.locator('#settingsPaneTools .tsw-ext[data-plugin="notes"]')).toBeVisible();
});

test("an extension approval card names the plugin and its tool", async ({ page }) => {
  await boot(page);
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/tool",
        tool: { id: "x1", name: "extension__jira__lookup", arguments: '{"q":"PAL-1"}', result: "", status: "running" },
      },
      {
        event: "chat/approval",
        approval: {
          id: "x1",
          tool: "extension__jira__lookup",
          kind: "extension",
          args: '{"q":"PAL-1"}',
          reasons: ["extension tool: it runs local code without the sandbox"],
          choices: ["once", "session", "folder", "global", "deny"],
          remember: [{ tool: "extension", pattern: "jira/lookup", action: "allow" }],
          state: "pending",
        },
      },
    ];
  });
  await page.fill("#promptInput", "look up PAL-1");
  await page.click("#sendBtn");
  const card = page.locator('.approval-card[data-approval="x1"]');
  await expect(card).toContainText("Run this extension tool?");
  await expect(card.locator(".approval-mcp-server")).toHaveText("jira");
  await expect(card.locator(".approval-mcp-tool")).toHaveText("lookup");
  await expect(card.locator('[data-approve="session"]')).toContainText("jira/lookup");
  await card.locator('[data-approve="session"]').click();
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers).toEqual([{ sessionId: "sess-notify-1", callId: "x1", decision: "session" }]);
  // The rule the card offered, and that the answer saves, names the plugin
  // and the tool.
  await expect(card.locator(".approval-receipt")).toContainText("Allowed for this session");
  await expect(card).toHaveClass(/is-resolved/);
});
