// usage.ts owns the header usage pill and the session analytics
// dashboard: per-session context fill and spend from the
// `sessionUsage` sidecar method. It refreshes on session open and
// after every turn (tokens bill even on failed turns), and clears
// when there is no active session. Charts are hand-rolled SVG so the
// packaged app never needs a CDN dependency.

import * as api from "../core/api";
import {
  usageBodyEl,
  usageCloseBtnEl,
  usageLabelEl,
  usageModalEl,
  usageModalSessionEl,
  usagePillEl,
  usageTabModelsEl,
  usageTabOverviewEl,
} from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { DailyPoint, SessionUsage } from "../core/types";
import { icon } from "../icons";
import { closeModal, isModalOpen, openModal } from "../ui/anim";

// compactTokens renders 1500 as 1.5k and 2000000 as 2M.
export function compactTokens(n: number): string {
  if (n >= 1_000_000) {
    return `${trimNum(roundTo(n / 1_000_000, 1))}M`;
  }
  if (n >= 1000) {
    return `${trimNum(roundTo(n / 1000, 1))}k`;
  }
  return `${n}`;
}

// formatCost renders sub-cent spend with enough decimals to stay
// nonzero ($0.00033, not $0.00) and larger amounts with two.
export function formatCost(cost: number): string {
  if (cost < 0.01) {
    return `$${trimNum(roundTo(cost, 4))}`;
  }
  return `$${trimNum(roundTo(cost, 2))}`;
}

// formatUsd renders money with cents ($0.40, $2.00); sub-cent spend
// keeps formatCost's extra digits so it never reads $0.00.
export function formatUsd(n: number): string {
  const v = n || 0;
  return v > 0 && v < 0.01 ? formatCost(v) : `$${v.toFixed(2)}`;
}

export function roundTo(n: number, decimals: number): number {
  const factor = 10 ** decimals;
  return Math.round(n * factor) / factor;
}

export function trimNum(n: number): string {
  return `${n}`.replace(/\.0+$/, "").replace(/(\.\d*?)0+$/, "$1");
}

// cacheHitRate is cache-read share of total input: cache reads hit
// against the input tokens the calls consumed. Zero when nothing was
// read.
export function cacheHitRate(usage: { promptTokens: number; cacheReadTokens: number }): number {
  const denom = usage.promptTokens;
  if (denom <= 0) {
    return 0;
  }
  return (usage.cacheReadTokens / denom) * 100;
}

// pillText is the compact pill body. With a known window it leads with
// the fill percentage; without one it shows raw tokens instead of a
// misleading zero.
function pillText(usage: SessionUsage): string {
  const spend = usage.hasCost ? ` · ${formatCost(usage.cost)}` : "";
  if (usage.contextKnown && usage.fillPercent !== undefined) {
    return `${trimNum(roundTo(usage.fillPercent, 1))}%${spend}`;
  }
  return `${compactTokens(usage.totalTokens)} tok${spend}`;
}

// pillTitle is the hover breakdown: model, last-call input/window,
// spend, calls. It never shows a percentage for unknown windows.
function pillTitle(usage: SessionUsage): string {
  const parts = [usage.model || "unknown model"];
  const input = usage.lastInputTokens ?? usage.totalTokens;
  const tokens = `${input.toLocaleString()} input tokens`;
  if (usage.contextKnown) {
    const pct = usage.fillPercent !== undefined ? ` (${trimNum(roundTo(usage.fillPercent, 1))}%)` : "";
    parts.push(`${tokens} / ${usage.contextWindow.toLocaleString()}${pct}`);
  } else {
    parts.push(`${usage.totalTokens.toLocaleString()} tokens (window unknown)`);
  }
  if (usage.hasCost) {
    const spend =
      usage.listCost > 0 ? `${formatCost(usage.cost)} (list ${formatCost(usage.listCost)})` : formatCost(usage.cost);
    parts.push(`spent ${spend}`);
  } else {
    parts.push("spend unknown");
  }
  parts.push(`${usage.calls} call${usage.calls === 1 ? "" : "s"}`);
  return parts.join(" · ");
}

