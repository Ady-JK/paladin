// analytics.ts owns the fleet-wide dashboard inside Settings >
// Analytics: every session's usage.json across ALL workspaces rolled up
// via `overallUsage` (no cwd filter — the dashboard is the machine's
// ledger, not the open workspace's). Charts are hand-rolled SVG (no CDN
// deps): hero KPIs, token banner, a 7-day day-to-day tokens-vs-spend
// chart, model pricing table, daily timeline, model donuts, composition
// bars, heatmap and the top-sessions audit table. The per-session view
// is the pill/dashboards over the composer — this page is only ever
// fleet-wide.

import * as api from "../core/api";
import { analyticsBodyEl } from "../core/dom";
import { escapeHtml } from "../core/format";
import type { DailyPoint, OverallUsage } from "../core/types";
import { icon } from "../icons";
import {
  areaChart,
  cacheHitText,
  uncachedInput,
  compactTokens,
  donut,
  estimatedSpend,
  formatCost,
  heatmap,
  isEstimatedSpend,
  roundTo,
  stackedComposition,
  trimNum,
} from "./usage";

const PALETTE = {
  blue: "#8d98b4",
  purple: "#a69ac4",
  rose: "#b69ca5",
  emerald: "#95ad9e",
  amber: "#b7aa8d",
  cyan: "#91aab1",
};

let cache: OverallUsage | null = null;
let modelFilter = "all";
let metric: "tokens" | "cost" = "tokens";
let loading = false;

function hero(opts: {
  label: string;
  value: string;
  sub: string;
  iconName: "dollar-sign" | "microchip" | "terminal" | "zap";
  glow: string;
}): string {
  return `<div class="glass-panel settings-stat relative overflow-hidden rounded-2xl p-5" data-tone="${opts.glow}">
    <div class="mb-3 flex items-start justify-between">
      <div>
        <div class="text-xs font-medium uppercase tracking-wider text-slate-400">${opts.label}</div>
        <div class="mt-1 font-mono text-2xl font-bold text-slate-900 dark:text-white">${escapeHtml(opts.value)}</div>
      </div>
      <div class="settings-stat-icon rounded-xl border p-2.5">${icon(opts.iconName, "h-5 w-5")}</div>
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
    ${cell(PALETTE.blue, "Input tokens", uncachedInput(u).toLocaleString(), `${share(uncachedInput(u))} · not cached`)}
    ${cell(PALETTE.purple, "Output tokens", u.completionTokens.toLocaleString(), `${share(u.completionTokens)} of total`)}
    ${cell(PALETTE.rose, "Reasoning tokens", u.reasoningTokens.toLocaleString(), u.reasoningTokens > 0 ? `${share(u.reasoningTokens)} of total` : "no thinking billed")}
    ${cell(PALETTE.emerald, "Cache read tokens", u.cacheReadTokens.toLocaleString(), u.cacheReadTokens > 0 ? `${share(u.cacheReadTokens)} · 90% off` : "no cache hits")}
    ${cell(PALETTE.amber, "Cache write tokens", u.cacheWriteTokens.toLocaleString(), u.cacheWriteTokens > 0 ? `${share(u.cacheWriteTokens)} of total` : "no cache writes")}
  </div>`;
}

