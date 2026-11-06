// contextstatus.spec.ts covers the one strip above the composer: the
// auto-loaded context summary on the left and the running turn's status
// on the right, sharing a row instead of stacking as two floaters.
import { test, expect, type Page } from "@playwright/test";

// scriptTurn parks the turn on a promise the spec releases, so mid-turn
// assertions are made against a turn that is certainly still running.
async function scriptTurn(page: Page, tool = "shell"): Promise<void> {
  await page.addInitScript((name: string) => {
    (window as any).__e2eRunTurn = async (_params: unknown, h: any) => {
      const w = window as any;
      h?.onTool?.({
        id: "t1",
        name,
        arguments: '{"cmd":"make test","path":"src/a/b.ts"}',
        result: "",
        status: "running",
      });
      w.__e2eReleased = new Promise((resolve) => {
        w.__e2eRelease = resolve;
      });
      await w.__e2eReleased;
      return { reply: "done" };
    };
  }, tool);
}

test("context and the turn status share one strip, one row", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await scriptTurn(page);
  await page.goto("/");

  const box = page.locator("#contextBox");
  const bar = page.locator("#contextBox #workingBar");
  await expect(box).toBeVisible();
  await expect(bar).toBeHidden();

  const idle = await box.boundingBox();
  expect(idle).not.toBeNull();

  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("Running a command");

  // One row, not two stacked floaters: the context summary and the
  // status line up on the same horizontal band.
  const ctx = await page.locator("#contextSummary").boundingBox();
  const live = await bar.boundingBox();
  expect(ctx && live).toBeTruthy();
  expect(Math.abs(ctx!.y + ctx!.height / 2 - (live!.y + live!.height / 2))).toBeLessThan(6);
  // Status on the right, context on the left.
  expect(live!.x).toBeGreaterThan(ctx!.x + ctx!.width);

  // The strip keeps its height and width while the status is showing.
  const running = await box.boundingBox();
  expect(running!.height).toBe(idle!.height);
  expect(running!.width).toBe(idle!.width);

  // The verb only: the command and the path stay in the turn's tool rows.
  await expect(box).not.toContainText("make test");
  await expect(box).not.toContainText("b.ts");

  await page.evaluate(() => (window as any).__e2eRelease());
  await expect(bar).toBeHidden({ timeout: 8000 });
});

test("the strip stays expandable while a turn runs", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await scriptTurn(page);
  await page.goto("/");

  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");
  const bar = page.locator("#contextBox #workingBar");
  await expect(bar).toBeVisible();

  // Opening the strip still lists the sources, with the status above.
  await page.click("#contextToggle");
  const list = page.locator("#contextList");
  await expect(list).toBeVisible();
  await expect(list.locator(".context-row")).toHaveCount(2);
  await expect(bar).toBeVisible();

  // A row still previews its injected text.
  await list.locator(".context-entry-head").first().click();
  await expect(list.locator(".context-body .context-text").first()).toContainText("short sentences");

  await page.evaluate(() => (window as any).__e2eRelease());
});

test("a folder with no auto-loaded context still shows the status", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eContext = { cwd: "/tmp/plain", sections: [], bytes: 0, files: 0, skills: 0, mcp: 0 };
  });
  await scriptTurn(page);
  await page.goto("/");

  const box = page.locator("#contextBox");
  await expect(box).toBeVisible();
  await expect(page.locator("#contextSummary")).toHaveText("No context loaded for this folder");

  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");
  await expect(page.locator("#contextBox #workingBar")).toBeVisible();
  await expect(box).toContainText("Running a command");

  await page.evaluate(() => (window as any).__e2eRelease());
});
