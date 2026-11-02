// memory.ts is the settings Memory pane: what Paladin remembers, in
// three stores — about you (user), across every folder (global), and
// per folder (project). The model writes them through its memory tool;
// here the user reviews, edits, adds and removes entries. Sessions read
// a frozen snapshot at their first turn, so edits reach a running
// session only after "Refresh snapshot".

import * as api from "../core/api";
import { memoryBodyEl } from "../core/dom";
import { escapeHtml, shortPath } from "../core/format";
import { state } from "../core/state";
import type { MemoryConfigView, MemoryProject, MemorySnapshotState, MemoryTarget, MemoryView } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";

const BLURB: Record<MemoryTarget, [string, string]> = {
  user: ["About you", "Who you are and how you like to work — every session sees it."],
  global: ["Everywhere", "Conventions and facts that hold across all folders."],
  project: ["This folder", "What Paladin learned about this folder, shared by its sessions."],
};

type Section = { view: MemoryView; key: string };

let sections: Section[] = [];
let projects: MemoryProject[] = [];
let projectCwd = "";
let loading = 0;
let snapshot: MemorySnapshotState = { frozen: false, stale: false, changed: [] };
let config: MemoryConfigView | null = null;
// filter narrows the entries shown in every section, so a store with a
// hundred entries is browsable without leaving the pane.
let filter = "";

function keyOf(target: MemoryTarget, cwd: string): string {
  return target === "project" ? `project:${cwd}` : target;
}

// loadMemory fetches every store and repaints the pane.
export async function loadMemory(cwd?: string): Promise<void> {
  const mine = ++loading;
  if (cwd !== undefined) projectCwd = cwd;
  if (!projectCwd) projectCwd = state.cwd;
  memoryBodyEl.innerHTML = `<div class="mem-empty">Loading memory…</div>`;
  try {
    const targets: [MemoryTarget, string][] = [
      ["user", ""],
      ["global", ""],
    ];
    if (projectCwd) targets.push(["project", projectCwd]);
    const [views, list, snap, cfg] = await Promise.all([
      Promise.all(targets.map(([t, c]) => api.memoryGet(t, c || null))),
      api.memoryProjects().catch(() => [] as MemoryProject[]),
      state.sessionId
        ? api.memorySnapshotStatus(state.sessionId, state.cwd || null).catch(() => null)
        : Promise.resolve(null),
      api.memoryConfig().catch(() => null),
    ]);
    if (mine !== loading) return;
    sections = views.map((view, i) => ({ view, key: keyOf(targets[i][0], targets[i][1]) }));
    projects = list;
    snapshot = snap ?? { frozen: false, stale: false, changed: [] };
    config = cfg;
    paint();
  } catch (error) {
    if (mine !== loading) return;
    memoryBodyEl.innerHTML = `<div class="mem-empty mem-error">Could not load memory: ${escapeHtml(String(error))}</div>`;
  }
}

function usage(view: MemoryView): string {
  const pct = view.limit > 0 ? Math.min(100, Math.round((view.used / view.limit) * 100)) : 0;
  const tone = pct >= 90 ? " is-hot" : pct >= 70 ? " is-warm" : "";
  return (
    `<div class="mem-usage${tone}" data-mem-usage title="${pct}% of the store's budget">` +
    `<span class="mem-bar"><span style="width:${pct}%"></span></span>` +
    `<span>${view.used.toLocaleString()} / ${view.limit.toLocaleString()} chars</span></div>`
  );
}

// describeChanged names the stores that moved, for the staleness banner.
function describeChanged(changed: string[]): string {
  const names: Record<string, string> = { user: "About you", global: "Everywhere", project: "This folder" };
  const known = changed.map((c) => names[c] ?? c).filter((n, i, all) => all.indexOf(n) === i);
  const what = known.length ? known.join(", ") : "Memory";
  return `${what} changed outside this session`;
}

