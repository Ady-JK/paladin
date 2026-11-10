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
import { toggleSidebarFromKey } from "./window";

interface Shortcut {
  // id keys the row in the cheat-sheet.
  id: string;
  // keys is the chord as shown to the user.
  keys: string;
  // key is the bare key the matcher compares, lowercased.
  key: string;
  // shift marks an entry that requires Shift. Every other entry must be
  // pressed without it.
  shift?: boolean;
  what: string;
  group: "Workspace" | "Panes" | "Browser" | "Session" | "Shell";
  // available hides a row whose target needs more state to exist.
  available?(): boolean;
  run(): void;
}

const isMac = /Mac|iPhone|iPad/.test(navigator.userAgent);
// MOD is Cmd on macOS and Ctrl elsewhere, matching the rest of the app.
const MOD = isMac ? "⌘" : "Ctrl";

// DESKTOP_GRABS lists bare keys a desktop environment claims globally: a
// window manager handles them before the app is handed the key, so the
// shortcut would silently do nothing — or something worse. On Cinnamon and
// most GTK desktops Ctrl+Alt+L locks the screen and Ctrl+Alt+T opens the
// system terminal. No entry may use one of these without Shift, which is
// what moves a chord out of the desktop's reach (and the startup check
// below fails the moment one slips back in).
const DESKTOP_GRABS = new Set(["l", "t", "delete", "end", "escape", "print"]);

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
    keys: `${MOD}+Shift+Alt+T`,
    key: "t",
    what: "Open a terminal",
    group: "Panes",
    shift: true,
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

// checkShortcutTable refuses a table that would fight a desktop for a key.
// It runs at startup, and the shortcuts spec imports it over the served
// module URL to check both directions (the real table passes, a grabbed
// chord is refused), which is why it is exported.
// @public
export function checkShortcutTable(table: Shortcut[] = SHORTCUTS): void {
  for (const item of table) {
    if (DESKTOP_GRABS.has(item.key) && !item.shift) {
      throw new Error(
        `shortcut "${item.id}" uses ${item.keys}, which the desktop claims globally; give it Shift (${MOD}+Shift+Alt+...) or pick another key`,
      );
    }
    if (item.shift && !item.keys.includes("Shift")) {
      throw new Error(`shortcut "${item.id}" requires Shift but its label does not say so`);
    }
  }
}

// matchShortcut finds the entry for one keydown, or null. It insists on
// the platform modifier and Alt, refuses the other modifier, and matches
// Shift exactly: an entry either needs Shift or must have none, so a
// chord the desktop already owns can be avoided by adding Shift.
function matchShortcut(event: KeyboardEvent): Shortcut | null {
  const mod = isMac ? event.metaKey : event.ctrlKey;
  const stray = isMac ? event.ctrlKey : event.metaKey;
  if (!mod || !event.altKey || stray) return null;
  if (event.shiftKey && !SHORTCUTS.some((item) => item.shift)) return null;
  const pressed = event.key.toLowerCase();
  return (
    SHORTCUTS.find((item) => item.key === pressed && !!item.shift === event.shiftKey && item.available?.() !== false) ??
    null
  );
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
  checkShortcutTable();
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
  // Tooltips name the chord, so the pointer and the keyboard agree. The
  // theme button is absent on purpose: its old chord became the desktop's
  // screen-lock key, so the theme has no shortcut.
  for (const [selector, chord] of [
    ["#ideModeBtn", "I"],
    ["#paneLayoutBtn", "Y"],
    ["#agentTab", "B"],
  ] as const) {
    const el = document.querySelector<HTMLElement>(selector);
    if (!el) continue;
    el.title = `${el.title || el.getAttribute("aria-label") || selector} (${MOD}+Alt+${chord})`;
  }
}
