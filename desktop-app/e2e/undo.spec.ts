// undo.spec.ts covers undo and worktrees: "Revert to here" on a user
// message (preview, exact restore, /undo), the new-session worktree
// toggle, fork into a worktree, the branch badge, merge back, and the
// discard confirm when deleting a dirty worktree session.
import { showSidebar } from "./drawer";
import { test, expect, type Page } from "@playwright/test";

const WT = {
  repoRoot: "/home/tester/project",
  path: "/wt/demo",
  branch: "paladin/demo",
  baseBranch: "main",
  origin: "/home/tester/project",
};

async function set(page: Page, values: Record<string, unknown>) {
  await page.evaluate((values) => Object.assign(window as any, values), values);
}

async function get<T = any>(page: Page, key: string): Promise<T> {
  return page.evaluate((key) => (window as any)[key], key);
}

async function openDemo(page: Page) {
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#messages .bubble-user")).toHaveCount(1);
}

// boot seeds fixtures before the app loads, so boot-time reads
// (listSessions, gitInfo) already see them.
async function boot(page: Page, values: Record<string, unknown> = {}) {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((values) => Object.assign(window as any, values), values);
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
}

test("Revert to here previews, restores files and the conversation", async ({ page }) => {
  await boot(page, {
    __e2ePreview: {
      turns: 2,
      files: ["src/a.ts", "src/b.ts"],
      deleted: ["old.ts"],
      protection: { turn: 0, state: "partial", excluded: ["secret.env"] },
    },
    __e2eRevertResult: { restored: ["src/a.ts", "src/b.ts"], deleted: ["old.ts"], skipped: [] },
  });
  await openDemo(page);
  const row = page.locator("#messages .turn-row:has(.bubble-user)").first();
  await row.hover();
  await row.locator('[data-revert="0"]').click();

  const pop = page.locator("#revertPop");
  await expect(pop).toBeVisible();
  await expect(pop).toContainText("2 turns");
  await expect(pop.locator(".revert-files li")).toHaveText(["src/a.ts", "src/b.ts"]);
  await expect(pop.locator(".revert-conflicts li")).toHaveText(["old.ts"]);
  await expect(pop.locator(".revert-excluded li")).toHaveText(["secret.env"]);
  await expect(pop).toContainText("another chat");
  await expect(pop).toContainText("recovery snapshot");
  expect((await get(page, "__e2ePreviews"))[0]).toMatchObject({ sessionId: "sess-notify-1", index: 0 });

  await pop.locator("#revertConfirmBtn").click();
  await expect(pop).toBeHidden();
  expect((await get(page, "__e2eReverts"))[0]).toMatchObject({
    sessionId: "sess-notify-1",
    index: 0,
    version: "undo-preview-version",
  });
  await expect(page.locator("#toastText")).toContainText("2 files restored");
  await expect(page.locator("#toastText")).toContainText("1 file deleted");
  await expect(page.locator("#promptInput")).toHaveValue("run the slow job");
  await expect(page.locator("#messages .bubble-user")).toHaveCount(0);
  await expect(page.locator("#recoverLatestBtn")).toBeVisible();
  await page.locator("#recoverLatestBtn").click();
  await expect(pop).toContainText("later file changes may be overwritten or deleted");
  await expect(pop.locator(".revert-files li")).toHaveText(["src/a.ts"]);
  await pop.locator("#recoverConfirmBtn").click();
  await expect.poll(() => get(page, "__e2eRecoveries")).toHaveLength(1);
  expect((await get(page, "__e2eRecoveries"))[0]).toMatchObject({ version: "recovery-preview-version" });
  await expect(page.locator("#messages .bubble-user")).toHaveCount(1);
});

test("Cancel leaves everything alone; untracked folders note conversation-only", async ({ page }) => {
  await boot(page);
  await set(page, { __e2ePreview: { snapshots: false } });
  await openDemo(page);
  const row = page.locator("#messages .turn-row:has(.bubble-user)").first();
  await row.hover();
  await row.locator("[data-revert]").click();
  const pop = page.locator("#revertPop");
  await expect(pop.locator(".revert-note").first()).toContainText(/only the conversation/i);
  await page.keyboard.press("Escape");
  await expect(pop).toBeHidden();
  expect(await get(page, "__e2eReverts")).toBeUndefined();
});

