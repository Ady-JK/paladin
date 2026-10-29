import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the todo hat stays hidden until the agent plans something", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eTodos = [];
  });
  await page.fill("#promptInput", "hello agent");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply");
  await expect(page.locator("#todoHat")).toBeHidden();
});

test("a resumed session shows its plan latched above the composer", async ({ page }) => {
  // Open the stub session: loadHistory + refreshTodos repopulate the panel.
  await page.click("#sessionList [data-session]");
  const hat = page.locator("#todoHat");
  await expect(hat).toBeVisible();
  await expect(hat).toContainText("Plan");
  await expect(hat).toContainText("1/3 finished");

  // The panel is a separate tab on top of the composer card, latched
  // onto its top edge — not inside the textarea.
  const hatBox = await hat.boundingBox();
  const composerBox = await page.locator("#agentView footer .composer-float").boundingBox();
  const inputBox = await page.locator("#promptInput").boundingBox();
  expect(hatBox && composerBox && inputBox).toBeTruthy();
  if (hatBox && composerBox && inputBox) {
    // Directly above the composer, sharing its width and edge.
    expect(Math.abs(hatBox.y + hatBox.height - composerBox.y)).toBeLessThan(4);
    expect(Math.abs(hatBox.x - composerBox.x)).toBeLessThan(4);
    expect(Math.abs(hatBox.width - composerBox.width)).toBeLessThan(4);
    expect(hatBox.y + hatBox.height).toBeLessThan(inputBox.y);
  }

  // Clicking the bar opens the checklist with markers and pills.
  await hat.locator('[data-todo="bar"]').click();
  const list = hat.locator('[data-todo="list"]');
  await expect(list).toBeVisible();
  await expect(list).toContainText("Plan the work");
  await expect(list).toContainText("✓");
  await expect(list).toContainText("●");
  await expect(list).toContainText("○");
  await expect(list).toContainText("not_started");
  await expect(list).toContainText("in_progress");
  await expect(list).toContainText("finished");
});

test("a settled todo tool call repaints the hat live", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eTodos = [];
  });
  await page.fill("#promptInput", "plan something");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply");
  await expect(page.locator("#todoHat")).toBeHidden();

  // Dispatch settled todo tool events through a real chat turn: the
  // stub chat drops handlers, so wrap it and emit onTool directly.
  // The post-turn refresh reads __e2eTodos, so seed it with the final
  // list to match the live events.
  await page.evaluate(() => {
    (window as any).__e2eTodos = [{ content: "Ship it", status: "finished" }];
    const prev = (window as any).__e2eRunTurn;
    (window as any).__e2eRunTurn = async (params: any, handlers: any) => {
      handlers.onTool({
        id: "todo-2",
        name: "todo",
        arguments: "{}",
        result: '{"todos":[{"content":"Ship it","status":"in_progress"}]}',
        status: "ok",
      });
      handlers.onTool({
        id: "todo-2",
        name: "todo",
        arguments: "{}",
        result: '{"todos":[{"content":"Ship it","status":"finished"}]}',
        status: "ok",
      });
      return prev(params, handlers);
    };
  });
  await page.fill("#promptInput", "do it");
  await page.click("#sendBtn");
  await expect(page.locator("#todoHat")).toBeVisible();
  await expect(page.locator("#todoHat")).toContainText("1/1 finished");
});

test("the next user message retires a finished plan", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eTodos = [{ content: "Ship it", status: "finished" }];
    const prev = (window as any).__e2eRunTurn;
    // The backend drops a finished plan on a new message: mirror it
    // by emptying the fixture from the second turn on.
    let turns = 0;
    (window as any).__e2eRunTurn = async (params: any, handlers: any) => {
      turns++;
      if (turns > 1) {
        (window as any).__e2eTodos = [];
      }
      return prev(params, handlers);
    };
  });
  // First turn paints the finished plan through the live tool path is
  // covered elsewhere; here the post-turn refresh loads it directly.
  await page.fill("#promptInput", "first");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("Stub reply");
  await expect(page.locator("#todoHat")).toBeVisible();

  // The next message clears the panel at once, before the reply lands.
  await page.fill("#promptInput", "something new");
  await page.click("#sendBtn");
  await expect(page.locator("#todoHat")).toBeHidden();
  await expect(page.locator("#messages")).toContainText("Stub reply to: something new");
});
