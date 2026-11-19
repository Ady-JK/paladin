// codexSkills.ts is the `$` menu of a Codex session: typing `$` in the composer
// lists the skills Codex has for the folder, and picking one leaves `$name` in
// the text, which Codex reads itself (the core also attaches the skill). It is
// only active in a Codex session; a Paladin session has its own skill chip.

import * as api from "../../core/api";
import { inputEl } from "../../core/dom";
import { CODEX } from "../../core/harness";
import { escapeHtml } from "../../core/format";
import { state } from "../../core/state";
import type { CodexSkill } from "../../core/types";

const MAX_ROWS = 8;

let popup: HTMLElement | null = null;
let items: CodexSkill[] = [];
let activeIdx = 0;
let tokenStart = -1;
let open = false;
// skills are fetched once per folder: they change rarely, and the menu opens often.
let cached: { cwd: string; skills: CodexSkill[] } | null = null;
let lookupSeq = 0;

function ensurePopup(): HTMLElement {
  if (popup) return popup;
  const el = document.createElement("div");
  el.id = "skillPopup";
  el.className =
    "absolute bottom-full left-0 right-0 z-50 mb-1.5 hidden max-h-64 overflow-y-auto rounded-xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900 dark:shadow-black/60";
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    // Accept on mousedown: the textarea blurs before click fires.
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      accept(Number(row.dataset.idx));
    }
  });
  popup = el;
  return el;
}

// skillTokenAt finds the `$token` under the caret: a `$` at the start or after
// a space or bracket, running to the caret.
function skillTokenAt(value: string, caret: number): { start: number; query: string } | null {
  const m = /(?:^|[\s(])\$([\w:.-]*)$/.exec(value.slice(0, caret));
  return m ? { start: caret - m[1].length - 1, query: m[1] } : null;
}

function close(): void {
  open = false;
  lookupSeq++;
  tokenStart = -1;
  items = [];
  popup?.classList.add("hidden");
}

function paint(): void {
  const el = ensurePopup();
  if (!open || items.length === 0) {
    el.classList.add("hidden");
    return;
  }
  el.classList.remove("hidden");
  el.innerHTML = items
    .map((skill, i) => {
      const detail = skill.description || skill.title || "";
      const scope = skill.scope
        ? `<span class="flex-shrink-0 text-[10px] text-slate-400">${escapeHtml(skill.scope)}</span>`
        : "";
      const tone =
        i === activeIdx ? "bg-accent-600/10 text-slate-900 dark:text-slate-100" : "text-slate-600 dark:text-slate-300";
      return `<button data-idx="${i}" data-skill="${escapeHtml(skill.name)}" class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left font-mono text-xs ${tone}"><span class="min-w-0 flex-1 truncate">${escapeHtml(skill.name)}<span class="ml-1.5 text-slate-400">${escapeHtml(detail)}</span></span>${scope}</button>`;
    })
    .join("");
}

// matching ranks names that start with the query ahead of ones that only contain it.
function matching(skills: CodexSkill[], query: string): CodexSkill[] {
  const q = query.toLowerCase();
  if (!q) return skills.slice(0, MAX_ROWS);
  const starts = skills.filter((s) => s.name.toLowerCase().startsWith(q));
  const rest = skills.filter((s) => `${s.name} ${s.title ?? ""}`.toLowerCase().includes(q) && !starts.includes(s));
  return [...starts, ...rest].slice(0, MAX_ROWS);
}

async function refresh(query: string): Promise<void> {
  const seq = ++lookupSeq;
  const cwd = state.cwd || "";
  if (cached?.cwd !== cwd) {
    try {
      cached = { cwd, skills: await api.codexSkills(cwd) };
    } catch {
      cached = { cwd, skills: [] };
    }
  }
  // The caret may have moved on while the lookup was in flight.
  if (!open || seq !== lookupSeq) return;
  items = matching(cached.skills, query);
  activeIdx = 0;
  paint();
}

function accept(idx: number): boolean {
  if (!open || items.length === 0) return false;
  const skill = items[Math.min(Math.max(idx, 0), items.length - 1)];
  const value = inputEl.value;
  const caret = inputEl.selectionStart ?? value.length;
  const start = skillTokenAt(value, caret)?.start ?? tokenStart;
  if (start < 0) return false;
  const insert = `$${skill.name} `;
  inputEl.value = `${value.slice(0, start)}${insert}${value.slice(caret)}`;
  const next = start + insert.length;
  inputEl.selectionStart = next;
  inputEl.selectionEnd = next;
  close();
  inputEl.focus();
  return true;
}

// handleCodexSkillKey consumes the menu's navigation keys. Returns true when the
// keystroke was handled and the composer must ignore it.
export function handleCodexSkillKey(event: KeyboardEvent): boolean {
  if (!open) return false;
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    close();
    return true;
  }
  if (items.length === 0) return false;
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length;
    paint();
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    accept(activeIdx);
    return true;
  }
  return false;
}

function onInput(): void {
  if (state.harness !== CODEX) {
    if (open) close();
    return;
  }
  const tok = skillTokenAt(inputEl.value, inputEl.selectionStart ?? inputEl.value.length);
  if (!tok) {
    close();
    return;
  }
  open = true;
  tokenStart = tok.start;
  ensurePopup();
  void refresh(tok.query);
}

export function initCodexSkills(): void {
  inputEl.addEventListener("input", onInput);
  inputEl.addEventListener("click", onInput);
  inputEl.addEventListener("blur", () => {
    window.setTimeout(close, 120);
  });
}
