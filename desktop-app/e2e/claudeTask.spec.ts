// claudeTask.spec.ts: a Claude Code background task is one quiet row with a
// small auto-approved mark, not a raw "Called with / Returned" card and not
// a second line repeating the command.
import { expect, test } from "@playwright/test";

const COMMAND = "source ~/.nvm/nvm.sh >/dev/null 2>&1; ./node_modules/.bin/playwright test e2e/chat.spec.ts | tail -25";
const ARGS = JSON.stringify({
  task_id: "bpfx382n0",
  description: COMMAND,
  task_type: "local_bash",
  tool_use_id: "toolu_01H8TcuqfwAkAjFRdVRosKtn",
});

test("a Claude background task shows one compact row with an auto-approved mark", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    ([args]) => {
      const w = window as any;
      w.__e2eRunTurn = async (_params: unknown, handlers: any) => {
        handlers.onTool({
          id: "claude-task-1",
          name: "claude_task",
          arguments: args,
          result: '{"status":"running"}',
          status: "running",
        });
        await new Promise<void>((resolve) => (w.__release = resolve));
        handlers.onTool({
          id: "claude-task-1",
          name: "claude_task",
          arguments: args,
          result: '{"status":"completed","output":"12 passed"}',
          status: "ok",
        });
        return { reply: "Done.", stats: { durationMs: 1000, tokens: 10, genMs: 100 } };
      };
    },
    [ARGS],
  );
  await page.goto("/");
  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");

  const row = page.locator('#messages .tool-row[data-tool="claude_task"]');
  await expect(row).toBeAttached();
  const head = row.locator("> .tool-row-head");
  // The row reads as a task, not as a raw "Calling claude_task".
  await expect(head).not.toContainText("claude_task");
  await expect(head).toContainText("Running");
  await expect(head).toContainText("background");
  // One small mark says it was approved without asking; it carries no command text.
  const mark = head.locator(".tool-auto");
  await expect(mark).toHaveAttribute("title", /Auto-approved/);
  expect((await mark.innerText()).trim()).toBe("");

  // Opened, it shows the command once and none of the internal ids.
  await head.click();
  const body = row.locator(".tool-body");
  await expect(body).toBeVisible();
  await expect(body).not.toContainText("task_id");
  await expect(body).not.toContainText("tool_use_id");
  await expect(body).not.toContainText("Called with");
  // The command is read once while open: in the body, not again in the head.
  await expect(row.getByText("playwright test e2e/chat.spec.ts").filter({ visible: true })).toHaveCount(1);

  await page.evaluate(() => (window as any).__release());
  await expect(head).toContainText("Ran");
  await expect(body).toContainText("12 passed");
  // No other row in the transcript repeats the command.
  await expect(page.locator("#messages .notice-row")).toHaveCount(0);
});
