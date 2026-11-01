// resources.ts polls CPU/RAM and paints it in four places: a badge on
// each busy session row, a header pill for the open session, the app
// total in the sidebar footer, and the Settings > Resources table.
//
// Cost control: one `resources` RPC (a cached `ps` in the core) plus one
// Electron metrics call per tick, ticks only while the window is
// visible, and rows are patched in place rather than re-rendered.

import * as api from "../core/api";
import { bridge } from "../core/bridge";
import { escapeAttr } from "../core/format";
import { state } from "../core/state";
import type { ResourceSnapshot, ResourceUsage } from "../core/types";

const DEFAULT_INTERVAL = 3000;

let core: ResourceSnapshot | null = null;
let app: ResourceUsage | null = null;
let timer = 0;
let inFlight = false;

function interval(): number {
  const override = (window as unknown as { __paladinResInterval?: number }).__paladinResInterval;
  return typeof override === "number" && override > 0 ? override : DEFAULT_INTERVAL;
}

function fmtCpu(pct: number): string {
  if (!Number.isFinite(pct) || pct <= 0) {
    return "0%";
  }
  return pct < 10 ? `${Number(pct.toFixed(1))}%` : `${Math.round(pct)}%`;
}

function fmtBytes(bytes: number): string {
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) {
    return `${Number((mb / 1024).toFixed(1))} GB`;
  }
  return `${Math.round(mb)} MB`;
}

const fmt = (u: ResourceUsage): string => `${fmtCpu(u.cpu)} · ${fmtBytes(u.rss)}`;

function sessionUsage(id: string): ResourceUsage | null {
  if (!core?.supported) {
    return null;
  }
  const u = core.sessions[id];
  return u && u.procs > 0 ? u : null;
}

function total(): ResourceUsage | null {
  if (!core?.supported) {
    return null;
  }
  const sum: ResourceUsage = { cpu: core.self.cpu, rss: core.self.rss, procs: core.self.procs };
  for (const u of Object.values(core.sessions)) {
    sum.cpu += u.cpu;
    sum.rss += u.rss;
    sum.procs += u.procs;
  }
  if (app) {
    sum.cpu += app.cpu;
    sum.rss += app.rss;
    sum.procs += app.procs;
  }
  return sum;
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "es"}`;

// resBadge is the row badge HTML; empty for idle sessions.
export function resBadge(id: string): string {
  const u = sessionUsage(id);
  if (!u) {
    return "";
  }
  return `<span class="session-res" data-res-for="${escapeAttr(id)}" title="${escapeAttr(
    `CPU ${fmtCpu(u.cpu)} · memory ${fmtBytes(u.rss)} · ${plural(u.procs, "process")}`,
  )}">${fmt(u)}</span>`;
}

function paintRows(): void {
  for (const row of Array.from(document.querySelectorAll<HTMLElement>("#sessionList .session-row"))) {
    const id = row.querySelector<HTMLElement>("[data-session]")?.dataset.session;
    if (!id) {
      continue;
    }
    const u = sessionUsage(id);
    const old = row.querySelector<HTMLElement>(".session-res");
    if (!u) {
      old?.remove();
      continue;
    }
    const text = fmt(u);
    const title = `CPU ${fmtCpu(u.cpu)} · memory ${fmtBytes(u.rss)} · ${plural(u.procs, "process")}`;
    if (old) {
      // The 3s poll usually changes only the numbers; patch them
      // instead of rebuilding the badge (and its layout) every tick.
      if (old.textContent !== text) old.textContent = text;
      if (old.title !== title) old.title = title;
      continue;
    }
    const badge = document.createElement("span");
    badge.className = "session-res";
    badge.dataset.resFor = id;
    badge.title = title;
    badge.textContent = text;
    row.insertBefore(badge, row.querySelector(".session-count"));
  }
}

function paintPill(): void {
  const pill = document.getElementById("resPill");
  const label = document.getElementById("resPillLabel");
  if (!pill || !label) {
    return;
  }
  const show = !!core?.supported && !!state.sessionId;
  pill.classList.toggle("hidden", !show);
  pill.classList.toggle("sm:flex", show);
  if (!show) {
    return;
  }
  const u = sessionUsage(state.sessionId);
  label.textContent = u ? fmt(u) : "idle";
  pill.classList.toggle("is-busy", !!u);
  pill.title = u
    ? `This session's processes: ${plural(u.procs, "process")}, CPU ${fmtCpu(u.cpu)}, memory ${fmtBytes(u.rss)}`
    : "No processes running for this session";
}

function paintFooter(): void {
  const btn = document.getElementById("resTotal");
  const label = document.getElementById("resTotalLabel");
  if (!btn || !label) {
    return;
  }
  const t = total();
  btn.classList.toggle("hidden", !t);
  if (t) {
    label.textContent = fmt(t);
    btn.title = `paladin in total: CPU ${fmtCpu(t.cpu)}, memory ${fmtBytes(t.rss)} across ${plural(t.procs, "process")}`;
  }
}

type PaneRow = { key: string; name: string; sub: string; u: ResourceUsage };

