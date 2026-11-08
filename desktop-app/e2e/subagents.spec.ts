// subagents.spec.ts covers subagent navigation: children nest under
// their parent in the sidebar, open in the main view with a way back,
// and stream live into a side-pane tab opened from the spawn_agent row.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Date.now();
const link = (name: string, type: string, status: string) => ({
  parentId: "sess-parent",
  type,
  name,
  task: `task for ${name}`,
  forkTurns: "none",
  status,
  updatedAt: now,
});

async function fixtures(page: Page): Promise<void> {
  await page.addInitScript(
    ({ explore, reviewer }) => {
      const w = window as any;
      const base = { cwd: "/home/tester/project", createdAt: 1, provider: "Opencode Go", model: "openai/gpt-4o" };
      w.__e2eExtraSessions = [
        {
          ...base,
          sessionId: "sess-parent",
          messageCount: 2,
          title: "parent work",
          children: ["sess-child-1", "sess-child-2"],
        },
        { ...base, sessionId: "sess-child-1", messageCount: 2, title: "explore-1", subagent: explore },
        { ...base, sessionId: "sess-child-2", messageCount: 2, title: "reviewer-1", subagent: reviewer },
      ];
      w.__e2eOpenExtra = { "sess-child-1": { subagent: explore }, "sess-child-2": { subagent: reviewer } };
      w.__e2eHistories = {
        "sess-parent": [
          { role: "user", content: "map the auth flow", createdAt: 1, index: 0 },
          {
            role: "tool",
            name: "spawn_agent",
            arguments: '{"agent_type":"explore","task":"find auth entry points"}',
            content: '{"agent_id":"sess-child-1","name":"explore-1","agent_type":"explore","status":"running"}',
            status: "ok",
            createdAt: 1,
          },
          { role: "assistant", content: "Parent is waiting on explore-1.", createdAt: 1 },
        ],
        "sess-child-1": [
          { role: "user", content: "find auth entry points", createdAt: 1, index: 0 },
          { role: "assistant", content: "Child found login.go.", createdAt: 1 },
        ],
        "sess-child-2": [{ role: "assistant", content: "Reviewer done.", createdAt: 1 }],
      };
    },
    { explore: link("explore-1", "explore", "running"), reviewer: link("reviewer-1", "reviewer", "completed") },
  );
}

// runChild starts a live turn in sess-child-1 the way the sidecar
// would: attach then reports it running, with its events so far.
async function runChild(page: Page): Promise<Record<string, unknown>> {
  const turn = {
    turnId: "tc1",
    sessionId: "sess-child-1",
    cwd: "/home/tester/project",
    message: "x",
    state: "running",
    parentSessionId: "sess-parent",
  };
  await page.evaluate((t) => {
    const w = window as any;
    w.__childTurn = t;
    w.__childEvents = [];
    const base = w.paladin.invoke;
    w.paladin.invoke = (cmd: string, args: any) => {
      if (cmd === "attach" && args?.sessionId === "sess-child-1" && w.__childTurn) {
        const events = w.__childEvents;
        const rows = w.__e2eHistories["sess-child-1"] || [];
        return Promise.resolve({
          history: { messages: rows, start: 0, hasMore: false, total: rows.length },
          turn: w.__childTurn,
          events,
          queued: [],
          seq: events.length ? events[events.length - 1].seq : 0,
        });
      }
      return base(cmd, args);
    };
  }, turn);
  await childEmit(page, { event: "turn/start", turn });
  return turn;
}

let childSeq = 1000;
async function childEmit(page: Page, ev: Record<string, unknown>): Promise<void> {
  const full = { sessionId: "sess-child-1", turnId: "tc1", seq: ++childSeq, ...ev };
  await page.evaluate((e) => {
    const w = window as any;
    w.__childEvents.push(e);
    w.__e2eHub.emit(e);
  }, full);
}

