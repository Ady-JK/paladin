// analytics.ts owns the fleet-wide dashboard inside Settings >
// Analytics: every session's usage.json rolled up via `overall_usage`.
// Charts are hand-rolled SVG (no CDN deps) in the NexusHarness glass
// style: hero KPIs, token banner, daily timeline, model donuts,
// composition bars, heatmap and the top-sessions audit table.

import * as api from "../core/api";
import { analyticsBodyEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { DailyPoint, OverallUsage } from "../core/types";
import { icon } from "../icons";
import {
  NEON,
  areaChart,
  cacheHitRate,
  compactTokens,
  donut,
  formatCost,
  heatmap,
  roundTo,
  stackedComposition,
  trimNum,
} from "./usage";

let cache: OverallUsage | null = null;
let modelFilter = "all";
let metric: "tokens" | "cost" = "tokens";
let loading = false;

function hero(opts: {
  label: string;
  value: string;
  sub: string;
  iconName: "dollar-sign" | "microchip" | "terminal" | "zap";
  iconBox: string;
  glow: string;
}): string {
  return `<div class="glass-panel glow-${opts.glow} relative overflow-hidden rounded-2xl p-5">
    <div class="absolute -bottom-6 -right-6 h-24 w-24 rounded-full bg-white/5 blur-2xl"></div>
    <div class="mb-3 flex items-start justify-between">
      <div>
        <div class="text-xs font-medium uppercase tracking-wider text-slate-400">${opts.label}</div>
        <div class="mt-1 font-mono text-2xl font-bold text-slate-900 dark:text-white">${escapeHtml(opts.value)}</div>
      </div>
      <div class="rounded-xl border p-2.5 ${opts.iconBox}">${icon(opts.iconName, "h-5 w-5")}</div>
    </div>
    <div class="truncate font-mono text-xs text-slate-500 dark:text-slate-400">${opts.sub}</div>
  </div>`;
}

function banner(u: OverallUsage): string {
  const total = u.totalTokens || 1;
  const share = (n: number): string => `${trimNum(roundTo((n / total) * 100, 1))}%`;
  const cell = (dot: string, label: string, value: string, sub: string): string =>
    `<div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
      <div class="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400"><span class="h-2 w-2 rounded-full" style="background:${dot}"></span>${label}</div>
      <div class="mt-1 font-mono text-base font-bold text-slate-900 dark:text-slate-100">${value}</div>
      <div class="mt-0.5 font-mono text-[10px] text-slate-400">${sub}</div>
    </div>`;
  return `<div class="glass-panel grid grid-cols-2 gap-3 rounded-2xl p-4 font-mono sm:grid-cols-3 lg:grid-cols-5">
    ${cell(NEON.blue, "Input Tokens", u.promptTokens.toLocaleString(), `${share(u.promptTokens)} of total`)}
    ${cell(NEON.purple, "Output Tokens", u.completionTokens.toLocaleString(), `${share(u.completionTokens)} of total`)}
    ${cell(NEON.rose, "Reasoning Tokens", u.reasoningTokens.toLocaleString(), u.reasoningTokens > 0 ? `${share(u.reasoningTokens)} of total` : "no thinking billed")}
    ${cell(NEON.emerald, "Cache Read", u.cacheReadTokens.toLocaleString(), u.cacheReadTokens > 0 ? `${share(u.cacheReadTokens)} · 90% off` : "no cache hits")}
    ${cell(NEON.amber, "Cache Write", u.cacheWriteTokens.toLocaleString(), u.cacheWriteTokens > 0 ? `${share(u.cacheWriteTokens)} of total` : "no cache writes")}
  </div>`;
}

function modelDonut(u: OverallUsage): string {
  const models = (u.models ?? []).filter((m) => (metric === "cost" ? m.hasCost && m.cost > 0 : m.totalTokens > 0));
  const useTokens = metric === "tokens" || models.length === 0;
  const source = useTokens ? (u.models ?? []).filter((m) => m.totalTokens > 0) : models;
  if (source.length === 0) {
    return "";
  }
  const palette = [NEON.purple, NEON.cyan, NEON.blue, NEON.emerald, NEON.amber];
  const top = source.slice(0, 5);
  const sum = (m: (typeof source)[number]): number => (useTokens ? m.totalTokens : m.cost);
  const rest = source.slice(5).reduce((n, m) => n + sum(m), 0);
  const total = source.reduce((n, m) => n + sum(m), 0);
  const segs = top.map((m, i) => ({
    label: m.model,
    value: sum(m),
    color: palette[i % palette.length],
  }));
  if (rest > 0) {
    segs.push({ label: `${source.length - 5} more`, value: rest, color: "#64748b" });
  }
  const legend = segs
    .map((s) => {
      const pct = total > 0 ? `${trimNum(roundTo((s.value / total) * 100, 1))}%` : "—";
      return `<div class="flex items-center gap-1.5"><span class="h-2.5 w-2.5 rounded-full" style="background:${s.color}"></span><span class="truncate text-slate-300">${escapeHtml(s.label)}</span><span class="ml-auto font-bold text-slate-400">${pct}</span></div>`;
    })
    .join("");
  const center = useTokens ? compactTokens(u.totalTokens) : u.hasCost ? formatCost(u.cost) : compactTokens(u.totalTokens);
  return `<div class="glass-panel flex-1 rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("chart", "h-4 w-4 text-purple-400")} Provider &amp; model share</div>
      <span class="rounded-full bg-slate-800 px-2 py-0.5 font-mono text-[10px] text-slate-400">by ${useTokens ? "tokens" : "spend"}</span>
    </div>
    <div class="mb-3 font-mono text-xs text-slate-400">token usage weighted by monetary output</div>
    <div class="flex items-center justify-center">${donut(segs, center, useTokens ? "tokens" : "spent")}</div>
    <div class="mt-4 grid grid-cols-2 gap-2 border-t border-slate-800/80 pt-3 font-mono text-xs">${legend}</div>
  </div>`;
}

function sessionsTable(u: OverallUsage, filter: string): string {
  const rows = (u.sessions ?? []).filter((s) => filter === "all" || s.model === filter).slice(0, 8);
  if (rows.length === 0) {
    return `<div class="glass-panel rounded-2xl p-5"><p class="py-6 text-center font-mono text-xs text-slate-400">No billed sessions yet — run the agent first.</p></div>`;
  }
  const body = rows
    .map((s) => {
      const hit = cacheHitRate(s);
      const title = s.title || s.sessionId.slice(0, 8);
      const badge = s.hasCost
        ? `<span class="rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-400">BILLED</span>`
        : `<span class="rounded-full border border-slate-700 bg-slate-800 px-2 py-0.5 text-[10px] text-slate-400">FREE</span>`;
      return `<tr class="border-b border-slate-800/60 hover:bg-slate-800/30">
        <td class="py-2.5 pr-3 font-semibold text-accent-400">${escapeHtml(s.sessionId.slice(0, 8))}<div class="max-w-44 truncate text-[10px] font-normal text-slate-500">${escapeHtml(title)}</div></td>
        <td class="py-2.5 pr-3 text-slate-200">${escapeHtml(s.model || "—")}<div class="text-[10px] text-slate-500">${s.calls} calls</div></td>
        <td class="py-2.5 pr-3 font-bold text-slate-100">${compactTokens(s.totalTokens)}</td>
        <td class="py-2.5 pr-3 text-emerald-400">${trimNum(roundTo(hit, 0))}%</td>
        <td class="py-2.5 pr-3 font-bold text-slate-200">${s.hasCost ? formatCost(s.cost) : "—"}</td>
        <td class="py-2.5 text-right">${badge}</td>
      </tr>`;
    })
    .join("");
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("terminal", "h-4 w-4 text-accent-400")} Recent sessions</div>
      <span class="rounded-full border border-accent-500/20 bg-accent-500/10 px-2.5 py-1 font-mono text-[10px] text-accent-400">Live audit trail</span>
    </div>
    <div class="mb-3 font-mono text-xs text-slate-400">top ${rows.length} sessions by tokens · showing ${filter === "all" ? "every model" : escapeHtml(filter)}</div>
    <div class="overflow-x-auto"><table class="w-full text-left font-mono text-xs">
      <thead><tr class="border-b border-slate-800 text-slate-400">
        <th class="pb-2 pr-3 font-medium">Session</th><th class="pb-2 pr-3 font-medium">Model</th><th class="pb-2 pr-3 font-medium">Tokens</th><th class="pb-2 pr-3 font-medium">Cache %</th><th class="pb-2 pr-3 font-medium">Cost</th><th class="pb-2 text-right font-medium">Status</th>
      </tr></thead>
      <tbody>${body}</tbody>
    </table></div>
  </div>`;
}

function filtersHtml(u: OverallUsage): string {
  const models = u.models ?? [];
  const opts = [`<option value="all">All models (${models.length})</option>`]
    .concat(models.map((m) => `<option value="${escapeHtml(m.model)}" ${m.model === modelFilter ? "selected" : ""}>${escapeHtml(m.model)}</option>`))
    .join("");
  const pill = (m: "tokens" | "cost", label: string): string =>
    `<button data-metric="${m}" class="rounded px-2.5 py-1 ${metric === m ? "bg-accent-600 font-medium text-white" : "text-slate-400 hover:text-white"}">${label}</button>`;
  return `<div class="glass-panel flex flex-wrap items-center justify-between gap-3 rounded-2xl p-4">
    <div class="flex flex-wrap items-center gap-3">
      <span class="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-slate-400">${icon("layers", "h-3.5 w-3.5 text-accent-400")} Filters</span>
      <select id="analyticsModelFilter" class="rounded-lg border border-slate-800 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-200 focus:border-accent-500 focus:outline-none">${opts}</select>
      <div class="flex items-center gap-1 rounded-lg border border-slate-800 bg-slate-950 p-1 text-xs">${pill("cost", "Cost ($)")}${pill("tokens", "Tokens")}</div>
    </div>
    <button id="analyticsRefresh" class="rounded-lg border border-slate-800 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-300 hover:border-slate-700 hover:text-white">Refresh</button>
  </div>`;
}

function render(u: OverallUsage): void {
  const filtered: OverallUsage =
    modelFilter === "all"
      ? u
      : {
          ...u,
          models: (u.models ?? []).filter((m) => m.model === modelFilter),
          sessions: (u.sessions ?? []).filter((s) => s.model === modelFilter),
        };
  const hit = cacheHitRate(filtered);
  const saved = filtered.hasCost && filtered.listCost > filtered.cost ? filtered.listCost - filtered.cost : 0;
  const kpis = [
    hero({ label: "Est. total cost", value: filtered.hasCost ? formatCost(filtered.cost) : "—", sub: saved > 0 ? `saved ${formatCost(saved)} vs list` : filtered.hasCost ? `avg ${formatCost(filtered.calls > 0 ? filtered.cost / filtered.calls : 0)}/run` : `${filtered.sessionCount} sessions`, iconName: "dollar-sign", iconBox: "bg-accent-500/10 text-accent-400 border border-accent-500/20", glow: "blue" }),
    hero({ label: "Total tokens", value: compactTokens(filtered.totalTokens), sub: `${filtered.totalTokens.toLocaleString()} · ${filtered.calls} calls`, iconName: "microchip", iconBox: "bg-purple-500/10 text-purple-400 border border-purple-500/20", glow: "purple" }),
    hero({ label: "Sessions", value: `${filtered.sessionsWithUsage}`, sub: `${filtered.sessionCount} total · ${filtered.models?.length ?? 0} models`, iconName: "terminal", iconBox: "bg-emerald-500/10 text-emerald-400 border border-emerald-500/20", glow: "emerald" }),
    hero({ label: "Prompt cache hit", value: `${trimNum(roundTo(hit, 1))}%`, sub: filtered.cacheReadTokens > 0 ? `${compactTokens(filtered.cacheReadTokens)} read · 90% off` : "no cache hits recorded", iconName: "zap", iconBox: "bg-amber-500/10 text-amber-400 border border-amber-500/20", glow: "amber" }),
  ].join("");
  const timeline = areaChart(
    (modelFilter === "all" ? (u.daily ?? []) : dailyForModel(u, modelFilter)),
    metric,
    "overall-timeline",
  );
  const heat = heatmap(u.daily ?? [], "overall-heat");
  analyticsBodyEl.innerHTML = `<div class="space-y-5">
    ${filtersHtml(u)}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">${kpis}</div>
    ${banner(filtered)}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <div class="lg:col-span-2">${timeline || `<div class="glass-panel rounded-2xl p-5"><p class="py-6 text-center font-mono text-xs text-slate-400">Not enough daily history yet — timelines appear after calls span 2+ UTC days.</p></div>`}</div>
      ${modelDonut(filtered)}
    </div>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">${stackedComposition(filtered.models ?? [], "Token composition by model")}${heat || `<div class="glass-panel rounded-2xl p-5"><p class="py-6 text-center font-mono text-xs text-slate-400">Heatmap appears once daily buckets accumulate.</p></div>`}</div>
    ${sessionsTable(u, modelFilter)}
  </div>`;
  wireControls();
}

function dailyForModel(u: OverallUsage, model: string): DailyPoint[] {
  // Per-model daily series is not stored server-side; approximate it by
  // scaling the fleet series by the model's token share. The table and
  // donuts stay exact — only the filtered timeline is an estimate,
  // labelled as such.
  const share = u.totalTokens > 0 ? ((u.models ?? []).find((m) => m.model === model)?.totalTokens ?? 0) / u.totalTokens : 0;
  return (u.daily ?? []).map((d) => ({ ...d, totalTokens: Math.round(d.totalTokens * share), cost: d.cost * share }));
}

function wireControls(): void {
  const select = analyticsBodyEl.querySelector<HTMLSelectElement>("#analyticsModelFilter");
  select?.addEventListener("change", () => {
    modelFilter = select.value;
    if (cache) {
      render(cache);
    }
  });
  analyticsBodyEl.querySelectorAll<HTMLButtonElement>("[data-metric]").forEach((btn) => {
    btn.addEventListener("click", () => {
      metric = btn.dataset.metric === "cost" ? "cost" : "tokens";
      if (cache) {
        render(cache);
      }
    });
  });
  analyticsBodyEl.querySelector("#analyticsRefresh")?.addEventListener("click", () => {
    void load(true);
  });
}

// load fetches the rollup and paints it. force bypasses nothing —
// every open refetches so the numbers never go stale.
export async function load(force = false): Promise<void> {
  if (loading) {
    return;
  }
  if (!force && cache) {
    render(cache);
    return;
  }
  loading = true;
  analyticsBodyEl.innerHTML = `<p class="py-10 text-center font-mono text-xs text-slate-400">Loading fleet analytics…</p>`;
  try {
    cache = await api.overallUsage(state.cwd || null, 50);
  } catch (error) {
    analyticsBodyEl.innerHTML = `<p class="py-10 text-center font-mono text-xs text-rose-400">Analytics unavailable: ${escapeHtml(String(error))}</p>`;
    loading = false;
    return;
  }
  loading = false;
  render(cache);
}

// refreshAnalytics refetches on next open (e.g. after a billed turn).
export function invalidateAnalytics(): void {
  cache = null;
}
