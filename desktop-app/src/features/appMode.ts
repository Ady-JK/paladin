// appMode.ts is the shell's single mode: Agent (the conversation, with or
// without the side pane), Browser (pages own the window) or IDE (its own
// workspace). It replaces the old split/tabs layout toggle: there is one mode
// at a time, it is shown on the rail, and it is remembered.
//
// The module owns only the mode itself. What each mode paints belongs to
// sidePane.ts (Agent), browserMode.ts (Browser) and ide.ts (IDE), which
// subscribe through onChange so they never need to know about each other.
import { loadText, saveText } from "../core/storage";

export type AppMode = "agent" | "browser" | "ide";

const MODE_KEY = "paladin.appMode";

function readMode(): AppMode {
  const stored = loadText(MODE_KEY);
  // Anything unknown (a first run, or the old layout key) starts in Agent.
  return stored === "browser" || stored === "ide" ? stored : "agent";
}

let mode: AppMode = readMode();
const listeners = new Set<(mode: AppMode) => void>();

export function appMode(): AppMode {
  return mode;
}

// onChange subscribes to mode changes and returns an unsubscribe function.
export function onChange(listener: (mode: AppMode) => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// setAppMode switches modes. IDE is the one mode that has to be entered
// through its own switch, because enabling it loads that module and owns a
// workspace; asking for it here just announces the intent.
export function setAppMode(next: AppMode): void {
  if (mode === next) return;
  mode = next;
  saveText(MODE_KEY, next);
  if (next === "ide") window.dispatchEvent(new Event("paladin:enter-ide"));
  window.dispatchEvent(new CustomEvent("paladin:app-mode-changed", { detail: mode }));
  for (const listener of listeners) listener(mode);
}

// syncMode records a mode that was entered through another control (the IDE
// button), so the rail highlights the right entry without dispatching the
// enter events again.
export function syncMode(next: AppMode): void {
  if (mode === next) return;
  mode = next;
  saveText(MODE_KEY, next);
  for (const listener of listeners) listener(mode);
}

// toggleAppModeFromKey flips Agent <-> Browser, the same as the rail's mode
// buttons. IDE is left to its own control so the shortcut never drops a user
// into the editor by accident.
export function toggleAppModeFromKey(): void {
  setAppMode(mode === "browser" ? "agent" : "browser");
}