test("/undo reverts the last message", async ({ page }) => {
  await boot(page);
  await openDemo(page);
  await page.fill("#promptInput", "/undo");
  await page.keyboard.press("Enter");
  await expect(page.locator("#revertPop")).toBeVisible();
  await page.locator("#revertConfirmBtn").click();
  await expect.poll(() => get(page, "__e2eReverts")).toHaveLength(1);
  expect((await get(page, "__e2eReverts"))[0].index).toBe(0);
  await expect(page.locator("#toastText")).toContainText(/rewound/i);
});

test("only snapshot failures are shown in history", async ({ page }) => {
  await boot(page, {
    __e2eHistory: [
      { role: "user", content: "protected turn", createdAt: 1, index: 0, protection: { turn: 0, state: "protected" } },
      {
        role: "user",
        content: "excluded turn",
        createdAt: 2,
        index: 1,
        protection: {
          turn: 1,
          state: "partial",
          reason: "Some paths are excluded from snapshots.",
          excluded: ["ignored.env", "binary.dat"],
          truncated: true,
        },
      },
      {
        role: "user",
        content: "failed turn",
        createdAt: 3,
        index: 2,
        protection: { turn: 2, state: "unavailable", reason: "Snapshot timed out." },
      },
      {
        role: "user",
        content: "expired turn",
        createdAt: 4,
        index: 3,
        protection: { turn: 3, state: "expired", reason: "Only the last three turns can be undone." },
      },
    ],
  });
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  const statuses = page.locator("#messages .snapshot-protection");
  await expect(statuses).toHaveCount(1);
  await expect(statuses.first()).toContainText("Undo snapshot unavailable: Snapshot timed out.");
  await expect(statuses.first()).toHaveAttribute("data-state", "unavailable");
});

test("live turns show snapshot failure before the model finishes", async ({ page }) => {
  await boot(page, {
    __e2eHistory: [
      {
        role: "user",
        content: "run the slow job",
        createdAt: 1,
        index: 0,
        protection: { turn: 0, state: "protected" },
      },
    ],
  });
  await openDemo(page);
  await page.evaluate(() => {
    (window as any).__e2eRunTurn = () => new Promise(() => undefined);
  });
  await page.fill("#promptInput", "do work");
  await page.click("#sendBtn");
  await expect(page.locator("#messages .snapshot-protection")).toHaveCount(0);
  await page.evaluate(() => {
    const hub = (window as any).__e2eHub;
    const turn = hub.list().find((t: any) => t.state === "running");
    hub.emit({
      event: "chat/protection",
      sessionId: turn.sessionId,
      turnId: turn.turnId,
      protection: { turn: 6, floor: 1, state: "unavailable", reason: "Snapshot timed out." },
    });
  });
  const status = page.locator("#messages .snapshot-protection").last();
  await expect(status).toContainText("Undo snapshot unavailable");
  await status.locator("summary").click();
  await expect(status).toContainText("Snapshot timed out");
  await expect(page.locator("#messages .snapshot-protection")).toHaveCount(1);
});

test("the worktree toggle starts a new session in its own worktree", async ({ page }) => {
  await boot(page, { __e2eGit: { repo: true, hasCommits: true, branch: "main", root: "/home/tester/project" } });
  const chip = page.locator("#worktreeChip");
  await expect(chip).toBeVisible();
  await expect(chip).toHaveAttribute("aria-pressed", "false");
  await chip.click();
  await expect(chip).toHaveAttribute("aria-pressed", "true");
  await page.fill("#promptInput", "Add a cache layer");
  await page.click("#sendBtn");
  await expect
    .poll(async () => (await get(page, "__e2eOpens"))?.find((o: any) => o.worktree))
    .toMatchObject({
      sessionId: null,
      worktree: true,
      name: "Add a cache layer",
    });
});

