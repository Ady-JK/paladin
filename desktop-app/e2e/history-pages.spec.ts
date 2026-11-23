// history-pages.spec.ts covers the paged transcript: a long session
// paints the newest rows first, and scrolling to the top prepends older
// rows one whole turn at a time — no button to click, no duplicates,
// and the newest rows never fall out.
import { showSidebar } from "./drawer";
import { test, expect, type Page } from "@playwright/test";

const ROWS = 300;

async function setup(page: Page): Promise<void> {
  await page.addInitScript((rows) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    const history = Array.from({ length: rows }, (_, i) => ({
      role: i % 2 ? "assistant" : "user",
      content: `msg ${i}`,
      createdAt: 1,
      ...(i % 2 ? {} : { index: i }),
    }));
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "sessionHistoryPage") w.__pageCalls = [...(w.__pageCalls || []), args];
      if (cmd === "listSessions") {
        return [
          {
            sessionId: "sess-long",
            cwd: "/home/tester/project",
            createdAt: Math.floor(Date.now() / 1000),
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: rows,
            title: "long one",
          },
        ];
      }
      if (cmd === "sessionHistory") return history;
      if (cmd === "sessionChanges") return { available: false, turns: [], files: [] };
      return base(cmd, args);
    };
  }, ROWS);
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-long"]');
  await expect(page.locator("#sessionLabel")).toHaveText("sess-lon");
}

// scrollTop zero is the trigger; each hit must prepend exactly one turn
// group.
async function hitTop(page: Page): Promise<void> {
  await page.evaluate(() => {
    const view = document.querySelector<HTMLElement>("#chatView")!;
    view.scrollTop = 0;
    view.dispatchEvent(new Event("scroll"));
  });
}

test("a long transcript paints the newest page; scrolling up loads one turn at a time", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await setup(page);

  const messages = page.locator("#messages");
  await expect(messages.getByText("msg 299", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 0", { exact: true })).toHaveCount(0);

  // The newest page is turn-aligned: it begins on a user row, never
  // mid-turn, and the prefetch for the next page is already out.
  await expect
    .poll(() => page.evaluate(() => (window as any).__pageCalls?.some((call: any) => call.before === 180)))
    .toBe(true);

  // One scroll-hit prepends exactly one turn group: the turn just above
  // the painted top (msg 178..179 — a user row and its answer), nothing
  // older.
  await hitTop(page);
  await expect(messages.getByText("msg 179", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 178", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 177", { exact: true })).toHaveCount(0);

  // Another hit, another whole turn.
  await hitTop(page);
  await expect(messages.getByText("msg 177", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 176", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 175", { exact: true })).toHaveCount(0);

  // The user never has to click: the hint row is a label, not a button.
  await expect(page.locator(".history-earlier-btn")).toHaveCount(0);

  // Keep climbing to the very first row; the newest row never falls out.
  for (let guard = 0; guard < 200; guard++) {
    const left = await page.evaluate(() => {
      const view = document.querySelector<HTMLElement>("#chatView")!;
      return view.scrollHeight > view.clientHeight && view.scrollTop > 0;
    });
    if (!left) break;
    await hitTop(page);
    await page.waitForTimeout(30);
  }
  await expect(messages.getByText("msg 0", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 299", { exact: true })).toBeVisible();
  // Every row appears once, not once per page.
  await expect(messages.getByText("msg 150", { exact: true })).toHaveCount(1);
});

test("a short transcript has no earlier hint", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator(".history-earlier")).toHaveCount(0);
});
