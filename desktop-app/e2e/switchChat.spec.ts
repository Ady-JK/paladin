// switchChat.spec.ts covers switching between a Paladin chat and a Claude Code
// chat: the chat area says it is loading at once, the history paints as soon as
// it is read (never after Claude's slow CLI catalogs), and the composer does
// not wait either.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Math.floor(Date.now() / 1000);

type Holds = "history" | "status" | "commands";

async function setup(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(
    ({ now }) => {
      const w = window as any;
      w.__release = {};
      w.__e2eHistories = {
        pal: [
          { role: "user", content: "paladin question", createdAt: now, index: 0 },
          { role: "assistant", content: "paladin answer", createdAt: now, index: 1 },
        ],
        cla: [
          { role: "user", content: "claude question", createdAt: now, index: 0 },
          { role: "assistant", content: "claude answer", createdAt: now, index: 1 },
        ],
      };
      w.__e2eOpenExtra = { cla: { harness: "claude", provider: "claude", model: "claude-sonnet-5-5" } };
      const base = w.__paladinBaseInvoke;
      const hold = (name: string) => new Promise<void>((resolve) => (w.__release[name] = resolve));
      w.__held = new Set<string>();
      w.__paladinBaseInvoke = async (cmd: string, args: any) => {
        if (cmd === "listSessions") {
          return [
            {
              sessionId: "pal",
              cwd: "/work/p",
              createdAt: now - 20,
              provider: "Opencode Go",
              model: "openai/gpt-4o",
              messageCount: 2,
              title: "paladin chat",
            },
            {
              sessionId: "cla",
              cwd: "/work/p",
              createdAt: now - 40,
              provider: "claude",
              model: "claude-sonnet-5-5",
              messageCount: 2,
              title: "claude chat",
              harness: "claude",
            },
          ];
        }
        if (cmd === "claudeStatus" && args?.deep && w.__held.has("status")) await hold("status");
        if (cmd === "claudeCommands" && w.__held.has("commands")) await hold("commands");
        if (cmd === "sessionHistoryPage" && args?.sessionId === "cla" && w.__held.has("history")) await hold("history");
        return base(cmd, args);
      };
    },
    { now },
  );
  await page.goto("/");
  await showSidebar(page);
}

const hold = (page: Page, ...names: Holds[]) =>
  page.evaluate((n) => n.forEach((x) => (window as any).__held.add(x)), names);
const release = (page: Page, name: Holds) => page.evaluate((n) => (window as any).__release[n]?.(), name);

test("switching to a Claude chat shows loading, then history without waiting for Claude's CLI", async ({ page }) => {
  await setup(page);
  await page.locator('[data-session="pal"]').click();
  await expect(page.locator("#messages")).toContainText("paladin answer");

  await hold(page, "history", "status", "commands");
  await page.locator('[data-session="cla"]').click();

  // The chat area owns up to the switch straight away, once a beat has passed.
  await expect(page.locator("#chatView")).toHaveClass(/is-switching/);
  await expect(page.locator("#chatLoading")).toBeVisible();
  // The sidebar selection has already moved to the clicked chat.
  await expect(page.locator('#sidebar .session-row:has([data-session="cla"])')).toHaveClass(/is-pending/);
  // The composer already looks like Claude Code and shows its model.
  await expect(page.locator("#promptInput")).toHaveAttribute("placeholder", /Claude Code/);
  await expect(page.locator("#modelSelectLabel")).toContainText("sonnet");

  // History lands while Claude's status and commands are still loading.
  await release(page, "history");
  await expect(page.locator("#messages")).toContainText("claude answer");
  await expect(page.locator("#messages")).not.toContainText("paladin answer");
  await expect(page.locator("#chatView")).not.toHaveClass(/is-switching/);
  await expect(page.locator("#chatLoading")).toBeHidden();
  await expect(page.locator("#sidebar .is-pending")).toHaveCount(0);
  await expect(page.locator('#sidebar .session-row:has([data-session="cla"])')).toHaveClass(/is-active/);

  // Catalogs arriving later change nothing visible.
  await release(page, "status");
  await release(page, "commands");
  await expect(page.locator("#modelSelectLabel")).toContainText("sonnet");
  await expect(page.locator("#messages")).toContainText("claude answer");
});

test("switching back to a Paladin chat also shows loading and then the chat", async ({ page }) => {
  await setup(page);
  await page.locator('[data-session="cla"]').click();
  await expect(page.locator("#messages")).toContainText("claude answer");
  await page.evaluate(() => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "sessionHistoryPage" && args?.sessionId === "pal")
        await new Promise<void>((r) => (w.__release.pal = r));
      return base(cmd, args);
    };
  });
  await page.locator('[data-session="pal"]').click();
  await expect(page.locator("#chatLoading")).toBeVisible();
  await page.evaluate(() => (window as any).__release.pal());
  await expect(page.locator("#messages")).toContainText("paladin answer");
  await expect(page.locator("#chatLoading")).toBeHidden();
  await expect(page.locator("#promptInput")).not.toHaveAttribute("placeholder", /Claude Code/);
});