const opens = (page: Page) => page.evaluate(() => ((window as any).__e2eOpens || []).map((o: any) => o.sessionId));
const emit = (page: Page, ev: Record<string, unknown>) => page.evaluate((e) => (window as any).__e2eHub.emit(e), ev);

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await fixtures(page);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("children nest under their parent, not as sessions of their own", async ({ page }) => {
  // The sidebar is a drawer: closed on first paint, so open it before
  // reaching into it.
  await showSidebar(page);
  const parent = page.locator('.session-row:has([data-session="sess-parent"])');
  await expect(parent).toBeVisible();
  await expect(page.locator('[data-session="sess-child-1"]')).toHaveCount(0);
  await expect(page.locator("#recentList")).not.toContainText("explore-1");

  const kids = page.locator('.session-children[data-kids-of="sess-parent"]');
  // A running child keeps its parent's children open.
  await expect(kids).toBeVisible();
  const explore = kids.locator('[data-child="sess-child-1"]');
  await expect(explore).toContainText("explore-1");
  await expect(explore.locator(".agent-status")).toHaveAttribute("data-status", "running");
  await expect(kids.locator('[data-child="sess-child-2"] .agent-status')).toHaveAttribute("data-status", "completed");

  await showSidebar(page);
  await page.locator('[data-kids-toggle="sess-parent"]').click();
  await expect(kids).toBeHidden();
  await showSidebar(page);
  await page.locator('[data-kids-toggle="sess-parent"]').click();
  await expect(kids).toBeVisible();
});

test("a tool card opens a child using its short agent ID", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const full = "sess-child-1-long";
    w.__e2eExtraSessions.find((s: any) => s.sessionId === "sess-child-1").sessionId = full;
    w.__e2eHistories[full] = w.__e2eHistories["sess-child-1"];
    w.__e2eOpenExtra[full] = w.__e2eOpenExtra["sess-child-1"];
    w.__e2eHistories["sess-parent"][1].content = JSON.stringify({
      agent_id: "sess-child-1",
      name: "explore-1",
      agent_type: "explore",
      status: "running",
    });
    w.__e2eHub.emit({
      event: "agents/changed",
      parentSessionId: "sess-parent",
      agent: {
        agentId: full,
        parentSessionId: "sess-parent",
        name: "explore-1",
        type: "explore",
        status: "running",
      },
    });
  });
  await showSidebar(page);
  await expect(page.locator('[data-child="sess-child-1-long"]')).toHaveCount(1);
  await page.locator('[data-session="sess-parent"]').first().click();
  await page.locator('[data-open-agent="sess-child-1"]').click();
  const child = page.locator('.pane-agent[data-agent="sess-child-1-long"]');
  await expect(child).toBeVisible();
  await expect(child).toContainText("Child found login.go.");
});

test("a child opens in its own pane window, with a way into the main view", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-child="sess-child-1"]').click();

  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(pane.locator(".pane-tab.is-active")).toContainText("explore-1");
  const agent = pane.locator('.pane-agent[data-agent="sess-child-1"]');
  await expect(agent).toContainText("Child found login.go.");
  await expect(agent.locator(".pane-agent-head .agent-status")).toHaveAttribute("data-status", "running");
  // The child never replaces the main view; its parent does.
  expect(await opens(page)).not.toContain("sess-child-1");
  // The parent stays on screen, so its composer is still the main one.
  await expect(page.locator("#promptInput")).toBeEnabled();
  await expect(page.locator("#parentBar")).toBeHidden();
  // A running child takes guidance...
  await expect(agent.locator(".pane-agent-ask")).toBeVisible();

  // ...and Open moves it into the full view, where only its parent
  // gives it work.
  await agent.locator(".pane-agent-open").click();
  await expect(page.locator("#parentBar")).toBeVisible();
  await expect(page.locator("#parentBar")).toContainText("parent work");
  await expect(page.locator("#parentBar")).toContainText("explore-1");
  await expect(page.locator("#parentBar .agent-status")).toHaveAttribute("data-status", "running");
  await expect(page.locator("#promptInput")).toBeDisabled();
  await expect(pane).toBeHidden();
  await expect(page.locator("#messages .revert-btn")).toHaveCount(0);

  await page.locator("#parentBackBtn").click();
  await expect(page.locator("#messages")).toContainText("Parent is waiting on explore-1.");
  await expect(page.locator("#parentBar")).toBeHidden();
  await expect(page.locator("#promptInput")).toBeEnabled();
});

