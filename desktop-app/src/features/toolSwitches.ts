// toolSwitches.ts owns tool on/off in the renderer: Settings → Tools
// (global switches), the composer's Tools popover (per-session switches,
// which beat global ones), and the "new tools" banner that asks before
// a session's tool list — and so its prompt cache — changes.

import * as api from "../core/api";
import { modelSelectBtn, settingsPaneToolsEl, toolsBodyEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { ToolSwitch, ToolsOverview } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";

type Choice = "inherit" | "on" | "off";

let overview: ToolsOverview | null = null;
let loadedFor = "";
let loading = 0;
// Session switches picked before the session exists; applied by
// applyToolDraft once the first message opens it.
const draft = new Map<string, boolean>();
// Sessions whose banner was put off, with the pending names at the time:
// a newer tool brings the banner back.
const later = new Map<string, Set<string>>();
let query = "";
let menuQuery = "";
let refreshTimer = 0;
let chip: HTMLButtonElement | null = null;
let menu: HTMLElement | null = null;
let pendingChip: HTMLButtonElement | null = null;
let banner: HTMLElement | null = null;
let openPane: () => void = () => {};

const keyNow = () => `${state.cwd}\u0000${state.sessionId}`;

// ---- loading ----

// loadTools fetches the switches for the working folder and session.
export async function loadTools(): Promise<void> {
  const mine = ++loading;
  const key = keyNow();
  try {
    const v = await api.toolsList(state.cwd || null, state.sessionId || null);
    if (mine !== loading) return;
    loadedFor = key;
    apply(v);
  } catch (error) {
    if (mine !== loading) return;
    overview = null;
    loadedFor = key;
    paintAll();
    if (!settingsPaneToolsEl.classList.contains("hidden")) {
      toolsBodyEl.innerHTML = `<div class="mem-empty mem-error">Could not load tools: ${escapeHtml(String(error))}</div>`;
    }
  }
}

// syncTools reloads after the visible session or folder changed.
export function syncTools(): void {
  if (keyNow() !== loadedFor || !overview) void loadTools();
  else paintAll();
}

function apply(v: ToolsOverview): void {
  overview = v;
  paintAll();
}

function paintAll(): void {
  paintPane();
  paintChip();
  paintBanner();
  if (menu && !menu.classList.contains("hidden")) paintMenu();
}

// shown is the tool list with unsaved draft switches folded in.
function shown(): ToolSwitch[] {
  const list = overview?.tools ?? [];
  if (state.sessionId || !draft.size) return list;
  return list.map((t) => {
    const s = draft.get(t.name);
    if (s === undefined) return t;
    return { ...t, session: s, enabled: s };
  });
}

// ---- grouping ----

type Group = { id: string; label: string; tools: ToolSwitch[] };

function groups(list: ToolSwitch[]): Group[] {
  const out: Group[] = [];
  const builtin = list.filter((t) => t.source !== "mcp");
  if (builtin.length) out.push({ id: "builtin", label: "Built-in", tools: builtin });
  const servers = new Map<string, ToolSwitch[]>();
  for (const t of list) {
    if (t.source !== "mcp") continue;
    const s = t.server ?? "mcp";
    servers.set(s, [...(servers.get(s) ?? []), t]);
  }
  for (const [server, tools] of servers) out.push({ id: `mcp:${server}`, label: server, tools });
  return out;
}

const shortName = (t: ToolSwitch) => (t.source === "mcp" && t.tool ? t.tool : t.name);

function badges(t: ToolSwitch): string {
  const b: string[] = [];
  if (t.readOnly) b.push(`<span class="tsw-badge is-ro">read-only</span>`);
  if (t.destructive) b.push(`<span class="tsw-badge is-risky">destructive</span>`);
  return b.join("");
}

function matches(t: ToolSwitch, q: string): boolean {
  if (!q) return true;
  const hay = `${t.name} ${t.tool ?? ""} ${t.server ?? ""} ${t.description ?? ""}`.toLowerCase();
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w));
}

const switchHtml = (on: boolean, attr: string, label: string) =>
  `<button type="button" class="mcp-switch" role="switch" ${attr} aria-checked="${on}" aria-label="${escapeHtml(label)}" title="${escapeHtml(label)}"><span></span></button>`;

