// skills.ts owns `/skill` in the composer.
//
// Typing `/skill` opens a picker backed by the `listSkills` sidecar
// method (global config/skills + workspace .paladin/skills, each a
// `<name>/SKILL.md` directory). Picking a row (or typing
// `/skill <name> <message>` by hand) attaches the skill to this turn:
// the composer keeps the short `/skill <name> <text>` line and shows a
// chip above it, while the chat call carries the name separately and
// the sidecar injects the SKILL.md content before the LLM call — the
// content is never pasted into the textarea.

import * as api from "../core/api";
import { inputEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { SkillEntry } from "../core/types";

let popup: HTMLElement | null = null;
let items: SkillEntry[] = [];
let all: SkillEntry[] | null = null;
let activeIdx = 0;
let open = false;
let debounce = 0;
let seq = 0;

// attached is the skill for the pending turn. It clears on send (chat.ts
// takes it) and on detach.
let attached: string | null = null;
let chipEl: HTMLElement | null = null;

function ensurePopup(): HTMLElement {
  if (popup) {
    return popup;
  }
  const el = document.createElement("div");
  el.id = "skillPopup";
  el.className =
    "absolute bottom-full left-0 right-0 z-50 mb-1.5 hidden max-h-64 overflow-y-auto rounded-xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900 dark:shadow-black/60";
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      acceptSkill(Number(row.dataset.idx));
    }
  });
  popup = el;
  return el;
}

// skillPrefix returns the name filter when the whole composer is a
// `/skill` prefix with the caret at the end: `/skill`, `/skill `,
// `/skill pre`. Anything with a second token (the message) is not a
// prefix — the picker stays shut once the user starts typing.
export function skillPrefix(value: string, caret: number): string | null {
  if (caret !== value.length || value.includes("\n")) {
    return null;
  }
  const trimmed = value.trimStart();
  const m = /^\/skill(?:\s+([a-zA-Z0-9_-]*))?$/.exec(trimmed);
  if (!m || !trimmed.toLowerCase().startsWith("/skill")) {
    return null;
  }
  return m[1] ?? "";
}

function closeSkill(): void {
  open = false;
  items = [];
  popup?.classList.add("hidden");
}

function paintSkill(): void {
  const el = ensurePopup();
  if (!open) {
    el.classList.add("hidden");
    return;
  }
  if (items.length === 0) {
    el.classList.remove("hidden");
    el.innerHTML = `<div class="px-2.5 py-1.5 font-mono text-xs text-slate-400">no skills — add &lt;name&gt;/SKILL.md under config/skills/ or .paladin/skills/</div>`;
    return;
  }
  el.classList.remove("hidden");
  el.innerHTML = items
    .map(
      (s, i) => `<button data-idx="${i}" class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left font-mono text-xs ${
        i === activeIdx
          ? "bg-accent-600/10 text-slate-900 dark:text-slate-100"
          : "text-slate-600 dark:text-slate-300"
      }"><span class="min-w-0 flex-1 truncate">/${escapeHtml(s.name)}<span class="ml-1.5 text-slate-400">${escapeHtml(s.description || s.source)}</span></span><span class="flex-shrink-0 text-[10px] text-slate-400">${escapeHtml(s.source)}</span></button>`,
    )
    .join("");
}

async function refreshSkill(filter: string, mySeq: number): Promise<void> {
  try {
    if (!all) {
      all = await api.listSkills(state.cwd || null);
    }
  } catch {
    all = [];
  }
  if (!open || mySeq !== seq) {
    return;
  }
  const q = filter.toLowerCase();
  items = (all || []).filter((s) => s.name.toLowerCase().startsWith(q));
  activeIdx = 0;
  paintSkill();
}

function scheduleRefresh(filter: string): void {
  const mySeq = ++seq;
  window.clearTimeout(debounce);
  debounce = window.setTimeout(() => void refreshSkill(filter, mySeq), 120);
  // Paint from cache immediately so the popup opens without waiting.
  if (all) {
    const q = filter.toLowerCase();
    items = (all || []).filter((s) => s.name.toLowerCase().startsWith(q));
    activeIdx = 0;
    paintSkill();
  }
}

