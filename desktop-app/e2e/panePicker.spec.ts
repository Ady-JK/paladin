// panePicker.spec.ts pins how the side pane is filled. The header + opens the
// pane; an empty pane offers three large choices (Side chat / Terminal /
// Browser) instead of an empty box, and once anything is open that same list
// becomes the pane's strip with a + at the end.
//
// Terminals only ever live in the pane: there is no full-width terminal tab.
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

test("an empty pane offers the three things it can hold", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await expect(page.locator("#sidePane")).toBeHidden();

  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  // The empty state is the picker, not a blank column.
  const picker = page.locator("#panePicker");
  await expect(picker).toBeVisible();
  await expect(picker.getByRole("button", { name: /Side chat/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Terminal/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Browser/ })).toBeVisible();
});

test("picking Terminal from the picker puts a terminal in the pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();

  // In the pane, beside the conversation — never a full-width tab.
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eTerminalStarts?.length ?? 0)).toBe(1);
});

test("picking Browser from the picker opens a page in the pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Browser/ })
    .click();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("once something is open the picker gives way to the strip", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();

  // It stays in the document and hides, so the pane can bring it straight back.
  await expect(page.locator("#panePicker")).toBeHidden();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  // The strip's own + is how a second one is added.
  await expect(page.locator("#paneTabsAdd")).toBeVisible();
});

test("closing the last tab brings the picker back", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();
  await page.locator("#paneTabs .pane-tab .pane-tab-close").first().click();
  // The pane stays open, and with nothing left it offers the three choices.
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#panePicker")).toBeVisible();
});

test("a terminal is never given the whole card", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page
    .locator("#panePicker")
    .getByRole("button", { name: /Terminal/ })
    .click();
  const pane = (await page.locator("#sidePane").boundingBox())!;
  await expect(page.locator("#agentMain")).toBeVisible();
  const chat = (await page.locator("#agentMain").boundingBox())!;
  // The conversation keeps its share: the terminal is a column, not the view.
  expect(chat.width).toBeGreaterThan(pane.width);
  expect(pane.x).toBeGreaterThan(chat.x);
});
