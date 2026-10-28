// sessions.ts owns the session list and transcripts.
// It renders the sidebar and loads history. No event stream in v1;

import * as api from "../core/api";
import {
  confirmModalEl,
  confirmTitleEl,
  inputEl,
  messagesEl,
  modelSelectLabelEl,
  searchInputEl,
  sessionLabelEl,
  sessionListEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { dayOf, escapeHtml, renderText, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import type { SessionEvent, ToolInfo } from "../core/types";
import { toast } from "../ui/toast";
import { closeModal, openModal } from "../ui/anim";
import { setStatus } from "../ui/status";
import { icon } from "../icons";
import { addTurn, renderGreeting, scrollToBottom } from "./chatView";
import { createToolActivity } from "./tools";
import { clearUsage, refreshUsage } from "./usage";
import { syncModelSelects } from "./models";
import { refreshExternalVisibility } from "./sites";

export async function onSessionEvent(event: SessionEvent): Promise<void> {
  // Slow-consumer recovery: the core counts drops per subscriber and tags
  // the next delivered event with missed > 0. A gapped stream cannot be
  // trusted, so refetch the list and the open transcript instead of applying
  // the single event.
  if (event.missed && event.missed > 0) {
    console.warn(
      `session stream gapped, missed ${event.missed} event(s); refetching`,
    );
    await refreshSessions();
    if (state.sessionId) {
      try {
        await loadHistory(state.sessionId);
      } catch (error) {
        console.warn(`refetch after gap failed: ${String(error)}`);
      }
    }
    return;
  }

  if (event.kind === "message_added") {
    if (event.sessionId === state.sessionId && event.messageCount > state.shownMessages) {
      try {
        await loadHistory(event.sessionId);
      } catch (error) {
        console.warn(`loadHistory failed: ${String(error)}`);
      }
    }
    return;
  }

  await refreshSessions();
}

export async function refreshSessions(): Promise<void> {
  // The sidebar shows every workspace's sessions, not just the current
  // folder's: filtering by cwd hid old sessions whenever the workspace
  // changed (e.g. after New Session picked another folder).
  try {
    state.sessions = await api.listSessions(null, 100);
  } catch (error) {
    console.warn(`refreshSessions failed: ${String(error)}`);
    state.sessions = [];
  }

  renderSessions();
}

// renderSessions is a function in this module.
// It supports the desktop app frontend.
export function renderSessions(): void {
  const filter = searchInputEl.value.trim().toLowerCase();

  const visible = filter
    ? state.sessions.filter((s) => s.title.toLowerCase().includes(filter))
    : state.sessions;

  if (visible.length === 0) {
    sessionListEl.innerHTML = `<p class="px-2 py-1 text-slate-400">${
      filter ? "no session matches" : "no sessions yet"
    }</p>`;
    return;
  }

  const groups = new Map<string, typeof state.sessions>();
  for (const session of visible) {
    const day = dayOf(session.createdAt);
    const group = groups.get(day) ?? [];
    group.push(session);
    groups.set(day, group);
  }

  let html = "";
  for (const [day, group] of groups) {
    html += `<div class="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-slate-400">${day}</div><div class="mt-1 space-y-0.5">`;

    for (const session of group) {
      const active = session.sessionId === state.sessionId;
      const title = session.title || "untitled session";

      html += `
        <div class="session-row group flex items-center gap-1 rounded-md border p-1 ${
          active
            ? "border-indigo-200 bg-indigo-50/80 text-indigo-900 dark:border-indigo-800 dark:bg-indigo-950/40 dark:text-indigo-200"
            : "border-transparent text-slate-700 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800/60"
        }">
          <button
            data-session="${session.sessionId}"
            class="flex min-w-0 flex-1 items-center gap-2 px-1 py-0.5 text-left"
            title="${escapeHtml(title)}"
          >
            ${icon("terminal", "h-3 w-3 flex-shrink-0 text-accent-500")}
            <span class="truncate font-medium">${escapeHtml(title)}</span>
          </button>
          <span class="flex-shrink-0 rounded border border-slate-200 bg-slate-100 px-1.5 py-0.5 font-mono text-[9px] text-slate-400 dark:border-slate-700/60 dark:bg-slate-800">${session.messageCount}</span>
          <button
            data-delete="${session.sessionId}"
            class="flex-shrink-0 rounded p-1 text-slate-400 opacity-40 hover:bg-agent-rose/10 hover:text-agent-rose hover:opacity-100 focus:opacity-100"
            title="Delete the session"
          >
            ${icon("trash", "h-3 w-3")}
          </button>
        </div>`;
    }

    html += "</div>";
  }

  sessionListEl.innerHTML = html;
}

// askDelete is a function in this module.
// It supports the desktop app frontend.
function askDelete(id: string): void {
  const session = state.sessions.find((s) => s.sessionId === id);

  state.pendingDelete = id;
  confirmTitleEl.textContent = session?.title || "this session";

  openModal(confirmModalEl);
  void refreshExternalVisibility();
}

// closeConfirm is a function in this module.
// It supports the desktop app frontend.
export function closeConfirm(): void {
  closeModal(confirmModalEl);
  state.pendingDelete = "";
  void refreshExternalVisibility();
}

export async function deleteSession(id: string): Promise<void> {
  if (!id) {
    return;
  }

  const session = state.sessions.find((s) => s.sessionId === id);
  const title = session?.title || "the session";

  const before = state.sessions;
  state.sessions = state.sessions.filter((s) => s.sessionId !== id);
  renderSessions();

  try {
    // Null cwd searches every workspace namespace, so sessions from
    // other folders open instead of 404ing on a cwd mismatch.
    const deleted = await api.deleteSession(id, null);

    if (!deleted) {
      throw new Error("the core did not delete the session");
    }
  } catch (error) {
    state.sessions = before;
    renderSessions();

    const message = String(error);
    setStatus(message, "error");
    toast(`Could not delete ${title}: ${message}`, "error");
    return;
  }

  toast(`Deleted ${title}`);

  if (id === state.sessionId) {
    newSession();
  }
}

export async function openSession(id: string): Promise<void> {
  try {
    // Null cwd searches every workspace namespace; the session brings
    // its own cwd with it, which becomes the new working folder.
    const session = await api.openSession(id, null);

    state.sessionId = session.sessionId;
    state.cwd = session.cwd;

    sessionLabelEl.textContent = state.sessionId.slice(0, 8);

    // The session brings its own workspace: adopt it so the header,
    // the composer, and the next turn all agree on the folder.
    workspaceLabelEl.textContent = shortPath(state.cwd);
    workspaceInputEl.value = state.cwd;

    if (session.provider) {
      state.selectedProvider = session.provider;
      state.selectedLab = session.model ? splitModel(session.model).lab : "";
      state.selectedModel = session.model;
      syncModelSelects();
    } else {
      modelSelectLabelEl.textContent = session.model || "default";
    }

    await loadHistory(session.sessionId);

    renderSessions();
    // The ledger persists across restarts, so opening a session shows
    // its last-known totals immediately.
    void refreshUsage();
    setStatus("ready", "ok");
  } catch (error) {
    setStatus(String(error), "error");
  }
}

export async function loadHistory(id: string): Promise<void> {
  // Null cwd searches every workspace namespace, matching openSession.
  const history = await api.sessionHistory(id, null);

  messagesEl.innerHTML = "";
  // One layout for the whole transcript: keep the container display:none
  // while rows are appended, then paint once.
  messagesEl.style.display = "none";

  let agentBody: HTMLElement | null = null;

  const openAgent = (text: string): void => {
    const { textEl } = addTurn("agent", text);
    agentBody = textEl.parentElement!;
  };

  const pushAgentText = (text: string): void => {
    if (!agentBody) {
      openAgent(text);
      return;
    }
    const el = document.createElement("div");
    el.className =
      "whitespace-pre-wrap break-words leading-relaxed text-slate-700 dark:text-slate-300";
    el.innerHTML = renderText(text);
    agentBody.append(el);
  };

  const pushAgentTool = (tool: ToolInfo): void => {
    if (!agentBody) {
      openAgent("");
    }
    // Cap the live DOM copy: the full result stays in the transcript.
    if (tool.result && tool.result.length > 8000) {
      tool = {
        ...tool,
        result: `${tool.result.slice(0, 8000)}\n…(truncated, kept in history)`,
      };
    }
    const row = document.createElement("div");
    row.className = "py-0.5";
    row.append(createToolActivity(tool).el);
    agentBody!.append(row);
  };

  for (const message of history) {
    if (message.role === "user") {
      agentBody = null;
      addTurn("user", message.content);
    } else if (message.role === "assistant") {
      pushAgentText(message.content);
    } else if (message.role === "tool") {
      pushAgentTool({
        id: "",
        name: message.name || "tool",
        arguments: message.arguments || "{}",
        result: message.content,
        status: message.status === "failed" ? "failed" : "ok",
      });
    }
  }

  messagesEl.style.display = "";

  state.shownMessages = history.filter(
    (message) => message.role === "user" || message.role === "assistant",
  ).length;

  if (messagesEl.childElementCount === 0) {
    renderGreeting();
  }

  scrollToBottom();
}

// newSession is a function in this module.
// It supports the desktop app frontend.
export function newSession(): void {
  state.sessionId = "";
  clearUsage();
  sessionLabelEl.textContent = "—";
  state.shownMessages = 0;

  messagesEl.innerHTML = "";
  renderGreeting();

  renderSessions();
  inputEl.focus();
}

// initSessions wires the session search and the delete confirm dialog.
// It supports the desktop app frontend.
export function initSessions(): void {
  let searchTimer = 0;
  searchInputEl.addEventListener("input", () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(renderSessions, 120);
  });

  sessionListEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const del = target.closest<HTMLElement>("[data-delete]");
    if (del?.dataset.delete) {
      event.stopPropagation();
      askDelete(del.dataset.delete);
      return;
    }
    const pick = target.closest<HTMLElement>("[data-session]");
    if (pick?.dataset.session) {
      void openSession(pick.dataset.session);
    }
  });

  const close = (): void => {
    closeConfirm();
  };

  confirmModalEl.querySelector("#confirmCancelBtn")?.addEventListener("click", close);

  confirmModalEl.querySelector("#confirmDeleteBtn")?.addEventListener("click", () => {
    const id = state.pendingDelete;
    closeConfirm();
    void deleteSession(id);
  });

  confirmModalEl.addEventListener("click", (event) => {
    if (event.target === confirmModalEl) {
      closeConfirm();
    }
  });
}
