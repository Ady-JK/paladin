import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#sessionList")).toBeVisible();
});

test("chat tab swaps the sidebar and the main view", async ({ page }) => {
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#externalView")).toBeHidden();

  await page.click("#chatTab");

  // Chat has no sidebar: the pages are browser-style tabs on top.
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#searchWrap")).toBeHidden();
  await expect(page.locator("#externalView #chatList")).toBeVisible();
  await expect(page.locator('[data-pick="site:Gemini"]')).toBeVisible();
  await expect(page.locator('[data-pick="site:ChatGPT"]')).toBeVisible();
  await expect(page.locator('[data-pick="site:Claude"]')).toBeVisible();
  await expect(page.locator('[data-pick="site:DeepSeek"]')).toBeVisible();

  await expect(page.locator("#agentView")).toBeHidden();
  await expect(page.locator("#externalView")).toBeVisible();

  await page.click('[data-pick="site:Gemini"]');
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");

  await page.click('[data-pick="site:ChatGPT"]');
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");

  await expect(page.locator("#pageReloadBtn")).toBeVisible();
  await expect(page.locator('[data-pick="site:ChatGPT"]')).toHaveAttribute("aria-selected", "true");
  await expect(page.locator('[data-pick="site:Gemini"]')).toHaveAttribute("aria-selected", "false");
  await expect(page.locator('#pageOpenBtn[data-open="site:ChatGPT"]')).toBeVisible();

  await page.click("#agentTab");
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#externalView")).toBeHidden();
  // Coming back to Agent opens the drawer.
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#pageReloadBtn")).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("Agent Chat");
});

test("every default page can be removed and stays removed", async ({ page }) => {
  await page.click("#chatTab");
  for (const id of ["site:Gemini", "site:ChatGPT", "site:Claude", "site:DeepSeek"]) {
    await expect(page.locator(`[data-pick="${id}"]`)).toBeVisible();
    await expect(page.locator(`[data-del="${id}"]`)).toBeVisible();
  }

  await page.locator('[data-del="site:Claude"]').click();
  await expect(page.locator('[data-pick="site:Claude"]')).toBeHidden();
  await page.reload();
  await page.click("#chatTab");
  await expect(page.locator('[data-pick="site:Claude"]')).toBeHidden();
  await expect(page.locator('[data-pick="site:DeepSeek"]')).toBeVisible();
});