function sectionHtml(s: Section, idx: number): string {
  const v = s.view;
  const [title, blurb] = BLURB[v.target];
  const where =
    v.target === "project" && v.cwd
      ? `<span class="mem-cwd" title="${escapeHtml(v.cwd)}">${escapeHtml(shortPath(v.cwd))}</span>`
      : "";
  const needle = filter.trim().toLowerCase();
  // Keep the original index: edits and removals address the store, not
  // the filtered view.
  const visible = v.entries.map((e, i) => ({ e, i })).filter(({ e }) => !needle || e.toLowerCase().includes(needle));
  const rows = v.entries.length
    ? visible.length
      ? visible
          .map(
            ({ e, i }) =>
              `<li class="mem-entry" data-mem-entry="${i}">` +
              `<span class="mem-entry-text" data-mem-edit="${i}" title="Click to edit">${escapeHtml(e)}</span>` +
              `<button type="button" class="mem-x" data-mem-remove="${i}" title="Forget this" aria-label="Forget this entry">${icon("xmark", "h-3 w-3")}</button></li>`,
          )
          .join("")
      : `<li class="mem-none" data-mem-nomatch>No entry matches “${escapeHtml(filter.trim())}”.</li>`
    : `<li class="mem-none">Nothing remembered yet.</li>`;
  const moved = snapshot.stale && snapshot.changed.includes(v.target);
  const dot = moved ? `<span class="mem-dot" data-mem-moved title="Changed outside this session"></span>` : "";
  const off = v.enabled === false;
  const addRow = off
    ? `<div class="mem-off" data-mem-off>Switched off in memory.json — Paladin will not read or write this store.</div>`
    : `<input type="text" class="mem-add" data-mem-add placeholder="Add a memory and press Enter…" spellcheck="false" />`;
  return (
    `<section class="mem-section${off ? " is-off" : ""}" data-mem-target="${v.target}" data-mem-idx="${idx}">` +
    `<header class="mem-head"><div><div class="mem-title">${dot}${title}${where}</div><div class="mem-blurb">${blurb}</div></div>${usage(v)}</header>` +
    `<ul class="mem-list">${rows}</ul>` +
    addRow +
    `</section>`
  );
}

function paint(): void {
  const others = projects.filter((p) => p.cwd !== projectCwd);
  const folderPick = others.length
    ? `<div class="mem-folders"><span>Other folders</span>` +
      others
        .map(
          (p) =>
            `<button type="button" class="mem-folder" data-mem-folder="${escapeHtml(p.cwd)}" title="${escapeHtml(p.cwd)}">${icon("rl-folder", "h-3 w-3")}${escapeHtml(shortPath(p.cwd))}<em>${p.entries}</em></button>`,
        )
        .join("") +
      `</div>`
    : "";
  const refresh = state.sessionId
    ? `<button type="button" class="mem-refresh" data-mem-refresh title="The open session keeps the memory it started with; refresh so its next turn sees edits">${icon("repeat", "h-3 w-3")}Refresh this session's snapshot</button>`
    : "";
  const stale =
    snapshot.stale && state.sessionId
      ? `<div class="mem-stale" data-mem-stale>` +
        `<span>${icon("repeat", "h-3 w-3")}${escapeHtml(describeChanged(snapshot.changed))} — the open session still sees the memory it started with.</span>` +
        `<button type="button" class="mem-refresh" data-mem-refresh>Refresh snapshot</button></div>`
      : "";
  const provider =
    config && config.provider !== "file"
      ? ` <span class="mem-provider" title="Set in memory.json">provider: ${escapeHtml(config.provider)}</span>`
      : "";
  const total = sections.reduce((n, s) => n + s.view.entries.length, 0);
  const finder =
    total > 6
      ? `<input type="search" class="mem-filter" data-mem-filter value="${escapeHtml(filter)}" ` +
        `placeholder="Filter ${total} entries…" aria-label="Filter memory entries" spellcheck="false" />`
      : "";
  memoryBodyEl.innerHTML =
    `<div class="mem-intro"><p>Paladin saves durable facts here through its memory tool. Sessions read a snapshot when they start.${provider}</p>${refresh}</div>` +
    stale +
    finder +
    sections.map(sectionHtml).join("") +
    folderPick;
  // Repainting rebuilt the input, so restore focus and caret for the
  // keystroke that triggered it.
  if (filter && finder) {
    const input = memoryBodyEl.querySelector<HTMLInputElement>("[data-mem-filter]");
    input?.focus();
    input?.setSelectionRange(filter.length, filter.length);
  }
}

