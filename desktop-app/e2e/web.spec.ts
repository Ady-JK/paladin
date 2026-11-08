// web.spec.ts covers the integrated browser: saved sites in the Web menu,
// web tabs in the agent side pane (split or full width), the shared
// login profile, and view lifetime.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

async function startSession(page: Page): Promise<void> {
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
}

async function openMenu(page: Page): Promise<void> {
  await page.click("#paneWebBtn");
  await expect(page.locator("#webMenu")).toBeVisible();
}

async function openSite(page: Page, url: string): Promise<void> {
  await openMenu(page);
  await page.locator(`[data-web-open="${url}"]`).click();
  await expect(page.locator("#webMenu")).toBeHidden();
}

function viewShown(page: Page): Promise<unknown> {
  return page.evaluate(() => (window as any).__e2eViewShown);
}

test("the separate chat window is gone and the agent keeps one header", async ({ page }) => {
  await expect(page.locator("#chatTab")).toHaveCount(0);
  await expect(page.locator("#externalView")).toHaveCount(0);
  await expect(page.locator("#pageReloadBtn")).toHaveCount(0);
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#headerTitle")).toHaveText("Agent Chat");
  await expect(page.locator("#paneWebBtn")).toBeVisible();
  await expect(page.locator("#paneBrowserBtn")).toHaveCount(0);
});

test("window buttons fill the top-right corner", async ({ page }) => {
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

test("the Web menu lists the saved sites and opens one beside the agent", async ({ page }) => {
  await startSession(page);
  await openMenu(page);
  for (const name of ["Gemini", "ChatGPT", "Claude", "DeepSeek"]) {
    await expect(page.locator(`[data-web-title="${name}"]`)).toBeVisible();
  }
  await expect(page.locator("#webMenu")).toContainText("Personal browser");
  await expect(page.locator("#webMenu")).toContainText("gemini.google.com");

  await page.locator('[data-web-open="https://gemini.google.com/"]').click();
  await expect(page.locator("#webMenu")).toBeHidden();
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toContainText("Gemini");
  await expect(page.locator("#sidePane .web-url")).toHaveValue("https://gemini.google.com/");

  // The native page is placed over the tab surface with the shared,
  // persistent login profile.
  const ensures = await page.evaluate(() => (window as any).__e2eViewEnsures || []);
  expect(ensures[0]).toMatchObject({ name: "web:t1", url: "https://gemini.google.com/" });
  expect(ensures[0].partition).toBe("persist:paladin-sites");
  expect(await viewShown(page)).toBe("web:t1");
  const bounds = await page.evaluate(() => (window as any).__e2eViewBounds);
  expect(bounds.width).toBeGreaterThan(100);
  expect(bounds.height).toBeGreaterThan(100);
  // The agent conversation and the website share the window.
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#messages")).toContainText("start");
  await expect(page.locator("#headerTitle")).toHaveText("Agent Chat");
});

test("any https address can be opened, and unsafe addresses are refused", async ({ page }) => {
  await startSession(page);
  await openMenu(page);
  await page.locator("[data-web-address]").click();
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
  const input = page.locator("#sidePane .web-url");
  await expect(input).toBeFocused();

  await input.fill("example.com");
  await input.press("Enter");
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toContainText("example.com");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eViewEnsures.at(-1).url))
    .toBe("https://example.com/");

  for (const bad of ["http://example.com", "javascript:alert(1)", "   "]) {
    await input.fill(bad);
    await input.press("Enter");
    await expect(page.locator("#sidePane .web-status")).toContainText("Only secure https addresses");
  }
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toHaveCount(1);
});

test("open web tabs stay alive when you switch away; closing one drops it", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  await openSite(page, "https://chatgpt.com/");
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toHaveCount(2);
  expect(await page.evaluate(() => (window as any).__e2eViewDrops || [])).toEqual([]);
  expect(await viewShown(page)).toBe("web:t2");

  await page.locator('#paneTabs .pane-tab[data-web="web:t1"] .pane-tab-pick').click();
  await expect.poll(() => viewShown(page)).toBe("web:t1");
  // Switching back did not rebuild or drop the other tab.
  expect(await page.evaluate(() => (window as any).__e2eViewDrops || [])).toEqual([]);

  await page.locator('#paneTabs .pane-tab[data-web="web:t1"] .pane-tab-close').click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewDrops || [])).toEqual(["web:t1"]);
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toHaveCount(1);
});

