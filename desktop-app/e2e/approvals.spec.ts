// approvals.spec.ts covers the permission UI: the in-chat approval card,
// the composer's preset chip, the sidebar/working-bar waiting state and
// the Settings → Permissions pane.
import { test, expect, type Page } from "@playwright/test";

const SHELL = {
  id: "c1",
  name: "shell",
  arguments: JSON.stringify({ cmd: "npm install left-pad" }),
  result: "",
  status: "running",
};

function pending(over: Record<string, unknown> = {}) {
  return {
    id: "c1",
    tool: "shell",
    kind: "shell",
    command: "npm install left-pad",
    reasons: ["not on the safe list"],
    choices: ["once", "session", "folder", "deny"],
    remember: [{ tool: "shell", pattern: "npm install", action: "allow" }],
    state: "pending",
    ...over,
  };
}

async function script(page: Page, events: unknown[]) {
  await page.evaluate((events) => {
    (window as any).__e2eChatEvents = events;
  }, events);
}

async function send(page: Page, text = "install it") {
  await page.fill("#promptInput", text);
  await page.click("#sendBtn");
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("an approval card shows the command and allows it for the folder", async ({ page }) => {
  await script(page, [
    { event: "chat/tool", tool: SHELL },
    { event: "chat/approval", approval: pending() },
    {
      event: "chat/tool",
      tool: {
        ...SHELL,
        status: "ok",
        result: JSON.stringify({ status: "done", exit_code: 0, output: "added 1 package" }),
      },
    },
  ]);
  await send(page);
  const card = page.locator('.approval-card[data-approval="c1"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText("npm install left-pad");
  await expect(card).toContainText("not on the safe list");
  await expect(card.locator("[data-approve]")).toHaveCount(4);
  await expect(card.locator('[data-approve="folder"]')).toContainText("npm install");
  await expect(page.locator("#workingBar")).toContainText("Waiting for approval");
  await expect(page.locator('.session-row[data-live="approval"]')).toHaveCount(1);

  await card.locator('[data-approve="folder"]').click();
  await expect(card).toHaveClass(/is-resolved/);
  await expect(card).toContainText(/Always allowed in this folder/i);
  await expect(card.locator("[data-approve]")).toHaveCount(0);
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers).toEqual([{ sessionId: "sess-notify-1", callId: "c1", decision: "folder" }]);
  await expect(page.locator('.session-row[data-live="approval"]')).toHaveCount(0);
});

test("Escape denies, and a destructive call only offers once or deny", async ({ page }) => {
  await script(page, [
    {
      event: "chat/approval",
      approval: pending({
        command: "rm -rf build",
        destructive: true,
        reasons: ["destructive command"],
        choices: ["once", "deny"],
        remember: [],
      }),
    },
  ]);
  await send(page, "clean");
  const card = page.locator('.approval-card[data-approval="c1"]');
  await expect(card).toBeVisible();
  await expect(card).toHaveClass(/is-destructive/);
  await expect(card.locator("[data-approve]")).toHaveCount(2);
  await expect(card.locator('[data-approve="once"]')).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(card).toContainText(/Denied/);
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers[0].decision).toBe("deny");
});

test("an escalation card asks to rerun without the sandbox", async ({ page }) => {
  await script(page, [
    {
      event: "chat/approval",
      approval: pending({
        kind: "escalate",
        reasons: ["the sandbox blocked it"],
        choices: ["once", "deny"],
        remember: [],
      }),
    },
  ]);
  await send(page);
  const card = page.locator('.approval-card[data-approval="c1"]');
  await expect(card).toContainText(/without the sandbox/i);
  await card.locator('[data-approve="once"]').click();
  await expect(card).toContainText(/Allowed once/);
});

test("a sandbox block offers to grant the blocked folder", async ({ page }) => {
  const dir = "/Users/me/.nvm/versions/node/v24/lib/node_modules";
  await script(page, [
    {
      event: "chat/approval",
      approval: pending({
        kind: "escalate",
        command: "npm i -g pnpm",
        path: dir,
        reasons: ["blocked by the sandbox"],
        choices: ["session", "folder", "global", "once", "deny"],
        remember: [{ tool: "write", pattern: dir, action: "allow" }],
      }),
    },
  ]);
  await send(page);
  const card = page.locator('.approval-card[data-approval="c1"]');
  await expect(card).toHaveClass(/is-escalate/);
  await expect(card.locator(".approval-title")).toContainText(/sandbox blocked a write/i);
  await expect(card.locator(".approval-grant")).toContainText(dir);
  await expect(card.locator("[data-approve]")).toHaveCount(5);
  await expect(card.locator('[data-approve="session"]')).toBeFocused();
  await expect(card.locator('[data-approve="session"]')).toHaveText(/^Allow for this session/);
  await expect(card.locator('[data-approve="folder"]')).toContainText(/for this project/i);
  await expect(card.locator('[data-approve="global"]')).toContainText(/everywhere/i);
  await expect(card.locator('[data-approve="once"]')).toContainText(/without the sandbox/i);
  await card.locator('[data-approve="global"]').click();
  await expect(card).toContainText(/Always allowed everywhere/);
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers[0].decision).toBe("global");
});

