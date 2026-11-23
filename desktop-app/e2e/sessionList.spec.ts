import { test, expect, type Page } from "@playwright/test";

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
// nobody acted on; it is gone. A fork now wears a small fork glyph so a
// copy is recognisable in the list.
test("a forked session is marked and no row shows a message count", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    (s) => {
      const w = window as any;
      const base = w.__paladinBaseInvoke;
      w.__paladinBaseInvoke = async (cmd: string, args: any) => (cmd === "listSessions" ? s : base(cmd, args));
    },
    [
      {
        sessionId: "s1",
        cwd: "/home/tester/project",
        createdAt: 2,
        provider: "p",
        model: "m",
        messageCount: 3296,
        title: "original",
      },
      {
        sessionId: "s2",
        cwd: "/home/tester/project",
        createdAt: 1,
        provider: "p",
        model: "m",
        messageCount: 12,
        title: "the copy",
        forkedFrom: "s1",
      },
    ],
  );
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  const rows = page.locator("#sessionList .session-row");
  await expect(rows).toHaveCount(2);
  // No count digits anywhere in the list.
  await expect(page.locator("#sessionList .session-count")).toHaveCount(0);
  await expect(page.locator("#sessionList")).not.toContainText("3296");
  // Exactly one row wears the fork badge: the copy, not its source.
  await expect(page.locator("#sessionList .session-forked")).toHaveCount(1);
  const forked = rows.filter({ hasText: "the copy" });
  await expect(forked.locator(".session-forked")).toHaveCount(1);
  await expect(rows.filter({ hasText: "original" }).locator(".session-forked")).toHaveCount(0);
});
