// tags.spec.ts covers the composer's tagged-item cards: a file, folder
// or image picked with @ becomes a preview card above the input, while
// the agent still receives the full absolute path.
import { expect, test, type Page } from "@playwright/test";

async function acceptMention(page: Page, query: string, label: string): Promise<void> {
  await page.fill("#promptInput", query);
  const popup = page.locator("#mentionPopup");
  await expect(popup).toBeVisible();
  await expect(popup).toContainText(label);
  await page.keyboard.press("Enter");
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

test("a tagged file sends its absolute path and keeps a compact @name", async ({ page }) => {
  await acceptMention(page, "@src/ma", "src/main.go");
  await expect(page.locator("#promptInput")).toHaveValue("@main.go");
  const card = page.locator("#tagCards .tag-card");
  await expect(card).toContainText("main.go");
  await expect(card).toContainText("src/main.go");
  await expect(card.locator(".tag-card-line").first()).toHaveText("package main");

  await page.fill("#promptInput", "@main.go review this");
  await page.click("#sendBtn");
  const submit = await page.evaluate(() => (window as any).__e2eSubmits[0]);
  expect(submit.display).toBe("@main.go review this");
  expect(submit.message).toContain("/home/tester/project/src/main.go");
  await expect(page.locator("#tagCards .tag-card")).toHaveCount(0);
});

test("a message made only of tags still sends", async ({ page }) => {
  await acceptMention(page, "@README", "README.md");
  // Deleting the compact token leaves the card as the only content.
  await page.fill("#promptInput", "");
  await page.click("#sendBtn");
  const submit = await page.evaluate(() => (window as any).__e2eSubmits[0]);
  expect(submit.message).toBe("/home/tester/project/README.md");
  expect(submit.display).toBe("@README.md");
});

test("images show a thumbnail, folders show their kind, and × removes a tag", async ({ page }) => {
  await acceptMention(page, "@assets/sh", "shot.png");
  await expect(page.locator("#tagCards .tag-card img.tag-card-thumb")).toBeVisible();

  await acceptMention(page, "@src/server", "src/server/");
  const cards = page.locator("#tagCards .tag-card");
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(1)).toContainText("folder");

  await cards.nth(1).locator(".tag-card-x").click();
  await expect(page.locator("#tagCards .tag-card")).toHaveCount(1);

  await page.click("#sendBtn");
  const submit = await page.evaluate(() => (window as any).__e2eSubmits[0]);
  expect(submit.message).toContain("/home/tester/project/assets/shot.png");
  expect(submit.message).not.toContain("/home/tester/project/src/server");
  expect(submit.attachmentIds).toEqual(["file-1"]);
  await expect(page.locator("#messages .session-image-thumb")).toHaveCount(1);
});