// ---- Settings → Tools ----

function paintPane(): void {
  if (!toolsBodyEl.querySelector(".tsw-pane")) {
    toolsBodyEl.innerHTML =
      `<div class="tsw-pane">` +
      `<div class="mem-intro"><span>Switch tools off everywhere. A session can switch them back on (or off) from the composer's Tools menu; its switch wins.</span>` +
      `<button type="button" class="mem-refresh" data-tsw-refresh>${icon("repeat", "h-3.5 w-3.5")}Refresh</button></div>` +
      `<label class="tsw-search">${icon("search", "h-3.5 w-3.5")}<input id="toolsSearch" type="search" placeholder="Filter tools" spellcheck="false" autocomplete="off" /></label>` +
      `<div class="tsw-groups"></div>` +
      `</div>`;
  }
  const host = toolsBodyEl.querySelector<HTMLElement>(".tsw-groups")!;
  if (!overview) {
    host.innerHTML = `<div class="mem-empty">Loading tools…</div>`;
    return;
  }
  host.innerHTML = groups(overview.tools)
    .map((g) => {
      const on = g.tools.filter((t) => t.global !== false).length;
      const all = on === g.tools.length;
      const head =
        `<div class="tsw-group-head">` +
        `<span class="tsw-group-ico">${icon(g.id === "builtin" ? "tl-tools" : "sl-plug", "h-3.5 w-3.5")}</span>` +
        `<span class="tsw-group-name">${escapeHtml(g.label)}</span>` +
        `<span class="tsw-group-count">${on} of ${g.tools.length} on</span>` +
        switchHtml(all, "data-tsw-group-toggle", `${all ? "Switch off" : "Switch on"} every ${g.label} tool`) +
        `</div>`;
      const rows = g.tools
        .map((t) => {
          const globalOn = t.global !== false;
          const sess =
            t.session !== undefined && state.sessionId
              ? `<span class="tsw-badge is-session" title="This session's switch wins">this session: ${t.session ? "on" : "off"}</span>`
              : "";
          return (
            `<li class="tsw-row" data-tool="${escapeHtml(t.name)}" data-enabled="${globalOn}" title="${escapeHtml(t.name)}">` +
            `<div class="tsw-text"><div class="tsw-line"><span class="tsw-name">${escapeHtml(shortName(t))}</span>${badges(t)}${sess}</div>` +
            (t.description ? `<div class="tsw-desc">${escapeHtml(t.description)}</div>` : "") +
            `</div>` +
            switchHtml(globalOn, "data-tsw-global", `${globalOn ? "Switch off" : "Switch on"} ${shortName(t)} everywhere`) +
            `</li>`
          );
        })
        .join("");
      return `<section class="tsw-group" data-group="${escapeHtml(g.id)}">${head}<ul class="tsw-list">${rows}</ul></section>`;
    })
    .join("") || `<div class="mem-none">No tools.</div>`;
  filterPane();
}

function filterPane(): void {
  toolsBodyEl.querySelectorAll<HTMLElement>(".tsw-group").forEach((g) => {
    let any = false;
    g.querySelectorAll<HTMLElement>(".tsw-row").forEach((row) => {
      const t = overview?.tools.find((x) => x.name === row.dataset.tool);
      const hit = !!t && matches(t, query);
      row.hidden = !hit;
      any ||= hit;
    });
    g.hidden = !any;
  });
}

async function setGlobal(names: string[], on: boolean): Promise<void> {
  try {
    apply(await api.toolsSet("global", state.cwd || null, state.sessionId || null, names, on ? null : false));
  } catch (error) {
    toast(`Could not switch tools: ${String(error)}`, "error");
  }
}

function onPaneClick(event: MouseEvent): void {
  const t = event.target as HTMLElement;
  if (t.closest("[data-tsw-refresh]")) {
    void loadTools();
    return;
  }
  const row = t.closest<HTMLElement>(".tsw-row");
  if (row && t.closest("[data-tsw-global]")) {
    void setGlobal([row.dataset.tool ?? ""], row.dataset.enabled !== "true");
    return;
  }
  const group = t.closest<HTMLElement>(".tsw-group");
  if (group && t.closest("[data-tsw-group-toggle]") && overview) {
    const g = groups(overview.tools).find((x) => x.id === group.dataset.group);
    if (!g) return;
    const all = g.tools.every((x) => x.global !== false);
    void setGlobal(
      g.tools.map((x) => x.name),
      !all,
    );
  }
}

