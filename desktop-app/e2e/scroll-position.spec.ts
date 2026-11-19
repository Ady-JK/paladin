import { showSidebar } from "./drawer";
import { readFileSync } from "node:fs";
import { test, expect, type Page } from "@playwright/test";

// scroll-position.spec.ts covers where the transcript sits when the view
// regains layout or its rows grow after the scroll. The tabs layout hides
// #agentMain (holding #chatView) while a side tab shows, and a display:none
// subtree has no layout, so scrollHeight reads 0; and a page of Markdown
// lays out its tables and code blocks only after the rows are inserted.
// Both used to leave the conversation above its newest row.

async function setup(page: Page) {
  await page.addInitScript(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    const rows: any[] = [];
    for (let i = 0; i < 20; i++) {
      rows.push({ role: "user", content: `q ${i}`, createdAt: 1, index: i * 2 });
      rows.push({ role: "assistant", content: `a ${i} ` + "filler words here ".repeat(10), createdAt: 1 });
    }
    w.__e2eHistory = rows;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions")
        return [
          {
            sessionId: "sess-long",
            cwd: "/home/tester/project",
            createdAt: Math.floor(Date.now() / 1000),
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: rows.length,
            title: "long",
          },
        ];
      if (cmd === "sessionChanges") return { available: false, turns: [], files: [] };
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await showSidebar(page);
}

test("a request to show the newest row survives having no layout", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await setup(page);
  await page.click('[data-session="sess-long"]');
  await page.waitForTimeout(600);

  // Tabs layout with a side tab showing: #agentMain is display:none, so
  // #chatView has no layout and scrollHeight reads 0.
  const during = await page.evaluate(() => {
    const view = document.querySelector("#agentView") as HTMLElement;
    const el = document.querySelector("#chatView") as HTMLElement;
    // Scroll to the top WHILE the view still has layout, then hide it —
    // this is the real sequence when a side tab is open.
    el.scrollTop = 0;
    const atTop = Math.round(el.scrollTop);
    view.classList.add("is-tab-layout", "is-side-tab");
    return { clientHeight: el.clientHeight, scrollHeight: el.scrollHeight, atTop, afterTop: Math.round(el.scrollTop) };
  });
  expect(during.clientHeight, "a display:none view has no layout").toBe(0);

  // Ask for the newest row while there is nothing to scroll, then switch
  // back to the conversation tab. The module is served by Vite at this
  // path in the browser; a computed specifier keeps tsc from resolving it.
  await page.evaluate(async () => {
    const specifier = "/src/features/chatView.ts";
    const mod = (await import(/* @vite-ignore */ specifier)) as { scrollToBottom: () => void };
    mod.scrollToBottom();
  });
  await page.evaluate(() => {
    (document.querySelector("#agentView") as HTMLElement).classList.remove("is-side-tab");
  });
  // Do not scroll again: only the held request may put us at the bottom.
  await page.waitForTimeout(700);

  const after = await page.evaluate(() => {
    const el = document.querySelector("#chatView") as HTMLElement;
    return {
      scrollTop: Math.round(el.scrollTop),
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      fromBottom: Math.round(el.scrollHeight - el.scrollTop - el.clientHeight),
    };
  });
  console.log("AFTER_NO_RESCROLL:", JSON.stringify(after));
  expect(after.fromBottom, "the held request must land at the newest row").toBeLessThan(40);
});

// The same class of bug hits undo: reverting reloads the transcript, and
// that reload's scroll-to-bottom must survive the view having no layout.
test("rewinding the conversation still lands on the newest row", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await setup(page);
  await page.click('[data-session="sess-long"]');
  await page.waitForTimeout(600);

  await page.evaluate(() => {
    (window as any).__e2ePreview = { turns: 1, files: [], deleted: [], protection: null };
    (window as any).__e2eRevertResult = { restored: [], deleted: [], skipped: [] };
  });

  // Hide the conversation behind a side tab, as the tabs layout does.
  await page.evaluate(() => {
    const el = document.querySelector("#chatView") as HTMLElement;
    el.scrollTop = 0;
    document.querySelector("#agentView")!.classList.add("is-tab-layout", "is-side-tab");
  });

  // Rewind from the newest user row.
  const rows = page.locator("#messages .turn-row:has(.bubble-user)");
  const last = rows.last();
  await page.evaluate(() => {
    // The popover lives on document.body, so it opens even while hidden.
    document.querySelector("#agentView")!.classList.remove("is-side-tab");
  });
  await last.hover();
  await last.locator("[data-revert]").click();
  await expect(page.locator("#revertPop")).toBeVisible();
  await page.evaluate(() => {
    document.querySelector("#agentView")!.classList.add("is-tab-layout", "is-side-tab");
  });
  await page.locator("#revertConfirmBtn").click();
  await page.evaluate(() => {
    document.querySelector("#agentView")!.classList.remove("is-side-tab");
  });
  await page.waitForTimeout(800);

  const after = await page.evaluate(() => {
    const el = document.querySelector("#chatView") as HTMLElement;
    return { fromBottom: Math.round(el.scrollHeight - el.scrollTop - el.clientHeight) };
  });
  console.log("AFTER_UNDO:", JSON.stringify(after));
  expect(after.fromBottom, "after a rewind the newest row must be in view").toBeLessThan(40);
});

// Heavy rows are the common case in a real session: a page of tables and
// code blocks can grow the transcript by thousands of pixels on the frame
// after the scroll, because the Markdown is laid out after the rows are
// inserted. The conversation must end at its newest row, not part-way up
// the page (the reported bug: 1734px short).
test("a page of heavy markdown still opens at the newest row", async ({ page }) => {
  const history = JSON.parse(readFileSync("e2e/rendering-history.json", "utf8"));
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((hist) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions")
        return [
          {
            sessionId: "sess-heavy",
            cwd: "/home/tester/project",
            createdAt: 1,
            provider: "p",
            model: "m",
            messageCount: hist.total,
            title: "heavy",
          },
        ];
      if (cmd === "sessionHistoryPage") return hist;
      if (cmd === "sessionHistory") return hist.messages;
      if (cmd === "sessionChanges") return { available: false, turns: [], files: [] };
      if (cmd === "sessionImages") return { latestUser: -1, images: [] };
      return base(cmd, args);
    };
  }, history);
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-heavy"]');
  await page.waitForTimeout(2500);

  const pos = await page.evaluate(() => {
    const el = document.querySelector("#chatView") as HTMLElement;
    return { fromBottom: Math.round(el.scrollHeight - el.scrollTop - el.clientHeight), height: el.scrollHeight };
  });
  console.log("HEAVY_POS:", JSON.stringify(pos));
  expect(pos.height, "the fixture must be tall enough to matter").toBeGreaterThan(1500);
  expect(pos.fromBottom, "must open at the newest row").toBeLessThan(40);
});