// renderUsage paints the pill from state. Callers fetch first via
// refreshUsage; direct calls only re-render already-fetched state.
function renderUsage(): void {
  const usage = state.usage;
  // The pill shares the session pill's responsive pattern (flex from sm
  // up), so hiding must drop sm:flex too or `hidden` loses to it.
  if (!usage || !state.sessionId) {
    usagePillEl.classList.add("hidden");
    usagePillEl.classList.remove("sm:flex");
    return;
  }
  usagePillEl.classList.remove("hidden");
  usagePillEl.classList.add("sm:flex");
  usageLabelEl.textContent = pillText(usage);
  usagePillEl.title = pillTitle(usage);
  usagePillEl.classList.toggle("text-agent-amber", knownFillAtLeast(usage, 70));
  usagePillEl.classList.toggle("text-agent-rose", knownFillAtLeast(usage, 90));
}

function knownFillAtLeast(usage: SessionUsage, pct: number): boolean {
  return usage.contextKnown && usage.fillPercent !== undefined && usage.fillPercent >= pct;
}

// refreshUsage fetches the active session's usage and repaints. Failures
// keep the last reading (a transient sidecar hiccup must not blink the
// pill away mid-session).
export async function refreshUsage(): Promise<void> {
  if (!state.sessionId) {
    clearUsage();
    return;
  }
  try {
    state.usage = await api.sessionUsage(state.sessionId, null);
  } catch (error) {
    console.warn(`refreshUsage failed: ${String(error)}`);
    return;
  }
  renderUsage();
  if (isModalOpen(usageModalEl)) {
    renderDashboard();
  }
}

// clearUsage drops the reading and hides the pill, e.g. on new session.
export function clearUsage(): void {
  state.usage = null;
  renderUsage();
  if (isModalOpen(usageModalEl)) {
    renderDashboard();
  }
}

// formatPct renders a fill percentage or an em dash for unknown windows.
function formatPct(pct: number | undefined): string {
  if (pct === undefined) {
    return "—";
  }
  return `${trimNum(roundTo(pct, 1))}%`;
}

type UsageTab = "overview" | "models";

let activeTab: UsageTab = "overview";

// openUsageDashboard shows the analytics card for the active session,
// fetching first when there is nothing cached yet.
export async function openUsageDashboard(): Promise<void> {
  if (!state.sessionId) {
    return;
  }
  if (!state.usage) {
    await refreshUsage();
  }
  renderDashboard();
  openModal(usageModalEl);
}

// closeUsageDashboard hides the analytics card.
export function closeUsageDashboard(): void {
  closeModal(usageModalEl);
}

function setTab(tab: UsageTab): void {
  activeTab = tab;
  paintTabs();
  renderDashboard();
}

function paintTabs(): void {
  const on = "bg-slate-200 text-slate-900 dark:bg-slate-700 dark:text-slate-100";
  for (const [el, name] of [
    [usageTabOverviewEl, "overview"],
    [usageTabModelsEl, "models"],
  ] as const) {
    el.classList.remove("bg-slate-200", "text-slate-900", "dark:bg-slate-700", "dark:text-slate-100");
    el.classList.add("text-slate-500", "dark:text-slate-400");
    if (name === activeTab) {
      el.classList.remove("text-slate-500", "dark:text-slate-400");
      el.classList.add(...on.split(" "));
    }
  }
}

// renderDashboard paints the open card from state. With no reading it
// shows an empty state instead of stale numbers.
function renderDashboard(): void {
  paintTabs();
  const usage = state.usage;
  usageModalSessionEl.textContent = state.sessionId
    ? `${state.sessionId.slice(0, 8)}${usage?.model ? ` · ${usage.model}` : ""}`
    : "";
  if (!usage) {
    usageBodyEl.innerHTML = `<p class="py-10 text-center text-sm text-slate-400">No billed usage yet — send a message first.</p>`;
    return;
  }
  usageBodyEl.innerHTML = activeTab === "models" ? modelsHtml(usage) : overviewHtml(usage);
}

// Neon palette shared with the fleet dashboard.
export const NEON = {
  blue: "#3b82f6",
  purple: "#a855f7",
  rose: "#f43f5e",
  emerald: "#10b981",
  amber: "#f59e0b",
  cyan: "#06b6d4",
};

