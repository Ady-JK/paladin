// slash.ts owns the `/` palette in the composer, styled after Codex:
// one card with Commands (run locally or as special turns), Prompts
// (prompt commands the sidecar expands: /init, /review, custom .md
// files) and Skills. Typing filters fuzzily; arrows move; Enter runs
// the row (or completes it when it needs arguments); Tab completes;
// Esc dismisses.

import { inputEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { icon, type IconName } from "../icons";
import { attach as attachSkill, loadSkills, skillsSnapshot } from "./skills";
import {
  type Args,
  SLASH_COMMANDS,
  PROMPT_ICONS,
  cachedPrompts,
  loadPrompts,
  promptLabel,
  promptArgs,
  setComposer,
} from "./slashCommands";

type Kind = "command" | "prompt" | "skill";

type Item = {
  kind: Kind;
  name: string;
  label: string;
  desc: string;
  icon: IconName;
  args: Args;
  chevron: boolean;
  tag: string;
};

let popup: HTMLElement | null = null;
let filtered: Item[] = [];
let activeIdx = 0;
let open = false;
let debounce = 0;
let runner: () => void = () => {};

// setSlashRunner lets the composer run a completed slash line (chat.ts
// owns send; importing it here would be a cycle).
export function setSlashRunner(run: () => void): void {
  runner = run;
}

function items(): Item[] {
  const out: Item[] = SLASH_COMMANDS.map((c) => ({
    kind: "command" as const,
    name: c.name,
    label: c.label,
    desc: c.desc,
    icon: c.icon,
    args: c.args,
    chevron: !!c.chevron,
    tag: c.hint,
  }));
  const local = new Set(out.map((i) => i.name));
  for (const c of cachedPrompts()) {
    if (local.has(c.name)) continue;
    out.push({
      kind: "prompt",
      name: c.name,
      label: promptLabel(c.name),
      desc: c.description || `Prompt from ${c.source}`,
      icon: PROMPT_ICONS[c.name] ?? "sl-prompt",
      args: promptArgs(c),
      chevron: false,
      tag: c.argumentHint ? `/${c.name} ${c.argumentHint}` : `/${c.name}`,
    });
  }
  for (const s of skillsSnapshot()) {
    out.push({
      kind: "skill",
      name: s.name,
      label: s.name,
      desc: s.description || `Skill from ${s.source}`,
      icon: "sl-skill",
      args: "optional",
      chevron: false,
      tag: s.source,
    });
  }
  return out;
}

// score ranks a row for the filter; -1 hides it. Name prefix beats a
// label word prefix, which beats a substring, which beats a fuzzy
// subsequence of the name.
function score(q: string, name: string, label: string): number {
  if (!q) return 0;
  const n = name.toLowerCase();
  const l = label.toLowerCase();
  if (n.startsWith(q)) return 0;
  if (l.split(/\s+/).some((w) => w.startsWith(q))) return 1;
  if (n.includes(q) || l.includes(q)) return 2;
  let at = 0;
  for (const ch of n) {
    if (ch === q[at]) at++;
    if (at === q.length) return 3;
  }
  return -1;
}

const GROUPS: [Kind, string][] = [
  ["command", "Commands"],
  ["prompt", "Prompts"],
  ["skill", "Skills"],
];

function filter(prefix: string): Item[] {
  const q = prefix.toLowerCase();
  const ranked = items()
    .map((it, i) => ({ it, i, s: score(q, it.name, it.label) }))
    .filter((r) => r.s >= 0);
  const out: Item[] = [];
  for (const [kind] of GROUPS) {
    out.push(
      ...ranked
        .filter((r) => r.it.kind === kind)
        .sort((a, b) => a.s - b.s || a.i - b.i)
        .map((r) => r.it),
    );
  }
  return out;
}

function ensurePopup(): HTMLElement {
  if (popup) {
    return popup;
  }
  const el = document.createElement("div");
  el.id = "slashPopup";
  el.className = "slash-popup hidden";
  el.setAttribute("role", "listbox");
  inputEl.parentElement?.appendChild(el);
  el.addEventListener("mousedown", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    if (row?.dataset.idx !== undefined) {
      event.preventDefault();
      activeIdx = Number(row.dataset.idx);
      runActive(true);
    }
  });
  el.addEventListener("mousemove", (event) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>("[data-idx]");
    const idx = Number(row?.dataset.idx ?? -1);
    if (idx >= 0 && idx !== activeIdx) {
      activeIdx = idx;
      paintActive();
    }
  });
  popup = el;
  return el;
}

// slashPrefix returns the command filter when the whole composer is
// a `/` prefix with the caret at the end.
function slashPrefix(value: string, caret: number): string | null {
  if (caret !== value.length || value.includes("\n")) {
    return null;
  }
  const m = /^\/([\w.-]*)$/.exec(value.trimStart());
  return m ? m[1] : null;
}

function closeSlash(): void {
  open = false;
  filtered = [];
  popup?.classList.add("hidden");
}

