// chatView.ts renders the conversation turns.
// It supports the desktop app frontend.

import { chatViewEl, inputEl, messagesEl } from "../core/dom";
import { compactTokens, escapeHtml, renderMarkdown, renderText, shortPath } from "../core/format";
import { state } from "../core/state";
import type { CompactionInfo, RetryInfo, Turn } from "../core/types";
import * as api from "../core/api";
import { toast } from "../ui/toast";

// scrollToBottom is a function in this module.
// It supports the desktop app frontend.
export function scrollToBottom(): void {
  chatViewEl.scrollTop = chatViewEl.scrollHeight;
}

// STARTERS are the hero suggestion chips on an empty conversation.
// Picking one drafts the prompt in the composer without sending.
const STARTERS: Array<{ label: string; prompt: string }> = [
  {
    label: "Map the codebase",
    prompt: "Explore the workspace and give me a guided tour of the codebase layout.",
  },
  {
    label: "Run the test suite",
    prompt: "Run the test suite and report what fails.",
  },
  {
    label: "Review my changes",
    prompt: "Review the latest changes and point out risks.",
  },
];

// dismissGreeting removes the empty-state hero so the first user turn
// docks at the top of the conversation. Safe to call when no hero
// exists; other turns must not pile up below the orb/title/chips.
export function dismissGreeting(): void {
  messagesEl.querySelectorAll(".greet-hero").forEach((el) => el.remove());
}

// renderGreeting paints the empty-state hero: drifting orb, panning
// gradient title, and starter chips that draft prompts.
export function renderGreeting(): void {
  if (messagesEl.childElementCount > 0) {
    return;
  }

  const wrapper = document.createElement("div");
  wrapper.className = "greet-hero mx-auto w-full max-w-3xl text-xs";

  wrapper.innerHTML = `
    <div class="greet-orb"></div>
    <h1 class="greet-title">Ready when you are.</h1>
    <p class="greet-sub">Workspace '${escapeHtml(shortPath(state.cwd || "workspace"))}'
    </p>
    <div class="greet-chips">${STARTERS.map((s, i) => `<button class="greet-chip" data-chip="${i}">${escapeHtml(s.label)}</button>`).join("")}</div>`;

  wrapper.querySelectorAll<HTMLButtonElement>("[data-chip]").forEach((chip) => {
    chip.addEventListener("click", () => {
      const starter = STARTERS[Number(chip.dataset.chip)];
      if (!starter) {
        return;
      }
      inputEl.value = starter.prompt;
      inputEl.focus();
    });
  });

  messagesEl.append(wrapper);
}

// initMessageLinks routes in-chat Markdown links to the system browser
// instead of navigating the app window. It supports the desktop app
// frontend.
export function initMessageLinks(): void {
  messagesEl.addEventListener("click", (event) => {
    const anchor = (event.target as HTMLElement).closest?.("a[data-ext]");
    if (!anchor) {
      return;
    }
    event.preventDefault();
    const url = anchor.getAttribute("data-ext");
    if (url) {
      void api.openExternalPage(url).catch((error) => toast(String(error), "error"));
    }
  });
}
// addTurn appends one message row. Codex-style split: user bubbles dock
// right, agent text docks left, errors read left in red. No names or
// avatars — the side alone says who is talking.
export function addTurn(
  role: Turn["role"],
  text: string,
  opts?: { skill?: string | null },
): { wrapper: HTMLElement; textEl: HTMLElement } {
  const turn = createTurn(role, text, opts);
  messagesEl.append(turn.wrapper);
  scrollToBottom();
  return turn;
}

