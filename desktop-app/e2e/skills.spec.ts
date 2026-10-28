// skills.spec.ts covers the /skill picker: popup listing, attach chip,
// and the bare-/skill usage hint.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("the /skill picker lists skills and attaches one to the turn", async ({
  page,
}) => {
  await page.fill("#promptInput", "/skill ");
  await expect(page.locator("#skillPopup")).toBeVisible();
  await expect(page.locator("#skillPopup")).toContainText("reviewer");
  await expect(page.locator("#skillPopup")).toContainText("deployer");

  await page.locator("#skillPopup").getByRole("button", { name: /reviewer/ }).click();
  await expect(page.locator("#promptInput")).toHaveValue("/skill reviewer ");
  await expect(page.locator("#skillChipName")).toHaveText("/reviewer");

  // Skills route to the agent with the name sent separately: the
  // chat call carries skill=reviewer while the text keeps the line.
  await page.fill("#promptInput", "/skill reviewer ship it");
  await page.click("#sendBtn");
  const chats = await page.evaluate(() => (window as any).__e2eChats || []);
  const last = chats[chats.length - 1];
  expect(last.args?.skill ?? last.skill).toBe("reviewer");

  // The user turn shows a skill chip plus the message after `/skill`,
  // never the SKILL.md content.
  const rows = page.locator("#messages .turn-row");
  await expect(rows.first()).toContainText("/reviewer");
  await expect(rows.first()).toContainText("ship it");
});

test("a bare /skill hints usage instead of reaching the agent", async ({ page }) => {
  await page.fill("#promptInput", "/skill");
  await page.click("#sendBtn");
  const chats = await page.evaluate(() => (window as any).__e2eChats || []);
  expect(chats).toHaveLength(0);
  await expect(page.locator("#toastText")).toContainText("Usage: /skill");
});
