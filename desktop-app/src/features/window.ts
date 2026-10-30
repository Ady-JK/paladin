// window.ts owns the desktop shell chrome: theme, sidebar, window buttons.

import { $, paintIcons, sidebarEl } from "../core/dom";
import * as api from "../core/api";
import { state } from "../core/state";
import { bridge, hasBridge } from "../core/bridge";
import { icon } from "../icons";
import { paintSideVisibility } from "./sites";

// applyShellTheme updates the renderer and the native guest surfaces. The
// guest pages paint their own content, but until they commit the view
// background is what the user sees, so it must follow the shell.
function applyShellTheme(): void {
  document.documentElement.classList.toggle("dark", state.dark);
  if (hasBridge()) {
    api.setExternalTheme(state.dark).catch(() => undefined);
  }
}

function toggleTheme(): void {
  state.dark = !state.dark;
  applyShellTheme();
  paintIcons();
}

// The sidebar is a floating drawer over the agent view: the main card
// keeps its full width whether it is open or not, so nothing reflows.
// Chat mode has no sidebar at all (its pages are tabs), see sites.ts.
// pickTimer closes the drawer a beat after a session pick, so a
// double-click (rename) on the same row can still land.
let pickTimer = 0;

function setDrawer(open: boolean): void {
  window.clearTimeout(pickTimer);
  if (state.sideView !== "agent" || state.collapsed === !open) {
    return;
  }
  state.collapsed = !open;
  paintSideVisibility();
}

function drawerOpen(): boolean {
  return state.sideView === "agent" && !state.collapsed;
}

// Clicks on these never dismiss the drawer: the rail (the Agent button
// toggles it itself), and the
// row menu and modals the drawer opens.
const KEEP_OPEN = "#sidebar, #rail, #sessionRowMenu, .modal-overlay";

function wireDrawer(): void {
  document.addEventListener(
    "pointerdown",
    (event) => {
      if (!drawerOpen()) return;
      const target = event.target as Element | null;
      if (target?.closest?.(KEEP_OPEN)) return;
      setDrawer(false);
    },
    true,
  );

  // Escape closes the drawer only when nothing layered on top of it is
  // open, and then stops there so it doesn't also arm "stop the turn".
  window.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || !drawerOpen()) return;
      const layered = document.querySelector(
        ".modal-overlay:not(.hidden), #sessionRowMenu, .perm-menu:not(.hidden), #thinkingMenu:not(.hidden), #modelMenu:not(.hidden), input.session-rename",
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
    if (target.closest("#newSessionBtn")) {
      setDrawer(false);
    } else if (target.closest("[data-session], [data-recent]")) {
      window.clearTimeout(pickTimer);
      pickTimer = window.setTimeout(() => setDrawer(false), 250);
    }
  });
  sidebarEl.addEventListener("dblclick", () => window.clearTimeout(pickTimer));
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
  applyShellTheme();
  $("#themeToggleBtn").addEventListener("click", toggleTheme);
  wireDrawer();

  void setupWindow();
}
