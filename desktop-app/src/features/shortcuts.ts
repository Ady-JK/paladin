// shortcuts.ts owns every app-wide keyboard shortcut: one table, one
// handler, one cheat-sheet. Chords are Shift + Cmd (macOS) or Ctrl
// (elsewhere) on one key. Ctrl+Alt was the first choice and a bad one:
// desktops claim several of those globally (Cinnamon locks the screen on
// Ctrl+Alt+L and opens a terminal on Ctrl+Alt+T), so the app never saw
// them.
//
// A chord only works if it survives three layers, any of which can take
// the key first:
//   1. the desktop — a global keybinding grab;
//   2. Electron's default menu — Ctrl+Shift+I opens DevTools, +R reloads;
//   3. the focused DOM node — a composer that stops propagation.
// The first two are named below so the startup check can refuse a
// colliding entry; the third is why a composer that swallows its own keys
// lets a chord through when it carries both modifiers.

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

// Three layers can take a key before the shortcut handler sees it, so each
// gets a list, and checkShortcutTable refuses a chord that lands in one.

// RESERVED_BY_DESKTOP: a window manager grabs these globally. Checked
// against a Cinnamon/GNOME session — Ctrl+Shift+U is ibus Unicode input
// (ibus runs on most Linux desktops) and Ctrl+Shift+Print is the area
// screenshot. A system GNOME Terminal's own Ctrl+Shift+T/N/Q only apply
// inside that terminal window, so those letters stay available here.
const RESERVED_BY_DESKTOP = new Set(["u", "print"]);

// RESERVED_BY_EDITOR: this app's own features claim these with the platform
// modifier, Shift or not, so the keydown never reaches the shortcut table:
//   k — the model picker (models.ts ignores Shift and calls preventDefault);
//   s — save in the editor and in the MCP/hook editors;
//   p, f, g — the editor's palette / workspace search / go-to-line;
//   e, w, n — the editor's explorer, close-tab and new-file.
// Keeping them out of the table means a chord cannot mean two things
// depending on which pane happens to have focus.
const RESERVED_BY_EDITOR = new Set(["k", "s", "p", "f", "g", "e", "w", "n"]);

const click = (selector: string): void => document.querySelector<HTMLElement>(selector)?.click();

const SHORTCUTS: Shortcut[] = [
  {
    id: "sidebar",
    keys: `${MOD}+Shift+B`,
    key: "b",
    what: "Show or hide the sessions sidebar",
    group: "Workspace",
    run: toggleSidebarFromKey,
  },
  {
    id: "theme",
    keys: `${MOD}+Shift+M`,
    key: "m",
    what: "Switch between light and dark",
    group: "Workspace",
    run: toggleThemeFromKey,
  },
  {
    id: "ide",
    keys: `${MOD}+Shift+I`,
    key: "i",
    what: "Turn IDE mode on or off",
    group: "Panes",
    run: () => click("#ideModeBtn"),
  },
  {
    id: "layout",
    keys: `${MOD}+Shift+L`,
    key: "l",
    what: "Switch between split and tabs layout",
    group: "Panes",
    run: toggleLayoutFromKey,
  },
  {
    id: "terminal",
    keys: `${MOD}+Shift+T`,
    key: "t",
    what: "Open a terminal",
    group: "Panes",
    run: openTerminalDockFromKey,
  },
  {
    id: "agent",
    keys: `${MOD}+Shift+H`,
    key: "h",
    what: "Back to the agent conversation",
    group: "Panes",
    run: focusAgentFromKey,
  },
  {
    id: "browser",
    keys: `${MOD}+Shift+J`,
    key: "j",
    what: "Open the browser menu",
    group: "Browser",
    run: openWebMenuFromKey,
  },
  {
    id: "changes",
    keys: `${MOD}+Shift+V`,
    key: "v",
    what: "View the files changed in this session",
    group: "Session",
    run: () => click("#sessionChangesBtn"),
  },
  {
    id: "usage",
    keys: `${MOD}+Shift+Y`,
    key: "y",
    what: "Open session analytics",
    group: "Session",
    available: () => !!state.sessionId,
    run: () => void openUsageDashboard(),
  },
  {
    id: "new-chat",
    keys: `${MOD}+Shift+O`,
    key: "o",
    what: "Start a new chat",
    group: "Session",
    run: () => click("#paneNewChatBtn"),
  },
  {
    id: "cheat-sheet",
    keys: `${MOD}+Shift+/`,
    key: "/",
    what: "Show this shortcut list",
    group: "Shell",
    run: openShortcutSheet,
  },
];

// checkShortcutTable refuses a table that would lose its chord to the
// desktop or to Electron's menu, or that repeats a key. It runs at
// startup, and the shortcuts spec imports it over the served module URL to
// check both directions (the real table passes, a known-bad chord is
// refused), which is why it is exported.
// @public
export function checkShortcutTable(table: Shortcut[] = SHORTCUTS): void {
  const seen = new Set<string>();
  for (const item of table) {
    if (RESERVED_BY_DESKTOP.has(item.key)) {
      throw new Error(`shortcut "${item.id}" uses ${item.keys}, which the desktop claims globally; pick another key`);
    }
    if (RESERVED_BY_EDITOR.has(item.key)) {
      throw new Error(
        `shortcut "${item.id}" uses ${item.keys}, which a built-in feature already claims (its handler runs first); pick another key`,
      );
    }
    if (seen.has(item.key)) {
      throw new Error(`shortcut "${item.id}" repeats the key "${item.key}"`);
    }
    seen.add(item.key);
  }
}

// matchShortcut finds the entry for one keydown, or null. It insists on
// the platform modifier plus Shift and refuses Alt, so a chord matches its
// label exactly and cannot collide with an Alt-based menu mnemonic.
function matchShortcut(event: KeyboardEvent): Shortcut | null {
  const mod = isMac ? event.metaKey : event.ctrlKey;
  const stray = isMac ? event.ctrlKey : event.metaKey;
  if (!mod || !event.shiftKey || event.altKey || stray) return null;
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
// prompt has focus: the platform modifier plus Shift is not how anyone
// types, and every control that owns a chord stops the event itself when
// it cares.
export function initShortcuts(): void {
  // A table that collides with another layer is a bug, but it must not
  // take every other shortcut down with it: report it, and keep the ones
  // that are fine working.
  try {
    checkShortcutTable();
  } catch (error) {
    // Stash the verdict for the spec to read: a caught error still means
    // the table is broken and must be fixed, not quietly tolerated.
    (window as unknown as { __shortcutTableError?: string }).__shortcutTableError = String(error);
    console.error(`shortcuts: ${String(error)}`);
  }
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
    ["#paneLayoutBtn", "L"],
    ["#themeToggleBtn", "M"],
    ["#agentTab", "B"],
  ] as const) {
    const el = document.querySelector<HTMLElement>(selector);
    if (!el) continue;
    el.title = `${el.title || el.getAttribute("aria-label") || selector} (${MOD}+Shift+${chord})`;
  }
}