function heroCard(opts: {
  label: string;
  value: string;
  sub: string;
  iconName: "dollar-sign" | "microchip" | "terminal" | "zap" | "chart" | "layers";
  iconBox: string;
  glow: string;
}): string {
  return `<div class="glass-panel glow-${opts.glow} relative overflow-hidden rounded-2xl p-5 transition-all">
    <div class="absolute -bottom-6 -right-6 h-24 w-24 rounded-full bg-white/5 blur-2xl"></div>
    <div class="mb-3 flex items-start justify-between">
      <div>
        <div class="text-xs font-medium uppercase tracking-wider text-slate-400">${opts.label}</div>
        <div class="mt-1 font-mono text-2xl font-bold text-slate-900 dark:text-white">${escapeHtml(opts.value)}</div>
      </div>
      <div class="rounded-xl border p-2.5 ${opts.iconBox}">${icon(opts.iconName, "h-5 w-5")}</div>
    </div>
    <div class="break-words font-mono text-xs text-slate-500 dark:text-slate-400">${opts.sub}</div>
  </div>`;
}

function tokenBanner(usage: SessionUsage): string {
  const total = usage.totalTokens || 1;
  const share = (n: number): string => `${trimNum(roundTo((n / total) * 100, 1))}%`;
  const cell = (dot: string, label: string, value: string, sub: string, highlight = ""): string =>
    `<div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
      <div class="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400">
        <span class="h-2 w-2 rounded-full" style="background:${dot}"></span>${label}
      </div>
      <div class="mt-1 font-mono text-base font-bold text-slate-900 dark:text-slate-100 ${highlight}">${value}</div>
      <div class="mt-0.5 font-mono text-[10px] text-slate-400">${sub}</div>
    </div>`;
  return `<div class="glass-panel grid grid-cols-2 gap-3 rounded-2xl p-4 font-mono sm:grid-cols-3 lg:grid-cols-5">
    ${cell(NEON.blue, "Input tokens", usage.promptTokens.toLocaleString(), `${share(usage.promptTokens)} of total`)}
    ${cell(NEON.purple, "Output tokens", usage.completionTokens.toLocaleString(), `${share(usage.completionTokens)} of total`)}
    ${cell(NEON.rose, "Reasoning tokens", usage.reasoningTokens.toLocaleString(), usage.reasoningTokens > 0 ? `${share(usage.reasoningTokens)} · thinking models` : "no thinking billed")}
    ${cell(NEON.emerald, "Cache read tokens", usage.cacheReadTokens.toLocaleString(), usage.cacheReadTokens > 0 ? `${share(usage.cacheReadTokens)} · 90% off` : "no cache hits", usage.cacheReadTokens > 0 ? "text-emerald-500" : "")}
    ${cell(NEON.amber, "Cache write tokens", usage.cacheWriteTokens.toLocaleString(), usage.cacheWriteTokens > 0 ? `${share(usage.cacheWriteTokens)} · 25% surcharge` : "no cache writes")}
  </div>`;
}

export function donut(
  segments: Array<{ label: string; value: number; color: string }>,
  centerTop: string,
  centerSub: string,
  size = 132,
): string {
  const thickness = 20;
  const r = (size - thickness) / 2;
  const c = size / 2;
  const circ = 2 * Math.PI * r;
  const total = segments.reduce((n, s) => n + s.value, 0);
  let offset = 0;
  const rings = segments
    .filter((s) => s.value > 0)
    .map((s) => {
      const len = total > 0 ? (s.value / total) * circ : 0;
      const el = `<circle cx="${c}" cy="${c}" r="${r}" fill="none" stroke="${s.color}" stroke-width="${thickness}" stroke-dasharray="${len.toFixed(2)} ${circ.toFixed(2)}" stroke-dashoffset="${(-offset).toFixed(2)}"/>`;
      offset += len;
      return el;
    })
    .join("");
  return `<svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" role="img">
    <circle cx="${c}" cy="${c}" r="${r}" fill="none" class="stroke-slate-200 dark:stroke-slate-800" stroke-width="${thickness}"/>
    <g transform="rotate(-90 ${c} ${c})">${rings}</g>
    <text x="${c}" y="${c - 2}" text-anchor="middle" class="fill-slate-900 dark:fill-slate-100" font-size="18" font-weight="700">${escapeHtml(centerTop)}</text>
    <text x="${c}" y="${c + 16}" text-anchor="middle" class="fill-slate-400" font-size="10">${escapeHtml(centerSub)}</text>
  </svg>`;
}

