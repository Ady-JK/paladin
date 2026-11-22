import { expect, test, type Page } from "@playwright/test";
import { showSidebar } from "./drawer";
import { showPane } from "./modes";

// Every app shortcut is Cmd on macOS and Ctrl elsewhere.
const MOD = process.platform === "darwin" ? "Meta" : "Control";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  // The app mode is remembered across reloads, and Playwright shares one
  // browser context per worker, so a mode left behind by an earlier test would
  // leak in here. Every test in this file starts from Agent.
  await page.addInitScript(() => localStorage.removeItem("paladin.appMode"));
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
  await expect(page.locator("#modeAgentBtn")).toHaveAttribute("aria-selected", "true");
});

// press sends a chord from the prompt, the place a user is most likely to
// be typing when they want one — the shortcuts must work from there. Pass
// focus=false when the prompt is behind a modal.
async function press(page: Page, key: string, focus = true): Promise<void> {
  // Focus the prompt only when it is on screen. Browser mode gives the window
  // to the pages and IDE mode gives it to the editor, so there is no composer
  // to click in either — and waiting for one would hang the call.
  if (focus) {
    const prompt = page.locator("#promptInput");
    if (await prompt.isVisible()) await prompt.click({ timeout: 2000 }).catch(() => undefined);
  }
  await page.keyboard.press(`${MOD}+Shift+${key}`);
}

test("the shortcut sheet lists every key group and toggles closed", async ({ page }) => {
  await press(page, "/");
  const modal = page.locator("#shortcutsModal");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("Keyboard shortcuts");
  for (const group of ["WORKSPACE", "PANES", "BROWSER", "SESSION", "SHELL"]) {
    await expect(page.locator("#shortcutsBody .shortcut-group h4", { hasText: group })).toBeVisible();
  }
  // One row per shortcut, each with its chord spelled out. The usage row
  // needs an open session, so a fresh page shows one fewer.
  await expect(page.locator("#shortcutsBody .shortcut-row")).toHaveCount(10);
  await expect(page.locator('[data-shortcut="ide"] kbd')).toHaveText(/Shift\+I$/);
  await expect(page.locator('[data-shortcut="terminal"] kbd')).toHaveText(/Shift\+T$/);
  await expect(page.locator('[data-shortcut="theme"] kbd')).toHaveText(/Shift\+M$/);
  // Pressing the chord again closes it (focus is inside the sheet now).
  await press(page, "/", false);
  await expect(modal).toBeHidden();
});

// A chord the desktop grabs, or that a built-in feature claims, never
// reaches the app, so the table must contain neither. Ctrl+Alt+L locked the
// machine instead of switching theme, and Ctrl+Shift+K opens the model
// picker whatever Shift says.
test("no shortcut uses a chord the desktop or a built-in feature claims", async ({ page }) => {
  // Startup logs a rejected table instead of dying, so read that verdict
  // first: a caught error still means the table is broken.
  const startup = await page.evaluate(
    () => (window as unknown as { __shortcutTableError?: string }).__shortcutTableError ?? "",
  );
  expect(startup).toBe("");
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
    // And the guard must actually reject a bad chord, or it protects
    // nothing. "u" is ibus Unicode input, "k" is the model picker.
    for (const key of ["u", "k"]) {
      try {
        mod.checkShortcutTable([
          { id: "probe", keys: `Ctrl+Shift+${key}`, key, what: "", group: "Shell", run: () => {} },
        ]);
        found.push(`guard let Ctrl+Shift+${key} through`);
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

test("the theme shortcut swaps light and dark", async ({ page }) => {
  const html = page.locator("html");
  const wasDark = await html.evaluate((el) => el.classList.contains("dark"));
  await press(page, "m");
  await expect(html).toHaveClass(wasDark ? /(^|\s)light(\s|$)/ : /(^|\s)dark(\s|$)/);
  await press(page, "m");
  await expect(html).toHaveClass(wasDark ? /(^|\s)dark(\s|$)/ : /(^|\s)light(\s|$)/);
});

test("the IDE shortcut turns IDE mode on and off", async ({ page }) => {
  const ide = page.locator("#ideWorkspace");
  const button = page.locator("#ideModeBtn");
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await press(page, "i");
  await expect(button).toHaveAttribute("aria-pressed", "true");
  // The IDE module is loaded on demand, so the workspace appears once its
  // chunk has run rather than on the same frame as the button's state.
  await expect(ide).toBeVisible({ timeout: 15_000 });
  await press(page, "i");
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await expect(ide).toBeHidden();
});

test("the layout shortcut flips Agent and Browser mode", async ({ page }) => {
  const mode = (name: string): ReturnType<typeof page.locator> => page.locator(name);
  await expect(mode("#modeAgentBtn")).toHaveAttribute("aria-selected", "true");
  await press(page, "l");
  await expect(mode("#modeBrowserBtn")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#browserView")).toBeVisible();
  await press(page, "l");
  await expect(mode("#modeAgentBtn")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#agentView")).toBeVisible();
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
  await press(page, "j");
  const menu = page.locator("#webMenu");
  await expect(menu).toBeVisible();
  // The address action is focused, so the keyboard can type an address.
  await expect(page.locator("#webMenu [data-web-address]")).toBeFocused();
  await expect(menu).toContainText("Open an address");
});

test("the agent shortcut focuses the prompt and hides the pane", async ({ page }) => {
  await showPane(page);
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
  await page.locator("#paneNewChatBtn").click();
  await page.getByRole("menuitem", { name: "New side chat" }).click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  // Park focus inside the side chat, so moving to the prompt is proof the
  // shortcut ran and not just where focus already was — and so the chord
  // has to survive a composer that stops its own keydown events.
  await page.locator("#sidePane .side-chat .side-input").focus();
  await expect(page.locator("#sidePane .side-chat .side-input")).toBeFocused();
  await press(page, "h", false);
  await expect(page.locator("#promptInput")).toBeFocused();
  // The shortcut means "give me the conversation": the pane closes, and the
  // side chat it held survives as a tab for when it opens again.
  await expect(page.locator("#sidePane")).toBeHidden();
  await showPane(page);
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
});

test("a shortcut typed into the prompt does not leak text or send a turn", async ({ page }) => {
  const input = page.locator("#promptInput");
  await input.fill("keep me");
  const closedBefore = await page.locator("#sidebar.is-closed").count();
  await page.keyboard.press(`${MOD}+Shift+B`);
  // The chord is claimed, so no character lands in the draft and nothing
  // was submitted: the draft is intact and no turn row appeared.
  await expect(input).toHaveValue("keep me");
  await expect(page.locator("#messages .turn-row")).toHaveCount(0);
  // And the chord really ran: the sidebar flipped.
  await expect(page.locator("#sidebar.is-closed")).toHaveCount(closedBefore === 1 ? 0 : 1);
});