async function save(idx: number, entries: string[]): Promise<void> {
  const s = sections[idx];
  if (!s) return;
  const before = s.view;
  s.view = { ...before, entries };
  paint();
  try {
    s.view = await api.memorySet(before.target, before.cwd ?? null, entries);
  } catch (error) {
    s.view = before;
    toast(`Could not save memory: ${String(error)}`, "error");
  }
  paint();
}

function sectionIdx(el: HTMLElement): number {
  return Number(el.closest<HTMLElement>("[data-mem-idx]")?.dataset.memIdx ?? -1);
}

function beginEdit(span: HTMLElement): void {
  const idx = sectionIdx(span);
  const at = Number(span.dataset.memEdit);
  const s = sections[idx];
  if (!s) return;
  const input = document.createElement("textarea");
  input.className = "mem-edit";
  input.value = s.view.entries[at] ?? "";
  input.rows = Math.min(6, Math.max(1, Math.ceil(input.value.length / 70)));
  span.replaceWith(input);
  input.focus();
  let done = false;
  const commit = (keep: boolean) => {
    if (done) return;
    done = true;
    const text = input.value.trim();
    const entries = [...s.view.entries];
    if (!keep || text === entries[at]) {
      paint();
      return;
    }
    if (text) entries[at] = text;
    else entries.splice(at, 1);
    void save(idx, entries);
  };
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      commit(true);
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      commit(false);
    }
  });
  input.addEventListener("blur", () => commit(true));
}

export function initMemory(): void {
  memoryBodyEl.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    const remove = t.closest<HTMLElement>("[data-mem-remove]");
    if (remove) {
      const idx = sectionIdx(remove);
      const entries = [...(sections[idx]?.view.entries ?? [])];
      entries.splice(Number(remove.dataset.memRemove), 1);
      void save(idx, entries);
      return;
    }
    const edit = t.closest<HTMLElement>("[data-mem-edit]");
    if (edit) {
      beginEdit(edit);
      return;
    }
    const folder = t.closest<HTMLElement>("[data-mem-folder]");
    if (folder?.dataset.memFolder) {
      filter = "";
      void loadMemory(folder.dataset.memFolder);
      return;
    }
    if (t.closest("[data-mem-refresh]") && state.sessionId) {
      void api
        .memoryRefreshSnapshot(state.sessionId, state.cwd || null)
        .then(() => toast("The next turn will see the current memory"))
        .catch((error) => toast(`Refresh failed: ${String(error)}`, "error"));
    }
  });
  memoryBodyEl.addEventListener("input", (event) => {
    const t = event.target as HTMLElement;
    if (!(t instanceof HTMLInputElement) || !t.matches("[data-mem-filter]")) return;
    filter = t.value;
    paint();
  });
  memoryBodyEl.addEventListener("keydown", (event) => {
    const t = event.target as HTMLElement;
    if (t instanceof HTMLInputElement && t.matches("[data-mem-filter]")) {
      if (event.key === "Escape") {
        event.preventDefault();
        filter = "";
        paint();
      }
      return;
    }
    if (!(t instanceof HTMLInputElement) || !t.matches("[data-mem-add]") || event.key !== "Enter") return;
    event.preventDefault();
    const text = t.value.trim();
    if (!text) return;
    const idx = sectionIdx(t);
    const s = sections[idx];
    if (!s) return;
    if (s.view.entries.includes(text)) {
      toast("Already remembered");
      return;
    }
    void save(idx, [...s.view.entries, text]);
  });
}
