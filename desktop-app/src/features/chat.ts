// chat.ts sends messages and streams the agent reply.
// It supports the desktop app frontend.

import * as api from "../core/api";
import { chatViewEl, continueBtn, inputEl, pauseBtn, sendBtn, sessionLabelEl, statusDetailEl } from "../core/dom";
import { renderText } from "../core/format";
import { state } from "../core/state";
import type { ToolView } from "../core/types";
import { toast } from "../ui/toast";
import { setStatus } from "../ui/status";
import { icon } from "../icons";
import { addTurn, scrollToBottom } from "./chatView";
import { handleMentionKey, expandMentions } from "./mentions";
import { refreshSessions } from "./sessions";
import { handleSlashKey, tryHandleSlash } from "./slash";
import { refreshUsage } from "./usage";
import { createToolActivity } from "./tools";

export async function send(resume = false): Promise<void> {
  const message = resume ? "" : inputEl.value.trim();
  if (!message && !resume) {
    return;
  }
  if (state.busy) {
    return;
  }

  // Slash commands never reach the agent: they run locally.
  if (!resume && tryHandleSlash(message)) {
    return;
  }

  if (!resume) {
    // Hand-typed @paths resolve to OS-specific absolute paths here;
    // popup-accepted rows are already absolute. The user turn keeps
    // the original text so the chat shows what was typed.
    state.busy = true;
    sendBtn.disabled = true;
    sendBtn.classList.add("hidden");
    pauseBtn.classList.remove("hidden");
    setStatus("thinking…", "busy");
    let outgoing: string;
    try {
      outgoing = await expandMentions(message);
    } catch (error) {
      console.warn(`expandMentions failed: ${String(error)}`);
      outgoing = message;
    }
    inputEl.value = "";
    addTurn("user", message);
    state.shownMessages++;
    return sendExpanded(outgoing, false);
  }
  continueBtn.classList.add("hidden");

  state.busy = true;
  sendBtn.disabled = true;
  sendBtn.classList.add("hidden");
  pauseBtn.classList.remove("hidden");
  setStatus("thinking…", "busy");
  return sendExpanded("", true);
}

