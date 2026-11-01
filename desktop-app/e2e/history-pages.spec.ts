// history-pages.spec.ts covers the paged transcript: a long session
// paints the newest rows first, and "Load earlier messages" prepends
// older rows without duplicates or losing the newest ones.
import { showSidebar } from "./drawer";
import { test, expect, type Page } from "@playwright/test";

const ROWS = 300;

async function setup(page: Page): Promise<void> {
  await page.addInitScript((rows) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    const history = Array.from({ length: rows }, (_, i) => ({
      role: i % 2 ? "assistant" : "user",
      content: `msg ${i}`,
      createdAt: 1,
      ...(i % 2 ? {} : { index: i }),
    }));
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        return [
          {
            sessionId: "sess-long",
            cwd: "/home/tester/project",
            createdAt: Math.floor(Date.now() / 1000),
            provider: "Opencode Go",
            model: "openai/gpt-4o",
            messageCount: rows,
            title: "long one",
          },
        ];
      }
      if (cmd === "sessionHistory") return history;
      if (cmd === "sessionChanges") return { available: false, turns: [], files: [] };
      return base(cmd, args);
    };
  }, ROWS);
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-long"]');
  await expect(page.locator("#sessionLabel")).toHaveText("sess-lon");
}

test("a long transcript paints the newest page and loads older ones", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await setup(page);

  const messages = page.locator("#messages");
  await expect(messages.getByText("msg 299", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 0", { exact: true })).toHaveCount(0);

  const earlier = page.locator(".history-earlier-btn");
  await expect(earlier).toBeVisible();
  for (let guard = 0; guard < 5 && (await earlier.count()); guard++) {
    await earlier.click();
    await page.waitForTimeout(150);
  }

  await expect(messages.getByText("msg 0", { exact: true })).toBeVisible();
  await expect(messages.getByText("msg 299", { exact: true })).toBeVisible();
  await expect(earlier).toHaveCount(0);
  // Every row appears once, not once per page.
  await expect(messages.getByText("msg 150", { exact: true })).toHaveCount(1);
});

test("a short transcript shows no earlier-messages button", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await showSidebar(page);
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator(".history-earlier-btn")).toHaveCount(0);
});