function rowHtml(it: Item, i: number): string {
  return (
    `<button type="button" role="option" data-idx="${i}" data-kind="${it.kind}" data-name="${escapeHtml(it.name)}" class="slash-row${i === activeIdx ? " is-active" : ""}">` +
    `<span class="slash-icon">${icon(it.icon, "h-4 w-4")}</span>` +
    `<span class="slash-text"><span class="slash-label">${escapeHtml(it.label)}</span>` +
    `<span class="slash-desc">${escapeHtml(it.desc)}</span></span>` +
    `<span class="slash-tag">${escapeHtml(it.kind === "skill" ? it.tag : `/${it.name}`)}</span>` +
    (it.chevron ? `<span class="slash-chev">${icon("rl-chevron", "h-3.5 w-3.5")}</span>` : "") +
    `</button>`
  );
}

function paintSlash(): void {
  const el = ensurePopup();
  if (!open) {
    el.classList.add("hidden");
    return;
  }
  el.classList.remove("hidden");
  if (filtered.length === 0) {
    el.innerHTML = `<div class="slash-empty">No matching commands</div>`;
    return;
  }
  let html = "";
  let group: Kind | "" = "";
  filtered.forEach((it, i) => {
    if (it.kind !== group) {
      group = it.kind;
      html += `<div class="slash-group">${GROUPS.find(([k]) => k === it.kind)?.[1] ?? ""}</div>`;
    }
    html += rowHtml(it, i);
  });
  el.innerHTML = html;
  paintActive();
}

function paintActive(): void {
  if (!popup) return;
  popup.querySelectorAll<HTMLElement>(".slash-row").forEach((row) => {
    const on = Number(row.dataset.idx) === activeIdx;
    row.classList.toggle("is-active", on);
    row.setAttribute("aria-selected", on ? "true" : "false");
    if (on) row.scrollIntoView({ block: "nearest" });
  });
}

function refreshSlash(prefix: string): void {
  filtered = filter(prefix);
  activeIdx = 0;
  paintSlash();
}

function currentPrefix(): string | null {
  return slashPrefix(inputEl.value, inputEl.selectionStart ?? inputEl.value.length);
}

// complete writes the row into the composer for its arguments.
function complete(it: Item): void {
  closeSlash();
  if (it.kind === "skill") {
    setComposer(`/skill ${it.name} `);
    attachSkill(it.name);
    return;
  }
  setComposer(`/${it.name} `);
}

// runActive runs (or completes, when arguments are required) the row.
function runActive(run: boolean): void {
  const it = filtered[Math.min(Math.max(activeIdx, 0), filtered.length - 1)];
  if (!it) {
    return;
  }
  if (!run || it.args === "required" || it.kind === "skill") {
    complete(it);
    return;
  }
  closeSlash();
  inputEl.value = `/${it.name}`;
  runner();
}

// handleSlashKey consumes popup navigation keys. Returns true when
// the composer must ignore the keystroke.
export function handleSlashKey(event: KeyboardEvent): boolean {
  if (!open) {
    return false;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeSlash();
    return true;
  }
  // The filter repaints on a debounce; act on what is typed now.
  const prefix = currentPrefix();
  if (prefix === null) {
    closeSlash();
    return false;
  }
  if (["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key)) {
    window.clearTimeout(debounce);
    const before = filtered.map((i) => i.name).join(",");
    const next = filter(prefix);
    if (next.map((i) => i.name).join(",") !== before) {
      filtered = next;
      activeIdx = 0;
      paintSlash();
    }
  }
  if (filtered.length === 0) {
    return false;
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    activeIdx = (activeIdx + 1) % filtered.length;
    paintActive();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    activeIdx = (activeIdx - 1 + filtered.length) % filtered.length;
    paintActive();
    return true;
  }
  if ((event.key === "Enter" && !event.shiftKey) || event.key === "Tab") {
    event.preventDefault();
    runActive(event.key === "Enter");
    return true;
  }
  return false;
}

function onInput(): void {
  const prefix = currentPrefix();
  if (prefix === null) {
    closeSlash();
    return;
  }
  const opening = !open;
  open = true;
  ensurePopup();
  if (opening) {
    // Paint from cache at once; refresh prompts and skills behind it.
    refreshSlash(prefix);
    void Promise.all([loadPrompts(), loadSkills()]).then(() => {
      const p = currentPrefix();
      if (open && p !== null) {
        const keep = filtered[activeIdx]?.name;
        filtered = filter(p);
        const at = filtered.findIndex((i) => i.name === keep);
        activeIdx = at >= 0 ? at : 0;
        paintSlash();
      }
    });
    return;
  }
  window.clearTimeout(debounce);
  debounce = window.setTimeout(() => refreshSlash(prefix), 40);
}

// initSlash wires the composer listener. Key handling lives in
// chat.ts (ordering matters) via handleSlashKey.
export function initSlash(): void {
  inputEl.addEventListener("input", onInput);
  inputEl.addEventListener("click", onInput);
  inputEl.addEventListener("blur", () => {
    window.setTimeout(closeSlash, 120);
  });
}