test("the worktree toggle hides outside a git repository", async ({ page }) => {
  await boot(page);
  await expect(page.locator("#worktreeChip")).toBeHidden();
});

test("the row menu forks into a worktree", async ({ page }) => {
  await boot(page);
  const row = page.locator('#sessionList .session-row:has([data-session="sess-notify-1"])').first();
  await showSidebar(page);
  await row.hover();
  await row.locator('[data-row-menu="sess-notify-1"]').click();
  await page.locator("#sessionRowMenu").getByRole("menuitem", { name: "Fork into worktree" }).click();
  await expect
    .poll(async () => (await get(page, "__e2eForks"))?.[0])
    .toMatchObject({ sessionId: "sess-notify-1", worktree: true });
});

test("worktree sessions show their branch under the origin folder and merge back", async ({ page }) => {
  await boot(page, {
    __e2eExtraSessions: [
      {
        sessionId: "sess-wt-1",
        cwd: "/wt/demo",
        createdAt: 1,
        provider: "",
        model: "",
        messageCount: 2,
        title: "cache work",
        worktree: WT,
      },
    ],
    __e2eOpenExtra: { "sess-wt-1": { cwd: "/wt/demo", worktree: WT } },
    __e2eWorktreeStatus: {
      exists: true,
      changed: 3,
      ahead: 1,
      branch: "paladin/demo",
      into: "main",
      path: "/wt/demo",
      repoRoot: "/home/tester/project",
    },
  });
  await showSidebar(page);
  const project = page.locator('#sessionList .project[data-cwd="/home/tester/project"]');
  const wtRow = project.locator('.session-row:has([data-session="sess-wt-1"])');
  await expect(wtRow).toBeVisible();
  await expect(wtRow.locator(".session-branch")).toHaveText("demo");

  await wtRow.locator("[data-session]").click();
  const bar = page.locator("#worktreeBar");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("paladin/demo");
  await expect(bar).toContainText("3 changed");
  await expect(bar).toContainText("main");

  await bar.locator("#mergeBtn").click();
  await expect.poll(async () => (await get(page, "__e2eMerges"))?.[0]).toMatchObject({ sessionId: "sess-wt-1" });
  await expect(page.locator("#toastText")).toContainText(/Merged into main/);

  await set(page, {
    __e2eMergeResult: { merged: false, nothingToMerge: false, conflicts: ["src/a.ts"], into: "main" },
  });
  await bar.locator("#mergeBtn").click();
  await expect(page.locator("#toastText")).toContainText(/conflict/i);
  await expect(page.locator("#toastText")).toContainText("src/a.ts");

  await set(page, { __e2eMergeError: "the repository checkout has uncommitted changes; commit or stash them first" });
  await bar.locator("#mergeBtn").click();
  await expect(page.locator("#toastText")).toContainText(/uncommitted changes/);
});

test("deleting a dirty worktree session asks before discarding its changes", async ({ page }) => {
  await boot(page, {
    __e2eSessionExtra: { worktree: WT },
    __e2eWorktreeStatus: {
      exists: true,
      changed: 2,
      ahead: 0,
      branch: "paladin/demo",
      into: "main",
      path: "/wt/demo",
      repoRoot: "/home/tester/project",
    },
  });
  await openDemo(page);
  await showSidebar(page);
  const row = page.locator('#sessionList .session-row:has([data-session="sess-notify-1"])').first();
  await showSidebar(page);
  await row.hover();
  await row.locator('[data-row-menu="sess-notify-1"]').click();
  await page.locator("#sessionRowMenu").getByRole("menuitem", { name: "Delete" }).click();
  const discard = page.locator("#confirmDiscard");
  await expect(discard).toBeVisible();
  await expect(page.locator("#confirmModal")).toContainText("2 uncommitted");
  await discard.check();
  await page.click("#confirmDeleteBtn");
  await expect
    .poll(async () => (await get(page, "__e2eDeletes"))?.[0])
    .toMatchObject({ sessionId: "sess-notify-1", discardWorktree: true });
});
