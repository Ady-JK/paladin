// pluginBridge.spec.ts covers the plugin bridge's allow-list and the
// host-rendered approval card.
import { expect, test } from "@playwright/test";
import { storeKeyValid, toolAllowed } from "../electron/pluginPolicy";

test("a view may call its own tools and read-only built-ins", () => {
  const read = ["invokeTool"];
  expect(toolAllowed(read, "board", "extension__board__lookup")).toBe(true);
  expect(toolAllowed(read, "board", "read")).toBe(true);
  expect(toolAllowed(read, "board", "grep")).toBe(true);
  expect(toolAllowed(read, "board", "extension__other__lookup")).toBe(false);
  expect(toolAllowed(read, "board", "write")).toBe(false);
  expect(toolAllowed(read, "board", "shell")).toBe(false);
  expect(toolAllowed([], "board", "read")).toBe(false);
});

test("only a destructive view reaches changing tools", () => {
  const danger = ["invokeToolDestructive"];
  expect(toolAllowed(danger, "board", "write")).toBe(true);
  expect(toolAllowed(danger, "board", "shell")).toBe(true);
  expect(toolAllowed(danger, "board", "extension__other__lookup")).toBe(true);
  expect(toolAllowed(danger, "board", "read")).toBe(true);
});

test("store keys are bounded and filesystem-safe", () => {
  expect(storeKeyValid("board.selected")).toBe(true);
  expect(storeKeyValid("a-b_c.1")).toBe(true);
  expect(storeKeyValid("../secrets")).toBe(false);
  expect(storeKeyValid("a/b")).toBe(false);
  expect(storeKeyValid("")).toBe(false);
  expect(storeKeyValid("x".repeat(65))).toBe(false);
});

test("a plugin approval renders in the host and answers the broker", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eApprovalWaits = { "plugin-1": () => undefined };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.evaluate(() => {
    (window as any).__e2eHub.emit({
      event: "plugin/approval",
      seq: 1,
      sessionId: "sess-notify-1",
      turnId: "",
      approval: {
        id: "plugin-1",
        tool: "extension__board__lookup",
        kind: "extension",
        state: "pending",
        choices: ["once", "session", "deny"],
        remember: [{ tool: "extension", pattern: "board/lookup", action: "allow" }],
        reasons: ["extension tool: it runs local code without the sandbox"],
        args: '{"key":"PAL-1"}',
      },
    });
  });
  const layer = page.locator(".plugin-approval-layer");
  await expect(layer).toBeVisible();
  await expect(layer).toContainText("A plugin panel wants to run a tool");
  await expect(layer).toContainText("Run this extension tool?");
  await page.locator('.plugin-approval-layer [data-approve="once"]').click();
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers || []);
  expect(answers.at(-1)).toMatchObject({ sessionId: "sess-notify-1", callId: "plugin-1", decision: "once" });
  await expect(layer).toBeHidden({ timeout: 3000 });
});

test("denying a plugin approval reports the denial and leaves the transcript alone", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eApprovalWaits = { "plugin-2": () => undefined };
  });
  await page.goto("/");
  await page.evaluate(() => {
    (window as any).__e2eHub.emit({
      event: "plugin/approval",
      seq: 1,
      sessionId: "sess-notify-1",
      turnId: "",
      approval: {
        id: "plugin-2",
        tool: "extension__board__lookup",
        kind: "extension",
        state: "pending",
        choices: ["once", "deny"],
        reasons: ["extension tool: it runs local code without the sandbox"],
      },
    });
  });
  await page.locator('.plugin-approval-layer [data-approve="deny"]').click();
  const answers = await page.evaluate(() => (window as any).__e2eApprovalAnswers || []);
  expect(answers.at(-1)).toMatchObject({ callId: "plugin-2", decision: "deny" });
  await expect(page.locator("#messages .plugin-approval-layer")).toHaveCount(0);
});