// sendExpanded runs the streaming turn for already-prepared text.
async function sendExpanded(outgoing: string, resume: boolean): Promise<void> {

  const { wrapper, textEl: body } = addTurn("agent", "");
  body.innerHTML = `<span class="text-slate-400">${icon("bolt", "h-3 w-3 inline")} thinking…</span>`;

  let reply = "";

  let segment = "";
  let segEl = body;
  const container = body.parentElement!;

  const newSegment = (after: Node): HTMLElement => {
    const el = document.createElement("div");
    el.className =
      "whitespace-pre-wrap break-words leading-relaxed text-slate-700 dark:text-slate-300";
    container.insertBefore(el, after.nextSibling);
    return el;
  };

  // Deltas arrive in bursts (dozens per frame). Painting full innerHTML
  // per event is O(n^2) over a reply; coalesce to one paint per frame and
  // only force-scroll while the user is already pinned to the bottom.
  let paintQueued = false;
  let scrollQueued = false;
  let stickToBottom = true;
  const onScroll = (): void => {
    const gap = chatViewEl.scrollHeight - chatViewEl.scrollTop - chatViewEl.clientHeight;
    stickToBottom = gap < 96;
  };
  chatViewEl.addEventListener("scroll", onScroll);
  const gatedScroll = (): void => {
    if (!stickToBottom || scrollQueued) {
      return;
    }
    scrollQueued = true;
    requestAnimationFrame(() => {
      scrollQueued = false;
      if (stickToBottom) {
        scrollToBottom();
      }
    });
  };

  const unlisten = api.onChatDelta((delta) => {
    reply += delta;
    segment += delta;
    if (paintQueued) {
      return;
    }
    paintQueued = true;
    requestAnimationFrame(() => {
      paintQueued = false;
      segEl.innerHTML = renderText(segment);
      gatedScroll();
    });
  });

  const toolViews = new Map<string, ToolView>();

  const unlistenTool = api.onChatTool((tool) => {
    const existing = toolViews.get(tool.id);

    if (existing) {
      existing.update(tool);
    } else {
      const row = document.createElement("div");
      row.className = "py-0.5";

      const view = createToolActivity(tool);
      row.append(view.el);
      container.insertBefore(row, segEl.nextSibling);
      toolViews.set(tool.id, view);

      segment = "";
      segEl = newSegment(row);
    }

    gatedScroll();
  });

  try {
    // The client owns the session: /chat only loads it, so open one
    // before the first message of a conversation.
    if (!state.sessionId) {
      const opened = await api.openSession(null, state.cwd);
      state.sessionId = opened.sessionId;
      sessionLabelEl.textContent = state.sessionId.slice(0, 8);
    }

    const result = await api.chatStream({
      sessionId: state.sessionId || null,
      message: outgoing,
      resume,
      cwd: state.cwd,
      provider: state.selectedProvider || null,
      model: state.selectedModel || null,
      systemPrompt: null,
    });

    if (!state.sessionId && result.sessionId) {
      state.sessionId = result.sessionId;
      sessionLabelEl.textContent = state.sessionId.slice(0, 8);
    }

    if (!reply) {
      reply = result.reply;
      segment = result.reply;
      segEl.innerHTML = renderText(segment);
    }

    state.shownMessages++;

    if (result.stopped) {
      setStatus("stopped", "ok");
      statusDetailEl.textContent = "STOPPED";
    } else if (result.needsContinue) {
      continueBtn.classList.remove("hidden");
      toast("Paused at the round cap — continue?");
      setStatus("paused", "ok");
      statusDetailEl.textContent = "PAUSED";
    } else {
      setStatus("ready", "ok");
      statusDetailEl.textContent = "READY";
    }

    await refreshSessions();
    // Tokens bill even on failed turns, so the pill refreshes on both
    // paths; refreshUsage keeps the last reading on fetch errors.
    void refreshUsage();
  } catch (error) {
    state.shownMessages--;

    if (!reply.trim() && toolViews.size === 0) {
      wrapper.remove();
    } else if (!reply.trim()) {
      segEl.innerHTML = `<span class="text-slate-400">interrupted — the tool calls above already ran</span>`;
    }

    addTurn("error", String(error));
    setStatus(String(error), "error");
    void refreshUsage();
  } finally {
    unlisten();
    unlistenTool();
    chatViewEl.removeEventListener("scroll", onScroll);
    state.busy = false;
    sendBtn.disabled = false;
    sendBtn.classList.remove("hidden");
    pauseBtn.classList.add("hidden");
    inputEl.focus();
  }
}

// requestStop asks the running turn to stop after what already streamed.
// It supports the desktop app frontend.
export function requestStop(): void {
  toast("Agent Stopped");
  void api.stopChat(state.sessionId || null, state.cwd || null);
}

// initChat wires the composer and the stop affordances.
// It supports the desktop app frontend.
export function initChat(): void {
  inputEl.addEventListener("keydown", (event) => {
    // Popups first: Enter/Tab/Esc/Arrows belong to them while open.
    if (handleSlashKey(event)) {
      return;
    }
    if (handleMentionKey(event)) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void send();
    }
  });

  sendBtn.addEventListener("click", () => void send());
  pauseBtn.addEventListener("click", () => {
    state.escArmedAt = 0;
    requestStop();
  });
  continueBtn.addEventListener("click", () => void send(true));

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !state.busy) {
      return;
    }
    const now = Date.now();
    if (now - state.escArmedAt < 2500) {
      state.escArmedAt = 0;
      requestStop();
    } else {
      state.escArmedAt = now;
      toast("Press ESC again to stop the turn");
    }
  });
}
