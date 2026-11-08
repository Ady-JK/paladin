import * as api from "../../core/api";
import { escapeAttr, escapeHtml } from "../../core/format";
import { state } from "../../core/state";
import { icon } from "../../icons";
import { toast } from "../../ui/toast";
import { abortAttach, beginAttach, finishAttach, liveBadge, liveRoot, repaintLive } from "../live";
import { agentStatus, agentStatusHtml, openSession, refreshSessions } from "../sessions";
import { buildHistory } from "../sessions/history";
import type { AgentTab } from "./types";
import { errorText } from "./utils";

function agentHead(tab: AgentTab): string {
  const child = state.sessions.find((s) => s.sessionId === tab.agentId);
  const sub = child?.subagent;
  const status = child && sub ? agentStatus(child) : liveBadge(tab.agentId) === "running" ? "running" : "completed";
  const stoppable = status === "running" || status === "queued";
  return (
    `<div class="pane-agent-title">` +
    `<span class="child-ico">${icon("rl-agent", "h-3.5 w-3.5")}</span>` +
    `<span class="pane-agent-name">${escapeHtml(sub?.name || tab.title)}</span>` +
    (sub ? `<span class="agent-type">${escapeHtml(sub.type)}</span>` : "") +
    agentStatusHtml(status) +
    `<span class="pane-agent-gap"></span>` +
    `<button type="button" class="pane-agent-stop"${stoppable ? "" : " hidden"} title="Stop this subagent">${icon("pause", "h-3 w-3")}<span>Stop</span></button>` +
    `<button type="button" class="pane-agent-open" title="Open in the main view">${icon("external", "h-3 w-3")}<span>Open</span></button>` +
    `</div>` +
    (sub?.task ? `<div class="pane-agent-task" title="${escapeAttr(sub.task)}">${escapeHtml(sub.task)}</div>` : "") +
    (sub?.error ? `<div class="pane-agent-error">${escapeHtml(sub.error)}</div>` : "")
  );
}

export function createAgentTab(agentId: string, key: string, closePane: () => void): AgentTab {
  const child = state.sessions.find((s) => s.sessionId === agentId);
  const body = document.createElement("div");
  body.className = "pane-agent";
  body.dataset.agent = agentId;
  body.innerHTML =
    `<div class="pane-agent-head"></div>` +
    `<div class="pane-agent-body"><div class="pane-agent-history"></div></div>` +
    `<form class="pane-agent-ask" hidden>` +
    `<textarea class="pane-agent-input" rows="1" aria-label="Message this subagent" placeholder="Message this subagent while it runs…"></textarea>` +
    `<button type="submit" class="pane-agent-send" title="Send into the running turn" aria-label="Send into the running turn">${icon("arrow-up", "h-3.5 w-3.5")}</button>` +
    `</form>` +
    `<div class="pane-agent-foot">Driven by its parent agent · shares the parent's workspace</div>`;
  const tab: AgentTab = {
    kind: "agent",
    key,
    agentId,
    title: child?.subagent?.name || child?.title || agentId.slice(0, 8),
    body,
    loading: null,
    head: "",
  };
  const parentId = child?.subagent?.parentId || state.sessionId;
  body.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest(".pane-agent-open")) {
      // Full view and back: the pane would otherwise reopen a blank
      // side chat for the child's own session.
      closePane();
      void openSession(agentId);
    } else if (target.closest(".pane-agent-stop")) {
      api
        .interruptAgent(parentId, agentId)
        .catch((error: unknown) => toast(`Could not stop: ${errorText(error)}`, "error"));
    }
  });
  const ask = body.querySelector<HTMLFormElement>(".pane-agent-ask")!;
  const input = body.querySelector<HTMLTextAreaElement>(".pane-agent-input")!;
  ask.addEventListener("submit", (event) => {
    event.preventDefault();
    void sendAgentMessage(agentId, input, ask);
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      ask.requestSubmit();
    }
  });
  return tab;
}

// sendAgentMessage steers the child's running turn, the same path the
// parent's send_message takes. It is only offered while the child runs:
// a finished child is read-only from here.
async function sendAgentMessage(agentId: string, input: HTMLTextAreaElement, form: HTMLFormElement): Promise<void> {
  const text = input.value.trim();
  if (!text) return;
  const send = form.querySelector<HTMLButtonElement>(".pane-agent-send")!;
  send.disabled = true;
  try {
    await api.steerChat(agentId, text, text);
    input.value = "";
    input.focus();
  } catch (error) {
    toast(`Could not reach the subagent: ${errorText(error)}`, "error");
    void refreshSessions();
  } finally {
    send.disabled = false;
  }
}

// loadAgent shows the child's history and then its live rows, which
// keep streaming. The main view can borrow the live rows; the tab takes
// them back (re-attaching) when it is shown again.
export function loadAgent(tab: AgentTab): Promise<void> {
  const root = liveRoot(tab.agentId);
  const view = tab.body.querySelector<HTMLElement>(".pane-agent-body")!;
  if (root.parentElement === view || tab.loading) return tab.loading ?? Promise.resolve();
  tab.loading = (async () => {
    beginAttach(tab.agentId);
    try {
      const snap = await api.attachSession(tab.agentId, 1000);
      const history = view.querySelector<HTMLElement>(".pane-agent-history")!;
      history.replaceChildren();
      const rows = Array.isArray(snap.history) ? snap.history : (snap.history?.messages ?? []);
      buildHistory(history, rows, false, tab.agentId);
      finishAttach(tab.agentId, snap);
      if (state.sessionId !== tab.agentId) view.append(root);
      view.scrollTop = view.scrollHeight;
      repaintLive(tab.agentId);
      paintHead(tab);
    } catch (error) {
      abortAttach(tab.agentId);
      view.querySelector(".pane-agent-history")!.innerHTML =
        `<div class="side-error" role="alert">${escapeHtml(errorText(error))}</div>`;
    } finally {
      tab.loading = null;
    }
  })();
  return tab.loading;
}

// paintHead repaints only on change: live events repaint often, and a
// replaced button would swallow a click in progress.
export function paintHead(tab: AgentTab): void {
  const child = state.sessions.find((s) => s.sessionId === tab.agentId);
  const status =
    child && child.subagent ? agentStatus(child) : liveBadge(tab.agentId) === "running" ? "running" : "completed";
  const html = agentHead(tab);
  if (html !== tab.head) {
    tab.head = html;
    tab.body.querySelector(".pane-agent-head")!.innerHTML = html;
  }
  // The composer is the user's line into the running turn, so it exists
  // only while the child runs; a finished child reads as history.
  const ask = tab.body.querySelector<HTMLFormElement>(".pane-agent-ask");
  if (!ask) return;
  const running = status === "running";
  ask.hidden = !running;
  const input = ask.querySelector<HTMLTextAreaElement>(".pane-agent-input")!;
  input.disabled = !running;
  const foot = tab.body.querySelector<HTMLElement>(".pane-agent-foot");
  if (foot) {
    foot.textContent = running
      ? "You can guide this subagent while it runs · shares the parent's workspace"
      : "Finished · read-only · shares the parent's workspace";
  }
}
