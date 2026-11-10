// shortcuts.ts owns every app-wide keyboard shortcut: one table, one
// handler, one cheat-sheet. Chords are Cmd (macOS) or Ctrl elsewhere plus
// Alt, deliberately: the unmodified and Shift variants are already taken
// by the editor (Cmd+P, Cmd+Shift+F), the model picker (Cmd+K) and the
// browser guests, so an app-level shortcut never shadows one of those.
//
// Each entry drives the same control a click would, so a shortcut and the
// matching button cannot drift apart: IDE mode and split/tabs layout route
// through their toolbar buttons.

import { $ } from "../core/dom";
import { state } from "../core/state";
import { closeModal, isModalOpen, openModal } from "../ui/anim";
import { focusAgentFromKey, openTerminalDockFromKey, toggleLayoutFromKey } from "./sidePane";
import { openUsageDashboard } from "./usage";
import { openWebMenuFromKey } from "./web";
import { toggleSidebarFromKey, toggleThemeFromKey } from "./window";

interface Shortcut {
  // id keys the row in the cheat-sheet.
  id: string;
  // keys is the chord as shown to the user.
  keys: string;
  // key is the bare key the matcher compares, lowercased.
  key: string;
  what: string;
  group: "Workspace" | "Panes" | "Browser" | "Session" | "Shell";
  // available hides a row whose target needs more state to exist.
  available?(): boolean;
  run(): void;
}

const isMac = /Mac|iPhone|iPad/.test(navigator.userAgent);
// MOD is Cmd on macOS and Ctrl elsewhere, matching the rest of the app.
const MOD = isMac ? "⌘" : "Ctrl";

const click = (selector: string): void => document.querySelector<HTMLElement>(selector)?.click();

const SHORTCUTS: Shortcut[] = [
  {
    id: "sidebar",
    keys: `${MOD}+Alt+B`,
    key: "b",
    what: "Show or hide the sessions sidebar",
    group: "Workspace",
    run: toggleSidebarFromKey,
  },
  {
    id: "theme",
    keys: `${MOD}+Alt+L`,
    key: "l",
    what: "Switch between light and dark",
    group: "Workspace",
    run: toggleThemeFromKey,
  },
  {
    id: "ide",
    keys: `${MOD}+Alt+I`,
    key: "i",
    what: "Turn IDE mode on or off",
    group: "Panes",
    run: () => click("#ideModeBtn"),
  },
  {
    id: "layout",
    keys: `${MOD}+Alt+Y`,
    key: "y",
    what: "Switch between split and tabs layout",
    group: "Panes",
    run: toggleLayoutFromKey,
  },
  {
    id: "terminal",
    keys: `${MOD}+Alt+T`,
    key: "t",
    what: "Open a terminal",
    group: "Panes",
    run: openTerminalDockFromKey,
  },
  {
    id: "agent",
    keys: `${MOD}+Alt+A`,
    key: "a",
    what: "Back to the agent conversation",
    group: "Panes",
    run: focusAgentFromKey,
  },
  {
    id: "browser",
    keys: `${MOD}+Alt+W`,
    key: "w",
    what: "Open the browser menu",
    group: "Browser",
    run: openWebMenuFromKey,
  },
  {
    id: "changes",
    keys: `${MOD}+Alt+G`,
    key: "g",
    what: "View the files changed in this session",
    group: "Session",
    run: () => click("#sessionChangesBtn"),
  },
  {
    id: "usage",
    keys: `${MOD}+Alt+U`,
    key: "u",
    what: "Open session analytics",
    group: "Session",
    available: () => !!state.sessionId,
    run: () => void openUsageDashboard(),
  },
  {
    id: "new-chat",
    keys: `${MOD}+Alt+N`,
    key: "n",
    what: "Start a new chat",
    group: "Session",
    run: () => click("#paneNewChatBtn"),
  },
  {
    id: "cheat-sheet",
    keys: `${MOD}+Alt+/`,
    key: "/",
    what: "Show this shortcut list",
    group: "Shell",
    run: openShortcutSheet,
  },
];

// matchShortcut finds the entry for one keydown, or null. It insists on
// the platform modifier and Alt and refuses Shift, so the chord cannot
// collide with a Shift variant an editor or a guest page owns.
function matchShortcut(event: KeyboardEvent): Shortcut | null {
  const mod = isMac ? event.metaKey : event.ctrlKey;
  const stray = isMac ? event.ctrlKey : event.metaKey;
  if (!mod || !event.altKey || event.shiftKey || stray) return null;
  const pressed = event.key.toLowerCase();
  return SHORTCUTS.find((item) => item.key === pressed && item.available?.() !== false) ?? null;
}

// openShortcutSheet shows the cheat-sheet, grouped the way the keys are
// used. Escape and a second press close it.
function openShortcutSheet(): void {
  const modal = $("#shortcutsModal");
  if (isModalOpen(modal)) {
    closeShortcutSheet();
    return;
  }
  const groups = new Map<Shortcut["group"], Shortcut[]>();
  for (const item of SHORTCUTS) {
    if (item.available && !item.available()) continue;
    const list = groups.get(item.group) ?? [];
    list.push(item);
    groups.set(item.group, list);
  }
  $("#shortcutsBody").innerHTML = [...groups]
    .map(
      ([group, items]) =>
        `<section class="shortcut-group"><h4>${group}</h4>${items
          .map(
            (item) =>
              `<div class="shortcut-row" data-shortcut="${item.id}"><span class="shortcut-what">${item.what}</span><kbd>${item.keys}</kbd></div>`,
          )
          .join("")}</section>`,
    )
    .join("");
  openModal(modal);
  $("#shortcutsCloseBtn").focus();
}

function closeShortcutSheet(): void {
  closeModal($("#shortcutsModal"));
}

// initShortcuts installs the single keydown listener and the cheat-sheet
// chrome. The listener runs on the document, so it even fires while the
// prompt has focus: Cmd/Ctrl+Alt is not how anyone types, and every
// control that owns a chord stops the event itself when it cares.
export function initShortcuts(): void {
  $("#shortcutsCloseBtn").addEventListener("click", closeShortcutSheet);
  $("#shortcutsModal").addEventListener("click", (event) => {
    if (event.target === $("#shortcutsModal")) closeShortcutSheet();
  });
  $("#shortcutsBtn").addEventListener("click", openShortcutSheet);
  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented) return;
    const match = matchShortcut(event);
    if (!match) return;
    event.preventDefault();
    event.stopPropagation();
    match.run();
  });
  // Tooltips name the chord, so the pointer and the keyboard agree.
  for (const [selector, chord] of [
    ["#ideModeBtn", "I"],
    ["#paneLayoutBtn", "Y"],
    ["#themeToggleBtn", "L"],
    ["#agentTab", "B"],
  ] as const) {
    const el = document.querySelector<HTMLElement>(selector);
    if (!el) continue;
    el.title = `${el.title || el.getAttribute("aria-label") || selector} (${MOD}+Alt+${chord})`;
  }
}