test("an existing profile gains the defaults without losing its pages", async ({ page }) => {
  // Simulate a profile from before the defaults were seeded: one custom
  // page, no seeded flag, and an old built-in page id selected.
  await page.addInitScript(() => {
    localStorage.setItem("paladin.sites", JSON.stringify([{ name: "Mine", url: "https://example.com/" }]));
    localStorage.removeItem("paladin.sites.seeded");
    localStorage.setItem("paladin.view", JSON.stringify({ view: "chat", page: "gemini" }));
  });
  await page.reload();

  await expect(page.locator('[data-pick="site:Mine"]')).toBeVisible();
  for (const id of ["site:Gemini", "site:ChatGPT", "site:Claude", "site:DeepSeek"]) {
    await expect(page.locator(`[data-pick="${id}"]`)).toBeVisible();
  }
  // The legacy "gemini" selection maps to the seeded entry instead of
  // leaving the chat side with no page.
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("deleting every page leaves an empty chat list", async ({ page }) => {
  await page.click("#chatTab");
  for (const id of ["site:Gemini", "site:ChatGPT", "site:Claude", "site:DeepSeek"]) {
    await page.locator(`[data-del="${id}"]`).click();
  }
  await expect(page.locator("#chatEntries")).toContainText("no pages yet");
  await expect(page.locator("#headerTitle")).toHaveText("Chat");

  await page.reload();
  await page.click("#chatTab");
  await expect(page.locator("#chatEntries")).toContainText("no pages yet");
});

test("window buttons fill the top-right corner", async ({ page }) => {
  await expect(page.locator("#windowMinimizeBtn")).toBeVisible();
  await expect(page.locator("#windowCloseBtn")).toBeVisible();
  const header = await page.locator("#titleBar").boundingBox();
  const minimize = await page.locator("#windowMinimizeBtn").boundingBox();
  const close = await page.locator("#windowCloseBtn").boundingBox();
  expect(header).toBeTruthy();
  expect(minimize).toBeTruthy();
  expect(close).toBeTruthy();
  expect(Math.abs(header!.y - minimize!.y)).toBeLessThanOrEqual(1);
  expect(Math.abs(header!.x + header!.width - (close!.x + close!.width))).toBeLessThanOrEqual(1);
  expect(Math.abs(header!.y + header!.height - (close!.y + close!.height))).toBeLessThanOrEqual(2);
  expect(Math.abs(minimize!.height - close!.height)).toBeLessThanOrEqual(1);
});

// Verifies the picked chat side survives a reload.
// It checks the external view and title persist.
test("the picked side survives a reload", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="site:Gemini"]');
  await page.reload();
  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("chat mode has no sidebar, only the page tabs", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="site:Gemini"]');

  await expect(page.locator("#sidebar")).toBeHidden();
  // Clicking Chat again never brings a sidebar.
  await page.click("#chatTab");
  await expect(page.locator("#sidebar")).toBeHidden();
  await expect(page.locator("#chatList [data-pick]")).toHaveCount(4);
  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
  // The tab strip spans the whole card: the page is never narrowed.
  const strip = await page.locator("#chatList").boundingBox();
  const host = await page.locator("#externalHost").boundingBox();
  expect(Math.abs(strip!.width - host!.width)).toBeLessThanOrEqual(1);
  expect(strip!.y + strip!.height).toBeLessThanOrEqual(host!.y + 1);
});

test("collapsing in agent keeps the sessions and hides the chats", async ({ page }) => {
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeHidden();
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#searchWrap")).toBeVisible();
  await expect(page.locator("#chatList")).toBeHidden();
  await expect(page.locator("#agentView")).toBeVisible();
});

test("collapse, expand, then switch sides stays clean", async ({ page }) => {
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeHidden();
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();

  await page.click("#chatTab");
  await page.click('[data-pick="site:ChatGPT"]');
  await expect(page.locator("#sidebar")).toBeHidden();

  await page.click("#agentTab");
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#chatList")).toBeHidden();

  await page.click("#chatTab");
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");
});

