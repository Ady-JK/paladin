// A failed tool call shows its reason in the transcript, not a generic
// line. The reason comes from the tool result the core stores.
import { test, expect } from "@playwright/test";

test("a failed shell status check shows the job's reason", async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  const result = JSON.stringify({
    job_id: "job-15",
    status: "failed",
    exit_code: 2,
    error: "shell: job job-15 failed",
  });
  await page.evaluate((result) => {
    (window as any).__e2eChatEvents = [
      {
        event: "chat/tool",
        tool: { id: "w1", name: "shell", arguments: '{"action":"status","job_id":"job-15"}', result, status: "failed" },
      },
      { event: "chat/delta", delta: "done" },
    ];
  }, result);
  await page.fill("#promptInput", "check the tests");
  await page.click("#sendBtn");
  const messages = page.locator("#messages");
  await expect(messages).toContainText("done");
  await messages.getByRole("button", { name: /Ran 1 command/ }).click();
  await messages.getByText("status job-15").click();
  await expect(messages).toContainText("shell: job job-15 failed");
  await expect(messages).not.toContainText("the call failed");
});
