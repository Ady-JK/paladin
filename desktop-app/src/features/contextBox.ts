// contextBox.ts owns the collapsible "Context" strip above the composer:
// what paladin adds to the system prompt on its own for the current
// folder, so the implicit context of a repo is never a mystery. Rows are
// one per source — instruction files (AGENTS.md and friends), memory
// stores, the skill catalog and MCP notes — with the injected text shown
// inline when a row is opened.
//
// Cost control: one `contextSources` RPC per folder (cached, plus a
// short re-check interval), rows patched in place, and no polling while
// the window is hidden. The collapsed/expanded choice is remembered.

import * as api from "../core/api";
import { escapeHtml } from "../core/format";
import { loadText, saveText } from "../core/storage";
import { state } from "../core/state";
import type { ContextEntry, ContextView } from "../core/types";
import { icon } from "../icons";
import { workingStatus } from "./workingBar";

const OPEN_KEY = "paladin.contextBox.open";
const RECHECK_MS = 4000;
const PREVIEW_LINES = 24;

let box: HTMLElement | null = null;
let list: HTMLElement | null = null;
let summary: HTMLElement | null = null;
let open = loadText(OPEN_KEY) === "1";
let view: ContextView | null = null;
let seq = 0;
let timer = 0;

const KIND_ICON: Record<ContextEntry["kind"], string> = {
  instructions: "file-code",
  memory: "brain",
  skills: "terminal",
  mcp: "globe",
};

const KIND_LABEL: Record<ContextEntry["kind"], string> = {
  instructions: "Instructions",
  memory: "Memory",
  skills: "Skills",
  mcp: "MCP",
};

// approxBytes renders an injected size as words or a char count.
function approxBytes(bytes: number): string {
  if (bytes < 1000) {
    return `${bytes} chars`;
  }
  return `${(bytes / 1000).toFixed(1).replace(/\.0$/, "")}k chars`;
}

