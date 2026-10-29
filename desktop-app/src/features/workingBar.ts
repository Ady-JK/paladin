// workingBar.ts is the "Working · 12s · Reading main.go" strip above
// the composer: its DOM, its labels and its time format. live.ts decides
// what it shows.

import type { ToolInfo } from "../core/types";
import { sendBtn } from "../core/dom";

export type WorkingPhase = "thinking" | "writing" | "retrying" | "compacting";

// workingLabel is the working bar's one-line summary of a tool call.
export function workingLabel(tool: ToolInfo): string {
  let args: Record<string, unknown> = {};
  try {
    const parsed = JSON.parse(tool.arguments || "{}");
    if (parsed && typeof parsed === "object") args = parsed as Record<string, unknown>;
  } catch {
    // Streaming arguments may be partial; fall back to the name.
  }
  const str = (k: string) => (typeof args[k] === "string" ? (args[k] as string) : "");
  const tail = (p: string) => p.split(/[\\/]/).filter(Boolean).slice(-2).join("/") || p;
  switch (tool.name) {
    case "shell":
      return str("cmd") ? `Running ${str("cmd").split("\n")[0]}` : "Running a command";
    case "read":
      return str("path") ? `Reading ${tail(str("path"))}` : "Reading a file";
    case "write":
      return str("path") ? `Writing ${tail(str("path"))}` : "Writing a file";
    case "edit":
      return str("path") ? `Editing ${tail(str("path"))}` : "Editing a file";
    case "search":
    case "grep":
      return str("pattern") || str("query") ? `Searching for ${str("pattern") || str("query")}` : "Searching";
    case "ask":
      return "Waiting for your answer";
    case "todo":
      return "Updating the plan";
    case "memory":
      return "Updating memory";
    case "skill":
      return str("name") ? `Loading skill ${str("name")}` : "Loading a skill";
    case "session_search":
      return "Searching past sessions";
    default:
      return `Using ${tool.name}`;
  }
}

export const PHASE_TEXT: Record<WorkingPhase, string> = {
  thinking: "Thinking",
  writing: "Writing the reply",
  retrying: "Retrying the model",
  compacting: "Compacting context",
};

let bar: HTMLElement | null = null;

// workingBar lazily mounts the bar above the composer.
export function workingBar(): HTMLElement {
  if (bar) return bar;
  const el = document.createElement("div");
  el.id = "workingBar";
  el.className = "working-bar hidden";
  el.setAttribute("role", "status");
  el.setAttribute("aria-live", "polite");
  el.innerHTML =
    `<span class="working-spin" aria-hidden="true"></span>` +
    `<span class="working-title">Working</span>` +
    `<span class="working-time"></span>` +
    `<span class="working-sep" aria-hidden="true">·</span>` +
    `<span class="working-what"></span>` +
    `<span class="working-more"></span>` +
    `<span class="working-hint">Esc Esc to stop</span>`;
  const footer = sendBtn.closest("footer");
  const host = footer?.firstElementChild ?? footer;
  host?.prepend(el);
  bar = el;
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
