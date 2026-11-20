// window.ts owns the desktop shell chrome: theme, sidebar, window buttons.

import { $, agentTabEl, paintIcons, sidebarEl } from "../core/dom";
import * as api from "../core/api";
import { state } from "../core/state";
import { bridge, hasBridge } from "../core/bridge";
import { icon } from "../icons";

export type ColorMode = "system" | "light" | "dark";
const colorKey = "paladin.colorMode";
const colorModes: ColorMode[] = ["system", "light", "dark"];
const systemDark = window.matchMedia("(prefers-color-scheme: dark)");
let colorMode: ColorMode = colorModes.includes(localStorage.getItem(colorKey) as ColorMode)
  ? (localStorage.getItem(colorKey) as ColorMode)
  : "dark";

export function getColorMode(): ColorMode {
  return colorMode;
}
export function setColorMode(mode: ColorMode): void {
  colorMode = mode;
  localStorage.setItem(colorKey, mode);
  state.dark = mode === "system" ? systemDark.matches : mode === "dark";
  applyShellTheme();
  paintIcons();
}

// applyShellTheme updates the renderer and the native guest surfaces. The
// guest pages paint their own content, but until they commit the view
// background is what the user sees, so it must follow the shell.
function applyShellTheme(): void {
  document.documentElement.classList.toggle("dark", state.dark);
  document.documentElement.classList.toggle("light", !state.dark);
  if (hasBridge()) {
    api.setExternalTheme(state.dark).catch(() => undefined);
  }
}

function toggleTheme(): void {
  setColorMode(state.dark ? "light" : "dark");
}

// toggleThemeFromKey flips the shell theme from a keyboard shortcut.
export function toggleThemeFromKey(): void {
  toggleTheme();
}

// toggleSidebarFromKey is the Sessions-button toggle without the
// pointer, so a shortcut can hide or show the sessions drawer.
export function toggleSidebarFromKey(): void {
  setDrawer(state.collapsed);
}

// paintSideVisibility shows the sessions column. It is a real column in
// the shell's flex row, so opening it narrows the main card instead of
// covering it; the card animates its width with the panel rather than
// reflowing the page under a floating overlay.
function paintSideVisibility(): void {
  const open = !state.collapsed;
  sidebarEl.classList.toggle("is-closed", !open);
  sidebarEl.inert = !open;
  agentTabEl.setAttribute("aria-expanded", String(open));
  agentTabEl.setAttribute("aria-selected", "true");
  agentTabEl.setAttribute("aria-label", "Sessions");
  agentTabEl.title = open ? "Hide sessions" : "Show sessions";
}

// The sidebar is a column now, so nothing dismisses it behind the user's
// back. A panel that vanishes because you clicked the conversation is a
// surprise when it also resized it, and it makes selecting text or
// dragging a file out of it impossible. The rail button, the close button
// and Escape are the only ways out.
function setDrawer(open: boolean): void {
  if (state.collapsed === !open) {
    return;
  }
  state.collapsed = !open;
  if (open) window.dispatchEvent(new Event("paladin:ide-hide-explorer"));
  paintSideVisibility();
}

function drawerOpen(): boolean {
  return !state.collapsed;
}

function wireDrawer(): void {
  // Escape closes the drawer only when nothing layered on top of it is
  // open, and then stops there so it doesn't also arm "stop the turn".
  window.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || !drawerOpen()) return;
      const layered = document.querySelector(
        // Anything with its own Escape handling wins: the drawer must not
        // swallow the key while a dialog, menu, rename field or the
        // revert popover is open. Named selectors, not [role=dialog]:
        // the sidebar itself contains dialogs, and matching one would
        // make Escape stop working.
        ".modal-overlay:not(.hidden), #sessionRowMenu, .perm-menu:not(.hidden), #thinkingMenu:not(.hidden), #modelMenu:not(.hidden), input.session-rename, #revertPop, #sessionImageViewer:not(.hidden), .image-viewer:not(.hidden)",
      );
      if (layered) return;
      event.stopImmediatePropagation();
      setDrawer(false);
    },
    true,
  );

  sidebarEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("[data-row-menu], [data-fold], input.session-rename")) return;
    // Picking a session keeps the column open: it is where the list of
    // sessions lives, and closing it after every pick makes browsing
    // several conversations a reopen-every-time job.
    if (target.closest("#newSessionBtn")) {
      window.dispatchEvent(new Event("paladin:ide-show-agent"));
    }
  });
  agentTabEl.addEventListener("click", toggleSidebarFromKey);
  // The two panels share one column slot, so opening one closes the other.
  window.addEventListener("paladin:ide-show-explorer", () => setDrawer(false));
  document.querySelector<HTMLElement>("#ideModeBtn")?.addEventListener("click", (event) => {
    if (!(event.currentTarget as HTMLElement).dataset.ideReplay) setDrawer(false);
  });
  window.addEventListener("paladin:ide-mode-changed", paintSideVisibility);
}

async function setupWindow(): Promise<void> {
  if (!hasBridge()) {
    return;
  }

  const windows = bridge().windows;

  const run = (action: Promise<unknown>): void => {
    action.catch((error) => console.error("window:", error));
  };

  $("#windowMinimizeBtn").addEventListener("click", () => {
    run(windows.minimize());
  });

  $("#windowMaximizeBtn").addEventListener("click", () => {
    run(windows.toggleMaximize());
  });

  $("#windowCloseBtn").addEventListener("click", () => {
    run(windows.close());
  });

  const paintMaximizeIcon = async (): Promise<void> => {
    const holder = document.querySelector<HTMLElement>("#windowMaximizeBtn [data-icon]");
    if (!holder) {
      return;
    }

    const maximized = await windows.isMaximized();
    holder.innerHTML = icon(maximized ? "window-restore" : "window-maximize", "h-3 w-3");
  };

  await paintMaximizeIcon().catch(() => undefined);
  windows.onResized(() => {
    void paintMaximizeIcon().catch(() => undefined);
  });

  const titleBar = $("#titleBar");

  // The header is a drag region now (frameless window). Double-clicking
  // any non-button part still toggles maximize where the compositor
  // forwards the event; the maximize button covers the rest.
  titleBar.addEventListener("dblclick", (event) => {
    if ((event.target as HTMLElement).closest("button")) {
      return;
    }
    run(windows.toggleMaximize());
  });
}

// initWindow wires the shell controls and sets up the native window.
export function initWindow(): void {
  setColorMode(colorMode);
  systemDark.addEventListener("change", () => {
    if (colorMode === "system") setColorMode("system");
  });
  $("#themeToggleBtn").addEventListener("click", toggleTheme);
  wireDrawer();
  paintSideVisibility();

  void setupWindow();
}