function baseName(path: string): string {
  const parts = path.split(/[/\\]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : path;
}

// headline is the chip line: what loads, and how much of it.
function headline(v: ContextView): string {
  const parts: string[] = [];
  if (v.files > 0) parts.push(`${v.files} instruction file${v.files === 1 ? "" : "s"}`);
  if (v.skills > 0) parts.push(`${v.skills} skill${v.skills === 1 ? "" : "s"}`);
  if (v.mcp > 0) parts.push("MCP notes");
  if (v.sections.some((e) => e.kind === "memory")) parts.push("memory");
  if (!parts.length) return "No context loaded for this folder";
  return `${parts.join(" · ")} · ${approxBytes(v.bytes)}`;
}

// ensureBox builds the strip once; later paints only touch its innards.
// The head row is one line: the context summary on the left, the
// running turn's status on the right. The status slot never leaves the
// DOM, so the row keeps its height whether a turn is running or not.
function ensureBox(): HTMLElement {
  if (box) return box;
  const slot = document.getElementById("contextBoxSlot");
  const wrap = document.createElement("div");
  wrap.id = "contextBox";
  wrap.className = "context-box";
  wrap.innerHTML =
    `<div class="context-head">` +
    `<button type="button" id="contextToggle" class="context-toggle" aria-expanded="false" aria-controls="contextList">` +
    `<span class="context-chevron" aria-hidden="true">${icon("chevron-right")}</span>` +
    `<span class="context-title">Context</span>` +
    `<span id="contextSummary" class="context-summary"></span>` +
    `</button>` +
    `<div id="workingSlot" class="working-slot"></div>` +
    `</div>` +
    `<div id="contextList" class="context-list" role="region" aria-label="Auto-loaded context"></div>`;
  (slot ?? document.body).appendChild(wrap);
  box = wrap;
  list = wrap.querySelector<HTMLElement>("#contextList");
  summary = wrap.querySelector<HTMLElement>("#contextSummary");
  wrap.querySelector<HTMLElement>("#workingSlot")!.appendChild(workingStatus());
  if (!slot) wrap.classList.add("hidden");

  wrap.querySelector<HTMLElement>("#contextToggle")?.addEventListener("click", () => {
    setOpen(!open);
  });
  // A row's head opens the source; the body holds the text.
  list?.addEventListener("click", (event) => {
    const head = (event.target as HTMLElement).closest<HTMLElement>("[data-entry]");
    if (!head || !list?.contains(head)) return;
    const row = head.closest<HTMLElement>(".context-row");
    if (!row) return;
    row.classList.toggle("is-open");
    head.setAttribute("aria-expanded", row.classList.contains("is-open") ? "true" : "false");
    // Bodies are filled by paint, which skips them while collapsed.
    paint();
  });
  return wrap;
}

function setOpen(next: boolean): void {
  open = next;
  saveText(OPEN_KEY, open ? "1" : "0");
  paint();
}

// rowEl builds one source row with an empty body to fill in later.
function rowEl(entry: ContextEntry): HTMLElement {
  const row = document.createElement("div");
  row.className = "context-row";
  row.dataset.kind = entry.kind;
  row.dataset.scope = entry.scope;
  row.dataset.path = entry.path ?? "";
  row.innerHTML =
    `<button type="button" class="context-entry-head" data-entry aria-expanded="false">` +
    `<span class="context-kind" aria-hidden="true">${icon(KIND_ICON[entry.kind])}</span>` +
    `<span class="context-entry-main">` +
    `<span class="context-entry-title"></span>` +
    `<span class="context-entry-detail"></span>` +
    `</span>` +
    `<span class="context-entry-size"></span>` +
    `<span class="context-row-chevron" aria-hidden="true">${icon("chevron-right")}</span>` +
    `</button>` +
    `<div class="context-body"></div>`;
  return row;
}

function paintRow(row: HTMLElement, entry: ContextEntry): void {
  row.dataset.kind = entry.kind;
  row.dataset.scope = entry.scope;
  row.dataset.path = entry.path ?? "";
  const title = row.querySelector<HTMLElement>(".context-entry-title")!;
  const detail = row.querySelector<HTMLElement>(".context-entry-detail")!;
  const size = row.querySelector<HTMLElement>(".context-entry-size")!;
  const body = row.querySelector<HTMLElement>(".context-body")!;
  const label = KIND_LABEL[entry.kind];
  const name = entry.title || (entry.path ? baseName(entry.path) : label);
  const titleText = `${name}`;
  if (title.textContent !== titleText) title.textContent = titleText;
  // The path is what the user came for, so it stays visible on the row;
  // the core's explanation becomes the hover title.
  const where = entry.path ?? "";
  const detailText = [label, where || entry.detail, entry.truncated ? "truncated" : ""].filter(Boolean).join(" · ");
  if (detail.textContent !== detailText) detail.textContent = detailText;
  if (detail.title !== `${where}\n${entry.detail ?? ""}`) detail.title = `${where}\n${entry.detail ?? ""}`;
  const sizeText = approxBytes(entry.bytes);
  if (size.textContent !== sizeText) size.textContent = sizeText;
  row.querySelector<HTMLElement>(".context-entry-head")!.title =
    `${label}${name !== label ? `: ${name}` : ""}` + `${where ? `\n${where}` : ""}\nClick to show the injected text`;

  // The body only exists while it is shown, so a long prompt section
  // is not parsed for every row on every paint.
  const wanted = open && row.classList.contains("is-open");
  const text = entry.content ?? "";
  const lines = text.split("\n");
  const cut = lines.length > PREVIEW_LINES;
  const shown = cut ? lines.slice(0, PREVIEW_LINES).join("\n") : text;
  const signature = `${wanted}|${shown.length}|${cut}`;
  if (body.dataset.sig === signature) return;
  body.dataset.sig = signature;
  body.innerHTML = wanted
    ? `<pre class="context-text">${escapeHtml(shown)}</pre>` +
      (cut ? `<div class="context-more">… ${lines.length - PREVIEW_LINES} more lines in the prompt</div>` : "") +
      (where ? `<div class="context-path">${escapeHtml(where)}</div>` : "")
    : "";
}

// paint repaints the strip from the cached view. The strip also hosts the
// running turn's status, so it stays up while a turn runs even when the
// folder has no context and the sidecar has no `contextSources`: hiding
// it then would take the status down with it.
function paint(): void {
  const wrap = ensureBox();
  const v = view;
  const busy = !workingStatus().classList.contains("hidden");
  wrap.classList.toggle("hidden", !v && !busy);
  wrap.dataset.open = open ? "1" : "0";
  // With no context view there is nothing to expand, so the head row is
  // a plain line and the chevron goes away.
  wrap.dataset.hasContext = v ? "1" : "0";
  const toggle = wrap.querySelector<HTMLButtonElement>("#contextToggle")!;
  toggle.disabled = !v;
  toggle.setAttribute("aria-expanded", v && open ? "true" : "false");
  toggle.title = !v ? "" : open ? "Hide what loads automatically" : "Show what loads automatically";
  if (summary) {
    const text = v ? headline(v) : "";
    if (summary.textContent !== text) summary.textContent = text;
  }
  if (!v) {
    list!.classList.add("hidden");
    return;
  }

  const host = list!;
  host.classList.toggle("hidden", !open);
  if (!open) return;

  if (!v.sections.length) {
    host.innerHTML = `<div class="context-empty">Nothing is loaded automatically for this folder. Add an <code>AGENTS.md</code> to give the agent standing instructions.</div>`;
    return;
  }

  const empty = host.querySelector(".context-empty");
  empty?.remove();
  const existing = new Map<string, HTMLElement>();
  for (const row of Array.from(host.querySelectorAll<HTMLElement>(".context-row"))) {
    existing.set(rowKey(row), row);
  }
  const keep = new Set<string>();
  for (const entry of v.sections) {
    const key = `${entry.kind}|${entry.scope}|${entry.path ?? entry.title}`;
    keep.add(key);
    let row = existing.get(key);
    if (!row) {
      row = rowEl(entry);
      host.append(row);
    }
    paintRow(row, entry);
    host.append(row); // re-orders without rebuilding the list
  }
  for (const [key, row] of existing) {
    if (!keep.has(key)) row.remove();
  }
}

function rowKey(row: HTMLElement): string {
  return `${row.dataset.kind}|${row.dataset.scope}|${row.dataset.path ?? ""}`;
}

// reload asks the core for the current folder's sources.
export function reloadContextSources(): Promise<void> {
  const id = ++seq;
  const cwd = state.cwd || null;
  return api.contextSources(cwd).then(
    (v) => {
      if (id !== seq) return;
      view = { ...v, sections: v.sections ?? [] };
      paint();
    },
    () => {
      // An older sidecar has no contextSources: hide the box rather than
      // showing an empty one that claims nothing is loaded.
      if (id !== seq) return;
      view = null;
      paint();
    },
  );
}

// clearContextBox forgets the current folder's view (new or closed session).
export function clearContextBox(): void {
  seq++;
  view = null;
  paint();
}

// showContextBox reveals the strip for the running turn when the folder
// has no context to list. paint() then decides visibility from the view
// and the status together, so the status is never painted into a hidden
// strip. A no-op when a context view is already loaded.
export function showContextBox(): void {
  paint();
}

function schedule(): void {
  window.clearTimeout(timer);
  if (document.visibilityState === "hidden") return;
  timer = window.setTimeout(() => {
    void reloadContextSources().finally(schedule);
  }, RECHECK_MS);
}

export function initContextBox(): void {
  ensureBox();
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      window.clearTimeout(timer);
    } else {
      void reloadContextSources().finally(schedule);
    }
  });
  void reloadContextSources().finally(schedule);
}
