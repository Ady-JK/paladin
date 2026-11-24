// startProgress.spec.ts covers the "still loading" feedback when a Codex or
// Claude Code session starts: a status pill, a spinner on the folder's +, the
// "still starting" note, and a start that the user has moved on from.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Math.floor(Date.now() / 1000);
const SESSIONS = [
  { sessionId: "solo", cwd: "/work/solo", title: "only paladin", at: now - 40 },
  { sessionId: "other", cwd: "/work/mixed", title: "another chat", at: now - 20 },
];

// setup holds the engines' deep status check (the slow CLI launch) until the
// test releases it with window.__release.codex() / .claude().
async function setup(page: Page, fail = ""): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    ({ sessions, fail }) => {
      const w = window as any;
      w.__release = {};
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
        if ((cmd === "claudeStatus" || cmd === "codexStatus") && args?.deep) {
          const engine = cmd === "claudeStatus" ? "claude" : "codex";
          await new Promise<void>((resolve) => (w.__release[engine] = resolve));
          if (fail === engine)
            return { installed: true, loggedIn: false, models: [], error: `${engine} failed to start` };
        }
        return base(cmd, args);
      };
    },
    { sessions: SESSIONS, fail },
  );
  await page.goto("/");
  await showSidebar(page);
  await expect(page.locator('[data-session="solo"]')).toBeVisible();
}

const add = (page: Page, cwd: string) => page.locator(`.project[data-cwd="${cwd}"] .project-add`);
const pill = (page: Page) => page.locator("#startPill");
const release = (page: Page, engine: "claude" | "codex") =>
  page.evaluate((e) => (window as any).__release[e]?.(), engine);
const waitHeld = (page: Page, engine: "claude" | "codex") =>
  expect.poll(() => page.evaluate((e) => typeof (window as any).__release[e], engine)).toBe("function");

async function startFromFolder(page: Page, cwd: string, engine: RegExp): Promise<void> {
  await add(page, cwd).click();
  await page.locator("#projectNewMenu").getByRole("menuitem", { name: engine }).click();
}

test("a slow Claude start shows a pill and a spinner, and the app stays usable", async ({ page }) => {
  await setup(page);
  await startFromFolder(page, "/work/solo", /Claude Code/);
  await waitHeld(page, "claude");
  await expect(pill(page)).toBeVisible();
  await expect(pill(page)).toContainText("Starting Claude Code…");
  await expect(pill(page)).toContainText("solo");
  await expect(add(page, "/work/solo")).toBeDisabled();
  await expect(add(page, "/work/solo")).toHaveAttribute("aria-busy", "true");
  await expect(add(page, "/work/solo")).toHaveAttribute("title", /Starting Claude Code/);
  // Other folders stay clickable, and the composer is not blocked.
  await expect(add(page, "/work/mixed")).toBeEnabled();
  await page.fill("#promptInput", "typing while it loads");
  await expect(page.locator("#promptInput")).toHaveValue("typing while it loads");
  // The chat on screen is untouched until the engine is ready.
  await expect(page.locator("body")).not.toHaveClass(/harness-/);

  await release(page, "claude");
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
  await expect(pill(page)).toBeHidden();
  await expect(add(page, "/work/solo")).toBeEnabled();
  await expect(add(page, "/work/solo")).not.toHaveAttribute("aria-busy", "true");
  await expect(page.locator("#workspaceLabel")).toHaveText(/solo/);
});

test("the pill is a polite status for assistive tech and ignores the pointer", async ({ page }) => {
  await setup(page);
  await startFromFolder(page, "/work/solo", /Codex/);
  await waitHeld(page, "codex");
  await expect(pill(page)).toHaveAttribute("role", "status");
  await expect(pill(page)).toHaveCSS("pointer-events", "none");
  await release(page, "codex");
  await expect(page.locator("body")).toHaveClass(/harness-codex/);
});

test("a start that takes long says it is still starting", async ({ page }) => {
  await page.clock.install();
  await setup(page);
  await startFromFolder(page, "/work/solo", /Claude Code/);
  await waitHeld(page, "claude");
  await expect(pill(page)).toContainText("Starting Claude Code…");
  await expect(pill(page)).not.toContainText("Still starting");
  await page.clock.fastForward(8100);
  await expect(pill(page)).toContainText("Still starting Claude Code… this can take a moment");
  await release(page, "claude");
  await expect(pill(page)).toBeHidden();
});

test("the New Session dialog closes at once and shows the same progress", async ({ page }) => {
  await setup(page);
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartClaudeBtn")).toBeEnabled();
  await page.click("#sessionStartClaudeBtn");
  await expect(page.locator("#sessionModal")).toBeHidden();
  await waitHeld(page, "claude");
  await expect(pill(page)).toContainText("Starting Claude Code…");
  await release(page, "claude");
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
  await expect(pill(page)).toBeHidden();
});

test("a failed start clears the indicator, says why, and leaves the chat alone", async ({ page }) => {
  await setup(page, "codex");
  await startFromFolder(page, "/work/solo", /Codex/);
  await waitHeld(page, "codex");
  await expect(pill(page)).toBeVisible();
  await release(page, "codex");
  await expect(page.locator("#toastText")).toContainText("codex failed to start");
  await expect(pill(page)).toBeHidden();
  await expect(add(page, "/work/solo")).toBeEnabled();
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
});

test("opening another chat while an engine loads is not undone when it finishes", async ({ page }) => {
  await setup(page);
  await startFromFolder(page, "/work/solo", /Codex/);
  await waitHeld(page, "codex");
  await page.locator('[data-session="other"]').click();
  await expect(page.locator("#sessionLabel")).toHaveText("other");
  const folder = await page.locator("#workspaceLabel").textContent();
  await release(page, "codex");
  await expect(pill(page)).toBeHidden();
  // The late engine does not switch the view or the folder under the user.
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
  await expect(page.locator("#sessionLabel")).toHaveText("other");
  await expect(page.locator("#workspaceLabel")).toHaveText(folder ?? "");
  await expect(page.locator("#workspaceLabel")).not.toHaveText(/solo/);
});

test("starting something newer wins over a slower earlier start", async ({ page }) => {
  await setup(page);
  await startFromFolder(page, "/work/solo", /Codex/);
  await waitHeld(page, "codex");
  await add(page, "/work/mixed").click();
  await page
    .locator("#projectNewMenu")
    .getByRole("menuitem", { name: /Paladin/ })
    .click();
  await expect(page.locator("#workspaceLabel")).toHaveText(/mixed/);
  await release(page, "codex");
  await expect(pill(page)).toBeHidden();
  await expect(page.locator("body")).not.toHaveClass(/harness-/);
  await expect(page.locator("#workspaceLabel")).toHaveText(/mixed/);
});
