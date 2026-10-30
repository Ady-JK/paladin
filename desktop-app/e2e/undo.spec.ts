// undo.spec.ts covers undo and worktrees: "Revert to here" on a user
// message (preview, banner, redo, /undo), the new-session worktree
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

test("Revert to here previews, reverts files and conversation, then redoes", async ({ page }) => {
  await boot(page, {
    __e2ePreview: { turns: 2, files: ["src/a.ts", "src/b.ts"], conflicts: ["README.md"] },
    __e2eRevertResult: { restored: ["src/a.ts", "src/b.ts"], skipped: ["README.md"] },
  });
  await openDemo(page);
  const row = page.locator("#messages .turn-row:has(.bubble-user)").first();
  await row.hover();
  await row.locator('[data-revert="0"]').click();

  const pop = page.locator("#revertPop");
  await expect(pop).toBeVisible();
  await expect(pop).toContainText("2 turns");
  await expect(pop.locator(".revert-files li")).toHaveText(["src/a.ts", "src/b.ts"]);
  await expect(pop.locator(".revert-conflicts li")).toHaveText(["README.md"]);
  expect((await get(page, "__e2ePreviews"))[0]).toMatchObject({ sessionId: "sess-notify-1", index: 0 });

  await pop.locator("#revertConfirmBtn").click();
  await expect(pop).toBeHidden();
  expect((await get(page, "__e2eReverts"))[0]).toMatchObject({ sessionId: "sess-notify-1", index: 0 });
  const banner = page.locator("#revertBanner");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("2 files restored");
  await expect(banner).toContainText("1 skipped");
  await expect(page.locator("#promptInput")).toHaveValue("run the slow job");
  await expect(page.locator("#messages .bubble-user")).toHaveCount(0);

  await banner.locator("#redoBtn").click();
  await expect(banner).toBeHidden();
  expect(await get(page, "__e2eUnreverts")).toBe(1);
  await expect(page.locator("#messages .bubble-user")).toHaveCount(1);
  await expect(page.locator("#promptInput")).toHaveValue("");
});

test("Cancel leaves everything alone; untracked folders note conversation-only", async ({ page }) => {
  await boot(page);
  await set(page, { __e2ePreview: { snapshots: false } });
  await openDemo(page);
  const row = page.locator("#messages .turn-row:has(.bubble-user)").first();
  await row.hover();
  await row.locator("[data-revert]").click();
  const pop = page.locator("#revertPop");
  await expect(pop.locator(".revert-note")).toContainText(/only the conversation/i);
  await page.keyboard.press("Escape");
  await expect(pop).toBeHidden();
  expect(await get(page, "__e2eReverts")).toBeUndefined();
});

test("/undo reverts the last message and the next send clears the banner", async ({ page }) => {
  await boot(page);
  await openDemo(page);
  await page.fill("#promptInput", "/undo");
  await page.keyboard.press("Enter");
  await expect.poll(() => get(page, "__e2eReverts")).toHaveLength(1);
  expect((await get(page, "__e2eReverts"))[0].index).toBe(0);
  const banner = page.locator("#revertBanner");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText(/conversation/i);
  await page.click("#sendBtn");
  await expect(banner).toBeHidden();
});

test("opening a session with a pending revert restores the banner", async ({ page }) => {
  await boot(page, {
    __e2eOpenExtra: { "sess-notify-1": { revert: { index: 0, restored: ["x.go"], skipped: [], snapshots: true } } },
    __e2eRevertedAt: 0,
  });
  await showSidebar(page);
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await expect(page.locator("#revertBanner")).toContainText("1 file restored");
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