test("every working child gets a pane window without a click", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-session="sess-parent"]').click();
  await expect(page.locator("#sidePane")).toBeHidden();

  // Opening the pane any way at all shows the child's tab already there.
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  const tabs = page.locator("#paneTabs");
  await expect(tabs.locator('[data-agent="sess-child-1"]')).toHaveCount(1);
  // The new side chat stays the active tab: auto-open never steals focus.
  await expect(tabs.locator(".pane-tab.is-active")).toContainText("Side chat");

  // Closing the tab does not bring it back on the next status change.
  await tabs.locator('[data-agent="sess-child-1"] .pane-tab-close').click();
  await expect(tabs.locator('[data-agent="sess-child-1"]')).toHaveCount(0);
  await emit(page, {
    event: "agents/changed",
    parentSessionId: "sess-parent",
    agent: {
      agentId: "sess-child-1",
      parentSessionId: "sess-parent",
      name: "explore-1",
      type: "explore",
      status: "running",
      updatedAt: now,
    },
  });
  await expect(tabs.locator('[data-agent="sess-child-1"]')).toHaveCount(0);
});

test("a message typed into a running child steers its turn", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-child="sess-child-1"]').click();
  // A real running turn, as the sidecar would have started. The runner
  // never resolves, so the child stays busy while the user types.
  await page.evaluate(() => {
    (window as any).__e2eRunTurn = () => new Promise(() => undefined);
    (window as any).__e2eHub.submit({ sessionId: "sess-child-1", message: "work" });
  });

  const agent = page.locator('.pane-agent[data-agent="sess-child-1"]');
  const ask = agent.locator(".pane-agent-ask");
  await expect(ask).toBeVisible();
  await ask.locator(".pane-agent-input").fill("focus on the handler tests");
  await ask.locator(".pane-agent-input").press("Enter");

  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eSteers || []))
    .toEqual([
      { sessionId: "sess-child-1", message: "focus on the handler tests", display: "focus on the handler tests" },
    ]);
  await expect(agent.locator(".pane-agent-input")).toHaveValue("");
  // The steer joins the child's own transcript, not the parent's.
  await expect(agent).toContainText("focus on the handler tests");
  await expect(page.locator("#messages")).not.toContainText("focus on the handler tests");
});

test("a finished child is read-only in its window", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-child="sess-child-2"]').click();
  const agent = page.locator('.pane-agent[data-agent="sess-child-2"]');
  await expect(agent).toContainText("Reviewer done.");
  await expect(agent.locator(".pane-agent-head .agent-status")).toHaveAttribute("data-status", "completed");
  await expect(agent.locator(".pane-agent-ask")).toBeHidden();
  await expect(agent.locator(".pane-agent-foot")).toContainText("Finished · read-only");
});

test("agents/changed repaints a child's status and finds new children", async ({ page }) => {
  await emit(page, {
    event: "agents/changed",
    parentSessionId: "sess-parent",
    agent: {
      agentId: "sess-child-1",
      parentSessionId: "sess-parent",
      name: "explore-1",
      type: "explore",
      status: "completed",
      updatedAt: now,
    },
  });
  await expect(page.locator('[data-child="sess-child-1"] .agent-status')).toHaveAttribute("data-status", "completed");

  await page.evaluate(
    (sub) => {
      const w = window as any;
      w.__e2eExtraSessions.push({
        sessionId: "sess-child-3",
        cwd: "/home/tester/project",
        createdAt: 1,
        provider: "Opencode Go",
        model: "openai/gpt-4o",
        messageCount: 0,
        title: "explore-2",
        subagent: sub,
      });
    },
    link("explore-2", "explore", "queued"),
  );
  await emit(page, {
    event: "agents/changed",
    parentSessionId: "sess-parent",
    agent: {
      agentId: "sess-child-3",
      parentSessionId: "sess-parent",
      name: "explore-2",
      type: "explore",
      status: "queued",
      updatedAt: now,
    },
  });
  await expect(page.locator('[data-child="sess-child-3"]')).toContainText("explore-2");
});

