// findings.spec.ts covers a /review's report_findings call: the card in
// history and live, the code excerpt with marked lines, the link into
// the session's Changes diff, and Fix filling the composer.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const REPORT = {
  verdict: "merge_after_fixes",
  summary: "One real bug and a risky edge.",
  findings: [
    {
      id: "F1",
      priority: "P0",
      title: "Nil map write",
      path: "/home/tester/project/src/a.go",
      scope: "inside",
      start_line: 4,
      end_line: 5,
      body: "m is nil when cfg is empty, so the write panics.",
      fix: "Initialise m with make().",
      confidence: "high",
    },
    {
      id: "F2",
      priority: "P2",
      title: "Empty input",
      path: "/home/tester/project/docs/guide.md",
      scope: "inside",
      start_line: 9,
      end_line: 9,
      body: "An empty list returns the wrong total.",
      confidence: "medium",
    },
  ],
};

async function openReview(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((report) => {
    const at = Math.floor(Date.now() / 1000);
    const w = window as any;
    w.__e2eHistory = [
      { role: "user", content: "/review", createdAt: at, index: 0 },
      {
        role: "tool",
        name: "shell",
        arguments: '{"cmd":"git diff"}',
        content: '{"status":"done","exit_code":0}',
        createdAt: at,
      },
      { role: "tool", name: "report_findings", arguments: "{}", content: JSON.stringify(report), createdAt: at },
      { role: "assistant", content: "Two findings; fix F1 first.", createdAt: at },
    ];
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "src/a.go", status: "modified" }] }],
      files: [{ path: "src/a.go", status: "modified" }],
    };
    w.__e2ePatch =
      "diff --git a/src/a.go b/src/a.go\n@@ -2,3 +2,4 @@\n line two\n-old\n+line four\n+line five\n line six\n";
  }, REPORT);
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
}

test("a review's findings show as a card in history", async ({ page }) => {
  await openReview(page);
  const card = page.locator("#messages .findings-card");
  await expect(card).toHaveCount(1);
  await expect(card.locator(".findings-verdict")).toHaveText("Merge after fixes");
  await expect(card.locator(".findings-note")).toHaveText("2 findings");
  await expect(card.locator(".findings-summary")).toHaveText("One real bug and a risky edge.");
  await expect(card.locator(".finding")).toHaveCount(2);
  await expect(card.locator('.finding[data-finding="F1"] .finding-loc')).toHaveText("src/a.go:4–5");
  await expect(card.locator(".finding-body").first()).toBeHidden();
  // The card sits outside the folded tool group.
  await expect(page.locator('#messages .tool-row[data-tool="report_findings"]')).toHaveCount(0);
});

test("opening a finding shows its code with the lines marked", async ({ page }) => {
  await openReview(page);
  const f1 = page.locator('#messages .finding[data-finding="F1"]');
  await f1.locator(".finding-head").click();
  await expect(f1.locator(".finding-text")).toContainText("m is nil");
  await expect(f1.locator(".finding-fix")).toContainText("make()");
  const marked = f1.locator(".finding-excerpt .file-line.is-mark");
  await expect(marked).toHaveCount(2);
  await expect(marked.first()).toContainText("line 4");
  const call = await page.evaluate(() => (window as any).__e2eExcerptCalls?.at(-1));
  expect(call).toMatchObject({ sessionId: "sess-notify-1", path: "/home/tester/project/src/a.go", start: 4, end: 5 });
});

test("a finding in a file this session changed opens the diff with markers", async ({ page }) => {
  await openReview(page);
  await expect(page.locator("#sessionChangesBtn")).toContainText("1");
  const f1 = page.locator('#messages .finding[data-finding="F1"]');
  await f1.locator(".finding-head").click();
  await f1.locator("[data-finding-changes]").click();
  const diff = page.locator("#sidePane .change-diff");
  await expect(diff).toContainText("+line four");
  await expect(diff.locator(".change-mark")).toHaveCount(2);
  // The hunk starts at new line 2, so lines 4-5 are "+line five" and "line six".
  await expect(diff.locator(".change-mark").first()).toHaveText("+line five");
  await expect(diff.locator(".change-mark").last()).toHaveText(" line six");
  // Files the session did not change get no link.
  const f2 = page.locator('#messages .finding[data-finding="F2"]');
  await f2.locator(".finding-head").click();
  await expect(f2.locator("[data-finding-changes]")).toBeHidden();
});

test("Fix puts the finding into the composer without sending it", async ({ page }) => {
  await openReview(page);
  const f1 = page.locator('#messages .finding[data-finding="F1"]');
  await f1.locator(".finding-head").click();
  await f1.locator("[data-finding-fix]").click();
  const input = page.locator("#promptInput");
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(/^Fix review finding F1 \(P0\): Nil map write\nsrc\/a\.go:4–5\n/);
  await expect(input).toHaveValue(/Suggested fix: Initialise m with make\(\)\.$/);
});

test("a live review draws the card as the call runs and finishes", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eRunTurn = (_p: any, handlers: any) =>
      new Promise((resolve) => {
        w.__h = handlers;
        w.__done = () => resolve({ sessionId: "", reply: "", stopped: false });
      });
  });
  await page.goto("/");
  await page.fill("#promptInput", "/review");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => Boolean((window as any).__h));
  await page.evaluate(() =>
    (window as any).__h.onTool({ id: "r1", name: "report_findings", arguments: "{}", result: "", status: "running" }),
  );
  const card = page.locator('#messages .findings-card[data-findings="r1"]');
  await expect(card).toContainText("Recording findings");
  await page.evaluate(
    (report) =>
      (window as any).__h.onTool({
        id: "r1",
        name: "report_findings",
        arguments: "{}",
        result: JSON.stringify(report),
        status: "ok",
      }),
    { verdict: "looks_good", summary: "Clean.", findings: [] },
  );
  await expect(card.locator(".findings-verdict")).toHaveText("Looks good");
  await expect(card.locator(".findings-note")).toHaveText("no findings");
  await expect(page.locator("#messages .findings-card")).toHaveCount(1);
});
