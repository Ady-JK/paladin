import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test("a turn's files open a snapshot diff and the header opens session changes", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eChanges = {
      available: true,
      turns: [
        {
          turn: 0,
          files: [
            { path: "index.html", status: "created" },
            { path: "page2.html", status: "created" },
          ],
        },
      ],
      files: [
        { path: "index.html", status: "created" },
        { path: "page2.html", status: "created" },
      ],
    };
    (window as any).__e2ePatch =
      "diff --git a/index.html b/index.html\nnew file mode 100644\n@@ -0,0 +1 @@\n+<h1>Hello</h1>\n";
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();

  await expect(page.locator("#sessionChangesBtn")).toContainText("2");
  const turnChange = page.locator('#messages [data-turn-index="0"] .turn-changes');
  await expect(turnChange).toContainText("2 files");
  await turnChange.click();
  await expect(page.locator("#sidePane .pane-changes")).toBeVisible();
  await page.locator('#sidePane [data-change-file="index.html"]').click();
  await expect(page.locator("#sidePane .change-diff")).toContainText("+<h1>Hello</h1>");
  expect(await page.evaluate(() => (window as any).__e2eDiffCalls?.at(-1)?.turn)).toBe(0);

  await page.locator("#sessionChangesBtn").click();
  await expect(page.locator("#sidePane .change-scope")).toContainText("Session changes");
  await page.locator('#sidePane [data-change-file="page2.html"]').click();
  expect(await page.evaluate(() => (window as any).__e2eDiffCalls?.at(-1)?.turn)).toBe(-1);
  await page.locator("#paneHistoryBtn").click();
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#sidePane .pane-changes")).toBeVisible();
  expect((await page.locator("#sidePane").boundingBox())!.width).toBeGreaterThan(page.viewportSize()!.width * 0.8);
});

test("a finished write shows its file indicator while the turn is still running", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, callIds: ["write-1"], files: [{ path: "index.html", status: "created" }] }],
      files: [{ path: "index.html", status: "created" }],
    };
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise(() => {
        w.__changeHandlers = handlers;
      });
  });
  await page.goto("/");
  await page.fill("#promptInput", "make a page");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__changeHandlers));
  await page.evaluate(() => {
    (window as any).__changeHandlers.onTool({
      id: "write-1",
      name: "write",
      arguments: '{"path":"index.html"}',
      result: '{"created":true}',
      status: "ok",
    });
  });
  await expect(page.locator("#messages [data-turn-id] .turn-changes")).toContainText("1 file");
  await expect(page.locator("#sessionChangesBtn")).toContainText("1");
});
