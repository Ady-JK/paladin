// sidebar-engines.spec.ts covers the engine sections inside a project: a
// folder worked on with Paladin, Codex and Claude Code lists each engine's
// chats under its own label. Recents stays one flat list with its chips.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Math.floor(Date.now() / 1000);
const SESSIONS = [
  { sessionId: "p-codex", cwd: "/work/mixed", title: "codex one", at: now, harness: "codex" },
  { sessionId: "p-claude", cwd: "/work/mixed", title: "claude one", at: now - 10, harness: "claude" },
  { sessionId: "p-paladin", cwd: "/work/mixed", title: "paladin one", at: now - 20 },
  { sessionId: "p-codex-2", cwd: "/work/mixed", title: "codex two", at: now - 30, harness: "codex" },
  { sessionId: "solo", cwd: "/work/solo", title: "only paladin", at: now - 40 },
];

async function setup(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((sessions) => {
    const w = window as any;
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
  }, SESSIONS);
  await page.goto("/");
  await showSidebar(page);
  await expect(page.locator('[data-session="p-codex"]')).toBeVisible();
}

test("a project lists Paladin, Codex and Claude Code chats in their own sections", async ({ page }) => {
  await setup(page);
  const project = page.locator('.project[data-cwd="/work/mixed"]');
  await expect(project.locator(".project-engine-label")).toHaveText(["Paladin", "Codex", "Claude Code"]);
  await expect(project.locator('[data-engine-group="paladin"] [data-session]')).toHaveText(["paladin one"]);
  await expect(project.locator('[data-engine-group="codex"] [data-session]')).toHaveText(["codex one", "codex two"]);
  await expect(project.locator('[data-engine-group="claude"] [data-session]')).toHaveText(["claude one"]);
  // Every chat appears exactly once, and the label replaces the per-row chip.
  await expect(project.locator("[data-session]")).toHaveCount(4);
  await expect(project.locator(".session-harness").first()).toBeHidden();
});

test("a project with one engine shows only that section", async ({ page }) => {
  await setup(page);
  const project = page.locator('.project[data-cwd="/work/solo"]');
  await expect(project.locator(".project-engine-label")).toHaveText(["Paladin"]);
});

test("Recents stays one flat list with engine chips", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#recentList .project-engine-label")).toHaveCount(0);
  const codex = page.locator("#recentList .recent-row", { hasText: "codex one" });
  await expect(codex.locator(".session-harness")).toBeVisible();
  await expect(codex.locator(".session-harness")).toHaveText("Codex");
});

test("search keeps the sections and folding hides them with the project", async ({ page }) => {
  await setup(page);
  await page.fill("#searchInput", "codex");
  const project = page.locator('.project[data-cwd="/work/mixed"]');
  await expect(project.locator(".project-engine-label")).toHaveText(["Codex"]);
  await page.fill("#searchInput", "");
  await project.locator(".project-head").click();
  await expect(project.locator(".project-engine-label").first()).toBeHidden();
  await project.locator(".project-head").click();
  await expect(project.locator(".project-engine-label").first()).toBeVisible();
});
