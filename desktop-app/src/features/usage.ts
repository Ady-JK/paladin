// usage.ts owns the header usage pill: per-session context fill and
// spend from the `session_usage` sidecar method. It refreshes on session
// open and after every turn (tokens bill even on failed turns), and
// clears when there is no active session.

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
import type { SessionUsage } from "../core/types";
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

function roundTo(n: number, decimals: number): number {
  const factor = 10 ** decimals;
  return Math.round(n * factor) / factor;
}

function trimNum(n: number): string {
  return `${n}`.replace(/\.0+$/, "").replace(/(\.\d*?)0+$/, "$1");
}

// pillText is the compact pill body. With a known window it leads with
// the fill percentage; without one it shows raw tokens instead of a
// misleading zero.
export function pillText(usage: SessionUsage): string {
  const spend = usage.hasCost ? ` · ${formatCost(usage.cost)}` : "";
  if (usage.contextKnown && usage.fillPercent !== undefined) {
    return `${trimNum(roundTo(usage.fillPercent, 1))}%${spend}`;
  }
  return `${compactTokens(usage.totalTokens)} tok${spend}`;
}

// pillTitle is the hover breakdown: model, tokens in/window, spend,
// calls. It never shows a percentage for unknown windows.
export function pillTitle(usage: SessionUsage): string {
  const parts = [usage.model || "unknown model"];
  const tokens = `${usage.totalTokens.toLocaleString()} tokens`;
  if (usage.contextKnown) {
    const pct =
      usage.fillPercent !== undefined ? ` (${trimNum(roundTo(usage.fillPercent, 1))}%)` : "";
    parts.push(`${tokens} / ${usage.contextWindow.toLocaleString()}${pct}`);
  } else {
    parts.push(`${tokens} (window unknown)`);
  }
  if (usage.hasCost) {
    const spend =
      usage.listCost > 0
        ? `${formatCost(usage.cost)} (list ${formatCost(usage.listCost)})`
        : formatCost(usage.cost);
    parts.push(`spent ${spend}`);
  } else {
    parts.push("spend unknown");
  }
  parts.push(`${usage.calls} call${usage.calls === 1 ? "" : "s"}`);
  return parts.join(" · ");
}

