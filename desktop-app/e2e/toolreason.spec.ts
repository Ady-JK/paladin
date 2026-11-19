// toolreason.spec.ts covers the reason steps inside an expanded tool
// group: each call shows the model's one-line reason with its tool icon,
// and clicking it opens the full tool row. A call without a reason
// shows its tool row directly.
import { test, expect, type Page } from "@playwright/test";

type Tool = { id: string; name: string; arguments: string; result: string; status: string };

const tool = (id: string, name: string, args: unknown, status = "ok", result: unknown = { ok: true }): Tool => ({
  id,
  name,
  arguments: JSON.stringify(args),
  result: JSON.stringify(result),
  status,
});

async function setup(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__h = handlers;
        w.__done = () => resolve({ sessionId: "", reply: "", stopped: false });
      });
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__h));
}

async function emit(page: Page, steps: Array<string | Tool>): Promise<void> {
  await page.evaluate((list) => {
    const h = (window as any).__h;
    for (const s of list) {
      if (typeof s === "string") h.onDelta(s);
      else h.onTool(s);
    }
  }, steps);
}

const run = (page: Page) => page.locator("#messages [data-tool-run]").first();

test("an open group lists each call's reason and a click reveals the full tool", async ({ page }) => {
  await setup(page);
  await emit(page, [
    tool("1", "shell", { command: "make lint", reason: "Running lint to check the staticcheck fix" }),
    tool("2", "read", { path: "src/conf.py", reason: "Reading conf.py to find where timeouts are set" }),
    tool("3", "shell", { command: "go test ./...", reason: "Running the Go tests to confirm the change" }),
  ]);
  await expect(run(page).locator(".tool-run-head")).toHaveText(/Ran 2 commands, read 1 file/);
  await expect(run(page).locator(".tool-step-head").first()).toBeHidden();

  await run(page).locator(".tool-run-head").click();
  const steps = run(page).locator(".tool-step-head");
  await expect(steps).toHaveCount(3);
  await expect(steps.nth(0)).toContainText("Running lint to check the staticcheck fix");
  await expect(steps.nth(1)).toContainText("Reading conf.py to find where timeouts are set");
  await expect(steps.nth(0).locator(".tool-ico svg")).toBeVisible();
  // The tool itself stays folded until its reason is clicked.
  await expect(run(page).locator(".tool-row")).toHaveCount(3);
  await expect(run(page).locator(".tool-row").first()).toBeHidden();

  await steps.nth(0).click();
  await expect(steps.nth(0)).toHaveAttribute("aria-expanded", "true");
  await expect(run(page).locator(".tool-row").nth(0).locator(".term-cmd")).toHaveText("make lint");
  await expect(run(page).locator(".tool-row").nth(1)).toBeHidden();
  await steps.nth(1).click();
  await expect(run(page).locator(".tool-row").nth(1)).toContainText("conf.py");

  await steps.nth(0).click();
  await expect(run(page).locator(".tool-row").nth(0)).toBeHidden();
});

test("a call without a reason shows its tool row directly", async ({ page }) => {
  await setup(page);
  await emit(page, [
    tool("1", "shell", { command: "ls src" }),
    tool("2", "shell", { command: "make lint", reason: "Linting the change" }),
  ]);
  await run(page).locator(".tool-run-head").click();
  await expect(run(page).locator(".tool-step-head:visible")).toHaveCount(1);
  await expect(run(page).locator(".tool-row").nth(0).locator(".term-cmd")).toBeVisible();
  await expect(run(page).locator(".tool-row").nth(1)).toBeHidden();
});

test("a failed call is marked in its step", async ({ page }) => {
  await setup(page);
  await emit(page, [
    tool("1", "shell", { command: "make lint", reason: "Linting the change" }, "failed", { exit_code: 1 }),
    tool("2", "read", { path: "a.txt", reason: "Reading a.txt" }),
  ]);
  await run(page).locator(".tool-run-head").click();
  await expect(run(page).locator(".tool-step-head").first().locator(".tool-fail")).toHaveText("failed");
});

test("the reason shows once the arguments arrive on a running call", async ({ page }) => {
  await setup(page);
  await emit(page, [tool("1", "shell", {}, "running", ""), tool("2", "read", { path: "a" }, "running", "")]);
  await run(page).locator(".tool-run-head").click();
  await expect(run(page).locator(".tool-step-head:visible")).toHaveCount(0);
  await emit(page, [tool("1", "shell", { command: "make test", reason: "Running the tests" }, "running", "")]);
  await expect(run(page).locator(".tool-step-head:visible")).toHaveText(/Running the tests/);
});
