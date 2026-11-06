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
  await expect(page.locator('#messages .turn-changes[data-change-turn="0"]')).toContainText("1 file");
  await expect(page.locator("#sessionChangesBtn")).toContainText("1");
});

test("a running turn shows its changes card as soon as it edits, before it persists", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    // A running turn has no transcript entry yet, so its change turn
    // carries no callIds: the card must still attach to the live row.
    w.__e2eChanges = {
      available: true,
      turns: [{ turn: 0, files: [{ path: "core/a.go", status: "created" }] }],
      files: [{ path: "core/a.go", status: "created" }],
    };
    w.__e2eRunTurn = (_params: any, handlers: any) =>
      new Promise(() => {
        w.__changeHandlers = handlers;
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
  const card = page.locator('#messages .turn-changes[data-change-turn="0"]');
  await expect(card).toContainText("1 file");
  await expect(card).toHaveAttribute("aria-expanded", "false");
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
  await expect(card).toContainText("2 files");
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

test("the card sits close to the Context strip below it", async ({ page }) => {
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
    const ctx = document.getElementById("contextBox") as HTMLElement;
    const chat = document.getElementById("chatView") as HTMLElement;
    // The card only joins the composer stack when it is the last thing
    // in the transcript, so make sure the pane is scrolled to the end.
    chat.scrollTop = chat.scrollHeight;
    const cardBox = card.getBoundingClientRect();
    const ctxBox = ctx.getBoundingClientRect();
    return {
      // The visible gap from the card's bottom edge to the Context strip:
      // 8px, matching the strip's gap above the composer.
      gap: ctxBox.top - cardBox.bottom,
      marginTop: parseFloat(getComputedStyle(card).marginTop),
      // And the two floaters still share an edge, so the smaller gap
      // reads as a pair rather than a misalignment.
      sameWidth: Math.abs(cardBox.width - ctxBox.width) < 40,
    };
  });
  expect(gap.gap).toBeGreaterThanOrEqual(7);
  expect(gap.gap).toBeLessThanOrEqual(9);
  expect(gap.marginTop).toBeLessThanOrEqual(12);
  expect(gap.sameWidth).toBe(true);
});