// ---- composer: Tools popover ----

function ensureChip(): void {
  if (chip) return;
  const wrap = document.createElement("div");
  wrap.className = "tools-wrap";
  wrap.innerHTML =
    `<button type="button" id="toolsChip" class="perm-chip tools-chip" aria-haspopup="menu" aria-expanded="false"></button>` +
    `<button type="button" id="toolsPendingChip" class="tools-pending-chip" hidden></button>` +
    `<div id="toolsMenu" class="perm-menu tools-menu hidden" role="menu"></div>`;
  const anchor = document.querySelector(".perm-wrap") ?? modelSelectBtn.parentElement;
  anchor?.after(wrap);
  chip = wrap.querySelector<HTMLButtonElement>("#toolsChip")!;
  pendingChip = wrap.querySelector<HTMLButtonElement>("#toolsPendingChip")!;
  menu = wrap.querySelector<HTMLElement>("#toolsMenu")!;
  chip.addEventListener("click", (event) => {
    event.stopPropagation();
    if (menu!.classList.contains("hidden")) openToolsMenu();
    else closeMenu();
  });
  pendingChip.addEventListener("click", () => {
    if (state.sessionId) later.delete(state.sessionId);
    paintBanner();
    paintChip();
  });
  menu.addEventListener("click", onMenuClick);
  menu.addEventListener("input", (event) => {
    const t = event.target as HTMLInputElement;
    if (!t.matches(".tools-menu-search input")) return;
    menuQuery = t.value;
    filterMenu();
  });
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeMenu();
      chip?.focus();
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && menu && !menu.classList.contains("hidden")) closeMenu();
  });
  document.addEventListener("click", (event) => {
    if (menu && !menu.classList.contains("hidden") && !wrap.contains(event.target as Node)) closeMenu();
  });
}

function offCount(): number {
  return shown().filter((t) => !t.enabled).length;
}

function paintChip(): void {
  ensureChip();
  const off = offCount();
  const total = overview?.tools.length ?? 0;
  chip!.innerHTML = `${icon("tl-tools", "h-3.5 w-3.5")}<span>Tools</span>${off ? `<span class="tools-chip-count">${off}</span>` : ""}`;
  chip!.title = off ? `${off} of ${total} tools switched off for this session` : "Tools: switch them on or off for this session";
  chip!.dataset.off = String(off > 0);
  const pending = visiblePending();
  const deferred = !!state.sessionId && later.has(state.sessionId) && pending.length > 0;
  pendingChip!.hidden = !deferred;
  if (deferred) {
    pendingChip!.innerHTML = `${icon("sl-sparkles", "h-3 w-3")}<span>${pending.length} new</span>`;
    pendingChip!.title = `${pending.length} new ${pending.length === 1 ? "tool" : "tools"} waiting — click to review`;
  }
}

function choiceOf(t: ToolSwitch): Choice {
  return t.session === undefined ? "inherit" : t.session ? "on" : "off";
}

