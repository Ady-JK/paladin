// chatView.ts renders the conversation turns.
// It supports the desktop app frontend.

import { chatViewEl, inputEl, messagesEl } from "../core/dom";
import { escapeHtml, renderText, shortPath } from "../core/format";
import { state } from "../core/state";
import type { Turn } from "../core/types";
import { icon } from "../icons";

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

// renderGreeting paints the empty-state hero: drifting orb, panning
// gradient title, and starter chips that draft prompts.
export function renderGreeting(): void {
  if (messagesEl.childElementCount > 0) {
    return;
  }

  const wrapper = document.createElement("div");
  wrapper.className = "greet-hero mx-auto w-full max-w-4xl text-xs";

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

// addTurn is a function in this module.
// It supports the desktop app frontend.
export function addTurn(
  role: Turn["role"],
  text: string,
  timestamp?: number,
): { wrapper: HTMLElement; textEl: HTMLElement } {
  const wrapper = document.createElement("div");
  wrapper.className = "turn-row mx-auto flex max-w-4xl gap-3 text-xs";

  const isUser = role === "user";
  const isError = role === "error";

  const avatar = document.createElement("div");
  avatar.className = `avatar ${isUser ? "avatar-user" : isError ? "avatar-error" : "avatar-agent"}`;
  avatar.innerHTML = isUser ? "YOU" : isError ? icon("xmark", "h-3 w-3") : icon("bolt", "h-3 w-3");

  const body = document.createElement("div");
  body.className = "min-w-0 flex-1 space-y-1";

  const when = new Date((timestamp ?? Date.now() / 1000) * 1000).toLocaleTimeString(
    [],
    { hour: "2-digit", minute: "2-digit" },
  );

  const head = document.createElement("div");
  head.className = "flex items-center gap-2";
  head.innerHTML = `
    <span class="font-semibold ${isError ? "text-red-600 dark:text-red-400" : "text-slate-900 dark:text-slate-100"}">${isUser ? "You" : isError ? "error" : "paladin"}</span>
    <span class="text-[10px] text-slate-400">${when}</span>
  `;

  const textEl = document.createElement("div");
  textEl.className = isUser
    ? "bubble-user whitespace-pre-wrap break-words"
    : isError
      ? "whitespace-pre-wrap break-words leading-relaxed text-red-600 dark:text-red-400"
      : "whitespace-pre-wrap break-words leading-relaxed text-slate-700 dark:text-slate-300";
  textEl.innerHTML = renderText(text);

  body.append(head, textEl);
  wrapper.append(avatar, body);
  messagesEl.append(wrapper);

  scrollToBottom();
  return { wrapper, textEl };
}
