// chatView.ts renders the conversation turns.
// It supports the desktop app frontend.

import { chatViewEl, messagesEl } from "../core/dom";
import { escapeHtml, renderText } from "../core/format";
import { state } from "../core/state";
import type { Turn } from "../core/types";
import { icon } from "../icons";

// scrollToBottom is a function in this module.
// It supports the desktop app frontend.
export function scrollToBottom(): void {
  chatViewEl.scrollTop = chatViewEl.scrollHeight;
}

// renderGreeting is a function in this module.
// It supports the desktop app frontend.
export function renderGreeting(): void {
  if (messagesEl.childElementCount > 0) {
    return;
  }

  const wrapper = document.createElement("div");
  wrapper.className = "mx-auto flex max-w-4xl gap-3 text-xs";

  wrapper.innerHTML = `
    <div class="avatar avatar-agent">${icon("robot", "h-3 w-3")}</div>
    <div class="min-w-0 flex-1 space-y-1">
      <div class="flex items-center gap-2">
        <span class="font-semibold text-slate-900 dark:text-slate-100">core-agent</span>
        <span class="text-[10px] text-slate-400">now</span>
      </div>
      <p class="leading-relaxed text-slate-600 dark:text-slate-300">
        The harness is connected to the workspace
        <code class="rounded bg-slate-200 px-1 py-0.5 font-mono text-[11px] dark:bg-slate-800">${escapeHtml(
          state.cwd || "workspace",
        )}</code>.
        I am ready to read the code, answer questions and write code. What are we building today?
      </p>
    </div>`;

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
  wrapper.className = "mx-auto flex max-w-4xl gap-3 text-xs";

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
    <span class="font-semibold ${isError ? "text-red-600 dark:text-red-400" : "text-slate-900 dark:text-slate-100"}">${isUser ? "You" : isError ? "error" : "core-agent"}</span>
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