function paintMenu(): void {
  if (!menu) return;
  const list = shown();
  const search = menu.querySelector<HTMLInputElement>(".tools-menu-search input");
  const focused = !!search && document.activeElement === search;
  const scroll = menu.querySelector(".tools-menu-list")?.scrollTop ?? 0;
  const scope = state.sessionId ? "this session" : "the next session";
  const body = groups(list)
    .map((g) => {
      const items = g.tools
        .map((t) => {
          const c = choiceOf(t);
          const def = t.global === false ? "off" : "on";
          const seg = (v: Choice, label: string, title: string) =>
            `<button type="button" data-tsw-set="${v}" aria-pressed="${c === v}" title="${escapeHtml(title)}">${label}</button>`;
          return (
            `<div class="tsw-item" role="menuitem" data-tool="${escapeHtml(t.name)}" data-enabled="${t.enabled}" title="${escapeHtml(t.description ? `${t.name} — ${t.description}` : t.name)}">` +
            `<span class="tsw-item-dot" aria-hidden="true"></span>` +
            `<span class="tsw-item-name">${escapeHtml(shortName(t))}${t.pending ? `<span class="tsw-badge is-new">new</span>` : ""}</span>` +
            `<span class="tsw-seg" role="group" aria-label="${escapeHtml(shortName(t))} for ${scope}">` +
            seg("inherit", `Default`, `Follow the global switch (${def})`) +
            seg("on", "On", `On for ${scope}`) +
            seg("off", "Off", `Off for ${scope}`) +
            `</span></div>`
          );
        })
        .join("");
      return `<div class="tsw-menu-group" data-group="${escapeHtml(g.id)}"><div class="tsw-menu-group-head">${icon(g.id === "builtin" ? "tl-tools" : "sl-plug", "h-3 w-3")}${escapeHtml(g.label)}</div>${items}</div>`;
    })
    .join("");
  menu.innerHTML =
    `<div class="perm-menu-head tools-menu-head"><span>Tools for ${scope}</span>` +
    `<button type="button" class="tools-menu-settings" data-tsw-open-settings title="Global switches">${icon("rl-gear", "h-3 w-3")}Global</button></div>` +
    `<label class="tools-menu-search">${icon("search", "h-3 w-3")}<input type="search" placeholder="Filter" spellcheck="false" autocomplete="off" value="${escapeHtml(menuQuery)}" /></label>` +
    `<div class="tools-menu-list">${body || `<div class="tools-menu-empty">${overview ? "No tools." : "Loading tools…"}</div>`}</div>` +
    `<div class="tools-menu-foot">Changes apply from the next turn.</div>`;
  const list2 = menu.querySelector<HTMLElement>(".tools-menu-list");
  if (list2) list2.scrollTop = scroll;
  if (focused) menu.querySelector<HTMLInputElement>(".tools-menu-search input")?.focus();
  filterMenu();
}

function filterMenu(): void {
  if (!menu) return;
  const list = shown();
  menu.querySelectorAll<HTMLElement>(".tsw-menu-group").forEach((g) => {
    let any = false;
    g.querySelectorAll<HTMLElement>(".tsw-item").forEach((row) => {
      const t = list.find((x) => x.name === row.dataset.tool);
      const hit = !!t && matches(t, menuQuery);
      row.hidden = !hit;
      any ||= hit;
    });
    g.hidden = !any;
  });
}

// openToolsMenu shows the composer's Tools popover (also /tools).
export function openToolsMenu(): void {
  ensureChip();
  paintMenu();
  menu!.classList.remove("hidden");
  chip!.setAttribute("aria-expanded", "true");
  void loadTools();
}

function closeMenu(): void {
  menu?.classList.add("hidden");
  chip?.setAttribute("aria-expanded", "false");
}

async function setSession(name: string, choice: Choice): Promise<void> {
  const value = choice === "inherit" ? null : choice === "on";
  if (!state.sessionId) {
    if (value === null) draft.delete(name);
    else draft.set(name, value);
    paintAll();
    return;
  }
  try {
    apply(await api.toolsSet("session", state.cwd || null, state.sessionId, [name], value));
  } catch (error) {
    toast(`Could not switch ${name}: ${String(error)}`, "error");
  }
}

function onMenuClick(event: MouseEvent): void {
  event.stopPropagation();
  const t = event.target as HTMLElement;
  if (t.closest("[data-tsw-open-settings]")) {
    closeMenu();
    openPane();
    return;
  }
  const set = t.closest<HTMLElement>("[data-tsw-set]");
  const item = t.closest<HTMLElement>(".tsw-item");
  if (set && item?.dataset.tool) void setSession(item.dataset.tool, set.dataset.tswSet as Choice);
}

// applyToolDraft saves switches picked before the session existed.
export async function applyToolDraft(sessionId: string): Promise<void> {
  if (!draft.size) return;
  const byValue = new Map<boolean, string[]>();
  for (const [name, on] of draft) byValue.set(on, [...(byValue.get(on) ?? []), name]);
  draft.clear();
  try {
    let last: ToolsOverview | null = null;
    for (const [on, names] of byValue) last = await api.toolsSet("session", state.cwd || null, sessionId, names, on);
    if (last && state.sessionId === sessionId) {
      loadedFor = keyNow();
      apply(last);
    }
  } catch (error) {
    toast(`Could not save tool switches: ${String(error)}`, "error");
  }
}

