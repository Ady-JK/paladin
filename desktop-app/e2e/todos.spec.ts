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

test("a resumed session shows its plan in the hat above the composer", async ({ page }) => {
  // Open the stub session: loadHistory + refreshTodos repopulate the hat.
  await page.click("#sessionList [data-session]");
  const hat = page.locator("#todoHat");
  await expect(hat).toBeVisible();
  await expect(hat).toContainText("1/3 finished");
  await expect(hat).toContainText("Build the hat");

  // The hat sits above the textarea, inside the composer card.
  const hatBox = await hat.boundingBox();
  const inputBox = await page.locator("#promptInput").boundingBox();
  expect(hatBox && inputBox && hatBox.y < inputBox.y).toBeTruthy();

  // Clicking the bar opens the list upwards with state pills on the right.
  await hat.locator('[data-todo="bar"]').click();
  const list = hat.locator('[data-todo="list"]');
  await expect(list).toBeVisible();
  await expect(list).toContainText("Plan the work");
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
    const paladin = (window as any).paladin;
    const prev = paladin.chat.bind(paladin);
    paladin.chat = async (params: any, handlers: any) => {
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