// acceptSkill attaches the row's skill and rewrites the composer to
// `/skill <name> ` so the message follows the reference.
export function acceptSkill(idx: number): boolean {
  if (!open) {
    return false;
  }
  if (items.length === 0) {
    return false;
  }
  const s = items[Math.min(Math.max(idx, 0), items.length - 1)];
  attach(s.name);
  inputEl.value = `/skill ${s.name} `;
  inputEl.selectionStart = inputEl.value.length;
  inputEl.selectionEnd = inputEl.value.length;
  closeSkill();
  inputEl.focus();
  return true;
}

// handleSkillKey consumes popup navigation keys. Returns true when the
// composer must ignore the keystroke.
export function handleSkillKey(event: KeyboardEvent): boolean {
  if (!open) {
    return false;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeSkill();
    return true;
  }
  if (items.length === 0) {
    return false;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIdx = (activeIdx + 1) % items.length;
    paintSkill();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx - 1 + items.length) % items.length;
    paintSkill();
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    acceptSkill(activeIdx);
    return true;
  }
  return false;
}

// parseSkillLine splits `/skill <name> <message>` (first line only).
// Returns null when the text is not a skill invocation.
export function parseSkillLine(text: string): { name: string; message: string } | null {
  const line = text.trimStart().split("\n", 1)[0];
  const m = /^\/skill(?:\s+([a-zA-Z0-9_-]+))?(?:\s+([\s\S]*))?$/.exec(line);
  if (!m || !line.trimStart().toLowerCase().startsWith("/skill")) {
    return null;
  }
  const name = (m[1] || "").trim();
  const message = (m[2] || "").trim();
  if (!name) {
    return null;
  }
  return { name, message };
}

function ensureChip(): HTMLElement {
  if (chipEl) {
    return chipEl;
  }
  const el = document.createElement("div");
  el.id = "skillChip";
  el.className = "mb-1.5 hidden items-center gap-1.5";
  el.innerHTML = `<span class="skill-chip"><span class="skill-chip-dot"></span><span id="skillChipName" class="font-mono"></span><button id="skillChipX" class="skill-chip-x" title="Remove the skill">✕</button></span>`;
  const slot = document.getElementById("skillChipSlot");
  (slot ?? inputEl.parentElement)?.appendChild(el);
  el.querySelector("#skillChipX")?.addEventListener("click", () => detach());
  chipEl = el;
  return el;
}

// attach marks a skill for the pending turn and shows the chip.
export function attach(name: string): void {
  attached = name;
  const el = ensureChip();
  el.classList.remove("hidden");
  el.classList.add("flex");
  const label = el.querySelector("#skillChipName");
  if (label) {
    label.textContent = `/${name}`;
  }
}

// detach clears the pending skill and hides the chip.
export function detach(): void {
  attached = null;
  chipEl?.classList.add("hidden");
  chipEl?.classList.remove("flex");
}

// takeAttached hands the pending skill to the sender and clears it.
export function takeAttached(): string | null {
  const name = attached;
  detach();
  return name;
}

// loadSkills returns the cached skill listing, fetching it once.
export async function loadSkills(): Promise<SkillEntry[]> {
  if (!all) {
    try {
      all = await api.listSkills(state.cwd || null);
    } catch {
      return [];
    }
  }
  return all;
}

// skillsSnapshot is the cached listing without fetching.
export function skillsSnapshot(): SkillEntry[] {
  return all ?? [];
}

// refreshSkillsCache drops the cached listing so the next open
// re-fetches (new SKILL.md files show up).
export function refreshSkillsCache(): void {
  all = null;
}

function onInput(): void {
  // Any edit that no longer parses as a skill line drops the chip:
  // the turn would otherwise carry a skill the text does not name.
  if (attached) {
    const parsed = parseSkillLine(inputEl.value);
    if (!parsed || parsed.name !== attached) {
      detach();
    }
  }
  const caret = inputEl.selectionStart ?? inputEl.value.length;
  const prefix = skillPrefix(inputEl.value, caret);
  if (prefix === null) {
    closeSkill();
    return;
  }
  open = true;
  ensurePopup();
  scheduleRefresh(prefix);
}

// initSkills wires the composer listener. Key handling lives in
// chat.ts (ordering matters) via handleSkillKey.
export function initSkills(): void {
  inputEl.addEventListener("input", onInput);
  inputEl.addEventListener("click", onInput);
  inputEl.addEventListener("blur", () => {
    window.setTimeout(closeSkill, 120);
  });
}
