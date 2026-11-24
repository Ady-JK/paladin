// projectNew.spec.ts covers the "+" on each sidebar folder: a menu of the
// three engines that starts a blank session in that folder.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Math.floor(Date.now() / 1000);
const SESSIONS = [
  { sessionId: "p-codex", cwd: "/work/mixed", title: "codex one", at: now, harness: "codex" },
  { sessionId: "p-paladin", cwd: "/work/mixed", title: "paladin one", at: now - 20 },
  { sessionId: "solo", cwd: "/work/solo", title: "only paladin", at: now - 40 },
];

async function setup(page: Page, status: Record<string, unknown> = {}): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    ({ sessions, status }) => {
      const w = window as any;
      Object.assign(w, status);
      const base = w.__paladinBaseInvoke;
      w.__paladinBaseInvoke = async (cmd: string, args: any) => {
        if (cmd === "listSessions") {
          return sessions.map((s: any) => ({
            sessionId: s.sessionId,
            cwd: s.cwd,
            createdAt: s.at,
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: 2,
            title: s.title,
            ...(s.harness ? { harness: s.harness } : {}),
          }));
        }
        return base(cmd, args);
      };
    },
    { sessions: SESSIONS, status },
  );
  await page.goto("/");
  await showSidebar(page);
  await expect(page.locator('[data-session="solo"]')).toBeVisible();
}

const add = (page: Page, cwd: string) => page.locator(`.project[data-cwd="${cwd}"] .project-add`);
const menu = (page: Page) => page.locator("#projectNewMenu");

test("every folder has a + that opens the three engines", async ({ page }) => {
  await setup(page);
  await expect(page.locator(".project-add")).toHaveCount(2);
  await expect(add(page, "/work/solo")).toBeVisible();
  await add(page, "/work/solo").click();
  await expect(menu(page)).toBeVisible();
  await expect(menu(page).getByRole("menuitem")).toHaveText([/Paladin/, /Codex/, /Claude Code/]);
  await expect(menu(page)).toContainText("New session in solo");
  // The Paladin row names the composer's current model, like the dialog.
  await expect(page.locator("#modelSelectLabel")).toContainText("claude-fable");
  await expect(menu(page).getByRole("menuitem", { name: /Paladin/ })).toContainText("claude-fable");
  await page.keyboard.press("Escape");
  await expect(menu(page)).toHaveCount(0);
  await expect(add(page, "/work/solo")).toBeFocused();
});

test("the + neither folds the folder nor starts anything until an engine is picked", async ({ page }) => {
  await setup(page);
  await add(page, "/work/mixed").click();
  await expect(page.locator('.project[data-cwd="/work/mixed"] .project-head')).toHaveAttribute("aria-expanded", "true");
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
  // Clicking it again, or anywhere else, closes the menu.
  await add(page, "/work/mixed").click();
  await expect(menu(page)).toHaveCount(0);
  await add(page, "/work/mixed").click();
  await page.locator("#messages").click({ position: { x: 5, y: 5 } });
  await expect(menu(page)).toHaveCount(0);
});

test("Paladin starts a blank chat in that folder", async ({ page }) => {
  await setup(page);
  await add(page, "/work/solo").click();
  await menu(page)
    .getByRole("menuitem", { name: /Paladin/ })
    .click();
  await expect(menu(page)).toHaveCount(0);
  await expect(page.locator("#workspaceLabel")).toHaveText(/solo/);
  await expect(page.locator("#sessionLabel")).toHaveText("—");
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
  await expect(page.locator('.project[data-cwd="/work/solo"]')).toHaveClass(/is-current/);
  expect(await page.evaluate(() => (window as any).__e2eSubmits?.length ?? 0)).toBe(0);
});

test("Codex and Claude Code start their own blank chats in that folder", async ({ page }) => {
  await setup(page);
  await add(page, "/work/mixed").click();
  await menu(page).getByRole("menuitem", { name: /Codex/ }).click();
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
  await expect(page.locator("#workspaceLabel")).toHaveText(/mixed/);
  await expect(page.locator("#sessionLabel")).toHaveText("—");

  await add(page, "/work/solo").click();
  await menu(page)
    .getByRole("menuitem", { name: /Claude Code/ })
    .click();
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
  await expect(page.locator("body")).not.toHaveClass(/harness-codex/);
  await expect(page.locator("#workspaceLabel")).toHaveText(/solo/);

  // And back to Paladin: the engine of the previous chat does not leak.
  await add(page, "/work/mixed").click();
  await menu(page)
    .getByRole("menuitem", { name: /Paladin/ })
    .click();
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
});

test("an engine that is not installed stays listed, greyed, with its reason", async ({ page }) => {
  await setup(page, {
    __e2eCodex: { installed: false, loggedIn: false, models: [] },
    __e2eClaude: { installed: false, loggedIn: false, models: [], error: "Install Claude Code" },
  });
  await add(page, "/work/solo").click();
  const codex = menu(page).getByRole("menuitem", { name: /Codex/ });
  const claude = menu(page).getByRole("menuitem", { name: /Claude Code/ });
  await expect(codex).toBeDisabled();
  await expect(codex).toContainText("not installed");
  await expect(codex).toHaveAttribute("title", /not found|install/i);
  await expect(claude).toBeDisabled();
  await expect(claude).toHaveAttribute("title", /Install Claude/);
  await expect(menu(page).getByRole("menuitem", { name: /Paladin/ })).toBeEnabled();
});

test("arrow keys move through the engines", async ({ page }) => {
  await setup(page);
  await add(page, "/work/solo").click();
  await expect(menu(page).getByRole("menuitem", { name: /Paladin/ })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(menu(page).getByRole("menuitem", { name: /Codex/ })).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");
  await expect(menu(page).getByRole("menuitem", { name: /Claude Code/ })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
});
