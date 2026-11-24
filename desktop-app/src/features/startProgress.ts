// startProgress.ts tracks engine starts that are still loading. Codex and
// Claude Code launch their CLI to read models and sign-in, which takes a
// moment during which nothing else on screen moves. This keeps a floating
// status pill honest about it, and tells the sidebar which folder is busy.
// It is a leaf module (no feature imports) so anything can use it.

import { shortPath } from "../core/format";

// SLOW_MS is when a start gets an extra "still starting" note.
const SLOW_MS = 8000;

export const STARTING_CHANGED = "paladin:starting-changed";

type Start = { label: string; cwd: string; slow: boolean };

let nextId = 0;
const active = new Map<number, Start>();

function notify(): void {
  paintPill();
  window.dispatchEvent(new Event(STARTING_CHANGED));
}

function paintPill(): void {
  const pill = document.getElementById("startPill");
  if (!pill) return;
  // The newest start is the one the user is waiting on.
  const latest = [...active.values()].pop();
  pill.hidden = !latest;
  if (!latest) return;
  pill.querySelector("#startPillText")!.textContent = latest.slow
    ? `Still starting ${latest.label}… this can take a moment`
    : `Starting ${latest.label}…`;
  pill.querySelector("#startPillWhere")!.textContent = shortPath(latest.cwd);
}

// beginStart marks an engine as starting in cwd and returns the function that
// ends it. The pill itself fades in after a beat (CSS), so a quick start never
// flashes it.
export function beginStart(label: string, cwd: string): () => void {
  const id = ++nextId;
  const start: Start = { label, cwd, slow: false };
  active.set(id, start);
  const timer = window.setTimeout(() => {
    start.slow = true;
    notify();
  }, SLOW_MS);
  notify();
  return () => {
    window.clearTimeout(timer);
    if (active.delete(id)) notify();
  };
}

// startingIn is the label of the engine starting in cwd, or "".
export function startingIn(cwd: string): string {
  for (const start of active.values()) if (start.cwd === cwd) return start.label;
  return "";
}
