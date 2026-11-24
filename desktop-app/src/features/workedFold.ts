// workedFold.ts folds a finished turn's working into one line. Everything the
// agent did before its final message (commentary, thinking, tool runs,
// approvals) moves into a "Worked for 8m 2s" disclosure; the user's message
// and the final reply stay in the open. Live turns and persisted history go
// through the same function, so a session looks the same either way.

import { formatTurnDuration } from "../core/format";
import type { TurnStats } from "../core/types";
import { icon } from "../icons";

const FOLD_CLASS = "worked-fold";

// KEEP are what the agent produced on purpose: they stay in the open, in
// their place, rather than hiding inside the fold.
const KEEP = ".findings-card, .visual-card, .agent-q, .steer-bubble";

// Only decorations may follow the final reply. A trailing tool, thinking
// block, notice or deliverable means the last text was still commentary.
const AFTER_REPLY = ".agent-text, .session-link-card, .session-image-thumb, .hook-row";

function hasText(node: Element): boolean {
  return node.classList.contains("agent-text") && (node.textContent ?? "").trim() !== "";
}

// foldBody is where a folded turn's late rows (a screenshot that arrives after
// the turn ended) belong: inside the fold, not under the final reply.
export function foldBody(wrapper: HTMLElement): HTMLElement | null {
  return wrapper.querySelector<HTMLElement>(`:scope > .${FOLD_CLASS} > .worked-body`);
}

function foldLabel(stats?: TurnStats): string {
  return stats && stats.durationMs > 0 ? `Worked for ${formatTurnDuration(stats.durationMs)}` : "Worked";
}

// foldWork folds the turn in wrapper (an agent row). It does nothing unless
// the turn ended with a reply after some work, so a stopped or failed turn, or
// a plain one-message answer, is left exactly as it was drawn.
export function foldWork(wrapper: HTMLElement, stats?: TurnStats): boolean {
  if (stats?.incomplete || wrapper.querySelector(`:scope > .${FOLD_CLASS}`)) return false;
  const children = Array.from(wrapper.children);
  let final = -1;
  for (let i = children.length - 1; i >= 0; i--) {
    if (hasText(children[i])) {
      final = i;
      break;
    }
  }
  if (final < 0) return false;
  if (children.slice(final + 1).some((node) => !node.matches(AFTER_REPLY) || hasText(node))) return false;
  const work = children.slice(0, final).filter((node) => !node.matches(KEEP));
  // Empty text segments (the "thinking…" placeholder that was replaced) are
  // not work: a turn of one reply has nothing to fold.
  if (!work.some((node) => !node.classList.contains("agent-text") || hasText(node))) return false;

  const fold = document.createElement("details");
  fold.className = FOLD_CLASS;
  const summary = document.createElement("summary");
  const label = document.createElement("span");
  label.className = "worked-label";
  label.textContent = foldLabel(stats);
  const caret = document.createElement("span");
  caret.className = "worked-caret";
  caret.setAttribute("aria-hidden", "true");
  caret.innerHTML = icon("chevron-right", "h-3.5 w-3.5");
  summary.append(label, caret);
  const body = document.createElement("div");
  body.className = "worked-body";
  fold.append(summary, body);
  wrapper.prepend(fold);
  body.append(...work);
  return true;
}
