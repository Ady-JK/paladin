// commands.spec.ts covers the Codex-style `/` palette and everything it
// drives: local commands, prompt commands, skills, plan mode, compact,
// fork/rename, memories, and the in-session working indicator.
import { test, expect, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

async function lastSubmit(page: Page): Promise<any> {
  return page.evaluate(() => {
    const s = (window as any).__e2eSubmits || [];
    return s[s.length - 1] ?? null;
  });
}

test("typing / opens a grouped palette with icons, labels and descriptions", async ({ page }) => {
  await page.fill("#promptInput", "/");
  const popup = page.locator("#slashPopup");
  await expect(popup).toBeVisible();
  await expect(popup.locator(".slash-group")).toContainText(["Commands", "Prompts", "Skills"]);
  const compact = popup.locator('.slash-row[data-name="compact"]');
  await expect(compact.locator(".slash-icon svg")).toHaveCount(1);
  await expect(compact.locator(".slash-label")).toHaveText("Compact");
  await expect(compact.locator(".slash-desc")).toContainText("context");
  await expect(popup.locator('.slash-row[data-name="review"] .slash-label')).toHaveText("Code review");
  await expect(popup.locator('.slash-row[data-kind="skill"][data-name="reviewer"]')).toBeVisible();
  // The first row is active; arrows move the pill highlight.
  await expect(popup.locator(".slash-row.is-active")).toHaveCount(1);
  const first = await popup.locator(".slash-row.is-active").getAttribute("data-name");
  await page.keyboard.press("ArrowDown");
  expect(await popup.locator(".slash-row.is-active").getAttribute("data-name")).not.toBe(first);
});

test("the palette filters fuzzily across names and labels", async ({ page }) => {
  await page.fill("#promptInput", "/rev");
  const popup = page.locator("#slashPopup");
  await expect(popup.locator('.slash-row[data-name="review"]')).toBeVisible();
  await expect(popup.locator('.slash-row[data-name="reviewer"]')).toBeVisible();
  await expect(popup.locator('.slash-row[data-name="settings"]')).toHaveCount(0);
  await page.fill("#promptInput", "/cmpct");
  await expect(popup.locator(".slash-row").first()).toHaveAttribute("data-name", "compact");
  await page.fill("#promptInput", "/zzzz");
  await expect(popup).toContainText("No matching commands");
});

test("Tab completes a command and Enter runs it", async ({ page }) => {
  await page.fill("#promptInput", "/he");
  await page.keyboard.press("Tab");
  await expect(page.locator("#promptInput")).toHaveValue("/help ");
  await page.fill("#promptInput", "/he");
  await page.keyboard.press("Enter");
  await expect(page.locator("#messages")).toContainText("/compact");
  await expect(page.locator("#promptInput")).toHaveValue("");
});

test("/compact submits a compaction turn", async ({ page }) => {
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await page.fill("#promptInput", "/compact");
  await page.keyboard.press("Enter");
  await expect.poll(async () => (await lastSubmit(page))?.compact).toBe(true);
  const sub = await lastSubmit(page);
  expect(sub.message).toBe("");
  expect(sub.display).toBe("/compact");
});

test("prompt commands go to the agent with their arguments", async ({ page }) => {
  await page.fill("#promptInput", "/review the auth module");
  await page.keyboard.press("Escape");
  await page.click("#sendBtn");
  await expect.poll(async () => (await lastSubmit(page))?.command).toBe("review");
  const sub = await lastSubmit(page);
  expect(sub.message).toBe("the auth module");
  expect(sub.display).toBe("/review the auth module");
  await expect(page.locator("#messages .turn-row").first()).toContainText("/review the auth module");
});

test("/plan toggles plan mode and Implement switches back to build", async ({ page }) => {
  await page.fill("#promptInput", "/plan");
  await page.keyboard.press("Enter");
  await expect(page.locator("#modeChip")).toBeVisible();
  await expect(page.locator("#modeChip")).toContainText("Plan");
  await page.fill("#promptInput", "design the cache");
  await page.click("#sendBtn");
  await expect.poll(async () => (await lastSubmit(page))?.mode).toBe("plan");
  await expect(page.locator("#modeImplementBtn")).toBeVisible();
  await page.click("#modeImplementBtn");
  await expect.poll(async () => (await lastSubmit(page))?.mode).toBe("build");
  await expect(page.locator("#modeChip")).toBeHidden();
  // /plan with text sends that text in plan mode at once.
  await page.fill("#promptInput", "/plan sketch the api");
  await page.keyboard.press("Escape");
  await page.click("#sendBtn");
  await expect.poll(async () => (await lastSubmit(page))?.message).toBe("sketch the api");
  expect((await lastSubmit(page)).mode).toBe("plan");
});

test("/fork and /rename manage the current session", async ({ page }) => {
  await page.locator('#sessionList [data-session="sess-notify-1"]').first().click();
  await page.fill("#promptInput", "/rename Cache redesign");
  await page.keyboard.press("Escape");
  await page.click("#sendBtn");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eRenames?.[0]?.title))
    .toBe("Cache redesign");
  await page.fill("#promptInput", "/fork");
  await page.keyboard.press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eForks?.[0]?.sessionId))
    .toBe("sess-notify-1");
  await expect(page.locator("#sessionLabel")).toHaveText("sess-for");
});

