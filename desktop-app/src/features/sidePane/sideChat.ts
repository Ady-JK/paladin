import * as api from "../../core/api";
import { escapeHtml, renderMarkdown } from "../../core/format";
import { state } from "../../core/state";
import type { SideChatMessage } from "../../core/types";
import { icon } from "../../icons";
import type { ChatTab, Tab } from "./types";
import { clip, errorText } from "./utils";

function chatMessage(msg: Pick<SideChatMessage, "role" | "content" | "model">): HTMLElement {
  const el = document.createElement("div");
  el.className = "side-msg";
  el.dataset.role = msg.role;
  el.dataset.state = "done";
  if (msg.role === "user") {
    el.textContent = msg.content;
  } else {
    el.innerHTML =
      `<div class="md">${renderMarkdown(msg.content)}</div>` +
      (msg.model ? `<div class="side-model">${escapeHtml(msg.model)}</div>` : "");
  }
  return el;
}

export function createChatTab(
  key: string,
  paintTabs: () => void,
  chatId: string | null = null,
  title = "Side chat",
  messages: SideChatMessage[] = [],
): ChatTab {
  const body = document.createElement("div");
  body.className = "side-chat";
  body.innerHTML =
    `<div class="side-msgs" role="log" aria-live="polite">` +
    `<div class="side-empty"${messages.length ? " hidden" : ""}>` +
    `<span class="side-empty-ico">${icon("comments", "h-5 w-5")}</span>` +
    `<p class="side-empty-title">Side chat</p>` +
    `<p class="side-empty-text">Ask about this conversation without adding to it. Answers use no tools.</p>` +
    `</div></div>` +
    `<form class="side-composer">` +
    `<textarea class="side-input" rows="1" placeholder="Ask a side question…" aria-label="Side question"></textarea>` +
    `<button type="submit" class="side-send" title="Send" aria-label="Send">${icon("arrow-up", "h-3.5 w-3.5")}</button>` +
    `</form>`;
  const list = body.querySelector<HTMLElement>(".side-msgs")!;
  for (const msg of messages) list.append(chatMessage(msg));
  const tab: ChatTab = { kind: "chat", key, chatId, title, body, busy: false };
  if (chatId) body.dataset.chat = chatId;

  const input = body.querySelector<HTMLTextAreaElement>(".side-input")!;
  const send = () => {
    const question = input.value.trim();
    if (!question || tab.busy) return;
    input.value = "";
    void sendChat(tab, question, paintTabs);
  };
  body.querySelector("form")!.addEventListener("submit", (event) => {
    event.preventDefault();
    send();
  });
  input.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      send();
    }
  });
  return tab;
}

export function isBlankChat(tab: Tab | undefined): tab is ChatTab {
  return tab?.kind === "chat" && !tab.chatId && !tab.busy && !tab.body.querySelector(".side-msg");
}

// sendChat asks one side question in tab's chat. The chat belongs to
// the session it was asked in, even if the user moves on meanwhile.
export async function sendChat(tab: ChatTab, question: string, paintTabs: () => void): Promise<void> {
  const sessionId = state.sessionId;
  const cwd = state.cwd || null;
  const list = tab.body.querySelector<HTMLElement>(".side-msgs")!;
  const sendBtn = tab.body.querySelector<HTMLButtonElement>(".side-send")!;
  list.querySelector(".side-empty")?.setAttribute("hidden", "");
  list.querySelector(".side-error")?.remove();
  list.append(chatMessage({ role: "user", content: question }));
  const pending = document.createElement("div");
  pending.className = "side-msg";
  pending.dataset.role = "assistant";
  pending.dataset.state = "pending";
  pending.innerHTML = `<span class="btw-spinner" aria-hidden="true"></span><span class="side-wait">Thinking…</span>`;
  list.append(pending);
  list.scrollTop = list.scrollHeight;
  if (!tab.chatId && tab.title === "Side chat") {
    tab.title = clip(question);
    paintTabs();
  }
  tab.busy = true;
  sendBtn.disabled = true;
  try {
    const res = await api.sideQuestion(
      sessionId,
      cwd,
      question,
      state.selectedProvider || null,
      state.selectedModel || null,
      tab.chatId,
    );
    tab.chatId = res.chatId || tab.chatId;
    if (tab.chatId) tab.body.dataset.chat = tab.chatId;
    pending.replaceWith(chatMessage({ role: "assistant", content: res.answer, model: res.model }));
  } catch (error) {
    const err = document.createElement("div");
    err.className = "side-error";
    err.setAttribute("role", "alert");
    err.textContent = errorText(error);
    pending.replaceWith(err);
  } finally {
    tab.busy = false;
    sendBtn.disabled = false;
    list.scrollTop = list.scrollHeight;
    paintTabs();
  }
}
