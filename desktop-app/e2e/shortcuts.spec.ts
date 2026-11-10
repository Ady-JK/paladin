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
// focus=false when the prompt is behind a modal, and shift=true for the
// one chord that needs it.
async function press(page: Page, key: string, focus = true, shift = false): Promise<void> {
  if (focus) await page.locator("#promptInput").click();
  await page.keyboard.press(`${MOD}+${shift ? "Shift+" : ""}Alt+${key}`);
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
  await expect(page.locator("#shortcutsBody .shortcut-row")).toHaveCount(9);
  await expect(page.locator('[data-shortcut="ide"] kbd')).toHaveText(/Alt\+I$/);
  // The terminal chord carries Shift, which is what keeps it clear of the
  // desktop's own Ctrl+Alt+T.
  await expect(page.locator('[data-shortcut="terminal"] kbd')).toHaveText(/Shift\+Alt\+T$/);
  // Pressing the chord again closes it (focus is inside the sheet now).
  await press(page, "/", false);
  await expect(modal).toBeHidden();
});

// A chord the desktop grabs never reaches the app, so the table must not
// contain one. Ctrl+Alt+L is the screen locker on Cinnamon and most GTK
// desktops; using it locked the user's machine instead of switching theme.
test("no shortcut uses a chord the desktop claims globally", async ({ page }) => {
  const bad = await page.evaluate(async () => {
    // Vite serves the module source by URL at runtime; it is not a
    // compile-time import target for the spec's tsconfig.
    const url = "/src/features/shortcuts.ts";
    const mod = (await import(/* @vite-ignore */ url)) as {
      checkShortcutTable(table?: unknown[]): void;
    };
    const found: string[] = [];
    try {
      // The real table must pass.
      mod.checkShortcutTable();
    } catch (error) {
      found.push(`table: ${String(error)}`);
    }
    // And the guard must actually reject a grabbed chord, or it protects
    // nothing.
    for (const item of [
      { id: "theme", keys: "Ctrl+Alt+L", key: "l", what: "", group: "Workspace", run: () => {} },
      { id: "term", keys: "Ctrl+Alt+T", key: "t", what: "", group: "Panes", run: () => {} },
    ]) {
      try {
        mod.checkShortcutTable([item]);
        found.push(`guard let ${item.keys} through`);
      } catch {
        // expected: the guard refused it
      }
    }
    return found;
  });
  expect(bad).toEqual([]);
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
  await press(page, "t", true, true);
  await expect(page.locator("#ideTerminalDock .pane-terminal")).toBeVisible();
});

// The plain (no Shift) form of the terminal chord belongs to the desktop,
// so the app must ignore it: on Cinnamon Ctrl+Alt+T launches a system
// terminal. Nothing in the app may react.
test("a desktop-owned chord without Shift does nothing in the app", async ({ page }) => {
  const before = await page.evaluate(() => document.querySelectorAll("#ideTerminalDock .pane-terminal").length);
  await press(page, "t");
  await page.waitForTimeout(300);
  const after = await page.evaluate(() => document.querySelectorAll("#ideTerminalDock .pane-terminal").length);
  expect(after).toBe(before);
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
  const closedBefore = await page.locator("#sidebar.is-closed").count();
  await page.keyboard.press(`${MOD}+Alt+B`);
  // The chord is claimed, so no character lands in the draft and nothing
  // was submitted: the draft is intact and no turn row appeared.
  await expect(input).toHaveValue("keep me");
  await expect(page.locator("#messages .turn-row")).toHaveCount(0);
  // And the chord really ran: the sidebar flipped.
  await expect(page.locator("#sidebar.is-closed")).toHaveCount(closedBefore === 1 ? 0 : 1);
});