// renderUsage paints the pill from state. Callers fetch first via
// refreshUsage; direct calls only re-render already-fetched state.
export function renderUsage(): void {
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
  return (
    usage.contextKnown &&
    usage.fillPercent !== undefined &&
    usage.fillPercent >= pct
  );
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
    state.usage = await api.sessionUsage(state.sessionId, state.cwd || null);
  } catch (error) {
    console.warn(`refreshUsage failed: ${String(error)}`);
    return;
  }
  renderUsage();
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
export function formatPct(pct: number | undefined): string {
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
export function renderDashboard(): void {
  paintTabs();
  const usage = state.usage;
  usageModalSessionEl.textContent = state.sessionId
    ? `${state.sessionId.slice(0, 8)}${usage?.model ? ` · ${usage.model}` : ""}`
    : "";
  if (!usage) {
    usageBodyEl.innerHTML = `<p class="py-10 text-center text-sm text-slate-400">No billed usage yet — send a message first.</p>`;
    return;
  }
  usageBodyEl.innerHTML =
    activeTab === "models" ? modelsHtml(usage) : overviewHtml(usage);
}

function heroCard(label: string, value: string, sub: string): string {
  return `<div class="rounded-xl border border-slate-200 bg-slate-50 p-4 dark:border-slate-800 dark:bg-slate-950/60">
    <div class="text-[11px] font-medium uppercase tracking-wider text-slate-400">${label}</div>
    <div class="mt-1 truncate text-2xl font-semibold text-slate-900 dark:text-slate-100">${value}</div>
    <div class="mt-0.5 truncate text-[11px] text-slate-400">${sub}</div>
  </div>`;
}

function statCell(label: string, value: string, title?: string): string {
  const tip = title ? ` title="${escapeHtml(title)}"` : "";
  return `<div class="rounded-lg border border-slate-200 px-3 py-2.5 dark:border-slate-800"${tip}>
    <div class="text-[10px] font-medium uppercase tracking-wider text-slate-400">${label}</div>
    <div class="mt-0.5 truncate text-sm font-semibold text-slate-800 dark:text-slate-200">${value}</div>
  </div>`;
}

function donut(
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
  segments: Array<{ label: string; value: number; color: string }>,
  centerTop: string,
  centerSub: string,
  formatValue: (n: number) => string,
): string {
  const legend = segments
    .map(
      (s) =>
        `<span class="inline-flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:${s.color}"></span>${escapeHtml(s.label)} · ${formatValue(s.value)}</span>`,
    )
    .join("");
  return `<div class="flex flex-1 items-center gap-4 rounded-xl border border-slate-200 p-4 dark:border-slate-800">
    <div class="flex-shrink-0">${donut(segments, centerTop, centerSub)}</div>
    <div class="min-w-0">
      <div class="mb-2 text-[11px] font-medium uppercase tracking-wider text-slate-400">${title}</div>
      <div class="flex flex-col gap-1 text-[11px] text-slate-500 dark:text-slate-400">${legend}</div>
    </div>
  </div>`;
}

function tokenMixCard(usage: SessionUsage): string {
  return donutCard(
    "Token mix",
    [
      { label: "Read", value: usage.promptTokens, color: "#0ea5e9" },
      { label: "Written", value: usage.completionTokens, color: "#10b981" },
      { label: "Reasoning", value: usage.reasoningTokens, color: "#8b5cf6" },
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
  const palette = ["#f59e0b", "#f43f5e", "#0ea5e9", "#10b981", "#8b5cf6"];
  const top = models.slice(0, 4);
  const restCost = models.slice(4).reduce((n, m) => n + m.cost, 0);
  const segments = top.map((m, i) => ({ label: m.model, value: m.cost, color: palette[i % palette.length] }));
  if (restCost > 0) {
    segments.push({ label: `${models.length - 4} more`, value: restCost, color: "#64748b" });
  }
  return donutCard("Spend by model", segments, formatCost(usage.cost), "spent", formatCost);
}

function distributionBar(usage: SessionUsage): string {
  const spend = spendMixCard(usage);
  const charts = spend ? `${tokenMixCard(usage)}${spend}` : tokenMixCard(usage);
  return `<div class="flex flex-col gap-3 sm:flex-row">${charts}</div>`;
}

function modelShareBars(usage: SessionUsage): string {
  const models = usage.models ?? [];
  if (models.length < 2) {
    return "";
  }
  const total = usage.totalTokens || 1;
  const palette = ["#0ea5e9", "#10b981", "#f59e0b", "#8b5cf6", "#f43f5e"];
  const rows = models.slice(0, 5).map((m, i) => {
    const pct = (m.totalTokens / total) * 100;
    const color = palette[i % palette.length];
    return `<div>
      <div class="mb-1 flex items-baseline justify-between gap-2 text-[11px]">
        <span class="truncate font-medium text-slate-700 dark:text-slate-300">${escapeHtml(m.model)}</span>
        <span class="flex-shrink-0 font-mono text-slate-400">${compactTokens(m.totalTokens)} · ${pct.toFixed(1)}%</span>
      </div>
      <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800">
        <div class="h-full rounded-full" style="width:${pct.toFixed(2)}%;background:${color}"></div>
      </div>
    </div>`;
  }).join("");
  return `<div class="rounded-xl border border-slate-200 p-4 dark:border-slate-800">
    <div class="mb-3 text-[11px] font-medium uppercase tracking-wider text-slate-400">Share by model</div>
    <div class="space-y-2.5">${rows}</div>
  </div>`;
}

function overviewHtml(usage: SessionUsage): string {
  const avgTokens = usage.calls > 0 ? Math.round(usage.totalTokens / usage.calls) : 0;
  const avgCost = usage.calls > 0 && usage.hasCost ? usage.cost / usage.calls : 0;
  const cacheTotal = usage.cacheReadTokens + usage.cacheWriteTokens;
  const heroes = [
    heroCard("Total tokens", compactTokens(usage.totalTokens), `${usage.totalTokens.toLocaleString()} across ${usage.calls} call${usage.calls === 1 ? "" : "s"}`),
    heroCard("Context fill", formatPct(usage.fillPercent), usage.contextKnown ? `of ${usage.contextWindow.toLocaleString()}` : "window unknown"),
    heroCard("Spend", usage.hasCost ? formatCost(usage.cost) : "—", usage.hasCost && usage.listCost > 0 ? `list ${formatCost(usage.listCost)}` : usage.hasCost ? "gateway-reported" : "not reported"),
    heroCard("LLM calls", `${usage.calls}`, usage.model || "unknown model"),
  ].join("");
  const cacheSub = cacheTotal > 0 ? `${cacheTotal.toLocaleString()} total` : "no cache hits recorded";
  const cells = [
    statCell("Read in", usage.promptTokens.toLocaleString()),
    statCell("Written out", usage.completionTokens.toLocaleString()),
    statCell("Reasoning", usage.reasoningTokens.toLocaleString()),
    statCell("Cache read", usage.cacheReadTokens.toLocaleString(), cacheSub),
    statCell("Cache write", usage.cacheWriteTokens.toLocaleString(), cacheSub),
    statCell("Avg / call", `${compactTokens(avgTokens)} tok`),
    statCell("Avg cost / call", usage.hasCost ? formatCost(avgCost) : "—"),
    statCell("List cost", usage.hasCost && usage.listCost > 0 ? formatCost(usage.listCost) : "—"),
  ].join("");
  return `<div class="space-y-5">
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">${heroes}</div>
    ${distributionBar(usage)}
    ${modelShareBars(usage)}
    <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">${cells}</div>
  </div>`;
}

function modelsHtml(usage: SessionUsage): string {
  const models = usage.models ?? [];
  if (models.length === 0) {
    return `<p class="py-10 text-center text-sm text-slate-400">No per-model breakdown recorded for this session.</p>`;
  }
  const rows = models
    .map((m) => {
      const bar = m.contextKnown && m.fillPercent !== undefined
        ? `<div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800"><div class="h-full rounded-full bg-accent-500" style="width:${Math.min(100, m.fillPercent).toFixed(2)}%"></div></div>`
        : "";
      const spend = m.hasCost ? formatCost(m.cost) : "—";
      return `<div class="rounded-xl border border-slate-200 p-4 dark:border-slate-800">
        <div class="flex items-baseline justify-between gap-3">
          <div class="truncate text-sm font-semibold text-slate-900 dark:text-slate-100">${escapeHtml(m.model)}</div>
          <div class="flex-shrink-0 font-mono text-[11px] text-slate-400">${m.calls} call${m.calls === 1 ? "" : "s"} · ${spend}</div>
        </div>
        <div class="mt-1 font-mono text-[11px] text-slate-400">${m.contextKnown ? `window ${m.contextWindow.toLocaleString()} · fill ${formatPct(m.fillPercent)}` : "window size not listed for this model"}</div>
        ${bar ? `<div class="mt-2">${bar}</div>` : ""}
        <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-5">
          ${statCell("Total", m.totalTokens.toLocaleString())}
          ${statCell("Read", m.promptTokens.toLocaleString())}
          ${statCell("Written", m.completionTokens.toLocaleString())}
          ${statCell("Reasoning", m.reasoningTokens.toLocaleString())}
          ${statCell("Cache r/w", `${compactTokens(m.cacheReadTokens)} / ${compactTokens(m.cacheWriteTokens)}`)}
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