test("a web tab is side-by-side in split and full width in tabs", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  const split = await page.evaluate(() => (window as any).__e2eViewBounds.width);

  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewBounds.width)).toBeGreaterThan(split * 1.5);

  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect.poll(() => viewShown(page)).toBe("agent");
  await page.locator("#paneTabs .pane-tab[data-web] .pane-tab-pick").click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect.poll(() => viewShown(page)).toBe("web:t1");
});

test("a modal hides the native page until it closes", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  await expect.poll(() => viewShown(page)).toBe("web:t1");

  await page.click("#settingsBtn");
  await expect(page.locator("#settingsModal")).toBeVisible();
  await expect.poll(() => viewShown(page)).toBe("agent");

  await page.click("#settingsCloseBtn");
  await expect(page.locator("#settingsModal")).toBeHidden();
  await expect.poll(() => viewShown(page)).toBe("web:t1");
});

test("a visible web page freezes the shell compositing", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://chatgpt.com/");
  await expect.poll(() => page.evaluate(() => document.body.classList.contains("guest-visible"))).toBe(true);
  const styles = await page.evaluate(() => {
    const pulse = getComputedStyle(document.getElementById("headerPulse")!);
    const sidebar = getComputedStyle(document.getElementById("sidebar")!);
    return { animation: pulse.animationName, blur: sidebar.backdropFilter };
  });
  expect(styles.animation).toBe("none");
  expect(styles.blur === "none" || styles.blur === "").toBe(true);

  await page.click("#paneCloseBtn");
  await expect.poll(() => page.evaluate(() => document.body.classList.contains("guest-visible"))).toBe(false);
});

test("a site can be added, kept, and removed", async ({ page }) => {
  await startSession(page);
  await openMenu(page);
  await page.locator("[data-web-new]").click();
  await expect(page.locator("#siteModal")).toBeVisible();
  await page.fill("#siteName", "Docs");
  await page.fill("#siteUrl", "example.com");
  await page.click("#siteAddBtn");

  await expect(page.locator("#siteModal")).toBeHidden();
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toContainText("Docs");

  await page.reload();
  await startSession(page);
  await openMenu(page);
  await expect(page.locator('[data-web-title="Docs"]')).toBeVisible();

  await page.locator('[data-web-del="https://example.com/"]').click();
  await expect(page.locator('[data-web-title="Docs"]')).toHaveCount(0);
});

test("every default site can be removed and stays removed", async ({ page }) => {
  await startSession(page);
  for (const url of [
    "https://gemini.google.com/",
    "https://chatgpt.com/",
    "https://claude.ai/",
    "https://chat.deepseek.com/",
  ]) {
    await openMenu(page);
    await page.locator(`[data-web-del="${url}"]`).click();
  }
  await openMenu(page);
  await expect(page.locator("#webMenu .web-menu-list")).toContainText("No saved sites");
  await expect(page.locator("#webMenu [data-web-open]")).toHaveCount(0);

  await page.reload();
  await startSession(page);
  await openMenu(page);
  await expect(page.locator("#webMenu [data-web-open]")).toHaveCount(0);
});

test("an existing profile gains the defaults without losing its sites", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("paladin.sites", JSON.stringify([{ name: "Mine", url: "https://example.com/" }]));
    localStorage.removeItem("paladin.sites.seeded");
  });
  await page.reload();
  await startSession(page);
  await openMenu(page);
  await expect(page.locator('[data-web-title="Mine"]')).toBeVisible();
  for (const name of ["Gemini", "ChatGPT", "Claude", "DeepSeek"]) {
    await expect(page.locator(`[data-web-title="${name}"]`)).toBeVisible();
  }
});

test("bad site URLs are rejected", async ({ page }) => {
  await startSession(page);
  await openMenu(page);
  await page.locator("[data-web-new]").click();

  await page.fill("#siteName", "Evil");
  await page.fill("#siteUrl", "javascript:alert(1)");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-web-title="Evil"]')).toHaveCount(0);

  await page.fill("#siteName", "Gemini");
  await page.fill("#siteUrl", "https://gemini.google.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();

  await page.fill("#siteName", "Insecure");
  await page.fill("#siteUrl", "http://example.com");
  await page.click("#siteAddBtn");
  await expect(page.locator("#siteModal")).toBeVisible();
  await expect(page.locator('[data-web-title="Insecure"]')).toHaveCount(0);

  await page.keyboard.press("Escape");
  await expect(page.locator("#siteModal")).toBeHidden();
});