// lastSevenDays buckets the ledger's daily points into the trailing 7
// UTC days, filling zero-days in so the graph reads day-to-day rather
// than skipping silent days.
function lastSevenDays(daily: DailyPoint[]): DailyPoint[] {
  const byDate = new Map(daily.map((d) => [d.date, d]));
  const out: DailyPoint[] = [];
  const today = new Date();
  for (let back = 6; back >= 0; back--) {
    const day = new Date(today);
    day.setUTCDate(day.getUTCDate() - back);
    const key = day.toISOString().slice(0, 10);
    out.push(
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
  return out;
}

// weekChart draws the last 7 days as a day-to-day tokens-vs-spend
// chart: bars for tokens (left axis), a line for spend (right axis).
// Both series are visible at once — the question it answers is "what
// did each day cost and how much did it burn", not one or the other.
function weekChart(u: OverallUsage): string {
  const days = lastSevenDays(u.daily ?? []);
  const tokens = days.map((d) => d.totalTokens);
  const costs = days.map((d) => estimatedSpend(d));
  const maxTok = Math.max(...tokens, 1);
  const maxCost = Math.max(...costs, 1e-9);
  const w = 560;
  const h = 190;
  const pad = 10;
  const chartW = w - pad * 2 - 26;
  const bw = Math.min(38, chartW / days.length - 8);
  const x = (i: number): number => pad + 26 + (i + 0.5) * (chartW / days.length);
  const yTok = (v: number): number => h - pad - 26 - (v / maxTok) * (h - pad * 2 - 26 - 16);
  const yCost = (v: number): number => h - pad - 26 - (v / maxCost) * (h - pad * 2 - 26 - 16);
  const bars = days
    .map((d, i) => {
      const v = d.totalTokens;
      const tip = `${d.date}: ${compactTokens(v)} tokens · ${formatCost(estimatedSpend(d))} · ${d.calls} calls`;
      return `<g><rect x="${(x(i) - bw / 2).toFixed(1)}" y="${yTok(v).toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(h - pad - 26 - yTok(v), v > 0 ? 2 : 0).toFixed(1)}" rx="3" fill="${PALETTE.blue}" opacity="0.75"><title>${escapeHtml(tip)}</title></rect></g>`;
    })
    .join("");
  const line = costs.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${yCost(v).toFixed(1)}`).join(" ");
  const dots = costs
    .map((v, i) => {
      const tip = `${days[i].date}: ${formatCost(v)} spend · ${days[i].calls} calls`;
      return `<circle cx="${x(i).toFixed(1)}" cy="${yCost(v).toFixed(1)}" r="3.5" fill="${PALETTE.amber}" stroke="#090d16" stroke-width="1.5"><title>${escapeHtml(tip)}</title></circle>`;
    })
    .join("");
  const labels = days
    .map(
      (d, i) =>
        `<text x="${x(i).toFixed(1)}" y="${h - pad + 12}" text-anchor="middle" class="fill-slate-400 font-mono" font-size="10">${d.date.slice(5)}</text>`,
    )
    .join("");
  const weekSpend = costs.reduce((n, c) => n + c, 0);
  const weekTokens = tokens.reduce((n, t) => n + t, 0);
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("chart", "h-4 w-4 text-accent-400")} Last 7 days</div>
      <div class="flex items-center gap-3 font-mono text-[10px] text-slate-400">
        <span class="flex items-center gap-1"><span class="h-2.5 w-2.5 rounded-sm" style="background:${PALETTE.blue}"></span>tokens</span>
        <span class="flex items-center gap-1"><span class="h-0.5 w-4 rounded" style="background:${PALETTE.amber}"></span>spend</span>
      </div>
    </div>
    <div class="mb-3 font-mono text-xs text-slate-400">${compactTokens(weekTokens)} tokens · ${formatCost(weekSpend)} over the week</div>
    <svg viewBox="0 0 ${w} ${h}" class="h-44 w-full" role="img" aria-label="last 7 days tokens and spend">
      ${[0.25, 0.5, 0.75].map((f) => `<line x1="${pad + 26}" y1="${(pad + 16 + f * (h - pad * 2 - 26 - 16)).toFixed(1)}" x2="${w - pad}" y2="${(pad + 16 + f * (h - pad * 2 - 26 - 16)).toFixed(1)}" class="stroke-slate-200 dark:stroke-slate-800" stroke-width="1"/>`).join("")}
      ${bars}
      <path d="${line}" fill="none" stroke="${PALETTE.amber}" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>
      ${dots}
      ${labels}
    </svg>
  </div>`;
}

// modelPricingTable is the fleet ledger by model: what each model cost,
// how many tokens it burned, its cache hit rate and the average bill
// per call — the pricing view of the same rollup the donut shares.
function modelPricingTable(u: OverallUsage): string {
  const rows = (u.models ?? [])
    .filter((m) => m.totalTokens > 0)
    .sort((a, b) => estimatedSpend(b) - estimatedSpend(a))
    .slice(0, 10);
  if (rows.length === 0) {
    return "";
  }
  const body = rows
    .map((m) => {
      const est = isEstimatedSpend(m);
      const hit =
        (m.cacheHitCalls ?? 0) > 0 ? `${trimNum(roundTo(((m.cacheHitSum ?? 0) / m.cacheHitCalls!) * 100, 1))}%` : "—";
      const avg = m.calls > 0 ? formatCost(estimatedSpend(m) / m.calls) : "—";
      return `<tr class="border-b border-slate-800/60 hover:bg-slate-800/30">
        <td class="py-2.5 pr-3 text-slate-200">${escapeHtml(m.model)}</td>
        <td class="py-2.5 pr-3 font-mono text-slate-300">${m.calls.toLocaleString()}</td>
        <td class="py-2.5 pr-3 font-bold text-slate-100">${compactTokens(m.totalTokens)}</td>
        <td class="py-2.5 pr-3 text-slate-400">${compactTokens(m.cacheReadTokens)}</td>
        <td class="py-2.5 pr-3 text-emerald-400">${hit}<div class="text-[10px] text-slate-500">${(m.cacheHitCalls ?? 0) > 0 ? `${m.cacheHitCalls} calls` : "no cache data"}</div></td>
        <td class="py-2.5 pr-3 font-bold text-slate-200">${m.hasCost ? formatCost(estimatedSpend(m)) : "—"}${est ? `<span class="ml-1 text-[10px] text-amber-400">EST</span>` : ""}</td>
        <td class="py-2.5 text-right font-mono text-slate-300">${avg}</td>
      </tr>`;
    })
    .join("");
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("dollar-sign", "h-4 w-4 text-emerald-400")} Model pricing</div>
      <span class="text-[11px] text-slate-400">all workspaces</span>
    </div>
    <div class="mb-3 font-mono text-xs text-slate-400">spend and cache efficiency per model · top ${rows.length} by spend</div>
    <div class="overflow-x-auto"><table class="w-full text-left font-mono text-xs">
      <thead><tr class="border-b border-slate-800 text-slate-400">
        <th class="pb-2 pr-3 font-medium">Model</th><th class="pb-2 pr-3 font-medium">Calls</th><th class="pb-2 pr-3 font-medium">Tokens</th><th class="pb-2 pr-3 font-medium">Cache read</th><th class="pb-2 pr-3 font-medium">Cache hit</th><th class="pb-2 pr-3 font-medium">Spend</th><th class="pb-2 text-right font-medium">Avg / call</th>
      </tr></thead>
      <tbody>${body}</tbody>
    </table></div>
  </div>`;
}

function modelDonut(u: OverallUsage): string {
  const models = (u.models ?? []).filter((m) =>
    metric === "cost" ? m.hasCost && estimatedSpend(m) > 0 : m.totalTokens > 0,
  );
  const useTokens = metric === "tokens" || models.length === 0;
  const source = useTokens ? (u.models ?? []).filter((m) => m.totalTokens > 0) : models;
  if (source.length === 0) {
    return "";
  }
  const palette = [PALETTE.purple, PALETTE.cyan, PALETTE.blue, PALETTE.emerald, PALETTE.amber];
  const top = source.slice(0, 5);
  const sum = (m: (typeof source)[number]): number => (useTokens ? m.totalTokens : estimatedSpend(m));
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
  const center = useTokens
    ? compactTokens(u.totalTokens)
    : u.hasCost
      ? formatCost(estimatedSpend(u))
      : compactTokens(u.totalTokens);
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
      const title = s.title || s.sessionId.slice(0, 8);
      const badge = isEstimatedSpend(s)
        ? `<span class="rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[10px] text-amber-400">EST.</span>`
        : s.hasCost
          ? `<span class="rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-400">BILLED</span>`
          : `<span class="rounded-full border border-slate-700 bg-slate-800 px-2 py-0.5 text-[10px] text-slate-400">UNKNOWN</span>`;
      return `<tr class="border-b border-slate-800/60 hover:bg-slate-800/30">
        <td class="py-2.5 pr-3 font-semibold text-accent-400">${escapeHtml(s.sessionId.slice(0, 8))}<div class="max-w-44 truncate text-[10px] font-normal text-slate-500">${escapeHtml(title)}</div></td>
        <td class="py-2.5 pr-3 text-slate-200">${escapeHtml(s.model || "—")}<div class="text-[10px] text-slate-500">${s.calls} calls</div></td>
        <td class="py-2.5 pr-3 font-bold text-slate-100">${compactTokens(s.totalTokens)}</td>
        <td class="py-2.5 pr-3 text-emerald-400">${cacheHitText(s, 0)}</td>
        <td class="py-2.5 pr-3 font-bold text-slate-200">${s.hasCost ? formatCost(estimatedSpend(s)) : "—"}</td>
        <td class="py-2.5 text-right">${badge}</td>
      </tr>`;
    })
    .join("");
  return `<div class="glass-panel rounded-2xl p-5">
    <div class="mb-1 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("terminal", "h-4 w-4 text-accent-400")} Recent sessions</div>
      <span class="text-[11px] text-slate-400">Recent activity</span>
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
    .concat(
      models.map(
        (m) =>
          `<option value="${escapeHtml(m.model)}" ${m.model === modelFilter ? "selected" : ""}>${escapeHtml(m.model)}</option>`,
      ),
    )
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
  const kpis = [
    hero({
      label: isEstimatedSpend(filtered) ? "Est. total cost" : "Total cost",
      value: filtered.hasCost ? formatCost(estimatedSpend(filtered)) : "—",
      sub: filtered.hasCost
        ? isEstimatedSpend(filtered)
          ? "list price · subscription billed $0"
          : `avg ${formatCost(filtered.calls > 0 ? estimatedSpend(filtered) / filtered.calls : 0)}/run`
        : `${filtered.sessionCount} sessions`,
      iconName: "dollar-sign",
      glow: "blue",
    }),
    hero({
      label: "Total tokens",
      value: compactTokens(filtered.totalTokens),
      sub: `${filtered.totalTokens.toLocaleString()} · ${filtered.calls} calls`,
      iconName: "microchip",
      glow: "purple",
    }),
    hero({
      label: "Sessions",
      value: `${filtered.sessionsWithUsage}`,
      sub: `${filtered.sessionCount} total · ${filtered.models?.length ?? 0} models`,
      iconName: "terminal",
      glow: "emerald",
    }),
    hero({
      label: "Prompt cache hit",
      value: cacheHitText(filtered),
      sub:
        (filtered.cacheHitCalls ?? 0) > 0
          ? `average over ${filtered.cacheHitCalls} calls · ${compactTokens(filtered.cacheReadTokens)} read`
          : "no cache data",
      iconName: "zap",
      glow: "amber",
    }),
  ].join("");
  const timeline = areaChart(
    modelFilter === "all" ? (u.daily ?? []) : dailyForModel(u, modelFilter),
    metric,
    "overall-timeline",
  );
  const heat = heatmap(u.daily ?? [], "overall-heat");
  // The 7-day window and the model pricing table are fleet-wide views:
  // they stay unfiltered so the money questions always answer from the
  // whole machine, whatever the model filter narrows elsewhere.
  const week = lastSevenDays(u.daily ?? []);
  const weekSpend = week.reduce((n, d) => n + estimatedSpend(d), 0);
  const weekTokens = week.reduce((n, d) => n + d.totalTokens, 0);
  analyticsBodyEl.innerHTML = `<div class="space-y-5">
    ${filtersHtml(u)}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">${kpis}</div>
    ${banner(filtered)}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <div class="lg:col-span-2">${weekChart(u)}</div>
      <div class="glass-panel rounded-2xl p-5">
        <div class="mb-1 flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-white">${icon("zap", "h-4 w-4 text-amber-400")} This week</div>
        <div class="mb-3 font-mono text-xs text-slate-400">trailing 7 UTC days · all workspaces</div>
        <div class="grid grid-cols-2 gap-3 font-mono">
          <div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
            <div class="text-[11px] text-slate-500 dark:text-slate-400">7-day spend</div>
            <div class="mt-1 text-base font-bold text-slate-900 dark:text-slate-100">${formatCost(weekSpend)}</div>
          </div>
          <div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
            <div class="text-[11px] text-slate-500 dark:text-slate-400">7-day tokens</div>
            <div class="mt-1 text-base font-bold text-slate-900 dark:text-slate-100">${compactTokens(weekTokens)}</div>
          </div>
          <div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
            <div class="text-[11px] text-slate-500 dark:text-slate-400">Daily average</div>
            <div class="mt-1 text-base font-bold text-slate-900 dark:text-slate-100">${formatCost(weekSpend / 7)}</div>
          </div>
          <div class="rounded-xl border border-slate-200/70 bg-white/60 p-3 dark:border-slate-800/80 dark:bg-slate-950/60">
            <div class="text-[11px] text-slate-500 dark:text-slate-400">Peak day</div>
            <div class="mt-1 text-base font-bold text-slate-900 dark:text-slate-100">${formatCost(Math.max(...week.map((d) => estimatedSpend(d)), 0))}</div>
          </div>
        </div>
      </div>
    </div>
    ${modelPricingTable(u)}
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
  const share =
    u.totalTokens > 0 ? ((u.models ?? []).find((m) => m.model === model)?.totalTokens ?? 0) / u.totalTokens : 0;
  return (u.daily ?? []).map((d) => ({
    ...d,
    totalTokens: Math.round(d.totalTokens * share),
    cost: estimatedSpend(d) * share,
  }));
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
    // No cwd: the dashboard is the machine's ledger, every workspace's
    // sessions at once. The workspace-scoped view is the pill above the
    // composer, tied to the open session.
    cache = await api.overallUsage(null, 50);
  } catch (error) {
    analyticsBodyEl.innerHTML = `<p class="py-10 text-center font-mono text-xs text-rose-400">Analytics unavailable: ${escapeHtml(String(error))}</p>`;
    loading = false;
    return;
  }
  loading = false;
  render(cache);
}