// createTurn builds a turn row without mounting it: live turns of a
// session that is not on screen render into detached nodes.
export function createTurn(
  role: Turn["role"],
  text: string,
  opts?: { skill?: string | null },
): { wrapper: HTMLElement; textEl: HTMLElement } {
  const wrapper = document.createElement("div");
  // Block by default so agent text and tool cards stack vertically;
  // the user row becomes a flex row to dock its bubble right.
  wrapper.className = "turn-row mx-auto w-full max-w-3xl text-xs";

  const isUser = role === "user";
  const isError = role === "error";

  const textEl = document.createElement("div");
  if (isUser) {
    wrapper.classList.add("flex", "justify-end");
    textEl.className = "bubble-user whitespace-pre-wrap break-words";
  } else if (isError) {
    wrapper.classList.add("justify-start");
    textEl.className =
      "whitespace-pre-wrap break-words text-red-600 dark:text-red-400";
  } else {
    textEl.className = "md agent-text";
  }
  // Agent turns render full Markdown (headings, bold, lists, code);
  // user and error rows stay plain text. A skill turn prefixes a
  // static chip with the skill name; the SKILL.md content itself is
  // never pasted here — the sidecar injects it before the LLM call.
  if (isUser && opts?.skill) {
    const chip = document.createElement("div");
    chip.innerHTML = `<span class="bubble-skill"><span class="bubble-skill-dot"></span>/${escapeHtml(opts.skill)}</span>`;
    textEl.append(chip.firstElementChild!);
  }
  const body = document.createElement("div");
  body.innerHTML = !isUser && !isError ? renderMarkdown(text) : renderText(text);
  textEl.append(body);

  wrapper.append(textEl);
  return { wrapper, textEl };
}

// Stick-to-bottom: live output only drags the view down while the user
// is already reading the tail.
let stick = true;
let stickWired = false;
let scrollQueued = false;

export function followTail(): void {
  if (!stickWired) {
    stickWired = true;
    chatViewEl.addEventListener("scroll", () => {
      const gap = chatViewEl.scrollHeight - chatViewEl.scrollTop - chatViewEl.clientHeight;
      stick = gap < 96;
    });
  }
  if (!stick || scrollQueued) {
    return;
  }
  scrollQueued = true;
  requestAnimationFrame(() => {
    scrollQueued = false;
    if (stick) {
      scrollToBottom();
    }
  });
}

// compactionText is the divider label for a compaction event.
export function compactionText(info: CompactionInfo): string {
  if (info.phase === "start") {
    return info.reason === "overflow"
      ? "Context window exceeded — compacting context…"
      : "Compacting context…";
  }
  if (info.phase === "failed") {
    return `Compaction failed: ${info.error || "unknown error"}`;
  }
  const after = info.tokensAfter ? ` → ${compactTokens(info.tokensAfter)}` : "";
  return `Context compacted · ${compactTokens(info.tokensBefore)}${after} tokens`;
}

// createCompactionDivider builds the full-width divider marking where
// older conversation was replaced by a summary. A summary (history
// view) sits in a collapsed <details>.
export function createCompactionDivider(label: string, summary?: string): HTMLElement {
  const el = document.createElement("div");
  el.className = "chat-divider";
  el.dataset.notice = "compaction";
  el.setAttribute("role", "status");
  const text = document.createElement("span");
  text.className = "chat-divider-label";
  text.textContent = label;
  if (summary) {
    const details = document.createElement("details");
    details.className = "chat-divider-details";
    const sum = document.createElement("summary");
    sum.append(text);
    const body = document.createElement("div");
    body.className = "md chat-divider-summary";
    body.innerHTML = renderMarkdown(summary);
    details.append(sum, body);
    el.append(details);
  } else {
    el.append(text);
  }
  return el;
}

// setDividerLabel updates a live divider's text.
export function setDividerLabel(el: HTMLElement, label: string): void {
  const text = el.querySelector(".chat-divider-label");
  if (text) {
    text.textContent = label;
  }
}

// createRetryNotice builds the inline "retrying" note shown while a
// failed provider round waits to run again.
export function createRetryNotice(): HTMLElement {
  const el = document.createElement("div");
  el.className = "chat-notice";
  el.dataset.notice = "retry";
  el.setAttribute("role", "status");
  el.setAttribute("aria-live", "polite");
  return el;
}

// retryText is the retry notice label.
export function retryText(info: RetryInfo): string {
  const secs = Math.max(1, Math.round(info.delayMs / 1000));
  return `Retrying in ${secs}s (attempt ${info.attempt}/${info.max}): ${info.reason}`;
}
