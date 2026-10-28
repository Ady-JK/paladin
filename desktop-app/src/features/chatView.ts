// chatView.ts renders the conversation turns.
// It supports the desktop app frontend.

import { chatViewEl, inputEl, messagesEl } from "../core/dom";
import { escapeHtml, renderText, shortPath } from "../core/format";
import { state } from "../core/state";
import type { Turn } from "../core/types";

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

// addTurn appends one message row. Codex-style split: user bubbles dock
// right, agent text docks left, errors read left in red. No names or
// avatars — the side alone says who is talking.
export function addTurn(
  role: Turn["role"],
  text: string,
): { wrapper: HTMLElement; textEl: HTMLElement } {
  const wrapper = document.createElement("div");
  // Block by default so agent text and tool cards stack vertically;
  // the user row becomes a flex row to dock its bubble right.
  wrapper.className = "turn-row mx-auto w-full max-w-4xl text-xs";

  const isUser = role === "user";
  const isError = role === "error";

  const textEl = document.createElement("div");
  if (isUser) {
    wrapper.classList.add("flex", "justify-end");
    textEl.className = "bubble-user max-w-[80%] whitespace-pre-wrap break-words";
  } else if (isError) {
    wrapper.classList.add("justify-start");
    textEl.className =
      "max-w-[85%] whitespace-pre-wrap break-words leading-relaxed text-red-600 dark:text-red-400";
  } else {
    wrapper.classList.add("justify-start");
    textEl.className =
      "max-w-[85%] whitespace-pre-wrap break-words leading-relaxed text-slate-700 dark:text-slate-300";
  }
  textEl.innerHTML = renderText(text);

  wrapper.append(textEl);
  messagesEl.append(wrapper);

  scrollToBottom();
  return { wrapper, textEl };
}