function donutCard(
  title: string,
  subtitle: string,
  segments: Array<{ label: string; value: number; color: string }>,
  centerTop: string,
  centerSub: string,
  formatValue: (n: number) => string,
): string {
  const legend = segments
    .map(
      (s) =>
        `<span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${s.color}"></span><span class="truncate">${escapeHtml(s.label)}</span><span class="font-semibold text-slate-600 dark:text-slate-300">${formatValue(s.value)}</span></span>`,
    )
    .join("");
  return `<div class="glass-panel flex flex-1 items-center gap-4 rounded-2xl p-4">
    <div class="flex-shrink-0">${donut(segments, centerTop, centerSub)}</div>
    <div class="min-w-0">
      <div class="text-[11px] font-medium uppercase tracking-wider text-slate-400">${title}</div>
      <div class="mb-2 font-mono text-[10px] text-slate-500">${escapeHtml(subtitle)}</div>
      <div class="flex flex-col gap-1 font-mono text-[11px] text-slate-500 dark:text-slate-400">${legend}</div>
    </div>
  </div>`;
}

function tokenMixCard(usage: SessionUsage): string {
  return donutCard(
    "Token mix",
    "input vs output vs reasoning",
    [
      { label: "Input", value: usage.promptTokens, color: NEON.blue },
      { label: "Output", value: usage.completionTokens, color: NEON.emerald },
      { label: "Reasoning", value: usage.reasoningTokens, color: NEON.purple },
    ],
    compactTokens(usage.totalTokens),
    "tokens",
    (n) => n.toLocaleString(),
  );
}

function spendMixCard(usage: SessionUsage): string | null {
  const models = (usage.models ?? []).filter((m) => m.hasCost && m.cost > 0);
  if (!usage.hasCost || models.length === 0) {
    return null;
  }
  const palette = [NEON.amber, NEON.rose, NEON.blue, NEON.emerald, NEON.purple];
  const top = models.slice(0, 4);
  const restCost = models.slice(4).reduce((n, m) => n + m.cost, 0);
  const segments = top.map((m, i) => ({ label: m.model, value: m.cost, color: palette[i % palette.length] }));
  if (restCost > 0) {
    segments.push({ label: `${models.length - 4} more`, value: restCost, color: "#64748b" });
  }
  return donutCard("Spend by model", "gateway-reported cost", segments, formatCost(usage.cost), "spent", formatCost);
}

