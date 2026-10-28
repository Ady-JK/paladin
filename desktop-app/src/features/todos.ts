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
let composerEl: Element | null = null;

// statusMark renders the left-hand state marker: orange ring while
// the work has not started, yellow dot while it runs, green check
// when it is done; a cancelled step shows a grey dash.
function statusMark(status: string): string {
  switch (status) {
    case "in_progress":
      return `<span class="flex h-4 w-4 flex-shrink-0 items-center justify-center text-[10px] leading-none text-amber-500">●</span>`;
    case "finished":
      return `<span class="flex h-4 w-4 flex-shrink-0 items-center justify-center text-[11px] leading-none text-emerald-500">✓</span>`;
    case "cancelled":
      return `<span class="flex h-4 w-4 flex-shrink-0 items-center justify-center text-[11px] leading-none text-slate-400">–</span>`;
    default:
      return `<span class="flex h-4 w-4 flex-shrink-0 items-center justify-center text-[11px] leading-none text-orange-400">○</span>`;
  }
}

// statusPill renders the right-hand state chip: orange while the work
// has not started, yellow while it runs, green when it is done; a
// cancelled step shows grey.
function statusPill(status: string): string {
  const base =
    "flex-shrink-0 rounded-full border px-2 py-0.5 font-mono text-[10px] font-semibold whitespace-nowrap";
  switch (status) {
    case "in_progress":
      return `<span class="${base} border-amber-300 bg-amber-100 text-amber-700 dark:border-amber-700/60 dark:bg-amber-950/60 dark:text-amber-300\">in_progress</span>`;
    case "finished":
      return `<span class="${base} border-emerald-300 bg-emerald-100 text-emerald-700 dark:border-emerald-700/60 dark:bg-emerald-950/60 dark:text-emerald-300\">finished</span>`;
    case "cancelled":
      return `<span class="${base} border-slate-300 bg-slate-100 text-slate-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-400\">cancelled</span>`;
    default:
      return `<span class="${base} border-orange-300 bg-orange-100 text-orange-700 dark:border-orange-700/60 dark:bg-orange-950/60 dark:text-orange-300\">not_started</span>`;
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

// summaryLine is the collapsed panel title: "Plan · 2/4 finished".
function summaryLine(): string {
  const done = todos.filter((t) => t.status === "finished" || t.status === "cancelled").length;
  return `Plan · ${done}/${todos.length} finished`;
}

function paint(): void {
  if (!hatEl || !barEl || !listEl) {
    return;
  }
  // No plan yet: the panel stays hidden entirely and the composer
  // gets its rounded top back.
  hatEl.classList.toggle("hidden", todos.length === 0);
  if (composerEl) {
    composerEl.classList.toggle("rounded-3xl", todos.length === 0);
    composerEl.classList.toggle("rounded-b-3xl", todos.length !== 0);
    composerEl.classList.toggle("rounded-t-none", todos.length !== 0);
    composerEl.classList.toggle("border-t-0", todos.length !== 0);
  }
  if (todos.length === 0) {
    return;
  }
  barEl.innerHTML =
    `<span class="min-w-0 flex-1 truncate">${escapeHtml(summaryLine())}</span>` +
    `<span data-icon="${open ? "chevron-down" : "chevron-right"}" class="flex-shrink-0 text-slate-400"></span>`;
  paintIcons(hatEl);
  listEl.classList.toggle("hidden", !open);
  if (open) {
    listEl.innerHTML = todos
      .map(
        (t) =>
          `<div class="flex items-center gap-2 px-4 py-1.5">` +
          `${statusMark(t.status)}` +
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

// initTodos builds the plan panel latched onto the composer: a
// separate tab above the composer card that shares its top edge, like
// a lid on the textarea. The bar shows the plan title; clicking it
// expands the checklist upward, above the composer.
export function initTodos(): void {
  const wrap = document.querySelector("#agentView footer div.w-full");
  if (!wrap || hatEl || !wrap.querySelector(".composer-float")) {
    return;
  }
  hatEl = document.createElement("div");
  hatEl.id = "todoHat";
  // Latched tab: same span as the composer, bottom corners squared so
  // it meets the composer's top edge; hidden until there is a plan.
  hatEl.className = "hidden px-0";
  hatEl.innerHTML =
    `<div class="rounded-t-3xl rounded-b-none border border-b-0 border-slate-300/80 bg-slate-100/90 dark:border-slate-700/70 dark:bg-slate-900/90">` +
    `<button type="button" data-todo="bar" class="flex w-full items-center gap-2 px-4 py-2 text-left text-xs font-semibold text-slate-700 dark:text-slate-200" title="Show the plan"></button>` +
    `<div data-todo="list" class="hidden max-h-56 divide-y divide-slate-200/70 overflow-y-auto border-t border-slate-200/70 text-xs text-slate-700 dark:divide-slate-700/60 dark:border-slate-700/60 dark:text-slate-300"></div>` +
    `</div>`;
  const composer = wrap.querySelector(".composer-float");
  if (composer) {
    composerEl = composer;
    wrap.insertBefore(hatEl, composer);
    // The composer card's top corners square off where the tab latches.
    composer.classList.remove("rounded-3xl");
    composer.classList.add("rounded-b-3xl", "rounded-t-none", "border-t-0");
  }
  barEl = hatEl.querySelector('[data-todo="bar"]');
  listEl = hatEl.querySelector('[data-todo="list"]');
  barEl?.addEventListener("click", () => toggleTodos());
  paint();
}
