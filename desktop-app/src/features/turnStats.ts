// turnStats.ts is the one-line summary under a finished turn:
// "10m 9s · 103.9 tok/s". live.ts mounts it when a turn ends;
// sessions.ts mounts it while painting persisted history.

import { formatTurnStats } from "../core/format";
import type { TurnStats } from "../core/types";

export function createTurnStats(stats: TurnStats): HTMLElement {
  const el = document.createElement("div");
  el.className = "turn-stats";
  el.textContent = formatTurnStats(stats);
  el.title =
    stats.genMs > 0
      ? `${stats.tokens.toLocaleString()} generated tokens in ${(stats.genMs / 1000).toFixed(1)}s of model time`
      : `${stats.tokens.toLocaleString()} generated tokens`;
  return el;
}