test("a write approval from a turn started elsewhere shows up live", async ({ page }) => {
  await send(page, "hello");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  await page.evaluate(() => {
    const hub = (window as any).__e2eHub;
    hub.emit({
      event: "turn/start",
      sessionId: "sess-notify-1",
      turnId: "t-r",
      turn: { turnId: "t-r", sessionId: "sess-notify-1", message: "x", state: "running" },
    });
    hub.emit({
      event: "chat/approval",
      sessionId: "sess-notify-1",
      turnId: "t-r",
      approval: {
        id: "c9",
        tool: "write",
        kind: "write",
        path: "/etc/hosts",
        reasons: ["outside the workspace"],
        choices: ["once", "session", "folder", "global", "deny"],
        remember: [{ tool: "write", pattern: "/etc", action: "allow" }],
        state: "pending",
      },
    });
  });
  const card = page.locator('.approval-card[data-approval="c9"]');
  await expect(card).toContainText("/etc/hosts");
  await expect(card.locator('[data-approve="folder"]')).toContainText("/etc");
  await expect(card.locator('[data-approve="global"]')).toContainText(/writes to \/etc everywhere/i);
});

test("an outside read asks with a read grant", async ({ page }) => {
  await send(page, "hello");
  await expect(page.locator("#messages")).toContainText("Stub reply to: hello");
  await page.evaluate(() => {
    const hub = (window as any).__e2eHub;
    hub.emit({
      event: "turn/start",
      sessionId: "sess-notify-1",
      turnId: "t-rd",
      turn: { turnId: "t-rd", sessionId: "sess-notify-1", message: "x", state: "running" },
    });
    hub.emit({
      event: "chat/approval",
      sessionId: "sess-notify-1",
      turnId: "t-rd",
      approval: {
        id: "c10",
        tool: "read",
        kind: "read",
        path: "/opt/app/conf.yml",
        reasons: ["outside the workspace"],
        choices: ["once", "session", "folder", "global", "deny"],
        remember: [{ tool: "read", pattern: "/opt/app", action: "allow" }],
        state: "pending",
      },
    });
  });
  const card = page.locator('.approval-card[data-approval="c10"]');
  await expect(card).toContainText(/Read this file\?/);
  await expect(card).toContainText("/opt/app/conf.yml");
  await expect(card.locator('[data-approve="global"]')).toContainText(/reads in \/opt\/app everywhere/i);
});

test("the preset chip switches the session's permissions and is sent with the turn", async ({ page }) => {
  const chip = page.locator("#permChip");
  await expect(chip).toContainText("Auto");
  await chip.click();
  const menu = page.locator("#permMenu");
  await expect(menu).toBeVisible();
  await expect(menu.locator("[data-preset]")).toHaveCount(3);
  await menu.locator('[data-preset="full-access"]').click();
  await expect(menu).toBeHidden();
  await expect(chip).toContainText("Full access");
  await expect(chip).toHaveAttribute("data-preset", "full-access");
  await send(page, "go");
  await expect(page.locator("#messages")).toContainText("Stub reply to: go");
  const submits = await page.evaluate(() => (window as any).__e2eSubmits);
  expect(submits[submits.length - 1].permissions).toBe("full-access");
  // The choice sticks to the session across reloads.
  await page.reload();
  await expect(page.locator("#permChip")).toContainText("Auto");
  await page.locator('button.session-pick[data-session="sess-notify-1"]').click();
  await expect(page.locator("#permChip")).toContainText("Full access");
});

test("the chip warns when there is no OS sandbox", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2ePerms = { global: [], folders: {}, kind: "" };
  });
  await page.reload();
  await expect(page.locator("#permChip")).toHaveAttribute("data-sandbox", "none");
  await expect(page.locator("#permChip")).toHaveAttribute("title", /no OS sandbox/i);
});

test("settings → permissions edits global and folder rules", async ({ page }) => {
  await page.click("#settingsBtn");
  await page.click("#settingsNavPermissions");
  const pane = page.locator("#settingsPanePermissions");
  await expect(pane).toBeVisible();
  await expect(pane).toContainText(/Seatbelt/i);

  await pane.locator('[data-default-preset="read-only"]').click();
  await expect(pane.locator('[data-default-preset="read-only"]')).toHaveAttribute("aria-pressed", "true");

  // Write rules double as folders sandboxed commands may write.
  await expect(pane.locator(".perm-grant-note")).toContainText(/sandboxed commands/i);

  const global = pane.locator('[data-rules="global"]');
  await global.locator("[data-rule-tool]").selectOption("write");
  await expect(global.locator("[data-rule-pattern]")).toHaveAttribute("placeholder", /sandbox/i);
  await global.locator("[data-rule-tool]").selectOption("read");
  await expect(global.locator("[data-rule-pattern]")).toHaveAttribute("placeholder", /outside the workspace/i);
  await global.locator("[data-rule-tool]").selectOption("shell");
  await global.locator("[data-rule-pattern]").fill("npm test");
  await global.locator("[data-rule-action]").selectOption("allow");
  await global.locator("[data-rule-add]").click();
  await expect(global.locator("[data-rule-row]")).toHaveCount(1);
  await expect(global).toContainText("npm test");

  const folder = pane.locator('[data-rules="folder"]');
  await folder.locator("[data-rule-tool]").selectOption("shell");
  await folder.locator("[data-rule-pattern]").fill("git push");
  await folder.locator("[data-rule-action]").selectOption("deny");
  await folder.locator("[data-rule-pattern]").press("Enter");
  await expect(folder.locator("[data-rule-row]")).toHaveCount(1);
  await folder.locator("[data-rule-remove]").first().click();
  await expect(folder.locator("[data-rule-row]")).toHaveCount(0);

  const sets = await page.evaluate(() => (window as any).__e2ePermSets);
  expect(sets.map((s: any) => [s.scope, s.rules.length])).toEqual([
    ["global", 1],
    ["folder", 1],
    ["folder", 0],
  ]);
  expect(sets[0].rules[0]).toEqual({ tool: "shell", pattern: "npm test", action: "allow" });
  // The default preset applies to new sessions' chip.
  const stored = await page.evaluate(() => localStorage.getItem("paladin.permissions.default"));
  expect(stored).toBe("read-only");
});
