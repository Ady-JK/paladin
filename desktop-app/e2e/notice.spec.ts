import { test, expect } from "@playwright/test";

// A harness (Codex) can tell the user things mid-turn: a warning, a model
// switch, a server that would not start. They show as quiet rows in the turn.
test("a harness notice shows as a quiet row in the turn", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/notice", notice: { kind: "warning", text: "MCP server docs failed to start: spawn failed" } },
      { event: "chat/notice", notice: { kind: "info", text: "Codex switched from gpt-a to gpt-b" } },
      { event: "chat/delta", delta: "done" },
    ];
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");

  const rows = page.locator("#messages .notice-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toHaveAttribute("data-kind", "warning");
  await expect(rows.nth(0)).toContainText("MCP server docs failed to start: spawn failed");
  await expect(rows.nth(1)).toHaveAttribute("data-kind", "info");
  await expect(page.locator("#messages")).toContainText("done");
});

// A memory entry is recalled into a conversation once; a later message matching
// the same entry is told it was skipped, as the same quiet info row.
test("a skipped memory recall shows as an info row", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/notice",
        notice: { kind: "info", text: "Skipped recalling project memory: this entry is already in the conversation." },
      },
      { event: "chat/delta", delta: "done" },
    ];
  });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await page.fill("#promptInput", "go");
  await page.click("#sendBtn");

  const row = page.locator("#messages .notice-row");
  await expect(row).toHaveCount(1);
  await expect(row).toHaveAttribute("data-kind", "info");
  await expect(row).toContainText("Skipped recalling project memory");
});