test("the sidebar drawer still toggles with a web tab open", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", "false");
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeVisible();
  await expect(page.locator("#agentView")).toBeVisible();
  await page.click("#agentTab");
  await expect(page.locator("#sessionList")).toBeHidden();
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
});

test("typing a new address in an open tab navigates it", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  const input = page.locator("#sidePane .web-url");
  await input.fill("example.com");
  await input.press("Enter");
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toContainText("example.com");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eViewEnsures.at(-1).url))
    .toBe("https://example.com/");
  expect(await viewShown(page)).toBe("web:t1");
});

test("page titles and links update the tab, and history buttons drive the page", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  await page.evaluate(() => {
    (window as any).__e2eViewStateCb({
      name: "web:t1",
      url: "https://gemini.google.com/app/abc",
      title: "Google Gemini",
      canGoBack: true,
      canGoForward: false,
    });
  });
  const tab = page.locator("#paneTabs .pane-tab[data-web]");
  await expect(tab).toContainText("Google Gemini");
  await expect(page.locator("#sidePane .web-url")).toHaveValue("https://gemini.google.com/app/abc");
  const back = page.locator("#sidePane .web-back");
  const forward = page.locator("#sidePane .web-forward");
  await expect(back).toBeEnabled();
  await expect(forward).toBeDisabled();

  await back.click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewBacks || [])).toEqual(["web:t1"]);

  await page.evaluate(() => {
    (window as any).__e2eViewStateCb({
      name: "web:t1",
      url: "https://gemini.google.com/app/abc",
      title: "Google Gemini",
      canGoBack: false,
      canGoForward: true,
    });
  });
  await expect(back).toBeDisabled();
  await expect(forward).toBeEnabled();
  await forward.click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eViewForwards || [])).toEqual(["web:t1"]);
});

test("a tab reopened after a link click keeps the new URL", async ({ page }) => {
  await startSession(page);
  await openSite(page, "https://gemini.google.com/");
  await openSite(page, "https://chatgpt.com/");
  // The hidden Gemini tab navigated to a new route.
  await page.evaluate(() => {
    (window as any).__e2eViewStateCb({
      name: "web:t1",
      url: "https://gemini.google.com/app/xyz",
      title: "Gemini chat",
      canGoBack: true,
      canGoForward: false,
    });
  });
  await page.locator('#paneTabs .pane-tab[data-web="web:t1"] .pane-tab-pick').click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eViewEnsures.at(-1).url))
    .toBe("https://gemini.google.com/app/xyz");
});

test("an https link in the conversation opens in the integrated browser", async ({ page }) => {
  await startSession(page);
  await page.locator("#messages").evaluate((el) => {
    el.insertAdjacentHTML(
      "beforeend",
      '<a class="md-link" href="#" data-ext="https://example.com/docs">https://example.com/docs</a>',
    );
  });
  await page.locator('#messages a[data-ext="https://example.com/docs"]').click();
  await expect(page.locator("#sidePane .pane-web")).toBeVisible();
  await expect(page.locator("#sidePane .web-url")).toHaveValue("https://example.com/docs");
  await expect(page.locator("#paneTabs .pane-tab[data-web]")).toContainText("example.com");
});

test("the tabs layout names the conversation tab Agent", async ({ page }) => {
  await startSession(page);
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator('[data-key="main"]')).toContainText("Agent");
});

test("the personal browser opens without starting a chat", async ({ page }) => {
  await openMenu(page);
  await page.locator("[data-web-address]").click();
  const input = page.locator("#sidePane .web-url");
  await expect(input).toBeVisible();
  await input.fill("example.com");
  await input.press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eViewEnsures?.at(-1)?.partition))
    .toBe("persist:paladin-sites");
  await page.locator("#paneTabs .pane-tab[data-web] .pane-tab-close").click();
  await expect(page.locator("#sidePane .pane-web")).toHaveCount(0);
});
