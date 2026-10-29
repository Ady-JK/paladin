// live.spec.ts covers async, multi-session turns: sessions stream at the
// same time, keep updating while hidden, queue follow-ups, surface
// background completion and questions, and re-attach after a reload.
import { test, expect, type Page } from "@playwright/test";

const SESSIONS = ["sess-a", "sess-b"];

// Two sessions with empty history; every turn is held until the spec
// releases it, and streams "started <message>" right away.
async function setup(page: Page): Promise<void> {
  await page.addInitScript((ids) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        return ids.map((id: string, i: number) => ({
          sessionId: id,
          cwd: "/home/tester/project",
          createdAt: Math.floor(Date.now() / 1000) - i,
          provider: "Opencode Go",
          model: "openai/gpt-4o",
          messageCount: 0,
          title: `title ${id}`,
        }));
      }
      if (cmd === "sessionHistory") return [];
      return base(cmd, args);
    };
    w.__held = {};
    w.__ran = [];
    w.__e2eRunTurn = (params: any, handlers: any) =>
      new Promise((resolve) => {
        w.__ran.push(params.message);
        w.__held[params.message] = { handlers, resolve };
        handlers.onDelta(`started ${params.message}`);
      });
    w.__release = (message: string, reply = "") =>
      w.__held[message].resolve({ sessionId: "", reply, stopped: false, needsContinue: false });
  }, SESSIONS);
  await page.goto("/");
  await expect(page.locator('[data-session="sess-b"]')).toBeVisible();
}

async function sendIn(page: Page, session: string, text: string): Promise<void> {
  await page.click(`[data-session="${session}"]`);
  await expect(page.locator("#sessionLabel")).toHaveText(session.slice(0, 8));
  await page.fill("#promptInput", text);
  await page.click("#sendBtn");
}

const row = (page: Page, id: string) => page.locator(`.session-row:has([data-session="${id}"])`);

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
});

test("two sessions stream at once and keep their own transcripts", async ({ page }) => {
  await setup(page);
  await sendIn(page, "sess-a", "alpha");
  await expect(page.locator("#messages")).toContainText("started alpha");

  await sendIn(page, "sess-b", "beta");
  await expect(page.locator("#messages")).toContainText("started beta");
  await expect(page.locator("#messages")).not.toContainText("alpha");
  expect(await page.evaluate(() => (window as any).__ran)).toEqual(["alpha", "beta"]);

  // sess-a keeps streaming while hidden; the sidebar shows it working.
  await page.evaluate(() => (window as any).__held.alpha.handlers.onDelta(" more-a"));
  await expect(row(page, "sess-a")).toHaveAttribute("data-live", "running");
  await expect(page.locator("#messages")).not.toContainText("more-a");

  // Back on sess-a mid-turn: everything it streamed is there, live.
  await page.click('[data-session="sess-a"]');
  await expect(page.locator("#messages")).toContainText("started alpha more-a");
  await expect(page.locator("#pauseBtn")).toBeVisible();
  await page.evaluate(() => (window as any).__held.alpha.handlers.onDelta(" live"));
  await expect(page.locator("#messages")).toContainText("more-a live");

  // sess-b finishes in the background: badge + toast, no transcript bleed.
  await page.evaluate(() => (window as any).__release("beta"));
  await expect(row(page, "sess-b")).toHaveAttribute("data-live", "done");
  await expect(page.locator("#toast")).toContainText("title sess-b");
  await expect(page.locator("#messages")).not.toContainText("beta");

  await page.evaluate(() => (window as any).__release("alpha"));
  await expect(page.locator("#pauseBtn")).toBeHidden();
  await expect(row(page, "sess-a")).not.toHaveAttribute("data-live", /.+/);

  // Opening sess-b clears its badge.
  await page.click('[data-session="sess-b"]');
  await expect(row(page, "sess-b")).not.toHaveAttribute("data-live", /.+/);
});

test("a follow-up queues behind the running turn and can be cancelled", async ({ page }) => {
  await setup(page);
  await sendIn(page, "sess-a", "one");
  await expect(page.locator("#messages")).toContainText("started one");

  await page.fill("#promptInput", "two");
  await page.click("#sendBtn");
  await page.fill("#promptInput", "three");
  await page.click("#sendBtn");
  const queued = page.locator("#messages [data-queued]");
  await expect(queued).toHaveCount(2);
  await expect(queued.first()).toContainText("two");
  await expect(row(page, "sess-a")).toHaveAttribute("data-live", "running");

  await queued.nth(1).locator("[data-cancel-turn]").click();
  await expect(queued).toHaveCount(1);

  await page.evaluate(() => (window as any).__release("one", ""));
  await expect(page.locator("#messages")).toContainText("started two");
  await expect(queued).toHaveCount(0);
  // The promoted bubble sits above its own reply.
  const text = (await page.locator("#messages").textContent()) ?? "";
  expect(text.indexOf("started one")).toBeLessThan(text.indexOf("two"));
  expect(text.indexOf("two")).toBeLessThan(text.indexOf("started two"));

  await page.evaluate(() => (window as any).__release("two", ""));
  await expect(page.locator("#pauseBtn")).toBeHidden();
  expect(await page.evaluate(() => (window as any).__ran)).toEqual(["one", "two"]);
});

