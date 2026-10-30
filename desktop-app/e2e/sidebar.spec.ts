// sidebar.spec.ts covers the Codex-style shell: an icon rail that holds
// the views and settings, and a panel that nests sessions under their
// project folder plus a short Recents list.
import { test, expect, type Page } from "@playwright/test";

const now = Math.floor(Date.now() / 1000);
const SESSIONS = [
  { sessionId: "s-api-2", cwd: "/work/api", title: "fix the auth bug", at: now },
  { sessionId: "s-web-1", cwd: "/work/web", title: "style the header", at: now - 10 },
  { sessionId: "s-api-1", cwd: "/work/api", title: "add rate limits", at: now - 20 },
  { sessionId: "s-cli-1", cwd: "/work/cli", title: "parse flags", at: now - 30 },
  { sessionId: "s-web-2", cwd: "/work/web", title: "dark mode", at: now - 40 },
  { sessionId: "s-api-3", cwd: "/work/api", title: "old migration", at: now - 50 },
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
        }));
      }
      if (cmd === "openSession") {
        const s = sessions.find((x: any) => x.sessionId === args.sessionId);
        return {
          sessionId: args.sessionId,
          cwd: s ? s.cwd : "/work/api",
          provider: "Opencode Go",
          model: "openai/gpt-4o",
        };
      }
      if (cmd === "sessionHistory") return [];
      return base(cmd, args);
    };
  }, SESSIONS);
  await page.goto("/");
  await expect(page.locator('[data-session="s-api-2"]')).toBeVisible();
}

test("the rail holds the views and settings, with no separate sidebar button", async ({ page }) => {
  await setup(page);
  for (const id of ["#agentTab", "#chatTab", "#settingsBtn"]) {
    await expect(page.locator(`#rail ${id}`)).toBeVisible();
  }
  await expect(page.locator("#sidebarToggleBtn")).toHaveCount(0);
  await expect(page.locator("#rail .rail-btn")).toHaveCount(3);
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-selected", "true");
});

test("sessions nest under their project folder, newest project first", async ({ page }) => {
  await setup(page);
  const heads = page.locator("#sessionList .project-head .project-name");
  await expect(heads).toHaveText(["api", "web", "cli"]);
  const api = page.locator('.project[data-cwd="/work/api"]');
  await expect(api.locator("[data-session]")).toHaveCount(3);
  await expect(api.locator(".project-head")).toHaveAttribute("title", "/work/api");
  await expect(page.locator('.project[data-cwd="/work/web"] [data-session]')).toHaveCount(2);
});

test("a project folds and stays folded after a reload", async ({ page }) => {
  await setup(page);
  const api = page.locator('.project[data-cwd="/work/api"]');
  await api.locator(".project-head").click();
  await expect(api.locator('[data-session="s-api-2"]')).toBeHidden();
  await expect(api.locator(".project-head")).toHaveAttribute("aria-expanded", "false");
  await page.reload();
  await expect(page.locator('[data-session="s-web-1"]')).toBeVisible();
  await expect(page.locator('.project[data-cwd="/work/api"] [data-session="s-api-2"]')).toBeHidden();
  await page.locator('.project[data-cwd="/work/api"] .project-head').click();
  await expect(page.locator('[data-session="s-api-2"]')).toBeVisible();
});

test("recents lists the five newest sessions and opens them", async ({ page }) => {
  await setup(page);
  const recents = page.locator("#recentList [data-recent]");
  await expect(recents).toHaveCount(5);
  await expect(recents.first()).toHaveText(/fix the auth bug/);
  await page.locator('[data-recent="s-cli-1"]').click();
  await expect(page.locator("#sessionLabel")).toHaveText("s-cli-1".slice(0, 8));
  await expect(page.locator('.session-row:has([data-session="s-cli-1"])')).toHaveClass(/is-active/);
  await expect(page.locator('.recent-row:has([data-recent="s-cli-1"])')).toHaveClass(/is-active/);
});

test("search filters projects and hides recents", async ({ page }) => {
  await setup(page);
  await page.fill("#searchInput", "dark");
  await expect(page.locator("#sessionList [data-session]")).toHaveCount(1);
  await expect(page.locator("#sessionList .project")).toHaveCount(1);
  await expect(page.locator("#recentWrap")).toBeHidden();
  await page.fill("#searchInput", "");
  await expect(page.locator("#recentWrap")).toBeVisible();
});

test("the Agent button opens and closes the drawer", async ({ page }) => {
  await setup(page);
  await page.click("#agentTab");
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#agentTab")).toBeVisible();
  await expect(page.locator("#settingsBtn")).toBeVisible();
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the sidebar floats over a main view that never resizes", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#sidebar")).toBeVisible();
  const open = await page.locator("#mainCard").boundingBox();
  const side = await page.locator("#sidebar").boundingBox();
  const rail = await page.locator("#rail").boundingBox();
  // The panel sits on top of the card, not beside it.
  expect(side!.x).toBeLessThan(open!.x + 40);
  expect(open!.x).toBeLessThanOrEqual(rail!.x + rail!.width + 1);

  await page.click("#agentTab");
  await expect(page.locator("#sidebar")).toBeHidden();
  const closed = await page.locator("#mainCard").boundingBox();
  expect(closed).toEqual(open);
});

test("the drawer closes on an outside click, Escape, and a session pick", async ({ page }) => {
  await setup(page);
  const side = page.locator("#sidebar");
  await expect(side).toBeVisible();

  // Clicks inside the panel keep it open.
  await page.click("#searchInput");
  await expect(side).toBeVisible();

  await page.mouse.click(900, 300);
  await expect(side).toBeHidden();

  await page.click("#agentTab");
  await expect(side).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(side).toBeHidden();

  await page.click("#agentTab");
  await page.locator('[data-recent="s-cli-1"]').click();
  await expect(side).toBeHidden();
  await expect(page.locator("#sessionLabel")).toHaveText("s-cli-1".slice(0, 8));
});

test("the session row menu works inside the drawer", async ({ page }) => {
  await setup(page);
  await page.locator('.session-row:has([data-session="s-api-2"])').hover();
  await page.locator('[data-row-menu="s-api-2"]').click();
  await expect(page.locator("#sessionRowMenu")).toBeVisible();
  await page.locator('#sessionRowMenu [data-row-act="rename"]').click();
  await expect(page.locator("#sidebar")).toBeVisible();
});

test("the Agent button reports the drawer state", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "true");
  await page.click("#agentTab");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-selected", "true");
});