// areaChart renders a hand-rolled SVG time series with a gradient
// fill. Points with zero span (0-1 entries) return "" so callers show
// richer cards instead of a flat line.
export function areaChart(daily: DailyPoint[], metric: "tokens" | "cost", id: string): string {
  const pts = daily.slice(-30);
  if (pts.length < 2) {
    return "";
  }
  const values = pts.map((d) => (metric === "cost" ? d.cost : d.totalTokens));
  const max = Math.max(...values, 1e-9);
  const w = 560;
  const h = 180;
  const pad = 8;
  const x = (i: number): number => pad + (i / (pts.length - 1)) * (w - pad * 2);
  const y = (v: number): number => h - pad - (v / max) * (h - pad * 2 - 18);
  const line = pts
    .map(
      (d, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(metric === "cost" ? d.cost : d.totalTokens).toFixed(1)}`,
    )
    .join(" ");
  const fill = `${line} L${x(pts.length - 1).toFixed(1)},${h - pad} L${x(0).toFixed(1)},${h - pad} Z`;
  const dots = pts
    .map((d, i) => {
      const v = metric === "cost" ? d.cost : d.totalTokens;
      const tip = `${d.date}: ${metric === "cost" ? formatCost(v) : `${v.toLocaleString()} tok`} · ${d.calls} calls`;
      return `<circle cx="${x(i).toFixed(1)}" cy="${y(v).toFixed(1)}" r="3.5" fill="#3b82f6" stroke="#090d16" stroke-width="1.5"><title>${escapeHtml(tip)}</title></circle>`;
    })
    .join("");
  const first = pts[0].date.slice(5);
  const last = pts[pts.length - 1].date.slice(5);
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("chart", "h-4 w-4 text-accent-400")} Daily ${metric === "cost" ? "spend" : "tokens"}</div>
    <div class="mb-3 font-mono text-xs text-slate-400">${escapeHtml(first)} → ${escapeHtml(last)} · ${pts.length} days with billed calls</div>
    <svg viewBox="0 0 ${w} ${h}" class="h-44 w-full" role="img" aria-label="daily ${metric} chart">
      <defs>
        <linearGradient id="${id}-g" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#3b82f6" stop-opacity="0.35"/>
          <stop offset="100%" stop-color="#3b82f6" stop-opacity="0"/>
        </linearGradient>
      </defs>
      ${[0.25, 0.5, 0.75].map((f) => `<line x1="${pad}" y1="${(pad + f * (h - pad * 2)).toFixed(1)}" x2="${w - pad}" y2="${(pad + f * (h - pad * 2)).toFixed(1)}" class="stroke-slate-200 dark:stroke-slate-800" stroke-width="1"/>`).join("")}
      <path d="${fill}" fill="url(#${id}-g)"/>
      <path d="${line}" fill="none" stroke="#3b82f6" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>
      ${dots}
    </svg>
  </div>`;
}

// heatmap renders a GitHub-style run-activity grid for the trailing
// days present in the ledger. Empty ledgers return "".
export function heatmap(daily: DailyPoint[], id: string): string {
  if (daily.length === 0) {
    return "";
  }
  const byDate = new Map(daily.map((d) => [d.date, d]));
  const today = new Date();
  const days: DailyPoint[] = [];
  for (let i = 27; i >= 0; i--) {
    const dt = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() - i));
    const key = dt.toISOString().slice(0, 10);
    days.push(
      byDate.get(key) ?? {
        date: key,
        calls: 0,
        promptTokens: 0,
        completionTokens: 0,
        reasoningTokens: 0,
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        totalTokens: 0,
        cost: 0,
        listCost: 0,
        hasCost: false,
      },
    );
  }
  const max = Math.max(...days.map((d) => d.totalTokens), 1);
  const cells = days
    .map((d) => {
      const f = d.totalTokens / max;
      const bg = f <= 0 ? "bg-slate-800" : f < 0.3 ? "bg-accent-600/40" : f < 0.65 ? "bg-accent-500" : "bg-cyan-400";
      const tip = `${d.date}: ${d.totalTokens.toLocaleString()} tok · ${d.calls} calls`;
      void id;
      return `<div class="heatmap-cell ${bg}" title="${escapeHtml(tip)}"></div>`;
    })
    .join("");
  const busiest = days.reduce((a, b) => (b.totalTokens > a.totalTokens ? b : a), days[0]);
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("zap", "h-4 w-4 text-amber-400")} Run heatmap</div>
      <span class="font-mono text-[10px] text-slate-400">past 28 days</span>
    </div>
    <div class="mb-3 font-mono text-xs text-slate-400">busiest: ${escapeHtml(busiest.date)} · ${busiest.totalTokens.toLocaleString()} tok</div>
    <div class="grid grid-cols-7 gap-2">${cells}</div>
    <div class="mt-3 flex items-center justify-between font-mono text-[10px] text-slate-500">
      <span>Less</span>
      <span class="flex items-center gap-1"><span class="h-2.5 w-2.5 rounded-sm bg-slate-800"></span><span class="h-2.5 w-2.5 rounded-sm bg-accent-600/40"></span><span class="h-2.5 w-2.5 rounded-sm bg-accent-500"></span><span class="h-2.5 w-2.5 rounded-sm bg-cyan-400"></span></span>
      <span>More</span>
    </div>
  </div>`;
}

function modelShareBars(usage: SessionUsage): string {
  const models = usage.models ?? [];
  if (models.length < 2) {
    return "";
  }
  const total = usage.totalTokens || 1;
  const palette = [NEON.blue, NEON.emerald, NEON.amber, NEON.purple, NEON.rose];
  const rows = models
    .slice(0, 5)
    .map((m, i) => {
      const pct = (m.totalTokens / total) * 100;
      const color = palette[i % palette.length];
      return `<div>
      <div class="mb-1 flex items-baseline justify-between gap-2 font-mono text-[11px]">
        <span class="truncate font-medium text-slate-700 dark:text-slate-300">${escapeHtml(m.model)}</span>
        <span class="flex-shrink-0 text-slate-400">${compactTokens(m.totalTokens)} · ${pct.toFixed(1)}%</span>
      </div>
      <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800">
        <div class="h-full rounded-full" style="width:${pct.toFixed(2)}%;background:${color}"></div>
      </div>
    </div>`;
    })
    .join("");
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-3 text-[11px] font-medium uppercase tracking-wider text-slate-400">Share by model</div>
    <div class="space-y-2.5">${rows}</div>
  </div>`;
}