test("rapid tab switching ends in one clean side", async ({ page }) => {
  await page.click("#chatTab");
  await page.click("#agentTab");
  await page.click("#chatTab");
  await page.click('[data-pick="site:ChatGPT"]');
  await page.click("#agentTab");
  await page.click("#chatTab");

  await expect(page.locator("#externalView")).toBeVisible();
  await expect(page.locator("#agentView")).toBeHidden();
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#searchWrap")).toBeHidden();
  await expect(page.locator("#chatList")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("ChatGPT");
});

test("a visible chat page freezes the shell compositing", async ({ page }) => {
  await page.click("#chatTab");
  await page.click('[data-pick="site:ChatGPT"]');
  await expect(page.locator("#externalView")).toBeVisible();

  await expect.poll(() => page.evaluate(() => document.body.classList.contains("guest-visible"))).toBe(true);

  const styles = await page.evaluate(() => {
    const pulse = getComputedStyle(document.getElementById("headerPulse")!);
    const sidebar = getComputedStyle(document.getElementById("sidebar")!);
    return { animation: pulse.animationName, blur: sidebar.backdropFilter };
  });
  // The infinite pulse and the blur layers would force the compositor to
  // produce frames for the shell while the guest page is scrolling.
  expect(styles.animation).toBe("none");
  expect(styles.blur === "none" || styles.blur === "").toBe(true);

  await page.click("#agentTab");
  await expect.poll(() => page.evaluate(() => document.body.classList.contains("guest-visible"))).toBe(false);
});

test("chat mode never moves the page", async ({ page }) => {
  const hostBox = () => page.locator("#externalHost").boundingBox();
  await page.click("#chatTab");
  await page.click('[data-pick="site:Gemini"]');
  const first = await hostBox();

  await page.click('[data-pick="site:Claude"]');
  expect(await hostBox()).toEqual(first);

  // Opening the drawer in agent, then coming back, leaves it unchanged.
  await page.click("#agentTab");
  await page.click("#agentTab");
  await page.click("#chatTab");
  expect(await hostBox()).toEqual(first);
});

test("a site can be added, kept, and removed", async ({ page }) => {
  await page.click("#chatTab");

  await page.click("#chatAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await page.fill("#siteName", "Docs");
  await page.fill("#siteUrl", "example.com");
  await page.click("#siteAddBtn");

  const entry = page.locator('[data-pick="site:Docs"]');
  await expect(entry).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Docs");
  await expect(entry).toHaveAttribute("title", /example\.com/);

  await page.reload();
  await expect(page.locator('[data-pick="site:Docs"]')).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Docs");

  await page.locator('[data-del="site:Docs"]').click();
  await expect(page.locator('[data-pick="site:Docs"]')).toBeHidden();
  await expect(page.locator("#headerTitle")).toHaveText("Gemini");
});

test("a deleted default can be added back by hand", async ({ page }) => {
  await page.click("#chatTab");
  await page.locator('[data-del="site:Claude"]').click();
  await expect(page.locator('[data-pick="site:Claude"]')).toBeHidden();

  await page.click("#chatAddBtn");
  await page.fill("#siteName", "Claude");
  await page.fill("#siteUrl", "https://claude.ai");
  await page.click("#siteAddBtn");

  await expect(page.locator('[data-pick="site:Claude"]')).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Claude");
  await page.reload();
  await expect(page.locator('[data-pick="site:Claude"]')).toBeVisible();
});

test("a site can be added after deleting every default", async ({ page }) => {
  await page.click("#chatTab");
  for (const id of ["site:Gemini", "site:ChatGPT", "site:Claude", "site:DeepSeek"]) {
    await page.locator(`[data-del="${id}"]`).click();
  }
  await expect(page.locator("#chatEntries")).toContainText("no pages yet");

  await page.click("#chatAddBtn");
  await page.fill("#siteName", "Docs");
  await page.fill("#siteUrl", "https://example.com");
  await page.click("#siteAddBtn");

  await expect(page.locator('[data-pick="site:Docs"]')).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Docs");
  await page.reload();
  await expect(page.locator('[data-pick="site:Docs"]')).toBeVisible();
});

test("bad site URLs are rejected", async ({ page }) => {
  await page.click("#chatTab");
  await page.click("#chatAddBtn");

  await page.fill("#siteName", "Evil");
  await page.fill("#siteUrl", "javascript:alert(1)");
  await page.click("#siteAddBtn");

  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-pick="site:Evil"]')).toBeHidden();

  await page.fill("#siteName", "Gemini");
  await page.fill("#siteUrl", "https://gemini.google.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator("#chatEntries [data-pick]")).toHaveCount(4);

  await page.fill("#siteName", "Insecure");
  await page.fill("#siteUrl", "http://example.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-pick="site:Insecure"]')).toBeHidden();

  await page.keyboard.press("Escape");
  await expect(page.locator("#siteModal")).toBeHidden();
});

test("+ in the tab strip adds a site; the sidebar button starts a session", async ({ page }) => {
  await expect(page.locator("#newSessionBtnLabel")).toHaveText("New Session");

  await page.click("#chatTab");
  await page.click("#chatAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await page.keyboard.press("Escape");

  await page.click("#agentTab");
  await expect(page.locator("#newSessionBtnLabel")).toHaveText("New Session");

  await page.click("#newSessionBtn");
  await expect(page.locator("#sessionModal")).toBeVisible();
});
