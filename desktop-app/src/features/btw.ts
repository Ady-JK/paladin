// btw.ts runs `/btw <question>`: a side question about the current
// conversation, answered in a dismissible card. It works while a turn
// is running, never joins the history and is gone on reload.

import * as api from "../core/api";
import { messagesEl } from "../core/dom";
import { escapeHtml, renderMarkdown } from "../core/format";
import { state } from "../core/state";
import { icon } from "../icons";
import { dismissGreeting, followTail, scrollToBottom } from "./chatView";

// askSideQuestion adds a card for the question and fills it in when
// the answer lands. The card belongs to the session it was asked in.
export async function askSideQuestion(question: string): Promise<void> {
  const sessionId = state.sessionId;
  const card = document.createElement("div");
  card.className = "btw-card";
  card.dataset.state = "pending";
  card.dataset.sessionId = sessionId;
  card.innerHTML =
    `<div class="btw-head">` +
    `<span class="btw-ico">${icon("comments", "h-3.5 w-3.5")}</span>` +
    `<span class="btw-tag">btw</span>` +
    `<span class="btw-question">${escapeHtml(question)}</span>` +
    `<button type="button" class="btw-close" aria-label="Dismiss" title="Dismiss">${icon("xmark", "h-3 w-3")}</button>` +
    `</div>` +
    `<div class="btw-body"><span class="btw-spinner" aria-hidden="true"></span><span class="btw-wait">Thinking…</span></div>`;
  card.querySelector(".btw-close")!.addEventListener("click", () => card.remove());
  dismissGreeting();
  messagesEl.append(card);
  scrollToBottom();

  const body = card.querySelector<HTMLElement>(".btw-body")!;
  try {
    const res = await api.sideQuestion(
      sessionId,
      state.cwd || null,
      question,
      state.selectedProvider || null,
      state.selectedModel || null,
    );
    card.dataset.state = "done";
    body.innerHTML = `<div class="btw-answer md">${renderMarkdown(res.answer)}</div>`;
    if (res.model)
      card
        .querySelector(".btw-head")!
        .insertAdjacentHTML("beforeend", `<span class="btw-model">${escapeHtml(res.model)}</span>`);
  } catch (error) {
    card.dataset.state = "error";
    body.innerHTML = `<div class="btw-error">${escapeHtml(String(error instanceof Error ? error.message : error).replace(/^Error:\s*/, ""))}</div>`;
  }
  // Keep the close button last in the header.
  const head = card.querySelector(".btw-head")!;
  head.append(head.querySelector(".btw-close")!);
  if (card.isConnected) followTail();
}
