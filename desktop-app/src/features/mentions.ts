// mentions.ts owns `@` file/folder mentions in the composer.
//
// Typing `@` opens a popup backed by the `fileComplete` sidecar
// method (ripgrep-powered, .gitignore honored, `../` escapes to the
// workspace parent). Picking a row inserts the row's OS-specific
// absolute path into the message, so the agent sees exactly which
// file or folder was tagged. Hand-typed `@paths` are resolved the
// same way at send time; tokens that match nothing stay untouched.

import * as api from "../core/api";
import { inputEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { FileEntry } from "../core/types";

let popup: HTMLElement | null = null;
let items: FileEntry[] = [];
let activeIdx = 0;
let tokenStart = -1;
let tokenQuery = "";
let debounce = 0;
let open = false;

function ensurePopup(): HTMLElement {
  if (popup) {
    return popup;
  }
  const el = document.createElement("div");
  el.id = "mentionPopup";
  el.className =
    "absolute bottom-full left-0 right-0 z-50 mb-1.5 hidden max-h-64 overflow-y-auto rounded-xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900 dark:shadow-black/60";
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    // Accept on mousedown: the textarea blurs before click fires.
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      acceptMention(Number(row.dataset.idx));
    }
  });
  popup = el;
  return el;
}

// mentionTokenAt finds the `@token` under the caret: an `@` preceded
// by start/whitespace, running to the caret with no spaces.
export function mentionTokenAt(
  value: string,
  caret: number,
): { start: number; query: string } | null {
  const before = value.slice(0, caret);
  const m = /(?:^|\s)@([^\s@]*)$/.exec(before);
  if (!m) {
    return null;
  }
  return { start: caret - m[1].length - 1, query: m[1] };
}

function closeMention(): void {
  open = false;
  tokenStart = -1;
  items = [];
  popup?.classList.add("hidden");
}

function paintMention(): void {
  const el = ensurePopup();
  if (!open || items.length === 0) {
    el.classList.add("hidden");
    return;
  }
  el.classList.remove("hidden");
  el.innerHTML = items
    .map((entry, i) => {
      const badge = entry.isDir
        ? `<span class="flex-shrink-0 font-mono text-[10px] text-agent-amber">dir</span>`
        : entry.outside
          ? `<span class="flex-shrink-0 font-mono text-[10px] text-slate-400">out</span>`
          : "";
      return `<button data-idx="${i}" class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left font-mono text-xs ${
        i === activeIdx
          ? "bg-accent-600/10 text-slate-900 dark:text-slate-100"
          : "text-slate-600 dark:text-slate-300"
      }"><span class="min-w-0 flex-1 truncate">${escapeHtml(entry.display)}<span class="ml-1.5 text-slate-400">${escapeHtml(entry.path)}</span></span>${badge}</button>`;
    })
    .join("");
}

async function refreshMention(query: string): Promise<void> {
  try {
    items = await api.fileComplete(query, state.cwd || null, 8);
  } catch {
    items = [];
  }
  // The caret may have moved on while the lookup was in flight.
  if (!open || query !== tokenQuery) {
    return;
  }
  activeIdx = 0;
  paintMention();
}

function scheduleRefresh(query: string): void {
  tokenQuery = query;
  window.clearTimeout(debounce);
  debounce = window.setTimeout(() => void refreshMention(query), 120);
}

// acceptMention swaps the `@token` for the row's OS-specific path.
export function acceptMention(idx: number): boolean {
  if (!open || items.length === 0) {
    return false;
  }
  const entry = items[Math.min(Math.max(idx, 0), items.length - 1)];
  const value = inputEl.value;
  const caret = inputEl.selectionStart ?? value.length;
  const tok = mentionTokenAt(value, caret);
  const start = tok?.start ?? tokenStart;
  if (start < 0) {
    return false;
  }
  const end = caret;
  const insert = entry.resolvedPath;
  inputEl.value = `${value.slice(0, start)}${insert}${value.slice(end)}`;
  const next = start + insert.length;
  inputEl.selectionStart = next;
  inputEl.selectionEnd = next;
  closeMention();
  inputEl.focus();
  return true;
}

// handleMentionKey consumes popup navigation keys. Returns true when
// the keystroke was handled and the composer must ignore it.
export function handleMentionKey(event: KeyboardEvent): boolean {
  if (!open) {
    return false;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeMention();
    return true;
  }
  if (items.length === 0) {
    return false;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIdx = (activeIdx + 1) % items.length;
    paintMention();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx - 1 + items.length) % items.length;
    paintMention();
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    acceptMention(activeIdx);
    return true;
  }
  return false;
}

function onInput(): void {
  const caret = inputEl.selectionStart ?? inputEl.value.length;
  const tok = mentionTokenAt(inputEl.value, caret);
  if (!tok) {
    closeMention();
    return;
  }
  open = true;
  tokenStart = tok.start;
  ensurePopup();
  scheduleRefresh(tok.query);
}

// normMentionPath compares chat paths loosely: separators unified,
// one trailing slash ignored.
export function normMentionPath(p: string): string {
  return p.replace(/\\/g, "/").replace(/\/+$/, "");
}

// expandMentions resolves hand-typed `@paths` to their OS-specific
// absolute paths. Popup-accepted mentions are already absolute; this
// covers tokens typed without touching the popup. Only exact matches
// expand — emails and unknown paths stay as-is.
export async function expandMentions(text: string): Promise<string> {
  const tokens = new Set<string>();
  for (const m of text.matchAll(/(^|\s)@([^\s@]+)/g)) {
    tokens.add(stripMentionPunct(m[2]));
  }
  if (tokens.size === 0) {
    return text;
  }
  let out = text;
  for (const token of tokens) {
    if (!token) {
      continue;
    }
    let entries: FileEntry[];
    try {
      entries = await api.fileComplete(token, state.cwd || null, 8);
    } catch {
      continue;
    }
    const want = normMentionPath(token);
    const hit = entries.find((e) => normMentionPath(e.path) === want);
    if (!hit) {
      continue;
    }
    out = out
      .split(`@${token}`)
      .join(hit.resolvedPath);
  }
  return out;
}

function stripMentionPunct(token: string): string {
  return token.replace(/[.,;:!?)'"\]]+$/, "");
}

// initMentions wires the composer listeners. Key handling lives in
// chat.ts (ordering matters) via handleMentionKey.
export function initMentions(): void {
  inputEl.addEventListener("input", onInput);
  inputEl.addEventListener("click", onInput);
  inputEl.addEventListener("blur", () => {
    // Let mousedown acceptance win the race before hiding.
    window.setTimeout(closeMention, 120);
  });
}
