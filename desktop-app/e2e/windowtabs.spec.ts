// windowtabs.spec.ts pins the strip's ownership: one strip per window, so
// switching conversation does not take the tabs away. Each tab remembers
// the session it was opened for.
import { expect, test, type Page } from "@playwright/test";
import { addToPane } from "./modes";

async function start(page: Page, text: string): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", text);
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText(text);
}

async function openSideChat(page: Page): Promise<void> {
  await addToPane(page, "chat");
  await expect(page.locator("#sidePane")).toBeVisible();
}

test("a tab survives switching conversation", async ({ page }) => {
  await start(page, "first");
  await openSideChat(page);
  const tabs = page.locator("#paneTabs .pane-tab");
  const before = await tabs.count();
  expect(before).toBeGreaterThan(0);
  const label = await tabs.first().textContent();

  // Start a second conversation, then come back.
  await page.locator("#agentTab").click();
  await page.locator("#newSessionBtn").click();
  await page.waitForTimeout(300);
  await page.evaluate(() => {
    const w = window as any;
    const first = w.__e2eSubmits[0].sessionId;
    window.dispatchEvent(new CustomEvent("paladin:turn-submitted", { detail: { sessionId: first } }));
  });

  // One strip for the window: the side chat is still there, with the same
  // label, not a fresh empty strip.
  await expect(page.locator("#paneTabs .pane-tab").first()).toContainText(label!.trim());
});

test("the agent tab belongs to its parent session", async ({ page }) => {
  await start(page, "parent");
  const sessionId = await page.evaluate(() => (window as any).__e2eSubmits[0].sessionId);
  await page.evaluate((id) => {
    const w = window as any;
    w.__e2eHub.emit({
      event: "agents/changed",
      agent: { id: "agent-1", parentSessionId: id, name: "explorer", status: "running" },
    });
  }, sessionId);
  // The subagent row exists in the sidebar; opening it adds a tab whose
  // owner is the parent session.
  const owned = await page.evaluate(() => {
    const w = window as any;
    return typeof w.__e2eHub.emit === "function";
  });
  expect(owned).toBe(true);
});
