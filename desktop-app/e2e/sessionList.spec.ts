import { test, expect, type Page } from "@playwright/test";

import { showSidebar } from "./drawer";

// sessionList.spec.ts pins the sidebar's session list against the reads
// that race a busy turn. A failed or transiently empty listSessions used
// to assign state.sessions = [], which painted "no sessions yet" over a
// pane that had sessions — the reported glitch. The list is the user's
// work, so it must never empty itself on one bad read.

const SESSIONS = [
  {
    sessionId: "s1",
    cwd: "/home/tester/project",
    createdAt: 2,
    provider: "p",
    model: "m",
    messageCount: 3,
    title: "first session",
  },
  {
    sessionId: "s2",
    cwd: "/home/tester/project",
    createdAt: 1,
    provider: "p",
    model: "m",
    messageCount: 3,
    title: "second session",
  },
];

// refresh asks the running app for another session-list refresh. The
// module is served by Vite at this path in the browser; reading it off the
// window keeps tsc from trying to resolve a browser-only specifier.
async function refresh(page: Page): Promise<void> {
  // The host broadcasts sessions/changed when a session settles; live.ts
  // turns that into one coalesced listSessions. Going through the real
  // event keeps the spec on the app's own refresh path.
  await page.evaluate(() => {
    (window as any).__e2eHub.emit({ event: "sessions/changed" });
  });
  await page.waitForTimeout(400);
}

async function boot(page: Page, opts: { failAfter?: number; reorder?: boolean } = {}) {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((o) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    let call = 0;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        call++;
        w.__lsCalls = call;
        // A refresh that runs while a session folder is being written fails.
        if (o.failAfter && call >= o.failAfter)
          throw new Error("io: session: read /data/sessions/x: no such file or directory");
        if (o.reorder) await new Promise((r) => setTimeout(r, call === 2 ? 400 : 20));
        return w.__sessions;
      }
      return base(cmd, args);
    };
  }, opts);
  await page.addInitScript((s) => {
    (window as any).__sessions = s;
  }, SESSIONS);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.waitForTimeout(800);
}

test("a listSessions that fails after the list is shown keeps the list", async ({ page }) => {
  await boot(page, { failAfter: 2 });
  const before = (await page.locator("#sessionList").textContent()) || "";
  expect(before).toContain("first session");

  // A turn settling triggers a refresh, which now fails.
  await refresh(page);

  const after = (await page.locator("#sessionList").textContent()) || "";
  console.log("BEFORE:", JSON.stringify(before.slice(0, 60)));
  console.log("AFTER_FAIL:", JSON.stringify(after.slice(0, 60)));
  console.log("LS_CALLS:", await page.evaluate(() => (window as any).__lsCalls));
  expect(after).toContain("first session");
  expect(after).not.toContain("no sessions yet");
});

test("an out-of-order listSessions response does not win", async ({ page }) => {
  await boot(page, { reorder: true });
  for (let i = 0; i < 2; i++) {
    await refresh(page);
  }
  await page.waitForTimeout(1200);
  const text = (await page.locator("#sessionList").textContent()) || "";
  console.log("REORDER_TEXT:", JSON.stringify(text.slice(0, 60)));
  expect(text).toContain("first session");
});

test("a host restart mid-turn keeps the session list", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    let fail = false;
    w.__failAll = () => {
      fail = true;
    };
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (fail && ["listSessions", "listTurns", "attach"].includes(cmd)) {
        throw new Error("host restarted");
      }
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.waitForTimeout(700);
  const before = (await page.locator("#sessionList").textContent()) || "";
  expect(before).toContain("notify demo");

  // The host dies; every RPC now rejects until it is back.
  await page.evaluate(() => (window as any).__failAll());
  await refresh(page);
  const after = (await page.locator("#sessionList").textContent()) || "";
  console.log("BEFORE:", JSON.stringify(before.slice(0, 50)));
  console.log("AFTER_HOST_DOWN:", JSON.stringify(after.slice(0, 80)));
  expect(after).toContain("notify demo");
  expect(after).not.toContain("no sessions yet");
});

test("a burst of subagent refreshes keeps the parent list", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    let n = 0;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        n++;
        w.__lsCalls = n;
        // Subagent sessions appear/disappear as children are created and
        // settle; one of those reads can come back without the parent.
        if (n === 3) return [];
        if (n === 4) throw new Error("io: session: read: no such file or directory");
        return base(cmd, args).then((r: any) => r);
      }
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.waitForTimeout(600);
  expect(await page.locator("#sessionList").textContent()).toContain("notify demo");

  // Five child agents finish, one after another.
  for (let i = 0; i < 5; i++) {
    await refresh(page);
  }
  await page.waitForTimeout(800);
  const text = (await page.locator("#sessionList").textContent()) || "";
  console.log("AFTER_BURST:", JSON.stringify(text.slice(0, 70)));
  console.log("CALLS:", await page.evaluate(() => (window as any).__lsCalls));
  expect(text).toContain("notify demo");
});

