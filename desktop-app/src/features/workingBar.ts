// workingBar.ts owns the running-turn status: its short activity verbs,
// its elapsed-time format and the DOM that live.ts paints. The status is
// not a bar of its own — it docks on the right of the Context strip's
// head row, so one line above the composer carries both the context
// that loads automatically and what the turn is doing right now.
//
// The verbs are deliberately short and omit arguments: the command, the
// path and the query live in the turn's tool rows, and repeating them
// here only widened the strip enough to push the context summary out.

import type { ToolInfo } from "../core/types";

export type WorkingPhase = "thinking" | "writing" | "retrying" | "compacting";

// workingLabel is the status's short summary of a tool call.
export function workingLabel(tool: ToolInfo): string {
  switch (tool.name) {
    case "shell":
      return "Running a command";
    case "read":
      return "Reading";
    case "write":
      return "Writing";
    case "edit":
      return "Editing";
    case "search":
    case "grep":
      return "Searching";
    case "glob":
      return "Finding files";
    case "list":
      return "Listing files";
    case "ask":
      return "Waiting for your answer";
    case "todo":
      return "Updating the plan";
    case "memory":
      return "Updating memory";
    case "skill":
      return "Loading a skill";
    case "session_search":
      return "Searching past sessions";
    case "browser":
      return "Using the browser";
    case "web_fetch":
      return "Fetching a page";
    case "visual":
      return "Drawing a visual";
    default:
      return `Using ${tool.name}`;
  }
}

export const PHASE_TEXT: Record<WorkingPhase, string> = {
  thinking: "Thinking",
  writing: "Replying",
  retrying: "Retrying",
  compacting: "Compacting",
};

let status: HTMLElement | null = null;

// workingStatus builds the right-hand slot of the Context head row: a
// spinner and one verb. It stays in the DOM and is hidden by its host,
// so the head row keeps its height whether or not a turn is running.
export function workingStatus(): HTMLElement {
  if (status) return status;
  const el = document.createElement("div");
  el.id = "workingBar";
  el.className = "working-status hidden";
  el.setAttribute("role", "status");
  el.setAttribute("aria-live", "polite");
  el.innerHTML =
    `<span class="working-spin" aria-hidden="true"></span>` +
    `<span class="working-what"></span>` +
    `<span class="working-time"></span>`;
  status = el;
  return el;
}

// formatElapsed renders a duration as 12s, 3m 04s or 1h 05m.
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}
