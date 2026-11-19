import { test, expect, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

const QUESTIONS = JSON.stringify({
  questions: [{ question: "Pick an option", options: ["A", "B", "C"] }, { question: "Why?" }],
});

// holdTurn scripts a turn that shows one question card and then stays open, as a
// Codex turn does while its work goes on.
async function holdTurn(page: Page, result = '{"status":"pending"}'): Promise<void> {
  await page.evaluate(
    ({ args, result }) => {
      (window as any).__e2eRunTurn = async (_params: unknown, handlers: any) => {
        handlers.onTool({ id: "call_1", name: "agent_question", arguments: args, result, status: "ok" });
        handlers.onDelta("Carrying on while you decide.");
        await new Promise(() => undefined);
      };
    },
    { args: QUESTIONS, result },
  );
}

async function ask(page: Page): Promise<void> {
  await page.fill("#promptInput", "ask me something");
  await page.click("#sendBtn");
  await expect(page.locator("#messages .agent-q")).toBeVisible();
}

test("a question the agent asked is an inline form, and the work goes on around it", async ({ page }) => {
  await holdTurn(page);
  await ask(page);
  const card = page.locator("#messages .agent-q");
  await expect(card).toHaveAttribute("data-state", "pending");
  await expect(card.locator(".agent-q-opt").first()).toHaveAttribute("data-number", "1");
  await expect(card).toContainText("Pick an option");
  await expect(card.locator(".agent-q-opt")).toHaveText(["A", "B", "C"]);
  // The first suggestion is preselected, but nothing is sent until the user sends it.
  await expect(card.locator(".agent-q-opt.is-picked")).toHaveText("A");
  await expect(card.locator(".agent-q-send")).toBeDisabled(); // the second question has no answer yet
  // The turn did not stop for it: text kept streaming and the composer is still usable.
  await expect(page.locator("#messages")).toContainText("Carrying on while you decide.");
  await expect(page.locator("#promptInput")).toBeEditable();
  expect(await page.evaluate(() => (window as any).__e2eAnswers ?? [])).toEqual([]);
});

test("answering during the turn sends the answers, which steer the turn", async ({ page }) => {
  await holdTurn(page);
  await ask(page);
  const card = page.locator("#messages .agent-q");
  await card.locator(".agent-q-opt", { hasText: "B" }).click();
  await expect(card.locator(".agent-q-opt.is-picked")).toHaveText("B");
  await card.locator('.agent-q-item[data-q="1"] .agent-q-input').fill("because");
  await expect(card.locator(".agent-q-send")).toBeEnabled();
  await card.locator(".agent-q-send").click();

  await expect(card).toHaveAttribute("data-state", "answered");
  await expect(card).toContainText("B");
  await expect(card).toContainText("because");
  await expect(card.locator(".agent-q-send")).toHaveCount(0);
  const answers = await page.evaluate(() => (window as any).__e2eAnswers);
  expect(answers).toHaveLength(1);
  expect(answers[0]).toMatchObject({
    callId: "call_1",
    answers: [
      { question: "Pick an option", answer: "B" },
      { question: "Why?", answer: "because" },
    ],
  });
  expect(answers[0].sessionId).toBeTruthy();
  // The reply reads as a quoted answer in the conversation.
  await expect(page.locator("#messages")).toContainText("because");
});

test("typing your own answer replaces the picked option", async ({ page }) => {
  await holdTurn(page);
  await ask(page);
  const card = page.locator("#messages .agent-q");
  await card.locator('.agent-q-item[data-q="0"] .agent-q-input').fill("my own");
  await expect(card.locator('.agent-q-item[data-q="0"] .agent-q-opt.is-picked')).toHaveCount(0);
  await card.locator('.agent-q-item[data-q="1"] .agent-q-input').fill("why not");
  await card.locator(".agent-q-send").click();
  const answers = await page.evaluate(() => (window as any).__e2eAnswers);
  expect(answers[0].answers[0]).toMatchObject({ question: "Pick an option", answer: "my own" });
});

test("a failed answer says so and leaves the question open", async ({ page }) => {
  await holdTurn(page);
  await page.evaluate(() => ((window as any).__e2eAnswerFails = "the session is busy"));
  await ask(page);
  const card = page.locator("#messages .agent-q");
  await card.locator('.agent-q-item[data-q="1"] .agent-q-input').fill("because");
  await card.locator(".agent-q-send").click();
  await expect(page.locator("#toast")).toContainText("the session is busy");
  await expect(card).toHaveAttribute("data-state", "pending");
  await expect(card.locator(".agent-q-send")).toBeEnabled();
});

test("history shows a pending question as open and an answered one as settled", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-q-1",
        cwd: "/home/tester/project",
        createdAt: Math.floor(Date.now() / 1000) + 30,
        provider: "codex",
        model: "m",
        messageCount: 5,
        title: "questions",
        harness: "codex",
      },
    ];
    const at = Math.floor(Date.now() / 1000);
    (window as any).__e2eHistories = {
      "sess-q-1": [
        { role: "user", content: "ask", createdAt: at, index: 0 },
        { role: "assistant", content: "", createdAt: at },
        {
          role: "tool",
          name: "agent_question",
          toolCallId: "call_open",
          arguments: JSON.stringify({ questions: [{ question: "Still open?", options: ["Yes", "No"] }] }),
          content: '{"status":"pending"}',
          status: "ok",
          createdAt: at,
        },
        {
          role: "tool",
          name: "agent_question",
          toolCallId: "call_done",
          arguments: JSON.stringify({ questions: [{ question: "Done already?", options: ["Yes", "No"] }] }),
          content: JSON.stringify({ status: "answered", answers: [{ question: "Done already?", answer: "Yes" }] }),
          status: "ok",
          createdAt: at,
        },
      ],
    };
    (window as any).__e2eOpenExtra = { "sess-q-1": { provider: "codex", model: "m", harness: "codex" } };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.evaluate(() => document.querySelector<HTMLElement>("#agentTab")?.click());
  await page.locator("#sessionList .session-pick", { hasText: "questions" }).first().click();
  const open = page.locator('#messages .agent-q[data-call="call_open"]');
  const done = page.locator('#messages .agent-q[data-call="call_done"]');
  await expect(open).toHaveAttribute("data-state", "pending");
  await expect(open.locator(".agent-q-send")).toBeEnabled();
  await expect(done).toHaveAttribute("data-state", "answered");
  await expect(done).toContainText("Yes");
  await expect(done.locator(".agent-q-send")).toHaveCount(0);
  // The unsent text of the open one survives the session being repainted.
  await open.locator(".agent-q-input").fill("a draft");
  await page.evaluate(() => {
    const h = (window as any).__e2eHub;
    h.emit({ event: "session/refresh", sessionId: "sess-q-1", turnId: "" });
  });
  await expect(page.locator('#messages .agent-q[data-call="call_open"] .agent-q-input')).toHaveValue("a draft");
});

