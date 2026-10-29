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
import { escapeAttr, escapeHtml, renderMarkdown, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import type { SessionInfo, ToolInfo } from "../core/types";
import { paintResources, resBadge } from "./resources";
import { toast } from "../ui/toast";
import { closeModal, openModal } from "../ui/anim";
import { setStatus } from "../ui/status";
import { icon } from "../icons";
import { addTurn, createCompactionDivider, renderGreeting, scrollToBottom } from "./chatView";
import { ToolRun } from "./toolRun";
import { clearTodos, refreshTodos } from "./todos";
import { clearUsage, refreshUsage } from "./usage";
import { syncModelSelects } from "./models";
import { abortAttach, beginAttach, finishAttach, forgetSession, liveBadge, queuedCount, syncComposer } from "./live";
import type { MessageInfo } from "../core/types";
import { refreshExternalVisibility } from "./sites";
import { forkSessionById, renameSessionById } from "./sessionActions";
import { resetDraft, syncPermChip } from "./permissions";
import { paintRevert, setRevert } from "./undo";
import { forkIntoWorktree, setWorktree, syncWorktreeChip } from "./worktrees";

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

const FOLD_KEY = "paladin.foldedProjects";
// renaming is the row being renamed inline; renders keep its input.
let renaming: { id: string; draft: string } | null = null;
const RECENT_COUNT = 5;

function loadFolded(): Set<string> {
  try {
    const raw = JSON.parse(localStorage.getItem(FOLD_KEY) ?? "[]");
    return new Set(Array.isArray(raw) ? raw.filter((x) => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

const folded = loadFolded();

function toggleFold(cwd: string): void {
  if (!folded.delete(cwd)) {
    folded.add(cwd);
  }
  try {
    localStorage.setItem(FOLD_KEY, JSON.stringify([...folded]));
  } catch {
  }
}

function sessionRow(session: SessionInfo): string {
  const active = session.sessionId === state.sessionId;
  const title = session.title || "untitled session";
  const live = liveBadge(session.sessionId);
  return `
    <div${live ? ` data-live="${live}"` : ""} class="session-row group${active ? " is-active" : ""}">
      ${
        renaming?.id === session.sessionId
          ? `<input class="session-rename" data-rename="${escapeAttr(session.sessionId)}" value="${escapeHtml(renaming.draft)}" spellcheck="false" aria-label="Session title" />`
          : `<button data-session="${escapeAttr(session.sessionId)}" class="session-pick" title="${escapeHtml(title)}">
        <span class="truncate">${escapeHtml(title)}</span>
      </button>`
      }
      ${session.worktree ? `<span class="session-branch" title="Worktree ${escapeAttr(session.worktree.path)}">${icon("code-branch", "h-2.5 w-2.5")}<span>${escapeHtml(session.worktree.branch.replace(/^paladin\//, ""))}</span></span>` : ""}
      ${liveIndicator(session.sessionId, live)}
      ${resBadge(session.sessionId)}
      <span class="session-count">${session.messageCount}</span>
      <button data-row-menu="${escapeAttr(session.sessionId)}" class="session-del session-more" title="More" aria-haspopup="menu">
        ${icon("sl-more", "h-3.5 w-3.5")}
      </button>
    </div>`;
}

// renderSessions draws the sidebar like Codex: sessions nested under
// their project folder (newest project first, foldable), then a short
// Recents list across projects. Search narrows projects and hides
// Recents.
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

  const projects = new Map<string, SessionInfo[]>();
  for (const session of visible) {
    // Worktree sessions live under the project they branched from.
    const key = session.worktree?.origin || session.cwd;
    const group = projects.get(key) ?? [];
    group.push(session);
    projects.set(key, group);
  }

  let html = `<div class="side-label">Projects</div>`;
  for (const [cwd, group] of projects) {
    const open = filter !== "" || !folded.has(cwd);
    const current = cwd === state.cwd;
    html += `
      <div class="project${open ? " is-open" : ""}${current ? " is-current" : ""}" data-cwd="${escapeAttr(cwd)}">
        <button class="project-head" data-fold="${escapeAttr(cwd)}" title="${escapeAttr(cwd)}" aria-expanded="${open}">
          <span class="project-ico">${icon("rl-folder", "h-4 w-4")}</span>
          <span class="project-name">${escapeHtml(shortPath(cwd))}</span>
          <span class="project-caret">${icon("rl-chevron", "h-3 w-3")}</span>
        </button>
        <div class="project-list"${open ? "" : " hidden"}>${group.map(sessionRow).join("")}</div>
      </div>`;
  }

  if (!filter) {
    const recents = state.sessions.slice(0, RECENT_COUNT);
    html += `<div id="recentWrap"><div class="side-label">Recents</div><div id="recentList">`;
    for (const session of recents) {
      const title = session.title || "untitled session";
      const active = session.sessionId === state.sessionId;
      const live = liveBadge(session.sessionId);
      html += `
        <div${live ? ` data-live="${live}"` : ""} class="recent-row${active ? " is-active" : ""}">
          <button data-recent="${escapeAttr(session.sessionId)}" class="session-pick" title="${escapeHtml(title)} · ${escapeAttr(shortPath(session.cwd))}">
            <span class="truncate">${escapeHtml(title)}</span>
          </button>
          ${liveIndicator(session.sessionId, live)}
        </div>`;
    }
    html += `</div></div>`;
  }

  const focused = document.activeElement instanceof HTMLInputElement && document.activeElement.matches(".session-rename");
  sessionListEl.innerHTML = html;
  if (renaming) {
    const input = sessionListEl.querySelector<HTMLInputElement>(`input[data-rename="${CSS.escape(renaming.id)}"]`);
    if (input) {
      wireRename(input, focused);
    }
  }
}

function wireRename(input: HTMLInputElement, keepCaret: boolean): void {
  const id = input.dataset.rename ?? "";
  input.focus();
  if (!keepCaret) {
    input.select();
  }
  let done = false;
  const end = (commit: boolean) => {
    if (done || renaming?.id !== id) return;
    done = true;
    const title = input.value.trim();
    const before = state.sessions.find((s) => s.sessionId === id)?.title ?? "";
    renaming = null;
    renderSessions();
    if (commit && title && title !== before) {
      void renameSessionById(id, title);
    }
  };
  input.addEventListener("input", () => {
    if (renaming?.id === id) renaming.draft = input.value;
  });
  input.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter") {
      event.preventDefault();
      end(true);
    } else if (event.key === "Escape") {
      event.preventDefault();
      end(false);
    }
  });
  input.addEventListener("blur", () => end(true));
}

function startRename(id: string): void {
  const s = state.sessions.find((x) => x.sessionId === id);
  renaming = { id, draft: s?.title || "" };
  renderSessions();
}

let rowMenu: HTMLElement | null = null;

function closeRowMenu(): void {
  rowMenu?.remove();
  rowMenu = null;
}

// openRowMenu is the Codex-style "⋯" menu of a sidebar session.
function openRowMenu(id: string, anchor: HTMLElement): void {
  closeRowMenu();
  const menu = document.createElement("div");
  menu.id = "sessionRowMenu";
  menu.setAttribute("role", "menu");
  menu.className = "row-menu";
  const item = (act: string, label: string, ic: Parameters<typeof icon>[0], danger = false) =>
    `<button type="button" role="menuitem" data-row-act="${act}" class="row-menu-item${danger ? " is-danger" : ""}">${icon(ic, "h-3.5 w-3.5")}<span>${label}</span></button>`;
  menu.innerHTML = item("rename", "Rename", "tl-pencil") + item("fork", "Fork", "sl-fork") + item("fork-wt", "Fork into worktree", "code-branch") + `<div class="row-menu-sep"></div>` + item("delete", "Delete", "trash", true);
  document.body.append(menu);
  const r = anchor.getBoundingClientRect();
  const w = menu.offsetWidth;
  const h = menu.offsetHeight;
  menu.style.left = `${Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w))}px`;
  menu.style.top = `${r.bottom + 4 + h > window.innerHeight ? r.top - h - 4 : r.bottom + 4}px`;
  menu.addEventListener("click", (event) => {
    const act = (event.target as HTMLElement).closest<HTMLElement>("[data-row-act]")?.dataset.rowAct;
    if (!act) return;
    closeRowMenu();
    if (act === "rename") startRename(id);
    else if (act === "fork") void forkSessionById(id);
    else if (act === "fork-wt") void forkIntoWorktree(id);
    else if (act === "delete") askDelete(id);
  });
  rowMenu = menu;
  menu.querySelector<HTMLElement>("[role=menuitem]")?.focus();
}

const LIVE_LABELS: Record<string, string> = {
  running: "Working",
  queued: "Queued",
  done: "Finished in the background",
  error: "Failed in the background",
  input: "Waiting for your answer",
  approval: "Waiting for approval",
  paused: "Paused at the round cap",
};

// liveIndicator is the per-row turn state: a spinner while working
// (plus queued count), a dot for an outcome the user has not seen.
function liveIndicator(sessionId: string, live: string): string {
  if (!live) {
    return "";
  }
  const label = LIVE_LABELS[live] ?? live;
  const n = queuedCount(sessionId);
  const extra = n > 0 && live === "running" ? `<span class="live-count">+${n}</span>` : "";
  const mark = live === "running" ? `<span class="live-spinner"></span>` : `<span class="live-dot"></span>`;
  return `<span class="live-ind live-${live}" title="${escapeHtml(label)}" role="status" aria-label="${escapeHtml(label)}">${mark}${extra}</span>`;
}

function askDelete(id: string): void {
  const session = state.sessions.find((s) => s.sessionId === id);

  state.pendingDelete = id;
  confirmTitleEl.textContent = session?.title || "this session";
  const wrap = confirmModalEl.querySelector<HTMLElement>("#confirmDiscardWrap");
  const box = confirmModalEl.querySelector<HTMLInputElement>("#confirmDiscard");
  wrap?.classList.add("hidden");
  if (box) box.checked = false;

  openModal(confirmModalEl);
  void refreshExternalVisibility();

  // A worktree with uncommitted work is kept unless the user opts in.
  const wt = session?.worktree;
  if (wt && wrap) {
    void api
      .worktreeStatus(id, session.cwd)
      .then((st) => {
        if (state.pendingDelete !== id || !st.exists || st.changed === 0) return;
        const n = st.changed;
        confirmModalEl.querySelector("#confirmDiscardCount")!.textContent = `${n} uncommitted ${n === 1 ? "change" : "changes"}`;
        confirmModalEl.querySelector("#confirmDiscardBranch")!.textContent = wt.branch;
        wrap.classList.remove("hidden");
      })
      .catch(() => {});
  }
}

export function closeConfirm(): void {
  closeModal(confirmModalEl);
  state.pendingDelete = "";
  void refreshExternalVisibility();
}

export async function deleteSession(id: string, discardWorktree = false): Promise<void> {
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
    const deleted = await api.deleteSession(id, null, discardWorktree);

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
  forgetSession(id);

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
    syncPermChip();

    sessionLabelEl.textContent = state.sessionId.slice(0, 8);
    paintResources();
    setRevert(session.sessionId, session.revert);
    setWorktree(session.worktree);
    syncWorktreeChip();

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

    // Attach is atomic: persisted history plus the live turn (if it has
    // not landed yet) and the queue, so nothing shows twice or not at
    // all. Events arriving meanwhile are buffered and replayed.
    beginAttach(session.sessionId);
    let snap;
    try {
      snap = await api.attachSession(session.sessionId);
    } catch (error) {
      abortAttach(session.sessionId);
      throw error;
    }
    if (state.sessionId !== session.sessionId) {
      // The user moved on while this loaded; keep the buffer flowing.
      abortAttach(session.sessionId);
      return;
    }
    renderHistory(snap.history ?? []);
    finishAttach(session.sessionId, snap);

    renderSessions();
    // The ledger persists across restarts, so opening a session shows
    // its last-known totals immediately. The todo hat shows its plan.
    void refreshUsage();
    void refreshTodos();
    if (!state.busy) {
      setStatus("ready", "ok");
    }
  } catch (error) {
    setStatus(String(error), "error");
  }
}

export async function loadHistory(id: string): Promise<void> {
  // Null cwd searches every workspace namespace, matching openSession.
  renderHistory(await api.sessionHistory(id, null));
}

// renderHistory paints a persisted transcript.
function renderHistory(history: MessageInfo[]): void {
  messagesEl.innerHTML = "";
  // One layout for the whole transcript: keep the container display:none
  // while rows are appended, then paint once.
  messagesEl.style.display = "none";

  let agentBody: HTMLElement | null = null;
  // run is the open group of consecutive calls; any text closes it.
  let run: ToolRun | null = null;

  const openAgent = (text: string): void => {
    const { textEl } = addTurn("agent", text);
    agentBody = textEl.parentElement!;
  };

  const pushAgentText = (text: string): void => {
    if (text.trim()) {
      run = null;
    }
    if (!agentBody) {
      openAgent(text);
      return;
    }
    const el = document.createElement("div");
    el.className = "md agent-text";
    el.innerHTML = renderMarkdown(text);
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
    if (!run) {
      run = new ToolRun();
      agentBody!.append(run.el);
    }
    run.upsert(tool);
  };

  for (const [index, message] of history.entries()) {
    if (message.role === "user") {
      agentBody = null;
      run = null;
      addTurn("user", message.content, { index: message.index, revertable: message.index !== undefined });
    } else if (message.role === "assistant") {
      pushAgentText(message.content);
    } else if (message.role === "system" && message.kind === "compaction") {
      // Divider where older context was summarized; later agent text
      // and tools append below it.
      if (!agentBody) {
        openAgent("");
      }
      run = null;
      agentBody!.append(createCompactionDivider("Context compacted", message.content));
    } else if (message.role === "tool") {
      pushAgentTool({
        id: `h${index}`,
        name: message.name || "tool",
        arguments: message.arguments || "{}",
        result: message.content,
        status: message.status === "failed" ? "failed" : "ok",
      });
    }
  }

  messagesEl.style.display = "";

  if (messagesEl.childElementCount === 0) {
    renderGreeting();
  }

  scrollToBottom();
}

export function newSession(): void {
  state.sessionId = "";
  resetDraft();
  clearUsage();
  clearTodos();
  paintResources();
  sessionLabelEl.textContent = "—";
  paintRevert();
  setWorktree(null);
  syncWorktreeChip();

  messagesEl.innerHTML = "";
  renderGreeting();

  renderSessions();
  syncComposer();
  inputEl.focus();
}

// initSessions wires the session search and the delete confirm dialog.
export function initSessions(): void {
  let searchTimer = 0;
  searchInputEl.addEventListener("input", () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(renderSessions, 120);
  });

  document.addEventListener("mousedown", (event) => {
    if (rowMenu && !rowMenu.contains(event.target as Node) && !(event.target as HTMLElement).closest("[data-row-menu]")) {
      closeRowMenu();
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && rowMenu) {
      closeRowMenu();
    }
  });
  sessionListEl.addEventListener("dblclick", (event) => {
    const pick = (event.target as HTMLElement).closest<HTMLElement>("[data-session]");
    if (pick?.dataset.session) {
      startRename(pick.dataset.session);
    }
  });
  sessionListEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const more = target.closest<HTMLElement>("[data-row-menu]");
    if (more?.dataset.rowMenu) {
      event.stopPropagation();
      if (rowMenu) {
        closeRowMenu();
      } else {
        openRowMenu(more.dataset.rowMenu, more);
      }
      return;
    }
    if (target.closest("input.session-rename")) {
      return;
    }
    const fold = target.closest<HTMLElement>("[data-fold]");
    if (fold?.dataset.fold !== undefined) {
      toggleFold(fold.dataset.fold);
      renderSessions();
      return;
    }
    const pick = target.closest<HTMLElement>("[data-session],[data-recent]");
    const id = pick?.dataset.session ?? pick?.dataset.recent;
    if (id) {
      void openSession(id);
    }
  });

  const close = (): void => {
    closeConfirm();
  };

  confirmModalEl.querySelector("#confirmCancelBtn")?.addEventListener("click", close);

  confirmModalEl.querySelector("#confirmDeleteBtn")?.addEventListener("click", () => {
    const id = state.pendingDelete;
    const discard = !!confirmModalEl.querySelector<HTMLInputElement>("#confirmDiscard")?.checked;
    closeConfirm();
    void deleteSession(id, discard);
  });

  confirmModalEl.addEventListener("click", (event) => {
    if (event.target === confirmModalEl) {
      closeConfirm();
    }
  });
}