// stackedComposition shows per-model token-category splits as
// horizontal stacked bars (input/output/reasoning/cache-read).
export function stackedComposition(
  models: Array<{
    model: string;
    promptTokens: number;
    completionTokens: number;
    reasoningTokens: number;
    cacheReadTokens: number;
  }>,
  title = "Token composition by model",
): string {
  if (models.length === 0) {
    return "";
  }
  const rows = models
    .slice(0, 5)
    .map((m) => {
      const total = m.promptTokens + m.completionTokens + m.reasoningTokens + m.cacheReadTokens || 1;
      const seg = (v: number, color: string, label: string): string =>
        v > 0
          ? `<div class="h-full" style="width:${((v / total) * 100).toFixed(2)}%;background:${color}" title="${label}: ${v.toLocaleString()}"></div>`
          : "";
      return `<div>
      <div class="mb-1 flex items-baseline justify-between gap-2 font-mono text-[11px]">
        <span class="truncate font-medium text-slate-700 dark:text-slate-300">${escapeHtml(m.model)}</span>
        <span class="flex-shrink-0 text-slate-400">${(m.promptTokens + m.completionTokens + m.reasoningTokens + m.cacheReadTokens).toLocaleString()} tok</span>
      </div>
      <div class="flex h-2.5 w-full gap-px overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800">${seg(m.promptTokens, NEON.blue, "Input")}${seg(m.completionTokens, NEON.purple, "Output")}${seg(m.reasoningTokens, NEON.rose, "Reasoning")}${seg(m.cacheReadTokens, NEON.emerald, "Cache read")}</div>
    </div>`;
    })
    .join("");
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 text-sm font-bold text-slate-900 dark:text-white">${escapeHtml(title)}</div>
    <div class="mb-3 flex flex-wrap gap-x-4 gap-y-1 font-mono text-[10px] text-slate-400">
      <span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${NEON.blue}"></span>Input</span>
      <span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${NEON.purple}"></span>Output</span>
      <span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${NEON.rose}"></span>Reasoning</span>
      <span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${NEON.emerald}"></span>Cache read tokens</span>
    </div>
    <div class="space-y-2.5">${rows}</div>
  </div>`;
}

function overviewHtml(usage: SessionUsage): string {
  const avgCost = usage.calls > 0 && usage.hasCost ? usage.cost / usage.calls : 0;
  const hit = cacheHitRate(usage);
  const saved = usage.hasCost && usage.listCost > usage.cost ? usage.listCost - usage.cost : 0;
  const heroes = [
    heroCard({
      label: "Est. total cost",
      value: usage.hasCost ? formatCost(usage.cost) : "—",
      sub:
        saved > 0
          ? `saved ${formatCost(saved)} vs list`
          : usage.hasCost
            ? `avg ${formatCost(avgCost)}/run`
            : "spend not reported",
      iconName: "dollar-sign",
      iconBox: "bg-accent-500/10 text-accent-400 border border-accent-500/20",
      glow: "blue",
    }),
    heroCard({
      label: "Total tokens",
      value: compactTokens(usage.totalTokens),
      sub: `${usage.totalTokens.toLocaleString()} · ${usage.calls} call${usage.calls === 1 ? "" : "s"}`,
      iconName: "microchip",
      iconBox: "bg-purple-500/10 text-purple-400 border border-purple-500/20",
      glow: "purple",
    }),
    heroCard({
      label: "Context fill",
      value: formatPct(usage.fillPercent),
      sub: usage.contextKnown
        ? `of ${usage.contextWindow.toLocaleString()} · last call ${compactTokens(usage.lastInputTokens ?? 0)} in`
        : "window size not listed for this model",
      iconName: "layers",
      iconBox: "bg-cyan-500/10 text-cyan-400 border border-cyan-500/20",
      glow: "emerald",
    }),
    heroCard({
      label: "Prompt cache hit",
      value: `${trimNum(roundTo(hit, 1))}%`,
      sub:
        usage.cacheReadTokens > 0
          ? `${usage.cacheReadTokens.toLocaleString()} read at 90% off`
          : "no cache hits recorded",
      iconName: "zap",
      iconBox: "bg-amber-500/10 text-amber-400 border border-amber-500/20",
      glow: "amber",
    }),
  ].join("");
  const spend = spendMixCard(usage);
  const charts = spend ? `${tokenMixCard(usage)}${spend}` : tokenMixCard(usage);
  const timeline = areaChart(usage.daily ?? [], "tokens", "sess-timeline");
  const heat = heatmap(usage.daily ?? [], "sess-heat");
  return `<div class="space-y-5">
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">${heroes}</div>
    ${tokenBanner(usage)}
    ${timeline}
    <div class="flex flex-col gap-4 sm:flex-row">${charts}</div>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">${modelShareBars(usage)}${stackedComposition(usage.models ?? [])}</div>
    ${heat}
  </div>`;
}

function modelsHtml(usage: SessionUsage): string {
  const models = usage.models ?? [];
  if (models.length === 0) {
    return `<p class="py-10 text-center text-sm text-slate-400">No per-model breakdown recorded for this session.</p>`;
  }
  const rows = models
    .map((m) => {
      const bar =
        m.contextKnown && m.fillPercent !== undefined
          ? `<div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800"><div class="h-full rounded-full" style="width:${Math.min(100, m.fillPercent).toFixed(2)}%;background:linear-gradient(90deg,#3b82f6,#a855f7)"></div></div>`
          : "";
      const spend = m.hasCost ? formatCost(m.cost) : "—";
      const hit = cacheHitRate(m);
      return `<div class="glass-panel rounded-2xl p-4">
        <div class="flex items-baseline justify-between gap-3">
          <div class="truncate text-sm font-semibold text-slate-900 dark:text-slate-100">${escapeHtml(m.model)}</div>
          <div class="flex-shrink-0 font-mono text-[11px] text-slate-400">${m.calls} call${m.calls === 1 ? "" : "s"} · ${spend}</div>
        </div>
        <div class="mt-1 font-mono text-[11px] text-slate-400">${m.contextKnown ? `window ${m.contextWindow.toLocaleString()} · fill ${formatPct(m.fillPercent)}` : "window size not listed for this model"}</div>
        ${bar ? `<div class="mt-2">${bar}</div>` : ""}
        <div class="mt-2 flex h-2 w-full gap-px overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800">
          ${m.promptTokens > 0 ? `<div class="h-full" style="width:${((m.promptTokens / (m.totalTokens || 1)) * 100).toFixed(2)}%;background:${NEON.blue}"></div>` : ""}
          ${m.completionTokens > 0 ? `<div class="h-full" style="width:${((m.completionTokens / (m.totalTokens || 1)) * 100).toFixed(2)}%;background:${NEON.emerald}"></div>` : ""}
          ${m.reasoningTokens > 0 ? `<div class="h-full" style="width:${((m.reasoningTokens / (m.totalTokens || 1)) * 100).toFixed(2)}%;background:${NEON.purple}"></div>` : ""}
        </div>
        <div class="mt-3 grid grid-cols-2 gap-2 font-mono sm:grid-cols-5">
          <div><div class="text-[10px] uppercase tracking-wider text-slate-400">Total</div><div class="text-sm font-semibold">${m.totalTokens.toLocaleString()}</div></div>
          <div><div class="text-[10px] uppercase tracking-wider text-slate-400">Input</div><div class="text-sm font-semibold">${m.promptTokens.toLocaleString()}</div></div>
          <div><div class="text-[10px] uppercase tracking-wider text-slate-400">Output</div><div class="text-sm font-semibold">${m.completionTokens.toLocaleString()}</div></div>
          <div><div class="text-[10px] uppercase tracking-wider text-slate-400">Reasoning</div><div class="text-sm font-semibold">${m.reasoningTokens.toLocaleString()}</div></div>
          <div><div class="text-[10px] uppercase tracking-wider text-slate-400">Cache hit</div><div class="text-sm font-semibold">${trimNum(roundTo(hit, 1))}%</div></div>
        </div>
      </div>`;
    })
    .join("");
  return `<div class="space-y-3">${rows}</div>`;
}

// initUsage wires the pill and the dashboard chrome. Call once at boot.
export function initUsage(): void {
  usagePillEl.addEventListener("click", () => {
    void openUsageDashboard();
  });
  usageCloseBtnEl.addEventListener("click", () => closeUsageDashboard());
  usageTabOverviewEl.addEventListener("click", () => setTab("overview"));
  usageTabModelsEl.addEventListener("click", () => setTab("models"));
  usageModalEl.addEventListener("click", (event) => {
    if (event.target === usageModalEl) {
      closeUsageDashboard();
    }
  });
}
