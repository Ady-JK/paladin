import { expect, test } from "@playwright/test";
import { showSidebar } from "./drawer";

test("diff adapts to its panel width with line numbers and syntax colors", async ({ page }) => {
  const diffRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/src/features/sidePane/diffView.ts")) diffRequests.push(request.url());
  });
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.evaluate(async () => {
    const w = window as any;
    w.__e2ePatch =
      "diff --git a/app.ts b/app.ts\n--- a/app.ts\n+++ b/app.ts\n@@ -3,2 +3,2 @@\n const keep = 1;\n-const value = 2;\n+const value = 3;\n";
    const modulePath = "/src/features/sidePane/changesTab.ts";
    const { newChangesTab, paintChangesTab } = await import(modulePath);
    const data = { available: true, turns: [], files: [{ path: "app.ts", status: "modified" }] };
    const tab = newChangesTab("session", "changes", () => data);
    tab.body.id = "testDiff";
    tab.body.style.cssText = "position:fixed;inset:0 auto auto 0;width:1000px;height:600px;z-index:200";
    document.body.append(tab.body);
    paintChangesTab(tab, data);
  });
  expect(diffRequests).toHaveLength(0);
  await page.locator('#testDiff [data-change-file="app.ts"]').click();
  const split = page.locator("#testDiff .diff-split");
  const unified = page.locator("#testDiff .diff-unified");
  await expect(split).toBeVisible();
  expect(diffRequests).toHaveLength(1);
  await expect(unified).toBeHidden();
  await expect(split.locator('[data-line="3"]')).toHaveCount(2);
  await expect(split.locator(".tok-keyword").first()).toHaveText("const");
  await expect(split).toContainText("const value = 2;");
  await expect(split).toContainText("const value = 3;");
  await page.locator("#testDiff").evaluate((el) => (el.style.width = "420px"));
  await expect(unified).toBeVisible();
  await expect(split).toBeHidden();
  await expect(unified).toContainText("-const value = 2;");
  await expect(unified).toContainText("+const value = 3;");
});

test("diff preserves multiple hunks, newline notes, and escapes file content", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  const result = await page.evaluate(async () => {
    const modulePath = "/src/features/sidePane/diffView.ts";
    const { renderDiff } = await import(modulePath);
    const patch =
      '@@ -1 +1 @@\n-old\n+def greet():\n@@ -9 +9 @@\n-old\n+print("<img src=x onerror=alert(1)>")\n\\ No newline at end of file';
    const element = document.createElement("div");
    element.innerHTML = renderDiff(patch, "sample.py", { start: 9, end: 9 }, false);
    const go = document.createElement("div");
    go.innerHTML = renderDiff("@@ -1,1 +1,2 @@\n package main\n+func main() {}", "main.go", null, false);
    return {
      hunkCount: element.querySelectorAll(".diff-unified .diff-hunk").length,
      note: element.querySelector(".diff-unified .diff-note")?.textContent,
      marked: element.querySelector(".diff-unified .change-mark")?.textContent,
      keywords: element.querySelectorAll(".diff-unified .tok-keyword").length,
      goKeywords: go.querySelectorAll(".diff-unified .tok-keyword").length,
      images: element.querySelectorAll("img").length,
    };
  });
  expect(result.hunkCount).toBe(2);
  expect(result.note).toContain("No newline at end of file");
  expect(result.marked).toContain("<img src=x onerror=alert(1)>");
  expect(result.keywords).toBeGreaterThan(0);
  expect(result.goKeywords).toBeGreaterThan(0);
  expect(result.images).toBe(0);
});

test("overlapping change refreshes share the same in-flight diff request", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "modified" }] }],
      files: [{ path: "index.html", status: "modified" }],
    };
    const invoke = w.paladin.invoke;
    w.__heldDiffCalls = 0;
    w.paladin.invoke = (cmd: string, args: unknown) => {
      if (cmd !== "sessionDiff") return invoke(cmd, args);
      w.__heldDiffCalls++;
      return new Promise((resolve) => {
        (w.__releaseDiffs ??= []).push(() =>
          resolve({ path: "index.html", patch: "@@ -1 +1 @@\n-old\n+new", truncated: false }),
        );
      });
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await page.waitForFunction(() => (window as any).__heldDiffCalls > 0);
  await page.evaluate(async () => {
    const modulePath = "/src/features/sidePane.ts";
    const pane = await import(modulePath);
    void pane.refreshChanges("sess-notify-1");
    void pane.refreshChanges("sess-notify-1");
  });
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => (window as any).__heldDiffCalls)).toBe(1);
  await page.evaluate(() => (window as any).__releaseDiffs.forEach((release: () => void) => release()));
  await expect(page.locator('#messages .turn-changes[data-change-turn="0"]')).toContainText("+1");
});