test("a transient empty listSessions does not blank a populated sidebar", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    let n = 0;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        n++;
        w.__lsCalls = n;
        // One read lands while the namespace directory is mid-rewrite.
        if (n === 2) return [];
        return base(cmd, args);
      }
      return base(cmd, args);
    };
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.waitForTimeout(600);
  expect(await page.locator("#sessionList").textContent()).toContain("notify demo");

  for (let i = 0; i < 2; i++) {
    await refresh(page);
  }
  const text = (await page.locator("#sessionList").textContent()) || "";
  console.log("AFTER_EMPTY:", JSON.stringify(text.slice(0, 70)));
  expect(text).not.toContain("no sessions yet");
});

// The row used to end with the session's message count, which was noise
// How a project's list arrives: a parent, a fork of it, a fork of that
// fork, and an unrelated chat. listSessions is a FLAT list — the core
// sends forks interleaved by time, so nesting is entirely the sidebar's
// job.
const FORK_TREE = [
  {
    sessionId: "p1",
    cwd: "/home/tester/project",
    createdAt: 3,
    provider: "p",
    model: "m",
    messageCount: 3296,
    title: "original chat",
  },
  {
    sessionId: "f1",
    cwd: "/home/tester/project",
    createdAt: 2,
    provider: "p",
    model: "m",
    messageCount: 2,
    title: "a fork",
    forkedFrom: "p1",
  },
  {
    sessionId: "f2",
    cwd: "/home/tester/project",
    createdAt: 1,
    provider: "p",
    model: "m",
    messageCount: 1,
    title: "a fork of a fork",
    forkedFrom: "f1",
  },
  {
    sessionId: "other",
    cwd: "/home/tester/project",
    createdAt: 0,
    provider: "p",
    model: "m",
    messageCount: 4,
    title: "unrelated chat",
  },
];

async function bootForkTree(page: Page, sessions: unknown[]): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((s) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => (cmd === "listSessions" ? s : base(cmd, args));
  }, sessions);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  // The list paints after the first listSessions settles, and the sidebar
  // is a drawer: open it or every row is hidden.
  await page.waitForTimeout(800);
  await showSidebar(page);
  await expect(page.locator("#sessionList .session-row").first()).toBeVisible();
}

// A fork hangs under the chat it came from, like a subfolder in an
// explorer — not beside it as a second row with the same title.
test("a fork nests under its parent instead of standing beside it", async ({ page }) => {
  await bootForkTree(page, FORK_TREE);
  const list = page.locator("#sessionList");
  // Three rows in the list: the parent, its fork, and the unrelated chat.
  // The fork-of-a-fork is nested too, so every session has exactly one row.
  await expect(list.locator(".session-row")).toHaveCount(4);
  await expect(list).not.toContainText("3296");

  // Rows carry data-session ids; the row's text also holds the engine
  // chip, so titles are matched by id rather than by text.
  const parent = list.locator('.project-engine > .session-row:has([data-session="p1"])');
  await expect(parent).toHaveCount(1);
  // The fork is NOT a top-level row...
  await expect(list.locator('.project-engine > .session-row:has([data-session="f1"])')).toHaveCount(0);
  // ...it is inside the parent's fork container.
  const parentBlock = list.locator('.project-engine > .session-row:has([data-session="p1"]) + .session-forks');
  await expect(parentBlock).toHaveCount(1);
  await expect(parentBlock.locator("> .session-fork-row")).toHaveCount(1);
  await expect(parentBlock.locator('> .session-fork-row:has([data-session="f1"])')).toHaveCount(1);
  // A fork of a fork nests one level deeper, inside the first fork.
  await expect(parentBlock.locator('.session-forks .session-fork-row:has([data-session="f2"])')).toHaveCount(1);
  await expect(parentBlock.locator(".session-forks")).toContainText("a fork of a fork");

  // Indented to the right of its parent: the guide line proves the
  // nesting is visible, not just structural.
  const parentBox = (await parent.boundingBox())!;
  const forkBox = (await parentBlock.locator("> .session-fork-row").boundingBox())!;
  expect(forkBox.x).toBeGreaterThan(parentBox.x);
  expect(forkBox.y).toBeGreaterThan(parentBox.y);
  await expect(parentBlock.locator(".fork-guide")).toHaveCount(2);
});