test("stop drops the queue and ends the running turn", async ({ page }) => {
  await setup(page);
  await sendIn(page, "sess-a", "one");
  await expect(page.locator("#messages")).toContainText("started one");
  await page.fill("#promptInput", "two");
  await page.click("#sendBtn");
  await expect(page.locator("#messages [data-queued]")).toHaveCount(1);
  await page.click("#pauseBtn");
  await expect(page.locator("#messages [data-queued]")).toHaveCount(0);
  await expect(page.locator("#pauseBtn")).toBeHidden();
  expect(await page.evaluate(() => (window as any).__ran)).toEqual(["one"]);
});

test("a question from a background session waits until it is opened", async ({ page }) => {
  await setup(page);
  await sendIn(page, "sess-a", "deploy");
  await expect(page.locator("#messages")).toContainText("started deploy");
  await page.click('[data-session="sess-b"]');
  await page.evaluate(() =>
    (window as any).__held.deploy.handlers.onTool({
      id: "ask-bg",
      name: "ask",
      arguments: JSON.stringify({ questions: [{ question: "Which env?", options: ["staging"] }] }),
      result: "",
      status: "running",
    }),
  );
  await expect(row(page, "sess-a")).toHaveAttribute("data-live", "input");
  await expect(page.locator("#askModal")).toHaveCount(0);

  await page.click('[data-session="sess-a"]');
  await expect(page.locator("#askModal")).toBeVisible();
  await expect(page.locator("#askModal")).toContainText("Which env?");
});

test("a notification click opens its session", async ({ page }) => {
  await setup(page);
  await page.evaluate(() => (window as any).__e2eOpenSession("sess-b"));
  await expect(page.locator("#sessionLabel")).toHaveText("sess-b");
});

test("after a reload a running turn is re-attached and keeps streaming", async ({ page }) => {
  await page.addInitScript(() => {
    const w = window as any;
    const turn = {
      turnId: "t-9",
      sessionId: "sess-notify-1",
      cwd: "/home/tester/project",
      message: "long job expanded",
      display: "long job",
      state: "running",
    };
    w.__e2eSeedTurn = turn;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "turns") return [turn];
      if (cmd === "attach") {
        return {
          history: [
            { role: "user", content: "earlier", createdAt: 1 },
            { role: "assistant", content: "Earlier answer.", createdAt: 1 },
          ],
          turn,
          events: [{ event: "chat/delta", seq: 5, sessionId: turn.sessionId, turnId: "t-9", delta: "halfway" }],
          queued: [],
          seq: 5,
        };
      }
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await expect(row(page, "sess-notify-1")).toHaveAttribute("data-live", "running");

  await page.click('[data-session="sess-notify-1"]');
  const msgs = page.locator("#messages");
  await expect(msgs).toContainText("Earlier answer.");
  await expect(msgs).toContainText("long job");
  await expect(msgs).not.toContainText("long job expanded");
  await expect(msgs).toContainText("halfway");
  await expect(page.locator("#pauseBtn")).toBeVisible();

  // A stale event (seq <= snapshot) is ignored; newer ones stream in.
  await page.evaluate(() => {
    const hub = (window as any).__e2eHub;
    hub.emit({ event: "chat/delta", seq: 4, sessionId: "sess-notify-1", turnId: "t-9", delta: " STALE" });
    hub.emit({ event: "chat/delta", seq: 6, sessionId: "sess-notify-1", turnId: "t-9", delta: " there" });
  });
  await expect(msgs).toContainText("halfway there");
  await expect(msgs).not.toContainText("STALE");

  await page.evaluate(() => {
    const w = window as any;
    w.__e2eHub.emit({
      event: "turn/done",
      seq: 7,
      sessionId: "sess-notify-1",
      turnId: "t-9",
      turn: { ...w.__e2eSeedTurn, state: "done", reply: "halfway there", persisted: true },
    });
  });
  await expect(page.locator("#pauseBtn")).toBeHidden();
});