test("a session/refresh settles a background command's card", async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "sess-bg-1",
        cwd: "/home/tester/project",
        createdAt: Math.floor(Date.now() / 1000) + 30,
        provider: "codex",
        model: "m",
        messageCount: 3,
        title: "background",
        harness: "codex",
      },
    ];
    const at = Math.floor(Date.now() / 1000);
    const rows = (result: string) => [
      { role: "user", content: "sleep", createdAt: at, index: 0 },
      { role: "assistant", content: "", createdAt: at },
      {
        role: "tool",
        name: "shell",
        toolCallId: "exec-1",
        arguments: JSON.stringify({ command: "sleep 25", background: true }),
        content: result,
        status: "ok",
        createdAt: at,
      },
    ];
    (window as any).__e2eRows = rows;
    (window as any).__e2eHistories = { "sess-bg-1": rows('{"status":"running","output":""}') };
    (window as any).__e2eOpenExtra = { "sess-bg-1": { provider: "codex", model: "m", harness: "codex" } };
  });
  await page.reload();
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.evaluate(() => document.querySelector<HTMLElement>("#agentTab")?.click());
  await page.locator("#sessionList .session-pick", { hasText: "background" }).first().click();
  const status = page.locator("#messages .term-status");
  await expect(status).toHaveText("running in background");
  await page.evaluate(() => {
    const w = window as any;
    w.__e2eHistories = { "sess-bg-1": w.__e2eRows('{"status":"completed","exit_code":0,"output":"bgdone\\n"}') };
    w.__e2eHub.emit({ event: "session/refresh", sessionId: "sess-bg-1", turnId: "" });
  });
  await expect(page.locator("#messages .term-status")).toHaveText("exit 0");
});
