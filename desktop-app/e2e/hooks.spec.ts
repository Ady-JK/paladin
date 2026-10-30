// hooks.spec.ts covers Settings → Hooks (adding a hooks.json, toggling a
// handler, folder trust, Codex/Claude files) and the quiet hook row a
// turn shows when a hook acts.
import { test, expect, type Page } from "@playwright/test";

const calls = (page: Page) => page.evaluate(() => ((window as any).__e2eHooksCalls || []) as any[]);

async function openHooks(page: Page): Promise<void> {
  await page.click("#settingsBtn");
  await page.click("#settingsNavHooks");
  await expect(page.locator("#settingsPaneHooks")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
});

test("adding a global hooks.json lists its handlers and toggles one off", async ({ page }) => {
  await page.goto("/");
  await openHooks(page);
  const global = page.locator('[data-hook-section="global"]');
  await global.locator("[data-hook-edit]").click();
  const area = global.locator(".hook-editor textarea");
  await expect(area).toHaveValue(/PreToolUse/);
  await area.fill(
    JSON.stringify({
      hooks: { PreToolUse: [{ matcher: "shell", hooks: [{ type: "command", command: "./guard.sh" }] }] },
    }),
  );
  await global.locator("[data-hook-save]").click();

  const row = global.locator(".hook-handler");
  await expect(row).toHaveCount(1);
  await expect(row.locator(".hook-event")).toHaveText("PreToolUse");
  await expect(row.locator(".hook-matcher")).toHaveText("shell");
  await expect(row.locator(".hook-command")).toHaveText("./guard.sh");
  await expect(global.locator(".hook-editor")).toHaveCount(0);

  await row.locator("[data-hook-toggle]").click();
  await expect(row.locator("[data-hook-toggle]")).toHaveAttribute("aria-checked", "false");
  const toggle = (await calls(page)).find((c) => c.cmd === "hooksToggle");
  expect(toggle.args).toMatchObject({ key: expect.stringContaining("PreToolUse"), enabled: false });
});

test("invalid JSON stays in the editor with the error", async ({ page }) => {
  await page.goto("/");
  await openHooks(page);
  const global = page.locator('[data-hook-section="global"]');
  await global.locator("[data-hook-edit]").click();
  await global.locator(".hook-editor textarea").fill("{ nope");
  await global.locator("[data-hook-save]").click();
  await expect(global.locator(".mcp-editor-error")).toContainText("invalid JSON");
  await expect(global.locator(".hook-editor textarea")).toHaveValue("{ nope");
});

test("a folder's Claude hooks stay off until trusted", async ({ page }) => {
  await page.addInitScript(() => {
    const cwd = "/home/tester/project";
    (window as any).__e2eHooks = {
      sources: [
        {
          path: "/data/config/hooks.json",
          scope: "global",
          format: "paladin",
          exists: false,
          editable: true,
          trusted: true,
          text: "",
          handlers: [],
        },
        {
          path: cwd + "/.paladin/hooks.json",
          scope: "folder",
          format: "paladin",
          exists: false,
          editable: true,
          trusted: false,
          text: "",
          handlers: [],
        },
        {
          path: cwd + "/.codex/hooks.json",
          scope: "folder",
          format: "codex",
          exists: false,
          editable: false,
          trusted: false,
          text: "",
          handlers: [],
        },
        {
          path: cwd + "/.claude/settings.json",
          scope: "folder",
          format: "claude",
          exists: true,
          editable: false,
          trusted: false,
          text: "{}",
          warnings: ['unknown hook event "Notification" ignored'],
          handlers: [{ key: "c#Stop/0/0", event: "Stop", matcher: "", command: "./check.sh", enabled: true }],
        },
      ],
    };
  });
  await page.goto("/");
  await openHooks(page);
  const folder = page.locator('[data-hook-section="folder"]');
  // Missing Codex file is hidden; the Paladin one shows so it can be added.
  await expect(folder.locator('[data-format="codex"]')).toHaveCount(0);
  await expect(folder.locator('[data-format="paladin"] [data-hook-edit]')).toHaveText("Add");

  const claude = folder.locator('[data-format="claude"]');
  await expect(claude.locator(".hook-format")).toHaveText("Claude Code");
  await expect(claude.locator(".hook-warning")).toContainText("Notification");
  await expect(claude.locator(".hook-untrusted")).toBeVisible();
  await expect(claude.locator(".hook-handler")).toHaveAttribute("data-live", "false");
  await expect(claude.locator("[data-hook-edit]")).toHaveCount(0);

  await claude.locator("[data-hook-trust]").click();
  await expect(claude.locator("[data-hook-revoke]")).toBeVisible();
  await expect(claude.locator(".hook-untrusted")).toHaveCount(0);
  await expect(claude.locator(".hook-handler")).toHaveAttribute("data-live", "true");
  const trust = (await calls(page)).find((c) => c.cmd === "hooksTrust");
  expect(trust.args).toMatchObject({ path: "/home/tester/project/.claude/settings.json", trust: true });
});

test("a turn shows a quiet row when a hook acts", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/hook",
        hook: { event: "PreToolUse", command: "./guard.sh", outcome: "blocked", message: "no rm -rf here" },
      },
      { event: "chat/hook", hook: { event: "PostToolUse", command: "./lint.sh", outcome: "context" } },
      { event: "chat/delta", delta: "done" },
    ];
  });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
  await page.fill("#promptInput", "clean up");
  await page.click("#sendBtn");
  const rows = page.locator("#messages .hook-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toHaveAttribute("data-outcome", "blocked");
  await expect(rows.nth(0)).toContainText("PreToolUse hook blocked");
  await expect(rows.nth(0)).toContainText("no rm -rf here");
  await expect(rows.nth(0)).toHaveAttribute("title", "./guard.sh");
  await expect(rows.nth(1)).toContainText("PostToolUse hook added context");
  await expect(page.locator("#messages")).toContainText("done");
});
