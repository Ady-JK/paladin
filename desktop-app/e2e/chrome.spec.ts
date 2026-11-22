// chrome.spec.ts pins the three things about the window frame that Browser mode
// broke: the window controls must exist in every mode, there must always be a
// visible way back to Agent, and exactly one control may look "current".
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => localStorage.removeItem("paladin.appMode"));
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

const enterBrowser = async (page: Page): Promise<void> => {
  await page.locator("#modeBrowserBtn").click();
  await expect(page.locator("#browserView")).toBeVisible();
  await expect(page.locator("#titleBar")).toBeHidden();
};

test("the window can always be minimised, maximised and closed", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  // Agent mode.
  for (const id of ["#windowMinimizeBtn", "#windowMaximizeBtn", "#windowCloseBtn"]) {
    await expect(page.locator(id)).toBeVisible();
  }
  // Browser mode hides the agent header; the window controls are not in it.
  await enterBrowser(page);
  for (const id of ["#windowMinimizeBtn", "#windowMaximizeBtn", "#windowCloseBtn"]) {
    await expect(page.locator(id)).toBeVisible();
  }
  // Still in the window's corner, not floating in the middle of the bar.
  const card = (await page.locator("#mainCard").boundingBox())!;
  const close = (await page.locator("#windowCloseBtn").boundingBox())!;
  expect(Math.abs(card.y - close.y)).toBeLessThanOrEqual(1);
  expect(card.x + card.width - (close.x + close.width)).toBeLessThanOrEqual(1);
  // IDE mode too.
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#windowCloseBtn")).toBeVisible();
});

test("Browser mode always shows how to get back to Agent", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await enterBrowser(page);

  // Two labelled ways back, so it never depends on hunting an icon:
  // the rail's Agent button and the browser bar's own.
  await expect(page.locator("#railModes #modeAgentBtn")).toBeVisible();
  await expect(page.locator("#railModes #modeAgentBtn")).toContainText("Agent");
  await expect(page.locator("#browserToAgentBtn")).toBeVisible();
  await expect(page.locator("#browserToAgentBtn")).toContainText("Agent");

  // And the headline control works: one click is back to the conversation.
  await page.locator("#browserToAgentBtn").click();
  await expect(page.locator("#agentView")).toBeVisible();
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator("#browserView")).toBeHidden();
  await expect(page.locator("#railModes #modeAgentBtn")).toHaveAttribute("aria-selected", "true");
});

test("the bar's Agent control is a way back, not a toggle", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await enterBrowser(page);
  await page.locator("#browserToAgentBtn").click();
  await expect(page.locator("#agentView")).toBeVisible();
  // It does not exist in Agent mode, so it can never bounce you back out.
  await expect(page.locator("#browserToAgentBtn")).toBeHidden();
});

test("only the current mode claims to be selected on the rail", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);

  const selected = async (): Promise<string[]> =>
    page.evaluate(() =>
      Array.from(document.querySelectorAll('#rail [aria-selected="true"]')).map((el) => el.id || el.className),
    );

  // The Sessions button is an expand toggle, not a current-view tab: it used to
  // carry aria-selected in every mode, which read as a permanently stuck
  // highlight. It must not claim selection at all.
  await expect(page.locator('#rail [aria-selected="true"]')).toHaveCount(1);
  await expect(page.locator("#agentTab")).not.toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#agentTab")).toHaveAttribute("aria-expanded", /true|false/);

  expect(await selected()).toEqual(["modeAgentBtn"]);
  await enterBrowser(page);
  expect(await selected()).toEqual(["modeBrowserBtn"]);
  await page.locator("#modeAgentBtn").click();
  expect(await selected()).toEqual(["modeAgentBtn"]);
});

test("the mode pair is labelled so the highlighted one means something", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  // Both carry visible text, so "Agent" / "Browser" reads as a choice.
  await expect(page.locator("#modeAgentBtn .rail-mode-label")).toHaveText("Agent");
  await expect(page.locator("#modeBrowserBtn .rail-mode-label")).toHaveText("Browser");
  // The lit one is the current mode, and it is filled rather than merely tinted.
  const lit = await page.locator("#modeAgentBtn").evaluate((el) => getComputedStyle(el).backgroundColor);
  const dim = await page.locator("#modeBrowserBtn").evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(lit).not.toBe(dim);
});
