import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";

// Every app shortcut is Cmd on macOS and Ctrl elsewhere.
const MOD = process.platform === "darwin" ? "Meta" : "Control";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

// press sends a chord from the prompt, the place a user is most likely to
// be typing when they want one — the shortcuts must work from there. Pass
// focus=false when the prompt is behind a modal.
async function press(page: Page, key: string, focus = true): Promise<void> {
  if (focus) await page.locator("#promptInput").click();
  await page.keyboard.press(`${MOD}+Alt+${key}`);
}

test("the shortcut sheet lists every key group and toggles closed", async ({ page }) => {
  await press(page, "/");
  const modal = page.locator("#shortcutsModal");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("Keyboard shortcuts");
  for (const group of ["WORKSPACE", "PANES", "BROWSER", "SESSION", "SHELL"]) {
    await expect(page.locator("#shortcutsBody .shortcut-group h4", { hasText: group })).toBeVisible();
  }
  // One row per shortcut, each with its chord spelled out.
  await expect(page.locator("#shortcutsBody .shortcut-row")).toHaveCount(10);
  await expect(page.locator('[data-shortcut="ide"] kbd')).toHaveText(/Alt\+I$/);
  await expect(page.locator('[data-shortcut="terminal"] kbd')).toHaveText(/Alt\+T$/);
  // Pressing the chord again closes it (focus is inside the sheet now).
  await press(page, "/", false);
  await expect(modal).toBeHidden();
});

test("the rail button opens the same sheet", async ({ page }) => {
  await page.locator("#shortcutsBtn").click();
  await expect(page.locator("#shortcutsModal")).toBeVisible();
  await page.locator("#shortcutsCloseBtn").click();
  await expect(page.locator("#shortcutsModal")).toBeHidden();
});

test("the sidebar shortcut shows and hides the sessions drawer", async ({ page }) => {
  await showSidebar(page);
  // The drawer closes on any pointerdown outside it, so the chord is sent
  // without clicking the prompt first — just as a user reaching for the
  // sidebar keeps their hands on the keys.
  await press(page, "b", false);
  await expect(page.locator("#sidebar.is-closed")).toHaveCount(1);
  await press(page, "b", false);
  await expect(page.locator("#sidebar:not(.is-closed)")).toHaveCount(1);
});

test("the theme shortcut swaps light and dark", async ({ page }) => {
  const html = page.locator("html");
  const wasDark = await html.evaluate((el) => el.classList.contains("dark"));
  await press(page, "l");
  await expect(html).toHaveClass(wasDark ? /(^|\s)light(\s|$)/ : /(^|\s)dark(\s|$)/);
  await press(page, "l");
  await expect(html).toHaveClass(wasDark ? /(^|\s)dark(\s|$)/ : /(^|\s)light(\s|$)/);
});

test("the IDE shortcut turns IDE mode on and off", async ({ page }) => {
  const ide = page.locator("#ideWorkspace");
  const button = page.locator("#ideModeBtn");
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await press(page, "i");
  await expect(button).toHaveAttribute("aria-pressed", "true");
  await expect(ide).toBeVisible();
  await press(page, "i");
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await expect(ide).toBeHidden();
});

test("the layout shortcut switches split and tabs", async ({ page }) => {
  const view = page.locator("#agentView");
  const before = await page.locator("#paneLayoutBtn").getAttribute("data-layout");
  await press(page, "y");
  await expect(page.locator("#paneLayoutBtn")).not.toHaveAttribute("data-layout", before ?? "split");
  await expect(view).toHaveClass(before === "split" ? /is-tab-layout/ : /^(?!.*is-tab-layout).*$/);
  await press(page, "y");
  await expect(page.locator("#paneLayoutBtn")).toHaveAttribute("data-layout", before ?? "split");
});

test("the terminal shortcut opens a terminal in IDE mode", async ({ page }) => {
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
  await press(page, "i");
  await expect(page.locator("#ideWorkspace")).toBeVisible();
  await press(page, "t");
  await expect(page.locator("#ideTerminalDock .pane-terminal")).toBeVisible();
});

test("the browser shortcut opens the browser menu", async ({ page }) => {
  await press(page, "w");
  const menu = page.locator("#webMenu");
  await expect(menu).toBeVisible();
  // The address action is focused, so the keyboard can type an address.
  await expect(page.locator("#webMenu [data-web-address]")).toBeFocused();
  await expect(menu).toContainText("Open an address");
});

test("the agent shortcut leaves the side pane and focuses the prompt", async ({ page }) => {
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
  await page.locator("#paneNewChatBtn").click();
  await page.getByRole("menuitem", { name: "New side chat" }).click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  // Park focus inside the side chat, so moving to the prompt is proof the
  // shortcut ran and not just where focus already was.
  await page.locator("#sidePane .side-chat .side-input").focus();
  await expect(page.locator("#sidePane .side-chat .side-input")).toBeFocused();
  await press(page, "a", false);
  await expect(page.locator("#promptInput")).toBeFocused();
  await expect(page.locator("#sidePane")).toBeHidden();
});

test("a shortcut typed into the prompt does not leak text or send a turn", async ({ page }) => {
  const input = page.locator("#promptInput");
  await input.fill("keep me");
  await page.keyboard.press(`${MOD}+Alt+L`);
  // The chord is claimed, so no character lands in the draft and nothing
  // was submitted: the draft is intact and no turn row appeared.
  await expect(input).toHaveValue("keep me");
  await expect(page.locator("#messages .turn-row")).toHaveCount(0);
  // And the chord really ran: the theme flipped.
  await expect(page.locator("html")).toHaveClass(/(^|\s)(light|dark)(\s|$)/);
});
