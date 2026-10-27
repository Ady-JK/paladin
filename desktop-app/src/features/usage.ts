// usage.ts owns the header usage pill: per-session context fill and
// spend from the `session_usage` sidecar method. It refreshes on session
// open and after every turn (tokens bill even on failed turns), and
// clears when there is no active session.

import * as api from "../core/api";
import { usageLabelEl, usagePillEl } from "../core/dom";
import { state } from "../core/state";
import type { SessionUsage } from "../core/types";

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
}
