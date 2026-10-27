// window.ts owns the desktop shell chrome: theme, sidebar, window buttons.
// It supports the desktop app frontend.

import { $, paintIcons, sidebarEl } from "../core/dom";
import * as api from "../core/api";
import { state } from "../core/state";
import { bridge, hasTauri } from "../core/tauri";
import { icon } from "../icons";
import { paintSideVisibility, trackExternalFlight } from "./sites";

// applyShellTheme updates the renderer and the native guest surfaces. The
// guest pages paint their own content, but until they commit the view
// background is what the user sees, so it must follow the shell.
function applyShellTheme(): void {
  document.documentElement.classList.toggle("dark", state.dark);
  if (hasTauri()) {
    api.setExternalTheme(state.dark).catch(() => undefined);
  }
}

// toggleTheme is a function in this module.
// It supports the desktop app frontend.
function toggleTheme(): void {
  state.dark = !state.dark;
  applyShellTheme();
  paintIcons();
}

// toggleSidebar is a function in this module.
// It supports the desktop app frontend.
function toggleSidebar(): void {
  // Locked in chat mode: the embedded page is fixed, so the sidebar
  // must not move. The button is disabled there as well.
  if (state.sideView === "chat") {
    return;
  }
  state.collapsed = !state.collapsed;

  if (state.collapsed) {
    sidebarEl.classList.remove("w-72");
    sidebarEl.classList.add("w-16");
  } else {
    sidebarEl.classList.remove("w-16");
    sidebarEl.classList.add("w-72");
  }

  paintSideVisibility();

  trackExternalFlight();
}

async function setupWindow(): Promise<void> {
  if (!hasTauri()) {
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
    const holder = document.querySelector<HTMLElement>(
      "#windowMaximizeBtn [data-icon]",
    );
    if (!holder) {
      return;
    }

    const maximized = await windows.isMaximized();
    holder.innerHTML = icon(
      maximized ? "window-restore" : "window-maximize",
      "h-3 w-3",
    );
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
// It supports the desktop app frontend.
export function initWindow(): void {
  applyShellTheme();
  $("#themeToggleBtn").addEventListener("click", toggleTheme);
  $("#sidebarToggleBtn").addEventListener("click", toggleSidebar);

  void setupWindow();
}