// ---- the "new tools" banner ----

function visiblePending(): string[] {
  return state.sessionId && overview ? overview.pending : [];
}

function ensureBanner(): HTMLElement | null {
  if (banner) return banner;
  const wrap = document.querySelector("#agentView footer div.w-full");
  if (!wrap) return null;
  banner = document.createElement("div");
  banner.id = "toolsBanner";
  banner.className = "tools-banner";
  banner.setAttribute("role", "status");
  banner.hidden = true;
  wrap.prepend(banner);
  banner.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    if (t.closest("[data-tools-accept]")) void acceptPending();
    else if (t.closest("[data-tools-later]")) putOff();
    else if (t.closest("[data-tools-review]")) openToolsMenu();
  });
  return banner;
}

function paintBanner(): void {
  const el = ensureBanner();
  if (!el) return;
  const pending = visiblePending();
  const sid = state.sessionId;
  const put = sid ? later.get(sid) : undefined;
  const fresh = put ? pending.some((n) => !put.has(n)) : true;
  if (!pending.length || !fresh) {
    el.hidden = true;
    return;
  }
  if (put) later.delete(sid);
  const tools = overview?.tools.filter((t) => pending.includes(t.name)) ?? [];
  const servers = [...new Set(tools.map((t) => (t.source === "mcp" ? t.server ?? "MCP" : "built-in")))];
  const names = tools.slice(0, 4).map((t) => `<code>${escapeHtml(shortName(t))}</code>`).join(", ") + (tools.length > 4 ? ` +${tools.length - 4}` : "");
  const n = pending.length;
  el.innerHTML =
    `<span class="tools-banner-ico">${icon("sl-sparkles", "h-4 w-4")}</span>` +
    `<div class="tools-banner-text">` +
    `<div class="tools-banner-title">${n} new ${n === 1 ? "tool" : "tools"} from ${servers.map(escapeHtml).join(", ")}</div>` +
    `<div class="tools-banner-sub">${names}. Adding ${n === 1 ? "it" : "them"} changes this session's tool list, so the prompt cache rebuilds on the next turn.</div>` +
    `</div>` +
    `<div class="tools-banner-actions">` +
    `<button type="button" class="mcp-btn" data-tools-review>Review</button>` +
    `<button type="button" class="mcp-btn" data-tools-later>Later</button>` +
    `<button type="button" class="mcp-btn is-primary" data-tools-accept>Add now</button>` +
    `</div>`;
  el.hidden = false;
}

async function acceptPending(): Promise<void> {
  const sid = state.sessionId;
  const names = visiblePending();
  if (!sid || !names.length) return;
  try {
    const v = await api.toolsAccept(state.cwd || null, sid, names);
    later.delete(sid);
    if (state.sessionId === sid) apply(v);
    toast(`Added ${names.length} ${names.length === 1 ? "tool" : "tools"} — they apply from the next turn`);
  } catch (error) {
    toast(`Could not add the tools: ${String(error)}`, "error");
  }
}

function putOff(): void {
  if (!state.sessionId) return;
  later.set(state.sessionId, new Set(visiblePending()));
  if (banner) banner.hidden = true;
  paintChip();
}

// ---- wiring ----

// initToolSwitches wires the pane, the chip, the banner and the events;
// open shows Settings → Tools.
export function initToolSwitches(open: () => void): void {
  openPane = open;
  toolsBodyEl.addEventListener("click", onPaneClick);
  toolsBodyEl.addEventListener("input", (event) => {
    const t = event.target as HTMLInputElement;
    if (t.id !== "toolsSearch") return;
    query = t.value;
    filterPane();
  });
  api.onAgentEvent((ev) => {
    if (ev.event === "tools/pending" && ev.sessionId && ev.sessionId === state.sessionId) {
      void loadTools();
    } else if (ev.event === "mcp/status") {
      window.clearTimeout(refreshTimer);
      refreshTimer = window.setTimeout(() => void loadTools(), 400);
    }
  });
  ensureChip();
  ensureBanner();
  paintChip();
  void loadTools();
}