test("refreshing unchanged file changes preserves keyboard focus", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "modified" }] }],
      files: [{ path: "index.html", status: "modified" }],
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#sessionChangesBtn")).toContainText("1");
  await page.locator("#sessionChangesBtn").click();
  const file = page.locator('#sidePane [data-change-file="index.html"]');
  await expect(file).toBeVisible();
  await file.focus();
  await page.evaluate(async () => {
    const modulePath = "/src/features/sidePane.ts";
    const pane = await import(modulePath);
    await pane.refreshChanges("sess-notify-1");
  });
  await expect(file).toBeFocused();
});

test("a later edit to the same file updates the live change counts", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "modified" }] }],
      files: [{ path: "index.html", status: "modified" }],
    };
    w.__e2ePatch = "@@ -1 +1 @@\n-old\n+first";
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise(() => {
        w.__changeHandlers = handlers;
      });
  });
  await page.goto("/");
  await page.fill("#promptInput", "edit a file");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__changeHandlers));
  await page.evaluate(() => {
    (window as any).__changeHandlers.onTool({
      id: "one",
      name: "edit",
      arguments: '{"path":"index.html"}',
      result: "{}",
      status: "ok",
    });
  });
  const card = page.locator("#liveChanges");
  await expect(card).toContainText("+1");
  await page.evaluate(() => {
    const w = window as any;
    w.__e2ePatch = "@@ -1 +1,2 @@\n-old\n+first\n+second";
    w.__changeHandlers.onTool({
      id: "two",
      name: "edit",
      arguments: '{"path":"index.html"}',
      result: "{}",
      status: "ok",
    });
  });
  await expect(card).toContainText("+2");
});

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
  const turnChange = page.locator('#messages .turn-changes[data-change-turn="0"]');
  await expect(turnChange).toContainText("2 files");
  await expect(turnChange).toContainText("+2");
  await expect(turnChange).toContainText("-0");
  // The file list is collapsed by default: one summary line with the
  // counts, no visible per-file rows until the card is opened.
  await expect(turnChange.locator(".turn-change-file:visible")).toHaveCount(0);
  await expect(turnChange).toHaveAttribute("aria-expanded", "false");
  await turnChange.click();
  await expect(turnChange.locator(".turn-change-file:visible")).toHaveCount(2);
  await expect(turnChange).toHaveAttribute("aria-expanded", "true");
  await turnChange.click();
  await expect(turnChange.locator(".turn-change-file:visible")).toHaveCount(0);
  // Expanding first, then View changes opens the diff tab.
  await turnChange.click();
  await turnChange.locator(".turn-change-view").click();
  await expect(page.locator("#sidePane .pane-changes")).toBeVisible();
  await page.locator('#sidePane [data-change-file="index.html"]').click();
  await expect(page.locator("#sidePane .change-diff")).toContainText("+<h1>Hello</h1>");
  expect(await page.evaluate(() => (window as any).__e2eDiffCalls?.at(-1)?.turn)).toBe(0);

  await page.locator("#sessionChangesBtn").click();
  await expect(page.locator("#sidePane .change-scope")).toContainText("Session changes");
  await page.locator('#sidePane [data-change-file="page2.html"]').click();
  expect(await page.evaluate(() => (window as any).__e2eDiffCalls?.at(-1)?.turn)).toBe(-1);
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#sidePane .pane-changes")).toBeVisible();
  // Tab view: the tab list is a shell drawer, so the changes body gets the
  // whole split, exactly like the conversation beside it.
  await expect
    .poll(
      async () => {
        const [pane, split] = await Promise.all([
          page.locator("#sidePane").boundingBox(),
          page.locator("#agentSplit").boundingBox(),
        ]);
        if (!pane || !split) return 0;
        return Math.round(pane.width - split.width);
      },
      { timeout: 4000 },
    )
    .toBeGreaterThanOrEqual(-2);
});

