// damaged.spec.ts: a session the host could not read is still listed,
// marked with the reason, and opening it explains instead of failing.
import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

const now = Math.floor(Date.now() / 1000);

async function setup(page: Page): Promise<{ opened: () => Promise<string[]> }> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript((at) => {
    const w = window as any;
    const base = w.__paladinBaseInvoke;
    w.__opened = [];
    w.__paladinBaseInvoke = async (cmd: string, args: any) => {
      if (cmd === "listSessions") {
        const row = { provider: "", model: "", messageCount: 0, createdAt: at };
        return [
          { ...row, sessionId: "s-good", cwd: "/work/api", title: "fine", messageCount: 2 },
          { ...row, sessionId: "s-bad", cwd: "", title: "Unreadable session", damaged: "metadata: unexpected EOF" },
        ];
      }
      if (cmd === "openSession") {
        w.__opened.push(args.sessionId);
        return { sessionId: args.sessionId, cwd: "/work/api", provider: "", model: "" };
      }
      if (cmd === "sessionHistory") return [];
      return base(cmd, args);
    };
  }, now);
  await page.goto("/");
  await showSidebar(page);
  return { opened: () => page.evaluate(() => (window as any).__opened as string[]) };
}

test("a damaged session shows a badge with the reason", async ({ page }) => {
  await setup(page);
  const row = page.locator(".session-row", { has: page.locator('[data-session="s-bad"]') });
  const badge = row.locator(".session-damaged");
  await expect(badge).toBeVisible();
  await expect(badge).toHaveAttribute("title", /metadata: unexpected EOF/);
  await expect(
    page.locator(".session-row", { has: page.locator('[data-session="s-good"]') }).locator(".session-damaged"),
  ).toHaveCount(0);
  await expect(page.locator('.project[data-cwd=""] .project-name')).toHaveText("Unknown folder");
});

test("opening a damaged session reports why instead of loading it", async ({ page }) => {
  const { opened } = await setup(page);
  await page.locator('[data-session="s-bad"]').click();
  await expect(page.locator("#daemonLabel")).toContainText("metadata: unexpected EOF");
  expect(await opened()).not.toContain("s-bad");
});
