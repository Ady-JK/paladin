// resources.ts polls CPU/RAM and paints it in four places: a badge on
// each busy session row, a header pill for the open session, the app
// total in the sidebar footer, and the Settings > Resources table.
//
// Cost control: one `resources` RPC (a cached `ps` in the core) plus one
// Electron metrics call per tick, ticks only while the window is
// visible, and rows are patched in place rather than re-rendered.

import * as api from "../core/api";
import { bridge } from "../core/tauri";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { ResourceSnapshot, ResourceUsage } from "../core/types";

const DEFAULT_INTERVAL = 3000;

let core: ResourceSnapshot | null = null;
let app: ResourceUsage | null = null;
let timer = 0;
let inFlight = false;
let stopped = false;

function interval(): number {
  const override = (window as unknown as { __paladinResInterval?: number }).__paladinResInterval;
  return typeof override === "number" && override > 0 ? override : DEFAULT_INTERVAL;
}

export function fmtCpu(pct: number): string {
  if (!Number.isFinite(pct) || pct <= 0) {
    return "0%";
  }
  return pct < 10 ? `${Number(pct.toFixed(1))}%` : `${Math.round(pct)}%`;
}

export function fmtBytes(bytes: number): string {
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
    const html = resBadge(id);
    const old = row.querySelector<HTMLElement>(".session-res");
    if (!html) {
      old?.remove();
      continue;
    }
    const tpl = document.createElement("template");
    tpl.innerHTML = html.trim();
    const next = tpl.content.firstElementChild as HTMLElement;
    if (old) {
      if (old.textContent !== next.textContent || old.title !== next.title) {
        old.textContent = next.textContent;
        old.title = next.title;
      }
    } else {
      row.insertBefore(next, row.querySelector(".session-count"));
    }
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

// paintPane fills Settings > Resources when it is open.
export function paintPane(): void {
  const body = document.getElementById("resourcesBody");
  if (!body || body.closest(".hidden")) {
    return;
  }
  if (!core?.supported) {
    body.innerHTML = `<p class="text-slate-400">Process usage is not available on this platform.</p>`;
    return;
  }
  const row = (key: string, name: string, sub: string, u: ResourceUsage) =>
    `<tr data-res-row="${escapeAttr(key)}">` +
    `<td class="res-name"><div class="truncate">${escapeHtml(name)}</div><div class="res-sub">${escapeHtml(sub)}</div></td>` +
    `<td class="res-num">${fmtCpu(u.cpu)}</td><td class="res-num">${fmtBytes(u.rss)}</td><td class="res-num">${u.procs}</td></tr>`;
  let rows = "";
  if (app) {
    rows += row("app", "App window", "Electron: UI, GPU and pages", app);
  }
  rows += row("core", "Core", "agent-host: every session's agent loop", core.self);
  const busy = Object.entries(core.sessions).filter(([, u]) => u.procs > 0);
  busy.sort((a, b) => b[1].cpu - a[1].cpu || b[1].rss - a[1].rss);
  for (const [id, u] of busy) {
    const title = state.sessions.find((s) => s.sessionId === id)?.title || id.slice(0, 8);
    rows += row(id, title, "shell commands of this session", u);
  }
  const t = total();
  if (t) {
    rows += row("total", "Total", "everything above", t);
  }
  body.innerHTML =
    `<table class="res-table"><thead><tr><th>Process</th><th>CPU</th><th>Memory</th><th>Procs</th></tr></thead><tbody>${rows}</tbody></table>` +
    (busy.length === 0 ? `<p class="mt-3 text-slate-400">No session is running commands right now.</p>` : "") +
    `<p class="mt-3 text-[10px] text-slate-400">CPU is a percentage of one core. Sampled every ${Math.round(interval() / 1000)}s while the window is visible.</p>`;
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
  if (stopped || document.visibilityState === "hidden") {
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

export function stopResources(): void {
  stopped = true;
  window.clearTimeout(timer);
}