test("a finished write shows its file indicator while the turn is still running", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2ePatch = "@@ -0,0 +1 @@\n+hello";
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
  await expect(page.locator("#liveChanges")).toContainText("1 file");
  await expect(page.locator('#messages .turn-changes[data-change-turn="0"]')).toHaveCount(0);
  await expect(page.locator("#sessionChangesBtn")).toContainText("1");
  await page.locator("#liveChanges").click();
  await expect(page.locator("#sidePane .change-diff")).toContainText("+");
});

test("a running turn shows a floating summary and a collapsed card after finishing", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2ePatch = "@@ -0,0 +1 @@\n+hello";
    // A running turn has no transcript entry yet, so its change turn
    // carries no callIds: the card must still attach to the live row.
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "core/a.go", status: "created" }] }],
      files: [{ path: "core/a.go", status: "created" }],
    };
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__changeHandlers = handlers;
        w.__finishChanges = () => resolve({ reply: "Done.", index: 0 });
      });
  });
  await page.goto("/");
  await page.fill("#promptInput", "write a file");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__changeHandlers));
  // No persisted user row yet.
  await expect(page.locator("#messages [data-turn-index]")).toHaveCount(0);
  await page.evaluate(() => {
    (window as any).__changeHandlers.onTool({
      id: "write-1",
      name: "write",
      arguments: '{"path":"core/a.go"}',
      result: '{"created":true}',
      status: "ok",
    });
  });
  const indicator = page.locator("#liveChanges");
  await expect(indicator).toContainText("1 file");
  await expect(page.locator('#messages .turn-changes[data-change-turn="0"]')).toHaveCount(0);
  // A second write grows the same card without waiting for the turn end.
  await page.evaluate(() => {
    const changes = (window as any).__e2eChanges;
    changes.turns[0].files.push({ path: "core/b.go", status: "created" });
    changes.files.push({ path: "core/b.go", status: "created" });
    (window as any).__changeHandlers.onTool({
      id: "write-2",
      name: "edit",
      arguments: '{"path":"core/b.go"}',
      result: '{"lines":1}',
      status: "ok",
    });
  });
  await expect(indicator).toContainText("2 files");
  await page.locator("#ideModeBtn").click();
  await expect(indicator).toBeHidden();
  await page.locator("#ideModeBtn").click();
  await expect(indicator).toContainText("2 files");
  await expect(page.locator('#messages .turn-changes[data-change-turn="0"]')).toHaveCount(0);
  await page.evaluate(() => (window as any).__finishChanges());
  await expect(indicator).toBeHidden();
  const card = page.locator('#messages .turn-changes[data-change-turn="0"]');
  await expect(card).toContainText("2 files");
  await expect(card).toHaveAttribute("aria-expanded", "false");
  await card.locator(".turn-change-view").click();
  await expect(page.locator("#sidePane .change-diff")).toContainText("+");
});

test("a finished turn shows its file changes even with no live call id", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "modified" }] }],
      files: [{ path: "index.html", status: "modified" }],
    };
    w.__e2ePatch = "diff --git a/index.html b/index.html\n@@ -1 +1 @@\n-old\n+new\n";
    // The turn persists at index 0, which is what lets the card attach
    // to the live row once the turn ends.
    w.__e2eRunTurn = async () => ({
      reply: "Done.",
      index: 0,
      stats: { durationMs: 65_000, tokens: 1_000, genMs: 65_000 },
    });
  });
  await page.goto("/");
  await page.fill("#promptInput", "edit the page");
  await page.click("#sendBtn");

  const card = page.locator('#messages .turn-changes[data-change-turn="0"]');
  await expect(card).toContainText("1 file");
  await expect(card).toContainText("+1");
  await expect(card).toContainText("-1");
  await expect(card).toHaveAttribute("aria-expanded", "false");
  // The summary and the card both close the turn, the card last.
  const stats = page.locator("#messages .turn-stats");
  await expect(stats).toHaveText("1m 5s · 15.4 tok/s");
  const order = await page.evaluate(() => {
    const nodes = Array.from(document.querySelectorAll("#messages > .live-root > *, #messages > *"));
    const s = nodes.findIndex((n) => n.classList.contains("turn-stats"));
    const c = nodes.findIndex((n) => n.classList.contains("turn-changes"));
    return { s, c };
  });
  expect(order.s).toBeGreaterThanOrEqual(0);
  expect(order.c).toBeGreaterThan(order.s);
});