test("a spawn_agent row opens the child live in the side pane", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-session="sess-parent"]').click();
  const row = page.locator('.tool-row[data-tool="spawn_agent"]');
  await expect(row).toContainText("Spawned");
  await expect(row).toContainText("explore-1");
  await expect(page.locator("#sidePane")).toBeHidden();

  await row.locator("[data-open-agent]").click();
  const pane = page.locator("#sidePane");
  await expect(pane).toBeVisible();
  await expect(pane.locator(".pane-tab.is-active")).toContainText("explore-1");
  const agent = pane.locator('.pane-agent[data-agent="sess-child-1"]');
  await expect(agent).toContainText("Child found login.go.");
  await expect(agent.locator(".pane-agent-head .agent-status")).toHaveAttribute("data-status", "running");

  // The child streams into the pane, not into the parent's transcript.
  const turn = await runChild(page);
  await childEmit(page, { event: "chat/delta", delta: "scanning handlers" });
  await expect(agent).toContainText("scanning handlers");
  await expect(page.locator("#messages")).not.toContainText("scanning handlers");

  await agent.locator(".pane-agent-stop").click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eAgentInterrupts || []))
    .toEqual([{ sessionId: "sess-parent", agentId: "sess-child-1" }]);
  // The sidecar persists the new status and says so.
  await page.evaluate(() => {
    (window as any).__e2eExtraSessions[1].subagent.status = "interrupted";
  });
  await emit(page, {
    event: "agents/changed",
    parentSessionId: "sess-parent",
    agent: {
      agentId: "sess-child-1",
      parentSessionId: "sess-parent",
      name: "explore-1",
      type: "explore",
      status: "interrupted",
      updatedAt: now,
    },
  });

  // Full view and back: Open closes the pane, and the child's window
  // reopens from the sidebar with its live rows intact.
  await agent.locator(".pane-agent-open").click();
  await expect(page.locator("#parentBar")).toBeVisible();
  await expect(page.locator("#messages")).toContainText("scanning handlers");
  await page.locator("#parentBackBtn").click();
  await expect(page.locator("#parentBar")).toBeHidden();
  await showSidebar(page);
  await page.locator('[data-child="sess-child-1"]').click();
  await expect(pane.locator(".pane-tab.is-active")).toContainText("explore-1");
  await childEmit(page, { event: "chat/delta", delta: " and routes" });
  await expect(pane.locator('.pane-agent[data-agent="sess-child-1"]')).toContainText("and routes");
  await page.evaluate(() => ((window as any).__childTurn = null));
  await childEmit(page, { event: "turn/done", turn: { ...turn, state: "done", stopped: true } });
  await expect(pane.locator(".pane-agent-stop")).toBeHidden();
  await expect(pane.locator(".pane-agent-head .agent-status")).toHaveAttribute("data-status", "interrupted");
});

test("a child's finished turn does not toast like a user session", async ({ page }) => {
  const turn = {
    turnId: "tc9",
    sessionId: "sess-child-2",
    cwd: "/home/tester/project",
    message: "x",
    state: "running",
    parentSessionId: "sess-parent",
  };
  await emit(page, { event: "turn/start", sessionId: "sess-child-2", turnId: "tc9", turn });
  await emit(page, { event: "turn/done", sessionId: "sess-child-2", turnId: "tc9", turn: { ...turn, state: "done" } });
  await page.waitForTimeout(150);
  await expect(page.locator("#toastText")).not.toContainText("reviewer-1");
});

test("deleting a parent drops its and its children's views, even with a child open", async ({ page }) => {
  await showSidebar(page);
  await page.locator('[data-session="sess-parent"]').click();
  await page.locator('.tool-row[data-tool="spawn_agent"] [data-open-agent]').click();
  await expect(page.locator('.pane-agent[data-agent="sess-child-1"]')).toContainText("Child found login.go.");
  await expect(page.locator('#paneTabs [data-agent="sess-child-1"]')).toHaveCount(1);
  // Open the child in the main view: the pane gives way to it.
  await page.locator('.pane-agent[data-agent="sess-child-1"] .pane-agent-open').click();
  await expect(page.locator("#parentBar")).toBeVisible();
  await expect(page.locator("#sidePane")).toBeHidden();

  await showSidebar(page);
  const row = page.locator('#sessionList .session-row:has([data-session="sess-parent"])');
  await row.hover();
  await row.locator('[data-row-menu="sess-parent"]').click();
  await page.locator("#sessionRowMenu").getByRole("menuitem", { name: "Delete" }).click();
  await page.click("#confirmDeleteBtn");

  // The open child went with its parent: the main view moves on.
  await expect(page.locator("#parentBar")).toBeHidden();
  await expect(page.locator("#messages")).not.toContainText("Child found login.go.");
  await expect(page.locator('[data-child="sess-child-1"]')).toHaveCount(0);
  await expect(page.locator('#paneTabs [data-agent="sess-child-1"]')).toHaveCount(0);
  await expect(page.locator('#paneTabs [data-agent="sess-child-2"]')).toHaveCount(0);
  await showSidebar(page);
  await expect(page.locator('#sessionList [data-session="sess-parent"]')).toHaveCount(0);
  await expect(page.locator('#sessionList [data-session="sess-child-1"]')).toHaveCount(0);
  await expect(page.locator('#sessionList [data-session="sess-child-2"]')).toHaveCount(0);
});
