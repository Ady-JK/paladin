// window.ts owns the desktop shell chrome: theme, sidebar, window buttons.
// It supports the desktop app frontend.

import { getCurrentWindow } from "@tauri-apps/api/window";
import { $, paintIcons, sidebarEl } from "../core/dom";
import { state } from "../core/state";
import { hasTauri } from "../core/tauri";
import { icon } from "../icons";
import { paintSideVisibility, trackExternalFlight } from "./sites";

// toggleTheme is a function in this module.
// It supports the desktop app frontend.
function toggleTheme(): void {
  state.dark = !state.dark;
  document.documentElement.classList.toggle("dark", state.dark);
  paintIcons();
}

// toggleSidebar is a function in this module.
// It supports the desktop app frontend.
function toggleSidebar(): void {
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

  const appWindow = getCurrentWindow();

  const run = (action: Promise<unknown>): void => {
    action.catch((error) => console.error("window:", error));
  };

  $("#windowMinimizeBtn").addEventListener("click", () => {
    run(appWindow.minimize());
  });

  $("#windowMaximizeBtn").addEventListener("click", () => {
    run(appWindow.toggleMaximize());
  });

  $("#windowCloseBtn").addEventListener("click", () => {
    run(appWindow.close());
  });

  const paintMaximizeIcon = async (): Promise<void> => {
    const holder = document.querySelector<HTMLElement>(
      "#windowMaximizeBtn [data-icon]",
    );
    if (!holder) {
      return;
    }

    const maximized = await appWindow.isMaximized();
    holder.innerHTML = icon(
      maximized ? "window-restore" : "window-maximize",
      "h-3 w-3",
    );
  };

  await paintMaximizeIcon().catch(() => undefined);
  await appWindow
    .onResized(() => {
      void paintMaximizeIcon().catch(() => undefined);
    })
    .catch(() => undefined);

  const titleBar = $("#titleBar");

  titleBar.addEventListener("mousedown", (event) => {
    if ((event.target as HTMLElement).closest("button")) {
      return;
    }
    run(appWindow.startDragging());
  });

  titleBar.addEventListener("dblclick", (event) => {
    if ((event.target as HTMLElement).closest("button")) {
      return;
    }
    run(appWindow.toggleMaximize());
  });
}

// initWindow wires the shell controls and sets up the native window.
// It supports the desktop app frontend.
export function initWindow(): void {
  $("#themeToggleBtn").addEventListener("click", toggleTheme);
  $("#sidebarToggleBtn").addEventListener("click", toggleSidebar);

  void setupWindow();
}