test("the live indicator follows the running session when switching chats", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eExtraSessions = [
      { sessionId: "other-session", cwd: "/home/tester/project", title: "Other", messageCount: 0 },
    ];
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 6, files: [{ path: "a.txt", status: "modified" }], callIds: ["w1"] }],
      files: [{ path: "a.txt", status: "modified" }],
    };
    w.__e2ePatch = "@@ -1 +1 @@\n-old\n+new";
    w.__e2eRunTurn = (_params: unknown, handlers: any) =>
      new Promise(() => {
        w.__changeHandlers = handlers;
      });
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('[data-session="sess-notify-1"]').first().click();
  await page.fill("#promptInput", "edit a file");
  await page.click("#sendBtn");
  await page.waitForFunction(() => Boolean((window as any).__changeHandlers));
  await page.evaluate(() =>
    (window as any).__changeHandlers.onTool({ id: "w1", name: "edit", arguments: "{}", result: "{}", status: "ok" }),
  );
  await expect(page.locator("#liveChanges")).toBeVisible();
  await showSidebar(page);
  await page.locator('[data-session="other-session"]').first().click();
  await expect(page.locator("#liveChanges")).toBeHidden();
  await showSidebar(page);
  await page.locator('[data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#liveChanges")).toContainText("1 file");
});

test("the collapsed card spans the conversation and does not repaint while a turn runs", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "created" }], callIds: ["t1"] }],
      files: [{ path: "index.html", status: "created" }],
    };
    w.__e2ePatch = "diff --git a/index.html b/index.html\n@@ -0,0 +1 @@\n+<h1>Hi</h1>\n";
    // A busy turn: four mutating tools settle in a row, each one asking
    // for the changes again.
    w.__e2eRunTurn = async (_p: unknown, h: any) => {
      for (let i = 1; i <= 4; i++) {
        h?.onTool?.({
          id: "t" + i,
          name: "shell",
          arguments: '{"cmd":"echo ' + i + '"}',
          result: JSON.stringify({ status: "done", exit_code: 0, output: "x" }),
          status: "done",
        });
        await new Promise((r) => setTimeout(r, 350));
      }
      await new Promise((r) => setTimeout(r, 600));
      return { reply: "done" };
    };
  });
  await page.goto("/");

  // Count every rewrite of the card's own children: an innerHTML
  // assignment replaces all of them at once, which is what flickered.
  await page.evaluate(() => {
    const w = window as any;
    w.__cardPaints = 0;
    new MutationObserver((records) => {
      for (const record of records) {
        const target = record.target as HTMLElement;
        if (target.classList?.contains("turn-change-card") && record.addedNodes.length) w.__cardPaints++;
      }
    }).observe(document.getElementById("messages")!, { childList: true, subtree: true });
  });

  await page.fill("#promptInput", "change stuff");
  await page.click("#sendBtn");
  const card = page.locator('#messages .turn-changes[data-change-turn="0"]');
  await expect(card).toContainText("Edited 1 file");
  // Let every tool settle, then let the turn end.
  await expect.poll(() => page.evaluate(() => (window as any).__cardPaints), { timeout: 8000 }).toBeLessThanOrEqual(1);

  // Full conversation width, like the turn's own rows.
  const box = await card.boundingBox();
  const msgs = await page.locator("#messages").boundingBox();
  expect(box && msgs).toBeTruthy();
  expect(Math.abs(box!.width - msgs!.width)).toBeLessThan(2);

  await expect(card).toHaveAttribute("aria-expanded", "false");
});

