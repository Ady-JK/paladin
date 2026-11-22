// sidebar.spec.ts covers the Codex-style shell: an icon rail that holds
// the views and settings, and a panel that nests sessions under their
// project folder plus a short Recents list.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

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
  await showSidebar(page);
  await expect(page.locator('[data-session="s-api-2"]')).toBeVisible();
}

test("the rail holds sessions, shortcuts and settings, with no separate sidebar button", async ({ page }) => {
  await setup(page);
  for (const id of ["#agentTab", "#shortcutsBtn", "#settingsBtn"]) {
    await expect(page.locator(`#rail ${id}`)).toBeVisible();
  }
  await expect(page.locator("#chatTab")).toHaveCount(0);
  await expect(page.locator("#sidebarToggleBtn")).toHaveCount(0);
  await expect(page.locator("#rail #ideModeBtn")).toBeVisible();
  await expect(page.locator("#rail #ideTerminalToggle")).toBeVisible();
  // Files and Activity are always on the rail; Problems only appears in IDE mode.
  for (const id of ["Explorer", "Activity"]) {
    await expect(page.locator(`#rail #ide${id}Toggle`)).toBeVisible();
  }
  await expect(page.locator("#rail #ideProblemsToggle")).toBeHidden();
  await expect(page.locator("#titleBar #sessionChangesBtn, #titleBar #ideModeBtn")).toHaveCount(0);
  // An expand toggle, not a current-view tab: it must not claim selection.
  await expect(page.locator("#agentTab")).not.toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", /true|false/);
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
  await showSidebar(page);
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
  await showSidebar(page);
  await page.locator('[data-recent="s-cli-1"]').click();
  await expect(page.locator("#sessionLabel")).toHaveText("s-cli-1".slice(0, 8));
  await expect(page.locator('.session-row:has([data-session="s-cli-1"])')).toHaveClass(/is-active/);
  await expect(page.locator('.recent-row:has([data-recent="s-cli-1"])')).toHaveClass(/is-active/);
});

test("search filters projects and hides recents", async ({ page }) => {
  await setup(page);
  await showSidebar(page);
  await page.fill("#searchInput", "dark");
  await expect(page.locator("#sessionList [data-session]")).toHaveCount(1);
  await expect(page.locator("#sessionList .project")).toHaveCount(1);
  await expect(page.locator("#recentWrap")).toBeHidden();
  await showSidebar(page);
  await page.fill("#searchInput", "");
  await expect(page.locator("#recentWrap")).toBeVisible();
});

test("the app starts with the drawer closed", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "false");
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the Sessions button opens and closes the drawer", async ({ page }) => {
  await setup(page);
  await page.click("#agentTab");
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#agentTab")).toBeVisible();
  await expect(page.locator("#settingsBtn")).toBeVisible();
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the sidebar pushes the main view instead of covering it", async ({ page }) => {
  await setup(page);
  await showSidebar(page);
  const sidebar = page.locator("#sidebar");
  await expect(sidebar).toBeVisible();
  // The column animates its width, so measure the settled state: a box
  // read mid-transition reports a partial width and a shifted card.
  await expect.poll(async () => (await sidebar.boundingBox())!.width, { timeout: 4000 }).toBeGreaterThan(300);
  // A real column: it ends where the card begins, and the card starts
  // after the rail rather than underneath the panel. Measured through a
  // poll so a box read mid-animation cannot fail the test: the columns
  // animate their width into place.
  await expect
    .poll(
      async () => {
        const [card, drawer, bar] = await Promise.all([
          page.locator("#mainCard").boundingBox(),
          sidebar.boundingBox(),
          page.locator("#rail").boundingBox(),
        ]);
        if (!card || !drawer || !bar) return Number.NaN;
        const startsAfterRail = drawer.x >= bar.x + bar.width - 1;
        const endsBeforeCard = drawer.x + drawer.width <= card.x + 1.5;
        return startsAfterRail && endsBeforeCard;
      },
      { timeout: 4000 },
    )
    .toBe(true);

  const openWidth = (await page.locator("#mainCard").boundingBox())!.width;
  await page.click("#agentTab");
  await expect(sidebar).toBeHidden();
  // Closing the column gives the space back to the card.
  await expect
    .poll(async () => (await page.locator("#mainCard").boundingBox())!.width, { timeout: 4000 })
    .toBeGreaterThan(openWidth);
});

test("clicking outside the sidebar leaves it open", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.click("#promptInput");
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.locator("#messages").click({ position: { x: 5, y: 5 } });
  await expect(page.locator("#sidebar")).toBeVisible();
  // Escape is still the way out.
  await page.keyboard.press("Escape");
  await expect(page.locator("#sidebar")).toBeHidden();
});

test("the main card has equal top and bottom insets", async ({ page }) => {
  await setup(page);
  const card = await page.locator("#mainCard").boundingBox();
  const viewport = page.viewportSize();
  expect(card).not.toBeNull();
  expect(viewport).not.toBeNull();
  expect(card!.y).toBe(8);
  expect(viewport!.height - card!.y - card!.height).toBe(8);
});

test("the drawer stays open through an outside click and closes on Escape or the rail", async ({ page }) => {
  await setup(page);
  await showSidebar(page);
  const side = page.locator("#sidebar");
  await expect(side).toBeVisible();

  // Clicks inside the panel keep it open.
  await page.click("#searchInput");
  await expect(side).toBeVisible();

  // A column is not a popover: clicking the conversation must not hide it.
  await page.mouse.click(900, 300);
  await expect(side).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(side).toBeHidden();

  await page.click("#agentTab");
  await expect(side).toBeVisible();
  await page.click("#agentTab");
  await expect(side).toBeHidden();
});

test("picking a session keeps the sessions column open", async ({ page }) => {
  await setup(page);
  await showSidebar(page);
  const side = page.locator("#sidebar");
  await page.locator('[data-recent="s-cli-1"]').click();
  // Reopening the list after every pick turned browsing into a chore.
  await page.waitForTimeout(400);
  await expect(side).toBeVisible();
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

test("the Sessions button reports the drawer state", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "true");
  await page.click("#agentTab");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "false");
  // Collapsed, and still not claiming to be the selected view.
  await expect(page.locator("#agentTab")).not.toHaveAttribute("aria-selected", "true");
});

test("switching to IDE closes the sessions drawer and keeps Sessions available", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-label", "Sessions");
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeHidden();
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-label", "Sessions");
  await page.locator("#agentTab").click();
  await expect(page.locator("#sidebar")).toBeVisible();
});
