// todos.ts renders the todo hat above the composer: a small bar that
// summarizes the session's activity list (the agent's todo tool state)
// and expands upwards into the full list with per-item status pills.
// The hat stays hidden until the agent has planned something; while a
// turn runs, every settled todo tool call repaints it live.
import { sessionTodos } from "../core/api";
import { paintIcons } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { TodoEntry, ToolInfo } from "../core/types";

let todos: TodoEntry[] = [];
let open = false;
let hatEl: HTMLElement | null = null;
let barEl: HTMLElement | null = null;
let listEl: HTMLElement | null = null;

// statusPill renders the right-hand state chip: orange while the work
// has not started, yellow while it runs, green when it is done; a
// cancelled step shows grey.
function statusPill(status: string): string {
  const base =
    "flex-shrink-0 rounded-full border px-2 py-0.5 font-mono text-[10px] font-semibold whitespace-nowrap";
  switch (status) {
    case "in_progress":
      return `<span class="${base} border-amber-300 bg-amber-100 text-amber-700 dark:border-amber-700/60 dark:bg-amber-950/60 dark:text-amber-300">in_progress</span>`;
    case "finished":
      return `<span class="${base} border-emerald-300 bg-emerald-100 text-emerald-700 dark:border-emerald-700/60 dark:bg-emerald-950/60 dark:text-emerald-300">finished</span>`;
    case "cancelled":
      return `<span class="${base} border-slate-300 bg-slate-100 text-slate-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-400">cancelled</span>`;
    default:
      return `<span class="${base} border-orange-300 bg-orange-100 text-orange-700 dark:border-orange-700/60 dark:bg-orange-950/60 dark:text-orange-300">not_started</span>`;
  }
}

// parseTodoResult reads the todos out of a settled todo tool result.
// Null means the result is not a todo list (or is malformed).
export function parseTodoResult(raw: string): TodoEntry[] | null {
  let args: unknown;
  try {
    args = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof args !== "object" || args === null) {
    return null;
  }
  const list = (args as Record<string, unknown>).todos;
  if (!Array.isArray(list)) {
    return null;
  }
  const out: TodoEntry[] = [];
  for (const item of list) {
    if (typeof item !== "object" || item === null) {
      return null;
    }
    const rec = item as Record<string, unknown>;
    if (typeof rec.content !== "string" || typeof rec.status !== "string") {
      return null;
    }
    out.push({ content: rec.content, status: rec.status });
  }
  return out;
}

// summaryLine is the collapsed hat text: "2/4 finished · building X".
function summaryLine(): string {
  const done = todos.filter((t) => t.status === "finished" || t.status === "cancelled").length;
  const active = todos.find((t) => t.status === "in_progress");
  const head = `${done}/${todos.length} finished`;
  return active ? `${head} · ${active.content}` : head;
}

function paint(): void {
  if (!hatEl || !barEl || !listEl) {
    return;
  }
  // No plan yet: the hat stays hidden entirely.
  hatEl.classList.toggle("hidden", todos.length === 0);
  if (todos.length === 0) {
    return;
  }
  const done = todos.filter((t) => t.status === "finished" || t.status === "cancelled").length;
  const frac = todos.length > 0 ? done / todos.length : 0;
  barEl.innerHTML =
    `<span data-icon="check" class="flex-shrink-0 text-accent-500"></span>` +
    `<span class="min-w-0 flex-1 truncate font-medium">${escapeHtml(summaryLine())}</span>` +
    `<span class="h-1 w-16 flex-shrink-0 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">` +
    `<span class="block h-full rounded-full bg-emerald-500" style="width: ${Math.round(frac * 100)}%"></span></span>` +
    `<span class="flex-shrink-0 font-mono text-slate-400">${done}/${todos.length}</span>` +
    `<span data-icon="${open ? "chevron-down" : "chevron-right"}" class="flex-shrink-0 text-slate-400"></span>`;
  paintIcons(hatEl);
  listEl.classList.toggle("hidden", !open);
  if (open) {
    listEl.innerHTML = todos
      .map(
        (t) =>
          `<div class="flex items-center gap-2 px-3 py-1.5">` +
          `<span class="min-w-0 flex-1 truncate" title="${escapeHtml(t.content)}">${escapeHtml(t.content)}</span>` +
          `${statusPill(t.status)}</div>`,
      )
      .join("");
  }
}

// setTodos replaces the list and repaints. Exported for tests.
export function setTodos(next: TodoEntry[]): void {
  todos = next;
  paint();
}

// handleTodoToolEvent folds a settled todo tool call into the hat so
// it updates live while the turn runs. Errors and running calls carry
// no list and are ignored.
export function handleTodoToolEvent(tool: ToolInfo): void {
  if (tool.name !== "todo" || tool.status !== "ok") {
    return;
  }
  const list = parseTodoResult(tool.result);
  if (!list) {
    return;
  }
  setTodos(list);
}

// refreshTodos loads the session's list (reads on open/history so a
// resumed session shows its plan) and repaints. Failures keep the last
// list instead of hiding the hat mid-turn.
export async function refreshTodos(): Promise<void> {
  if (!state.sessionId) {
    setTodos([]);
    return;
  }
  try {
    setTodos(await sessionTodos(state.sessionId, state.cwd || null));
  } catch (error) {
    console.warn(`refreshTodos failed: ${String(error)}`);
  }
}

// clearTodos empties the hat for a fresh session.
export function clearTodos(): void {
  todos = [];
  open = false;
  paint();
}

// toggleTodos opens or closes the hat list. Exported for tests.
export function toggleTodos(): void {
  open = !open;
  paint();
}

// initTodos builds the hat above the composer. The list pops upwards
// from the bar, so it never covers the textarea.
export function initTodos(): void {
  const composer = document.querySelector("#agentView footer .composer-float");
  const input = document.querySelector("#promptInput");
  if (!composer || !input || hatEl) {
    return;
  }
  hatEl = document.createElement("div");
  hatEl.id = "todoHat";
  hatEl.className = "hidden px-4 pt-2";
  hatEl.innerHTML =
    `<div class="overflow-hidden rounded-2xl border border-slate-200/80 bg-slate-100/80 dark:border-slate-700/60 dark:bg-slate-800/40">` +
    `<button type="button" data-todo="bar" class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[11px] text-slate-600 dark:text-slate-300" title="Show the activity list"></button>` +
    `<div data-todo="list" class="hidden max-h-56 divide-y divide-slate-200/70 overflow-y-auto border-t border-slate-200/70 text-xs text-slate-700 dark:divide-slate-700/60 dark:border-slate-700/60 dark:text-slate-300"></div>` +
    `</div>`;
  composer.prepend(hatEl);
  barEl = hatEl.querySelector('[data-todo="bar"]');
  listEl = hatEl.querySelector('[data-todo="list"]');
  barEl?.addEventListener("click", () => toggleTodos());
  paint();
}