test("the card sits close to the composer below it", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    // Enough prior conversation to fill the chat pane, so the live turn's
    // card really is the last thing above the composer stack.
    const at = Math.floor(Date.now() / 1000);
    w.__e2eHistory = Array.from({ length: 24 }, (_, i) =>
      i % 2 === 0
        ? { role: "user", content: `question ${i} ${"x".repeat(120)}`, createdAt: at, index: i }
        : { role: "assistant", content: `answer ${i} ${"y".repeat(120)}`, createdAt: at },
    );
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 24, files: [{ path: "index.html", status: "created" }], callIds: ["t1"] }],
      files: [{ path: "index.html", status: "created" }],
    };
    w.__e2ePatch = "diff --git a/index.html b/index.html\n@@ -0,0 +1 @@\n+<h1>Hi</h1>\n";
    w.__e2eRunTurn = async (_p: unknown, h: any) => {
      h?.onTool?.({
        id: "t1",
        name: "write",
        arguments: '{"path":"index.html"}',
        result: JSON.stringify({ status: "done" }),
        status: "done",
      });
      await new Promise((r) => setTimeout(r, 700));
      return { reply: "done", index: 24 };
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#messages .turn-row")).toHaveCount(24);
  await page.fill("#promptInput", "write it");
  await page.click("#sendBtn");

  const card = page.locator("#messages .turn-changes").first();
  await expect(card).toContainText("Edited 1 file");
  const gap = await page.evaluate(() => {
    const card = document.querySelector("#messages .turn-changes") as HTMLElement;
    // The plan bar (#todoHat) is drawn inside the composer's frame but sits above .composer-float.
    const stack = ["#todoHat", ".composer-float"]
      .map((sel) => document.querySelector<HTMLElement>(sel))
      .filter((el): el is HTMLElement => !!el && getComputedStyle(el).display !== "none");
    const composer = stack.reduce((top, el) =>
      el.getBoundingClientRect().top < top.getBoundingClientRect().top ? el : top,
    );
    const chat = document.getElementById("chatView") as HTMLElement;
    // The card only joins the composer stack when it is the last thing
    // in the transcript, so make sure the pane is scrolled to the end.
    chat.scrollTop = chat.scrollHeight;
    const cardBox = card.getBoundingClientRect();
    const composerBox = composer.getBoundingClientRect();
    return {
      // The visible gap from the card's bottom edge to the top of the
      // composer stack: 8px.
      gap: composerBox.top - cardBox.bottom,
      marginTop: parseFloat(getComputedStyle(card).marginTop),
      // And the two still share an edge, so the small gap reads as a
      // pair rather than a misalignment.
      sameWidth: Math.abs(cardBox.width - composerBox.width) < 40,
    };
  });
  expect(gap.gap).toBeGreaterThanOrEqual(7);
  expect(gap.gap).toBeLessThanOrEqual(9);
  expect(gap.marginTop).toBeLessThanOrEqual(12);
  expect(gap.sameWidth).toBe(true);
});

// A turn's card sits with its own turn: after that turn's user row and
// the rows it streamed, before the next turn starts. The anchor walk used
// to run past the next turn's user row, so an older card drifted to the
// bottom of the transcript and could be re-anchored on every repaint.
test("each turn keeps its own card, inside its own block", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const at = Math.floor(Date.now() / 1000);
    w.__e2eHistory = [
      { role: "user", content: "first question", createdAt: at, index: 0 },
      { role: "assistant", content: "first answer", createdAt: at },
      { role: "user", content: "second question", createdAt: at, index: 2 },
      { role: "assistant", content: "second answer", createdAt: at },
    ];
    const turns = (extra = false) => [
      { turn: 0, files: [{ path: "a.txt", status: "modified" }], callIds: [] },
      { turn: 2, files: [{ path: "b.txt", status: "created" }], callIds: [] },
      ...(extra ? [{ turn: 4, files: [{ path: "c.txt", status: "created" }], callIds: ["c3"] }] : []),
    ];
    w.__e2eChanges = { available: true, turns: turns(), files: [{ path: "a.txt", status: "modified" }] };
    w.__e2eRunTurn = async (_p: unknown, h: any) => {
      h?.onTool?.({ id: "c3", name: "write", arguments: '{"path":"c.txt"}', result: "{}", status: "done" });
      w.__e2eChanges = { available: true, turns: turns(true), files: [{ path: "a.txt", status: "modified" }] };
      for (let i = 0; i < 30; i++) {
        h?.onReasoning?.(`thinking ${i} `);
        await new Promise((r) => setTimeout(r, 60));
      }
      return { reply: "done", index: 4 };
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator('#messages .turn-change-card[data-change-turn="0"]')).toHaveCount(1);
  await page.locator("#promptInput").fill("third question");
  await page.click("#sendBtn");
  await page.waitForTimeout(1400);

  // Each card lives beside its own turn's rows, in the same container: a
  // persisted turn's card sits in #messages, a live turn's card inside
  // that turn's live root. The anchor walk used to leave the container,
  // putting an older turn's card at the bottom of the transcript.
  // A card must come before the next turn begins. Flatten #messages in
  // true document order (descending into the live root) and check that
  // the nearest row *before* each card belongs to that card's turn. The
  // anchor walk used to step onto the next turn's live root, so an older
  // turn's card landed after it — at the bottom of the transcript.
  const stray = await page.evaluate(() => {
    // The live turn is the highest change turn in the answer (4 here).
    const liveTurn = "4";
    const order: { kind: "row" | "card"; turn: string | null; live: boolean }[] = [];
    const walk = (el: Element): void => {
      for (const child of Array.from(el.children) as HTMLElement[]) {
        if (child.dataset.turnIndex !== undefined)
          order.push({ kind: "row", turn: child.dataset.turnIndex, live: false });
        else if (child.dataset.turnId) order.push({ kind: "row", turn: null, live: true });
        else if (child.classList.contains("turn-change-card"))
          order.push({ kind: "card", turn: child.dataset.changeTurn ?? null, live: false });
        walk(child);
      }
    };
    walk(document.getElementById("messages")!);

    const out: string[] = [];
    let lastRow: { kind: "row" | "card"; turn: string | null; live: boolean } | null = null;
    for (const step of order) {
      if (step.kind === "row") {
        lastRow = step;
        continue;
      }
      if (!lastRow) {
        out.push(`turn ${step.turn}: no turn starts before it`);
        continue;
      }
      // A live row is the turn being streamed: its own card belongs right
      // after its rows, but an older turn's card may not slide below it.
      if (lastRow.live && step.turn !== liveTurn) {
        out.push(`turn ${step.turn} sits after the live turn's row`);
      }
      if (!lastRow.live && lastRow.turn !== step.turn) {
        out.push(`turn ${step.turn} sits after the rows of turn ${lastRow.turn}`);
      }
    }
    return out;
  });
  expect(stray).toEqual([]);
});

