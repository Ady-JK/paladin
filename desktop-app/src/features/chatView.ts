// chatView.ts renders the conversation turns.

import { chatViewEl, inputEl, jumpLatestEl, messagesEl } from "../core/dom";
import { approxTokens, escapeHtml, renderMarkdown, renderText, shortPath } from "../core/format";
import { state } from "../core/state";
import type { CompactionInfo, RetryInfo, Turn } from "../core/types";
import * as api from "../core/api";
import { toast } from "../ui/toast";
import { openLocalBrowserURL, openWebTab } from "./sidePane";

// scrollToBottom puts the newest row in view. The rows it just built may
// not be laid out yet: heavy Markdown (tables, code blocks) can grow the
// transcript by thousands of pixels on the frame after this runs, and a
// view with no layout at all (the tabs layout hides #agentMain while a
// side tab shows, startup hides the shell) reports scrollHeight 0. Either
// way the assignment alone lands the conversation above its newest row, so
// follow the view while it is still growing and keep it at the end.
export function scrollToBottom(): void {
  pinned = true;
  keepAtEnd();
  updateJumpLatest();
}

// Our own scroll writes update the baseline immediately: the browser's
// queued scroll event must not be mistaken for the reader moving up.
function keepAtEnd(): void {
  if (chatViewEl.clientHeight > 0) {
    chatViewEl.scrollTop = chatViewEl.scrollHeight;
    rememberScroll();
  }
  updateJumpLatest();
  watchBottomLayout();
}

// Keep one observer for the page's lifetime. Tool groups can settle after a
// quiet gap, and the composer/view can resize without another text delta.
// The observer follows only while pinned; it never opts a reader back in.
let bottomObserver: ResizeObserver | null = null;

function watchBottomLayout(): void {
  if (bottomObserver || typeof ResizeObserver === "undefined") return;
  bottomObserver = new ResizeObserver(() => {
    updateJumpLatest();
    if (pinned && chatViewEl.clientHeight > 0) followTail();
  });
  bottomObserver.observe(chatViewEl);
  bottomObserver.observe(messagesEl);
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
  wrapper.className = "greet-hero w-full text-xs";

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

function isLocalBrowserURL(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (
      url.protocol === "http:" &&
      !url.username &&
      !url.password &&
      (url.hostname === "localhost" || url.hostname === "127.0.0.1")
    );
  } catch {
    return false;
  }
}

// initCodeCopy wires the copy button every fenced code block carries. The
// glyph is drawn in CSS, so blocks rendered anywhere (chat, side chat,
// history) need no painting; one delegated click copies the block's text
// and shows a check for a moment.
const COPIED_MS = 1400;

function initCodeCopy(): void {
  document.addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLElement>("button[data-copy-code]");
    const code = button?.parentElement?.querySelector("code");
    if (!button || !code) {
      return;
    }
    navigator.clipboard
      .writeText(code.textContent ?? "")
      .then(() => {
        button.classList.add("is-copied");
        window.setTimeout(() => button.classList.remove("is-copied"), COPIED_MS);
      })
      .catch((error: unknown) => toast(`Could not copy: ${String(error)}`, "error"));
  });
}

// Localhost links open the session browser. Other web links still use the
// system browser, and no link navigates the Paladin renderer itself.
export function initMessageLinks(): void {
  initCodeCopy();
  messagesEl.addEventListener("click", (event) => {
    const anchor = (event.target as HTMLElement).closest?.("a[data-ext]");
    if (!anchor) {
      return;
    }
    event.preventDefault();
    const url = anchor.getAttribute("data-ext");
    if (url) {
      if (isLocalBrowserURL(url)) openLocalBrowserURL(url);
      else if (state.sessionId && url.startsWith("https://")) openWebTab(state.sessionId, url);
      else void api.openExternalPage(url).catch((error) => toast(String(error), "error"));
    }
  });
}