// Fork nesting is always on: unlike the subagent list there is no toggle
// to collapse it, because hiding it would leave two identical titles with
// no visible relation.
test("fork nesting has no collapse toggle and survives a re-render", async ({ page }) => {
  await bootForkTree(page, FORK_TREE);
  const list = page.locator("#sessionList");
  const forks = list.locator(".session-forks");
  await expect(forks.first()).toBeVisible();
  // No kids toggle belongs to the fork container: the count of toggles in
  // this fixture is zero (no subagents at all).
  await expect(list.locator(".session-kids-toggle")).toHaveCount(0);
  // A status refresh repaints the list; the nesting must still be there.
  await refresh(page);
  await expect(list.locator(".session-forks").first()).toBeVisible();
  await expect(list.locator(".session-row")).toHaveCount(4);
});

// A fork whose parent is not in the list cannot nest; it stands on its
// own and keeps the fork glyph so it is still recognisable.
test("an orphaned fork stands alone and keeps its glyph", async ({ page }) => {
  await bootForkTree(page, [
    {
      sessionId: "orphan",
      cwd: "/home/tester/project",
      createdAt: 1,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "whose copy is this",
      forkedFrom: "gone",
    },
  ]);
  const list = page.locator("#sessionList");
  await expect(list.locator(".session-row")).toHaveCount(1);
  await expect(list.locator(".session-forks")).toHaveCount(0);
  await expect(list.locator(".session-forked")).toHaveCount(1);
});

// Searching a fork's title keeps its parent, or the match would render
// with no parent to nest under.
test("searching a fork keeps the chat it came from", async ({ page }) => {
  await bootForkTree(page, FORK_TREE);
  const search = page.locator("#searchInput");
  await search.fill("fork of a fork");
  const list = page.locator("#sessionList");
  await expect(list).toContainText("original chat");
  await expect(list).toContainText("a fork of a fork");
  await expect(list).not.toContainText("unrelated chat");
});

// A subagent can be forked, and a fork can spawn subagents. Both hang
// under their real parent, so nothing drops out of the tree.
test("a fork of a subagent still renders under it", async ({ page }) => {
  await bootForkTree(page, [
    {
      sessionId: "p1",
      cwd: "/home/tester/project",
      createdAt: 3,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "parent chat",
    },
    {
      sessionId: "kid",
      cwd: "/home/tester/project",
      createdAt: 2,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "reviewee",
      subagent: { parentId: "p1", type: "reviewer", name: "reviewee", status: "completed", updatedAt: 1 },
    },
    {
      sessionId: "kidfork",
      cwd: "/home/tester/project",
      createdAt: 1,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "a fork of the reviewer",
      forkedFrom: "kid",
    },
  ]);
  const list = page.locator("#sessionList");
  // Every session is in the tree exactly once: the parent row, its
  // subagent (a .session-child button), and the subagent's fork nested
  // under that child.
  await expect(list.locator('.session-row:has([data-session="p1"])')).toHaveCount(1);
  await expect(list.locator('.session-child[data-child="kid"]')).toHaveCount(1);
  await expect(list.locator('.session-forks .session-fork-row:has([data-session="kidfork"])')).toHaveCount(1);
  // The fork sits INSIDE the subagent's list, not at project level.
  const kidsList = list.locator('.session-children[data-kids-of="p1"]');
  await expect(kidsList.locator('.session-fork-row:has([data-session="kidfork"])')).toHaveCount(1);
  // Opening the subagent list reveals it.
  await list.locator('[data-kids-toggle="p1"]').click();
  await expect(kidsList).toBeVisible();
  await expect(kidsList.locator(".session-forks")).toBeVisible();
});

// A corrupt pair of parent links (A forked from B, B forked from A) must
// not hide both chats: the cycle is cut and both stay reachable.
test("a parent cycle does not hide either chat", async ({ page }) => {
  await bootForkTree(page, [
    {
      sessionId: "a",
      cwd: "/home/tester/project",
      createdAt: 2,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "chat a",
      forkedFrom: "b",
    },
    {
      sessionId: "b",
      cwd: "/home/tester/project",
      createdAt: 1,
      provider: "p",
      model: "m",
      messageCount: 1,
      title: "chat b",
      forkedFrom: "a",
    },
  ]);
  const list = page.locator("#sessionList");
  await expect(list.locator(".session-row")).toHaveCount(2);
  await expect(list).toContainText("chat a");
  await expect(list).toContainText("chat b");
});