test("a stale diff failure cannot overwrite the cleared selection", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  const result = await page.evaluate(async () => {
    const w = window as any;
    const invoke = w.paladin.invoke;
    let rejectDiff: (reason: Error) => void = () => undefined;
    w.paladin.invoke = (method: string, args: unknown) =>
      method === "sessionDiff"
        ? new Promise((_resolve, reject) => {
            rejectDiff = reject;
          })
        : invoke(method, args);
    const modulePath = "/src/features/sidePane/changesTab.ts";
    const { newChangesTab, paintChangesTab, loadChangeDiff } = await import(modulePath);
    const data = { available: true, turns: [], files: [{ path: "file.txt", status: "modified" }] };
    const tab = newChangesTab("session", "changes", () => data);
    tab.selected = "file.txt";
    const pending = loadChangeDiff(tab, "file.txt");
    tab.selected = null;
    paintChangesTab(tab, data);
    rejectDiff(new Error("old request failed"));
    await pending;
    return tab.body.querySelector(".change-diff")?.textContent;
  });
  expect(result).toBe("Select a file to view its changes.");
});

test("the edits card and composer stack tightly", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 520 });
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "index.html", status: "modified" }] }],
      files: [{ path: "index.html", status: "modified" }],
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  const card = page.locator("#messages .turn-changes").last();
  await expect(card).toBeVisible();
  await page.evaluate(() => {
    const chat = document.querySelector("#chatView")!;
    chat.scrollTop = chat.scrollHeight;
  });
  const cardBox = (await card.boundingBox())!;
  // The plan bar (#todoHat) is drawn inside the composer's frame but sits above .composer-float.
  const composerTop = await page.evaluate(() => {
    const tops = ["#todoHat", ".composer-float"]
      .map((s) => document.querySelector(s))
      .filter((e): e is Element => !!e && getComputedStyle(e).display !== "none")
      .map((e) => e.getBoundingClientRect().top);
    return Math.min(...tops);
  });
  expect(composerTop - (cardBox.y + cardBox.height)).toBeLessThanOrEqual(12);
  // The thinking toggle sits right above the prompt text, not a line apart.
  const toggle = (await page.locator(".think-inline").boundingBox())!;
  const prompt = (await page.locator("#promptInput").boundingBox())!;
  expect(prompt.y - (toggle.y + toggle.height)).toBeLessThanOrEqual(2);
});