export function mountLinkCards(target: HTMLElement, text: string): void {
  const urls = [...new Set(text.match(/https?:\/\/[^\s<>)\]]+/g) ?? [])].slice(0, 4);
  for (const raw of urls) {
    let url: URL;
    try {
      url = new URL(raw);
    } catch {
      continue;
    }
    if (!["http:", "https:"].includes(url.protocol)) continue;
    const card = document.createElement("a");
    card.className = "session-link-card";
    card.href = "#";
    card.dataset.ext = url.href;
    const label = document.createElement("span");
    label.textContent = url.hostname;
    const detail = document.createElement("small");
    detail.textContent = "Website";
    card.append(label, detail);
    target.append(card);
  }
}
// addTurn appends one message row. Codex-style split: user bubbles dock
// right, agent text docks left, errors read left in red. No names or
// avatars — the side alone says who is talking.
export type TurnOpts = { skill?: string | null; index?: number; revertable?: boolean };

export function addTurn(
  role: Turn["role"],
  text: string,
  opts?: TurnOpts,
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
  opts?: TurnOpts,
): { wrapper: HTMLElement; textEl: HTMLElement } {
  const wrapper = document.createElement("div");
  // Block by default so agent text and tool cards stack vertically;
  // the user row becomes a flex row to dock its bubble right.
  wrapper.className = "turn-row w-full text-xs";

  const isUser = role === "user";
  const isError = role === "error";

  const textEl = document.createElement("div");
  if (isUser) {
    wrapper.classList.add("flex", "flex-col", "items-end");
    textEl.className = "bubble-user whitespace-pre-wrap break-words";
  } else if (isError) {
    wrapper.classList.add("justify-start");
    textEl.className = "whitespace-pre-wrap break-words text-red-600 dark:text-red-400";
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
  if (isUser && opts?.revertable !== false) {
    // "Revert to here": rewinds files and the conversation to before
    // this message. Live rows learn their index lazily (see undo.ts).
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "revert-btn";
    btn.dataset.revert = opts?.index === undefined ? "" : String(opts.index);
    btn.title = "Undo this message and later turns";
    btn.setAttribute("aria-label", "Undo from this message");
    btn.textContent = "Undo from here";
    wrapper.prepend(btn);
  }
  return { wrapper, textEl };
}

// Follow mode: the transcript sticks to its end while the reader is at it.
// Only the reader's own input changes that: a wheel, touch or scrollbar move
// up, or scrolling back down to the end. Our own pins never change it, so a
// reply that keeps growing cannot drag a reader who scrolled away back down.
const PIN_GAP_PX = 96;
// scrollTop is fractional, while scrollHeight/clientHeight are rounded.
const BOTTOM_EPSILON_PX = 1;
let pinned = true;
let lastTop = 0;
let lastHeight = 0;
let lastClientHeight = 0;
let lastClientWidth = 0;
let touchY: number | null = null;
let scrollQueued = false;

function bottomGap(): number {
  return Math.max(0, chatViewEl.scrollHeight - chatViewEl.scrollTop - chatViewEl.clientHeight);
}

function rememberScroll(): void {
  lastTop = chatViewEl.scrollTop;
  lastHeight = chatViewEl.scrollHeight;
  lastClientHeight = chatViewEl.clientHeight;
  lastClientWidth = chatViewEl.clientWidth;
}

export function followTail(): void {
  if (!pinned || scrollQueued) return;
  scrollQueued = true;
  requestAnimationFrame(() => {
    scrollQueued = false;
    if (pinned) keepAtEnd();
  });
}

// A wheel/touch inside a thinking block or tool output belongs to that
// scroller, not the transcript. At its boundary, respect scroll containment.
function nestedScroll(target: EventTarget | null, deltaY: number): boolean {
  for (let el = target instanceof Element ? target : null; el && el !== chatViewEl; el = el.parentElement) {
    if (el.scrollHeight <= el.clientHeight + BOTTOM_EPSILON_PX) continue;
    const style = getComputedStyle(el);
    if (style.overflowY !== "auto" && style.overflowY !== "scroll") continue;
    if (deltaY < 0 ? el.scrollTop > 0 : el.scrollHeight - el.scrollTop - el.clientHeight > BOTTOM_EPSILON_PX)
      return true;
    if (style.overscrollBehaviorY === "contain" || style.overscrollBehaviorY === "none") return true;
  }
  return false;
}

// Input opts out before a queued pin can overtake the reader's scroll.
export function initChatFollow(): void {
  rememberScroll();
  watchBottomLayout();
  chatViewEl.addEventListener("scroll", onScroll, { passive: true });
  chatViewEl.addEventListener(
    "wheel",
    (event) => {
      if (
        !event.defaultPrevented &&
        event.deltaY < 0 &&
        chatViewEl.scrollTop > 0 &&
        !nestedScroll(event.target, event.deltaY)
      )
        unpin();
    },
    { passive: true },
  );
  chatViewEl.addEventListener(
    "touchstart",
    (event) => {
      touchY = event.touches.item(0)?.clientY ?? null;
    },
    { passive: true },
  );
  chatViewEl.addEventListener(
    "touchmove",
    (event) => {
      const y = event.touches.item(0)?.clientY;
      if (y === undefined || touchY === null) return;
      const deltaY = touchY - y;
      if (!event.defaultPrevented && deltaY < 0 && chatViewEl.scrollTop > 0 && !nestedScroll(event.target, deltaY))
        unpin();
      touchY = y;
    },
    { passive: true },
  );
  chatViewEl.addEventListener("click", (event) => {
    // Opening details is inspection, not a request to chase their new height.
    // Capture the resulting gap before the resize observer can pin it away.
    const target = event.target instanceof Element ? event.target : null;
    if (
      target?.closest(".worked-fold > summary") ||
      (target?.closest(".tool-run-head, .tool-row-head, .tool-step-head, .think-block > summary") &&
        bottomGap() > BOTTOM_EPSILON_PX)
    )
      unpin();
  });
  jumpLatestEl.addEventListener("click", scrollToBottom);
}

function onScroll(): void {
  // Hidden views report zero geometry; retain the held follow request.
  if (chatViewEl.clientHeight === 0) {
    updateJumpLatest();
    return;
  }
  const top = chatViewEl.scrollTop;
  const gap = bottomGap();
  const layoutChanged =
    chatViewEl.scrollHeight !== lastHeight ||
    chatViewEl.clientHeight !== lastClientHeight ||
    chatViewEl.clientWidth !== lastClientWidth;
  if (!layoutChanged && top < lastTop && gap > BOTTOM_EPSILON_PX) {
    unpin();
  } else if (gap <= BOTTOM_EPSILON_PX || (!layoutChanged && top > lastTop && gap < PIN_GAP_PX)) {
    pinned = true;
  }
  rememberScroll();
  if (layoutChanged && pinned) followTail();
  updateJumpLatest();
}

function unpin(): void {
  pinned = false;
  updateJumpLatest();
}

function updateJumpLatest(): void {
  jumpLatestEl.hidden = pinned || chatViewEl.clientHeight === 0 || bottomGap() <= BOTTOM_EPSILON_PX;
}

// compactionText is the divider label for a compaction event.
export function compactionText(info: CompactionInfo): string {
  if (info.phase === "start") {
    return info.reason === "overflow" ? "Context window exceeded — compacting context…" : "Compacting context…";
  }
  if (info.phase === "failed") {
    return `Compaction failed: ${info.error || "unknown error"}`;
  }
  const after = info.tokensAfter ? ` → ${approxTokens(info.tokensAfter)}` : "";
  const reason = info.reason === "overflow" ? " · provider context limit" : "";
  if (!info.tokensBefore) {
    return `Context compacted${info.tokensAfter ? ` · ${approxTokens(info.tokensAfter)} tokens` : ""}${reason}`;
  }
  return `Context compacted · ${approxTokens(info.tokensBefore)}${after} tokens${reason}`;
}

// createCompactionDivider builds the full-width divider marking where
// older conversation was replaced by a summary. The summary (live text
// or history rows) sits below the label, expanded: the point is to read
// what was kept, not to hide it behind a twistie.
export function createCompactionDivider(label: string, summary?: string): HTMLElement {
  const el = document.createElement("div");
  el.className = "chat-divider";
  el.dataset.notice = "compaction";
  el.setAttribute("role", "status");
  const text = document.createElement("span");
  text.className = "chat-divider-label";
  text.textContent = label;
  el.append(text);
  if (summary) {
    const body = document.createElement("div");
    body.className = "md chat-divider-summary";
    body.innerHTML = renderMarkdown(summary);
    el.append(body);
  }
  return el;
}

// paintDividerSummary sets the summary on a live divider once the done
// event carries it.
export function paintDividerSummary(el: HTMLElement, summary: string): void {
  let body = el.querySelector<HTMLElement>(".chat-divider-summary");
  if (!body) {
    body = document.createElement("div");
    body.className = "md chat-divider-summary";
    el.append(body);
  }
  body.innerHTML = renderMarkdown(summary);
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
