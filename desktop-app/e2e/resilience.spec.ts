import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("a provider retry shows a notice that clears once output streams", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/retry", retry: { attempt: 1, max: 6, delayMs: 2000, reason: "status 503: overloaded" } },
      { event: "chat/delta", delta: "Recovered.", waitMs: 1200 },
    ];
  });
  await page.fill("#promptInput", "do it");
  await page.click("#sendBtn");
  const notice = page.locator('#messages [data-notice="retry"]');
  await expect(notice).toContainText("Retrying in 2s (attempt 1/6)");
  await expect(notice).toContainText("status 503: overloaded");
  await expect(page.locator("#daemonLabel")).toContainText("retrying");
  await expect(page.locator("#messages")).toContainText("Recovered.");
  await expect(notice).toHaveCount(0);
});

test("a compaction streams a divider with the token change", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/compact", compaction: { phase: "start", reason: "auto", tokensBefore: 170000 } },
      { event: "chat/compact", compaction: { phase: "done", reason: "auto", tokensBefore: 170000, tokensAfter: 30500 }, waitMs: 600 },
    ];
  });
  await page.fill("#promptInput", "keep going");
  await page.click("#sendBtn");
  const divider = page.locator('#messages [data-notice="compaction"]');
  await expect(divider).toContainText("Compacting context");
  await expect(divider).toContainText("Context compacted · 170k → 31k tokens");
  await expect(page.locator("#messages")).toContainText("Stub reply to: keep going");
});

test("a failed compaction says so without failing the turn", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      { event: "chat/compact", compaction: { phase: "start", reason: "auto", tokensBefore: 170000 } },
      { event: "chat/compact", compaction: { phase: "failed", reason: "auto", tokensBefore: 170000, error: "summary boom" } },
    ];
  });
  await page.fill("#promptInput", "keep going");
  await page.click("#sendBtn");
  await expect(page.locator('#messages [data-notice="compaction"]')).toContainText("Compaction failed: summary boom");
  await expect(page.locator("#messages")).toContainText("Stub reply to: keep going");
});

test("history renders a compaction divider with an expandable summary", async ({ page }) => {
  await page.evaluate(() => {
    const w = window as any;
    const base = w.paladin.invoke;
    w.paladin.invoke = async (method: string, params: any) => {
      if (method === "sessionHistory") {
        const at = Math.floor(Date.now() / 1000);
        return [
          { role: "user", content: "first task", createdAt: at },
          { role: "assistant", content: "Did the first part.", createdAt: at },
          { role: "system", kind: "compaction", content: "The user asked for X; Y is done.", createdAt: at },
          { role: "assistant", content: "Continuing after compaction.", createdAt: at },
        ];
      }
      return base(method, params);
    };
  });
  await page.click('[data-session="sess-notify-1"]');
  await expect(page.locator("#messages")).toContainText("Continuing after compaction.");
  const divider = page.locator('#messages [data-notice="compaction"]');
  await expect(divider).toContainText("Context compacted");
  await expect(divider.locator("details")).not.toHaveAttribute("open", "");
  await divider.locator("summary").click();
  await expect(divider).toContainText("The user asked for X; Y is done.");
  const text = (await page.locator("#messages").textContent()) ?? "";
  expect(text.indexOf("Did the first part.")).toBeLessThan(text.indexOf("Context compacted"));
  expect(text.indexOf("Context compacted")).toBeLessThan(text.indexOf("Continuing after compaction."));
});

test("an edit applied by a fuzzy match is labeled", async ({ page }) => {
  await page.evaluate(() => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/tool",
        tool: {
          id: "e1",
          name: "edit",
          arguments: JSON.stringify({ path: "/home/tester/project/a.go", old_string: "a  :=  1", new_string: "a := 2" }),
          result: JSON.stringify({ path: "/home/tester/project/a.go", replacements: 1, match_strategy: "whitespace", note: "re-read" }),
          status: "ok",
        },
      },
    ];
  });
  await page.fill("#promptInput", "edit it");
  await page.click("#sendBtn");
  await page.locator('[data-tool="edit"] button').first().click();
  await expect(page.locator('[data-tool="edit"]')).toContainText("matched ignoring whitespace");
});