// paneRows lists what Settings > Resources should show, busiest first.
function paneRows(): PaneRow[] {
  const rows: PaneRow[] = [];
  if (app) {
    rows.push({ key: "app", name: "App window", sub: "Electron: UI, GPU and pages", u: app });
  }
  if (!core) {
    return rows;
  }
  rows.push({ key: "core", name: "Core", sub: "agent-host: every session's agent loop", u: core.self });
  const busy = Object.entries(core.sessions).filter(([, u]) => u.procs > 0);
  busy.sort((a, b) => b[1].cpu - a[1].cpu || b[1].rss - a[1].rss);
  for (const [id, u] of busy) {
    const title = state.sessions.find((s) => s.sessionId === id)?.title || id.slice(0, 8);
    rows.push({ key: id, name: title, sub: "shell commands of this session", u });
  }
  const t = total();
  if (t) {
    rows.push({ key: "total", name: "Total", sub: "everything above", u: t });
  }
  return rows;
}

function paneRowEl(key: string): HTMLTableRowElement {
  const tr = document.createElement("tr");
  tr.dataset.resRow = key;
  const name = document.createElement("td");
  name.className = "res-name";
  const title = document.createElement("div");
  title.className = "truncate";
  const sub = document.createElement("div");
  sub.className = "res-sub";
  name.append(title, sub);
  tr.append(name);
  for (let i = 0; i < 3; i++) {
    const cell = document.createElement("td");
    cell.className = "res-num";
    tr.append(cell);
  }
  return tr;
}

function paintPaneRow(tr: HTMLTableRowElement, row: PaneRow): void {
  const name = tr.children[0] as HTMLTableCellElement;
  const title = name.firstElementChild as HTMLElement;
  const sub = name.lastElementChild as HTMLElement;
  if (title.textContent !== row.name) title.textContent = row.name;
  if (sub.textContent !== row.sub) sub.textContent = row.sub;
  const values = [fmtCpu(row.u.cpu), fmtBytes(row.u.rss), String(row.u.procs)];
  for (let i = 0; i < values.length; i++) {
    const cell = tr.children[i + 1] as HTMLTableCellElement;
    if (cell.textContent !== values[i]) cell.textContent = values[i];
  }
}

// paintPane updates Settings > Resources in place: rebuilding the table
// with innerHTML every 3s re-parsed the DOM and reset anything the user
// had selected.
function paintPane(): void {
  const body = document.getElementById("resourcesBody");
  if (!body || body.closest(".hidden")) {
    return;
  }
  if (!core?.supported) {
    body.innerHTML = `<p class="text-slate-400">Process usage is not available on this platform.</p>`;
    return;
  }
  let table = body.querySelector<HTMLTableElement>(".res-table");
  if (!table) {
    body.replaceChildren();
    table = document.createElement("table");
    table.className = "res-table";
    table.innerHTML = `<thead><tr><th>Process</th><th>CPU</th><th>Memory</th><th>Procs</th></tr></thead><tbody></tbody>`;
    body.append(table);
  }
  const tbody = table.tBodies[0];
  const rows = paneRows();
  const existing = new Map<string, HTMLTableRowElement>();
  for (const tr of Array.from(tbody.rows)) {
    if (tr.dataset.resRow) existing.set(tr.dataset.resRow, tr);
  }
  const keep = new Set<string>();
  for (const row of rows) {
    keep.add(row.key);
    let tr = existing.get(row.key);
    if (!tr) {
      tr = paneRowEl(row.key);
    }
    paintPaneRow(tr, row);
    tbody.append(tr); // moves it into the new order without a rebuild
  }
  for (const [key, tr] of existing) {
    if (!keep.has(key)) tr.remove();
  }
  const busy = rows.some((row) => row.key !== "app" && row.key !== "core" && row.key !== "total");
  let empty = body.querySelector<HTMLElement>("#resEmpty");
  if (busy) {
    empty?.remove();
  } else if (!empty) {
    empty = document.createElement("p");
    empty.id = "resEmpty";
    empty.className = "mt-3 text-slate-400";
    empty.textContent = "No session is running commands right now.";
    body.append(empty);
  }
  const note = `CPU is a percentage of one core. Sampled every ${Math.round(interval() / 1000)}s while the window is visible.`;
  let footnote = body.querySelector<HTMLElement>("#resNote");
  if (!footnote) {
    footnote = document.createElement("p");
    footnote.id = "resNote";
    footnote.className = "mt-3 text-[10px] text-slate-400";
    body.append(footnote);
  }
  if (footnote.textContent !== note) footnote.textContent = note;
}

// paintResources repaints every surface from the last sample.
export function paintResources(): void {
  paintRows();
  paintPill();
  paintFooter();
  paintPane();
}

async function sample(): Promise<void> {
  if (inFlight) {
    return;
  }
  inFlight = true;
  try {
    const metrics = bridge().metrics;
    const [snap, own] = await Promise.all([
      api.resources().catch(() => null),
      metrics ? metrics().catch(() => null) : Promise.resolve(null),
    ]);
    core = snap && typeof snap === "object" ? { ...snap, sessions: snap.sessions ?? {} } : null;
    app = own ?? null;
    paintResources();
  } finally {
    inFlight = false;
  }
}

function schedule(): void {
  window.clearTimeout(timer);
  if (document.visibilityState === "hidden") {
    return;
  }
  timer = window.setTimeout(() => {
    void sample().finally(schedule);
  }, interval());
}

// refreshResources samples now (opening the pane, switching sessions).
export function refreshResources(): void {
  void sample().finally(schedule);
}

export function initResources(): void {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      window.clearTimeout(timer);
    } else {
      refreshResources();
    }
  });
  refreshResources();
}
