// chat.ts sends messages and streams the agent reply.

import * as api from "../core/api";
import { attachBtnEl, continueBtn, inputEl, pauseBtn, sendBtn, sessionLabelEl, workspaceInputEl, workspaceLabelEl } from "../core/dom";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { setStatus } from "../ui/status";
import { addTurn, dismissGreeting, initMessageLinks } from "./chatView";
import { handleMentionKey, expandMentions } from "./mentions";
import { openFilePicker } from "./filePicker";
import { renderSessions } from "./sessions";
import { handleSlashKey, routeSlash, setSlashRunner, type AgentRoute } from "./slash";
import { initMode, setMode, toggleMode } from "./mode";
import { currentPreset, initPermissions, keepDraftFor } from "./permissions";
import { dropFinishedPanel } from "./todos";
import { handleSkillKey, parseSkillLine, refreshSkillsCache, takeAttached } from "./skills";
import { isBusy, noteSubmitted, syncComposer } from "./live";
import { noteSent } from "./undo";
import { setWorktree, takeWorktreeRequest } from "./worktrees";
import { shortPath } from "../core/format";

// TurnPlan is what a composer line resolves to before it is sent.
interface TurnPlan {
  skill: string | null;
  // display is the text shown in the user bubble.
  display: string;
}

// planTurn resolves the skill and the bubble text for a non-slash line
// (route null) or a slash line that becomes a special agent turn.
function planTurn(message: string, route: AgentRoute | null, resume: boolean): TurnPlan {
  if (route) {
    const skill = route.compact || route.skill ? (route.skill ?? null) : takeAttached();
    return { skill, display: route.display };
  }
  if (resume) {
    return { skill: null, display: message };
  }
  const parsed = parseSkillLine(message);
  if (!parsed) {
    return { skill: takeAttached(), display: message };
  }
  // A hand-typed `/skill <name> <text>` attaches like the picker.
  // An empty message is allowed: the skill alone is the turn.
  takeAttached();
  return { skill: parsed.name, display: parsed.message || `/skill ${parsed.name}` };
}

// turnMode picks the agent mode: /compact has none, a prompt command
// keeps the sidecar default unless the route forces one.
function turnMode(route: AgentRoute | null): "build" | "plan" | undefined {
  if (route?.compact) {
    return undefined;
  }
  return route?.mode ?? (route?.command ? undefined : state.mode);
}

// ensureSession opens a session before the first message: the client
// owns the session id.
async function ensureSession(title: string): Promise<void> {
  if (state.sessionId) {
    return;
  }
  const opened = await api.openSession(null, state.cwd, takeWorktreeRequest(title));
  state.sessionId = opened.sessionId;
  if (opened.worktree) {
    // The session works in its own checkout from the first turn.
    state.cwd = opened.cwd;
    workspaceLabelEl.textContent = shortPath(state.cwd);
    workspaceInputEl.value = state.cwd;
    setWorktree(opened.worktree);
  }
  keepDraftFor(state.sessionId);
  sessionLabelEl.textContent = state.sessionId.slice(0, 8);
  renderSessions();
}

export async function send(resume = false): Promise<void> {
  const message = resume ? "" : inputEl.value.trim();
  if (!message && !resume) {
    return;
  }

  // Slash lines either run locally (never reaching the agent) or become
  // special turns: prompt commands, /compact, /plan <task>, /<skill>.
  // `/skill <name> …` routes to the agent with the name sent separately;
  // the sidecar injects SKILL.md before the LLM call.
  let route: AgentRoute | null = null;
  if (!resume) {
    const routed = await routeSlash(message);
    if (routed === "local") {
      autosizeComposer();
      return;
    }
    if (routed !== "none") {
      route = routed;
    }
  }
  // A new user message retires a fully-landed plan: the panel goes
  // empty at once and the agent plans the new work if it is multi-step.
  if (!resume && !route?.compact) {
    dropFinishedPanel();
  }
  const { skill, display } = planTurn(message, route, resume);

  // Hand-typed @paths resolve to OS-specific absolute paths here;
  // popup-accepted rows are already absolute. The bubble keeps the
  // original text (display) so the chat shows what was typed.
  let outgoing = "";
  if (!resume) {
    inputEl.value = "";
    autosizeComposer();
    try {
      outgoing = await expandMentions(route ? route.message : display);
    } catch (error) {
      console.warn(`expandMentions failed: ${String(error)}`);
      outgoing = display;
    }
  } else {
    continueBtn.classList.add("hidden");
  }

  try {
    await ensureSession(display || message);
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
      command: route?.command ?? null,
      compact: !!route?.compact,
      mode: turnMode(route),
      permissions: currentPreset(),
    });
    noteSubmitted(info);
    noteSent(state.sessionId);
  } catch (error) {
    if (!resume && !inputEl.value && !route?.compact) {
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
    // An approval card that took focus mid-submit keeps it.
    if (!document.activeElement?.closest(".approval-card")) inputEl.focus();
  }
}

// requestStop asks the running turn to stop after what already streamed.
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
export function initChat(): void {
  initMessageLinks();
  setSlashRunner(() => void send());
  initPermissions();
  initMode(() => {
    // Implement keeps whatever draft the user has in the composer.
    const draft = inputEl.value;
    setMode("build");
    inputEl.value = "Implement the plan above.";
    void send().finally(() => {
      if (draft && !inputEl.value) {
        inputEl.value = draft;
        autosizeComposer();
      }
    });
  });
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
    if (event.key === "Tab" && event.shiftKey) {
      event.preventDefault();
      toggleMode();
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
