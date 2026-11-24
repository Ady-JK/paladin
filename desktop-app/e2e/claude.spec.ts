import { test, expect, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});
async function startClaude(page: Page): Promise<void> {
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartClaudeBtn")).toBeEnabled();
  await page.click("#sessionStartClaudeBtn");
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
  await expect(page.locator("#sessionModal")).toBeHidden();
}
test("Claude discovery is nonblocking and restarts with the host", async ({ page }) => {
  await expect.poll(() => page.evaluate(() => (window as any).__e2eClaudeDiscoveryStarts)).toBe(1);
  await page.fill("#promptInput", "keep my draft");
  await page.evaluate(() => (window as any).__e2eHub.emit({ event: "host/restarted" }));
  await expect.poll(() => page.evaluate(() => (window as any).__e2eClaudeDiscoveryStarts)).toBe(2);
  await expect(page.locator("#promptInput")).toHaveValue("keep my draft");
});
test("the new-session dialog explains a missing Claude CLI", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eClaude = { installed: false, loggedIn: false, models: [], error: "Install Claude Code" };
  });
  await showSidebar(page);
  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionStartClaudeBtn")).toBeDisabled();
  await expect(page.locator("#sessionStartClaudeBtn")).toHaveAttribute("title", /Install Claude/);
  await expect(page.locator("#sessionStartBtn")).toBeEnabled();
});
test("Claude uses its CLI model catalog and creates a Claude session", async ({ page }) => {
  await startClaude(page);
  await expect(page.locator("#promptInput")).toHaveAttribute("placeholder", /Claude Code/);
  await page.fill("#promptInput", "hello Claude");
  await page.click("#sendBtn");
  await expect(page.locator("#messages")).toContainText("hello Claude");
  const opened = await page.evaluate(() => (window as any).__e2eOpens.find((v: any) => v.harness === "claude"));
  expect(opened.harness).toBe("claude");
  expect(opened.model).toBe("claude-haiku-5-5");
  const submitted = await page.evaluate(() => (window as any).__e2eSubmits.at(-1));
  expect(submitted.provider).toBe("claude");
  expect(submitted.model).toBe("claude-haiku-5-5");
});
test("Claude shows its native slash commands and plan mode", async ({ page }) => {
  await startClaude(page);
  await page.fill("#promptInput", "/rev");
  await expect(page.locator("#slashPopup")).toContainText("review");
  await page.press("#promptInput", "Escape");
  await page.fill("#promptInput", "/plan inspect the parser");
  await page.press("#promptInput", "Enter");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eSubmits.at(-1)?.mode)).toBe("plan");
  await expect(page.locator("body")).toHaveClass(/harness-claude/);
});
test("Claude keeps native-only tool controls hidden and effort available", async ({ page }) => {
  await startClaude(page);
  await expect(page.locator(".tools-wrap")).toBeHidden();
  await expect(page.locator(".mcp-chip")).toBeHidden();
  await expect(page.locator("#modelRefresh")).toBeHidden();
  await page.fill("#promptInput", "/thinking");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#thinkingMenu")).toBeVisible();
});
test("discovered Claude conversations have a Claude Code badge", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eExtraSessions = [
      {
        sessionId: "saved-claude",
        cwd: "/home/tester/project",
        title: "Saved Claude",
        provider: "claude",
        harness: "claude",
        model: "claude-haiku-5-5",
        messageCount: 0,
      },
    ];
    (window as any).__e2eHub.emit({ event: "sessions/changed" });
  });
  await showSidebar(page);
  await expect(page.locator('.session-row:has([data-session="saved-claude"]) .session-harness')).toHaveText(
    "Claude Code",
  );
});
