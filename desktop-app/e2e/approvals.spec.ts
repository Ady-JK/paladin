// approvals.spec.ts covers the approval UI: the in-chat card the guard (and
// Codex or Claude, for their own requests) raises, and the sidebar/composer
// waiting state.
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
    command: "ngrok http 3000",
    reasons: ["would expose a local port or service beyond this machine"],
    choices: ["once", "deny"],
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
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("an approval card shows the command and allows it once", async ({ page }) => {
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
  await expect(card).toContainText("ngrok http 3000");
  await expect(card).toContainText("expose a local port");
  await expect(card.locator("[data-approve]")).toHaveCount(2);
  await expect(card.locator('[data-approve="once"]')).toContainText("Allow once");
  await expect(page.locator("#contextBox #workingBar")).toContainText("Waiting for approval");
  await expect(page.locator('.session-row[data-live="approval"]')).toHaveCount(1);

  await card.locator('[data-approve="once"]').click();
  await expect(card).toHaveClass(/is-resolved/);
  await expect(card).toContainText(/Allowed once/i);
  await expect(card.locator("[data-approve]")).toHaveCount(0);
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers).toEqual([{ sessionId: "sess-notify-1", callId: "c1", decision: "once" }]);
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
      }),
    },
  ]);
  await send(page, "clean");
  const card = page.locator('.approval-card[data-approval="c1"]');
  await expect(card).toBeVisible();
  await expect(card).toHaveClass(/is-destructive/);
  await expect(card).toHaveCSS("display", "block");
  await expect(card.locator("[data-approve]")).toHaveCount(2);
  await expect(card.locator('[data-approve="once"]')).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(card).toContainText(/Denied/);
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers);
  expect(answers[0].decision).toBe("deny");
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
        state: "pending",
      },
    });
  });
  const card = page.locator('.approval-card[data-approval="c9"]');
  await expect(card).toContainText("/etc/hosts");
  await expect(card.locator('[data-approve="global"]')).toContainText(/always allow everywhere/i);
});

test("a read card from a turn started elsewhere shows up live", async ({ page }) => {
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
        state: "pending",
      },
    });
  });
  const card = page.locator('.approval-card[data-approval="c10"]');
  await expect(card).toContainText(/Read this file\?/);
  await expect(card).toContainText("/opt/app/conf.yml");
  await expect(card.locator('[data-approve="global"]')).toContainText(/always allow everywhere/i);
});