test("/memories opens the memory pane and saves edits", async ({ page }) => {
  await page.fill("#promptInput", "/memories");
  await page.keyboard.press("Enter");
  const pane = page.locator("#settingsPaneMemory");
  await expect(pane).toBeVisible();
  await expect(pane).toContainText("Prefers terse answers");
  await expect(pane.locator("[data-mem-usage]").first()).toContainText("/");
  await pane.locator('[data-mem-target="global"] [data-mem-add]').fill("Use uv for Python");
  await pane.locator('[data-mem-target="global"] [data-mem-add]').press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eMemorySets?.slice(-1)[0]))
    .toMatchObject({ target: "global", entries: ["Use uv for Python"] });
  await pane.locator('[data-mem-target="user"] [data-mem-remove="0"]').click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eMemorySets?.slice(-1)[0]))
    .toMatchObject({ target: "user", entries: [] });
});

test("a running turn shows the working bar with elapsed time", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/tool", tool: { id: "t1", name: "shell", arguments: '{"cmd":"make test"}', result: "", status: "running" } },
      { event: "chat/delta", delta: "done", waitMs: 1600 },
    ];
  });
  await page.fill("#promptInput", "run the tests");
  await page.click("#sendBtn");
  const bar = page.locator("#workingBar");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("Working");
  await expect(bar).toContainText(/\d+s/);
  await expect(bar).toContainText("make test");
  await expect(page.locator('#sessionList .session-row[data-live="running"] .live-spinner').first()).toBeVisible();
  await expect(bar).toBeHidden({ timeout: 5000 });
});

test("the sidebar row menu renames inline and forks", async ({ page }) => {
  const row = page.locator('#sessionList .session-row:has([data-session="sess-notify-1"])').first();
  await row.hover();
  await row.locator('[data-row-menu="sess-notify-1"]').click();
  const menu = page.locator("#sessionRowMenu");
  await expect(menu).toBeVisible();
  await expect(menu.getByRole("menuitem")).toHaveText(["Rename", "Fork", "Fork into worktree", "Delete"]);
  await menu.getByRole("menuitem", { name: "Rename" }).click();
  const input = page.locator("#sessionList input.session-rename");
  await expect(input).toBeFocused();
  await input.fill("Sidebar title");
  await input.press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eRenames?.[0]))
    .toMatchObject({ sessionId: "sess-notify-1", title: "Sidebar title" });
  await row.hover();
  await row.locator('[data-row-menu="sess-notify-1"]').click();
  await menu.getByRole("menuitem", { name: "Fork", exact: true }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eForks?.[0]?.sessionId))
    .toBe("sess-notify-1");
});