// A compaction after a finished turn is a boundary between turns. Reloaded
// from history, the turn's stats and its change card must come before the
// summary, never after it: the card used to land below the summary.
test("a compaction after a finished turn leaves its stats and card above the summary", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const at = Math.floor(Date.now() / 1000);
    w.__e2eHistory = [
      { role: "user", content: "make the change", createdAt: at, index: 0 },
      {
        role: "assistant",
        content: "done",
        createdAt: at,
        stats: { durationMs: 4000, tokens: 100, genMs: 3000 },
      },
      {
        role: "system",
        kind: "compaction",
        content: "## Summary\n\n- kept the change",
        createdAt: at,
        compaction: { tokensBefore: 1000, tokensAfter: 300, reason: "manual" },
      },
    ];
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "a.txt", status: "modified" }], callIds: [] }],
      files: [{ path: "a.txt", status: "modified" }],
    };
  });
  await page.goto("/");
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator('#messages .turn-change-card[data-change-turn="0"]')).toHaveCount(1);

  // Top-level order of the transcript: the turn, its stats, its card, then the divider.
  const order = await page.evaluate(() =>
    Array.from(document.getElementById("messages")!.children).map((el) => {
      const child = el as HTMLElement;
      if (child.classList.contains("turn-stats")) return "stats";
      if (child.classList.contains("turn-change-card")) return "card";
      if (child.dataset.notice === "compaction") return "divider";
      return child.dataset.turnIndex !== undefined ? "turn" : "other";
    }),
  );
  const at = (kind: "stats" | "card" | "divider") => order.indexOf(kind);
  expect(at("stats")).toBeGreaterThan(-1);
  expect(at("card")).toBe(at("stats") + 1);
  expect(at("divider")).toBe(at("card") + 1);
});

// One file the snapshot cannot diff must not hide the counts of the rest.
// A git add that refuses a path that already vanished (or a patch cut off
// at the size cap) used to drop every +x -y from the card, leaving status
// words only — which is what a turn full of edits looked like.
for (const failMode of ["error", "truncated"] as const) {
  test(`the card keeps +x -y when one diff ${failMode === "error" ? "fails" : "is truncated"}`, async ({ page }) => {
    await page.addInitScript({ path: "e2e/paladin-stub.js" });
    await page.addInitScript(
      (o) => {
        const w = window as any;
        const base = w.__paladinBaseInvoke;
        w.__paladinBaseInvoke = async (cmd: string, args: any) => {
          if (cmd === "sessionChanges")
            return {
              available: true,
              files: o.turn.files,
              turns: [o.turn],
            };
          if (cmd === "sessionDiff") {
            const patches: Record<string, string> = o.patches;
            const patch = patches[args.path as string];
            if (patch === undefined) {
              if (o.failMode === "error") {
                throw new Error("snapshot: git add: exit status 128: fatal: pathspec did not match any files");
              }
              return { path: args.path, patch: "+a\n+b\n", truncated: true };
            }
            return { path: args.path, patch, truncated: false };
          }
          return base(cmd, args);
        };
      },
      {
        failMode,
        patches: {
          "core/internal/transcript/store.go": "@@ -1,3 +1,4 @@\n-old line\n+new line\n+another line\n shared\n",
          "desktop-app/e2e/sessionList.spec.ts": "@@ -0,0 +1,2 @@\n+one\n+two\n",
        },
        turn: {
          turn: 0,
          callIds: ["c1"],
          files: [
            { path: "core/internal/transcript/store.go", status: "modified" },
            { path: "desktop-app/e2e/sessionList.spec.ts", status: "created" },
            { path: "graphify-out/.gfy-2nevgs1r.tmp", status: "created" },
          ],
        },
      },
    );
    await page.goto("/");
    await expect(page.locator("#promptInput")).toBeVisible();
    await page.fill("#promptInput", "change stuff");
    await page.click("#sendBtn");
    const card = page.locator('.turn-changes[data-change-turn="0"]').first();
    await expect(card).toContainText("Edited 3 files");
    // The turn total and both readable files keep their counts.
    await expect(card.locator(".turn-change-heading .tool-add")).toHaveText("+4");
    await expect(card.locator(".turn-change-heading .tool-del")).toHaveText("-1");
    const rows = card.locator(".turn-change-file");
    await expect(rows.nth(0).locator(".tool-add")).toHaveText("+2");
    await expect(rows.nth(0).locator(".tool-del")).toHaveText("-1");
    await expect(rows.nth(1).locator(".tool-add")).toHaveText("+2");
    // The undiffable file says what happened instead of a wrong count.
    await expect(rows.nth(2)).toContainText("created");
    await expect(rows.nth(2).locator(".tool-add")).toHaveCount(0);
  });
}
