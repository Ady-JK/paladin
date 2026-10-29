// chat.ts sends messages and streams the agent reply.
// It supports the desktop app frontend.

import * as api from "../core/api";
import { attachBtnEl, continueBtn, inputEl, pauseBtn, sendBtn, sessionLabelEl } from "../core/dom";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { setStatus } from "../ui/status";
import { addTurn, dismissGreeting, initMessageLinks } from "./chatView";
import { handleMentionKey, expandMentions } from "./mentions";
import { openFilePicker } from "./filePicker";
import { renderSessions } from "./sessions";
import { handleSlashKey, tryHandleSlash } from "./slash";
import { dropFinishedPanel } from "./todos";
import { handleSkillKey, parseSkillLine, refreshSkillsCache, takeAttached } from "./skills";
import { isBusy, noteSubmitted, syncComposer } from "./live";

export async function send(resume = false): Promise<void> {
  const message = resume ? "" : inputEl.value.trim();
  if (!message && !resume) {
    return;
  }

  // Slash commands never reach the agent: they run locally.
  // `/skill` is the exception: it routes to the agent with the skill
  // name sent separately — the sidecar injects the SKILL.md content
  // before the LLM call, so the content never lands in the textarea.
  if (!resume && tryHandleSlash(message)) {
    return;
  }
  // A new user message retires a fully-landed plan: the panel goes
  // empty at once and the agent plans the new work if it is multi-step.
  if (!resume) {
    dropFinishedPanel();
  }
  let skill: string | null = null;
  let display = message;
  if (!resume) {
    const parsed = parseSkillLine(message);
    if (parsed) {
      skill = parsed.name;
      // A hand-typed `/skill <name> <text>` attaches like the picker.
      // An empty message is allowed: the skill alone is the turn.
      takeAttached();
      display = parsed.message;
      if (!display) {
        display = `/skill ${skill}`;
      }
    } else {
      skill = takeAttached();
    }
  }

  // Hand-typed @paths resolve to OS-specific absolute paths here;
  // popup-accepted rows are already absolute. The bubble keeps the
  // original text (display) so the chat shows what was typed.
  let outgoing = "";
  if (!resume) {
    inputEl.value = "";
    autosizeComposer();
    try {
      outgoing = await expandMentions(display);
    } catch (error) {
      console.warn(`expandMentions failed: ${String(error)}`);
      outgoing = display;
    }
  } else {
    continueBtn.classList.add("hidden");
  }

  try {
    // The client owns the session: open one before the first message.
    if (!state.sessionId) {
      const opened = await api.openSession(null, state.cwd);
      state.sessionId = opened.sessionId;
      sessionLabelEl.textContent = state.sessionId.slice(0, 8);
      renderSessions();
    }
    dismissGreeting();
    // Fire and forget: the turn runs (or queues) in the sidecar and its
    // progress arrives as events for this session, even if the user
    // switches away or reloads.
    const info = await api.submitChat({
      sessionId: state.sessionId,
      message: outgoing,
      resume,
      cwd: state.cwd,
      provider: state.selectedProvider || null,
      model: state.selectedModel || null,
      skill,
      display: resume ? "" : display,
    });
    noteSubmitted(info);
    state.shownMessages++;
  } catch (error) {
    if (!resume && !inputEl.value) {
      inputEl.value = message;
      autosizeComposer();
    }
    if (skill && /not found: skill/i.test(String(error))) {
      refreshSkillsCache();
    }
    addTurn("error", String(error));
    setStatus(String(error), "error");
    syncComposer();
  } finally {
    inputEl.focus();
  }
}

// requestStop asks the running turn to stop after what already streamed.
// It supports the desktop app frontend.
export function requestStop(): void {
  if (!state.sessionId) {
    return;
  }
  toast("Agent Stopped");
  // Stops the running turn and drops the turns queued behind it.
  void api.stopChat(state.sessionId, state.cwd || null).catch((error) => {
    setStatus(String(error), "error");
  });
}

// initChat wires the composer and the stop affordances.
// It supports the desktop app frontend.
export function initChat(): void {
  initMessageLinks();
  attachBtnEl.addEventListener("click", () => {
    openFilePicker();
  });
  inputEl.addEventListener("input", autosizeComposer);

  inputEl.addEventListener("keydown", (event) => {
    // Popups first: Enter/Tab/Esc/Arrows belong to them while open.
    // Skill before slash: `/skill …` matches both prefixes, and the
    // skill picker is the specific one.
    if (handleSkillKey(event)) {
      return;
    }
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
    if (event.key !== "Escape" || !isBusy(state.sessionId)) {
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

// autosizeComposer grows the composer with its content, capped at
// ~35% of the viewport so long pastes stay usable and the chat above
// keeps its space. Reset by clearing the value then calling again.
export function autosizeComposer(): void {
  inputEl.style.height = "auto";
  const cap = Math.round(window.innerHeight * 0.35);
  inputEl.style.height = `${Math.min(inputEl.scrollHeight, cap)}px`;
  inputEl.style.overflowY = inputEl.scrollHeight > cap ? "auto" : "hidden";
}
