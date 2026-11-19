// contextstatus.spec.ts covers the running turn's status line above the
// composer in a folder with nothing auto-loaded. The auto-loaded context
// strip itself is gone; context-strip-removal.spec.ts covers that.
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

test("a folder with no auto-loaded context still shows the status", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eContext = { cwd: "/tmp/plain", sections: [], bytes: 0, files: 0, skills: 0, mcp: 0 };
  });
  await scriptTurn(page);
  await page.goto("/");

  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");
  await expect(page.locator("#workingBar")).toBeVisible();
  await expect(page.locator("#workingBar")).toContainText("Running a command");

  await page.evaluate(() => (window as any).__e2eRelease());
});
