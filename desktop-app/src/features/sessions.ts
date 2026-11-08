// sessions.ts owns the session list and transcripts.
// It renders the sidebar and loads history. No event stream in v1;

import * as api from "../core/api";
import { pluginCardsReady } from "./pluginCards";
import {
  chatViewEl,
  confirmModalEl,
  confirmTitleEl,
  inputEl,
  messagesEl,
  modelSelectLabelEl,
  parentBarEl,
  searchInputEl,
  sessionLabelEl,
  sessionListEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { escapeAttr, escapeHtml, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import type { AgentInfo, AgentStatus, HistoryPage, MessageInfo, SessionInfo } from "../core/types";
import { paintResources, resBadge } from "./resources";
import { toast } from "../ui/toast";
import { closeModal, openModal } from "../ui/anim";
import { setStatus } from "../ui/status";
import { icon } from "../icons";
import { renderGreeting, scrollToBottom } from "./chatView";
import { buildHistory } from "./sessions/history";
import { clearTodos, refreshTodos } from "./todos";
import { clearUsage, refreshUsage } from "./usage";
import { persistModelChoice, syncModelSelects } from "./models";
import { abortAttach, beginAttach, finishAttach, forgetSession, liveBadge, queuedCount, syncComposer } from "./live";
import { forkSessionById, renameSessionById } from "./sessionActions";
import { resetDraft, syncPermChip } from "./permissions";
import { forkIntoWorktree, setWorktree, syncWorktreeChip } from "./worktrees";
import { syncMcp } from "./mcp";
import { syncTools } from "./toolSwitches";
import { resetBudgetDraft, syncBudget } from "./budget";
import { clearContextBox, reloadContextSources } from "./contextBox";
import { loadJson, saveJson } from "../core/storage";
import { refreshRecovery } from "./undo";
import {
  autoOpenChildTabs,
  disposeSidePanes,
  hasAgentBrowser,
  openAgentTab,
  paintAgentTabs,
  refreshChanges,
  syncSidePane,
} from "./sidePane";

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
  // A child that is already working gets its window without a click.
  autoOpenChildTabs();
}

// scheduleRefreshSessions coalesces the list reloads that follow one
// turn (or a burst of subagent turns) into a single listSessions.
let sessionsRefreshTimer = 0;
export function scheduleRefreshSessions(delay = 250): void {
  if (sessionsRefreshTimer) return;
  sessionsRefreshTimer = window.setTimeout(() => {
    sessionsRefreshTimer = 0;
    void refreshSessions();
  }, delay);
}

const FOLD_KEY = "paladin.foldedProjects";
// renaming is the row being renamed inline; renders keep its input.
let renaming: { id: string; draft: string } | null = null;
const RECENT_COUNT = 5;

function loadFolded(): Set<string> {
  const raw = loadJson(FOLD_KEY);
  return new Set(Array.isArray(raw) ? raw.filter((x): x is string => typeof x === "string") : []);
}

const folded = loadFolded();

function toggleFold(cwd: string): void {
  if (!folded.delete(cwd)) {
    folded.add(cwd);
  }
  saveJson(FOLD_KEY, [...folded]);
}

// kidsOpen remembers a parent's children toggle the user clicked; by
// default they show while the parent is open or a child is at work.
const kidsOpen = new Map<string, boolean>();

const AGENT_STATUS_TEXT: Record<AgentStatus, string> = {
  queued: "queued",
  running: "running",
  completed: "done",
  failed: "failed",
  interrupted: "stopped",
};

// agentStatus is the status of a child session: its own live turn wins
// over the persisted status, which lags the event stream.
export function agentStatus(session: SessionInfo): AgentStatus {
  const live = liveBadge(session.sessionId);
  if (live === "running" || live === "approval" || live === "input") return "running";
  if (live === "queued") return "queued";
  return session.subagent?.status ?? "completed";
}

// agentStatusHtml is a status dot with its words, for screen readers too.
export function agentStatusHtml(status: AgentStatus): string {
  return `<span class="agent-status" data-status="${status}"><span class="agent-dot" aria-hidden="true"></span><span class="agent-status-text">${AGENT_STATUS_TEXT[status]}</span></span>`;
}

function childRow(child: SessionInfo): string {
  const sub = child.subagent!;
  const active = child.sessionId === state.sessionId;
  return `
    <button type="button" data-child="${escapeAttr(child.sessionId)}" class="session-child${active ? " is-active" : ""}" title="${escapeAttr(sub.task || sub.name)}">
      <span class="child-ico">${icon("rl-agent", "h-3 w-3")}</span>
      <span class="child-name truncate">${escapeHtml(sub.name || child.title)}</span>
      <span class="agent-type">${escapeHtml(sub.type)}</span>
      ${hasAgentBrowser(child.sessionId) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
      ${agentStatusHtml(agentStatus(child))}
    </button>`;
}

function childrenBlock(parent: SessionInfo, kids: SessionInfo[]): string {
  if (kids.length === 0) return "";
  const busy = kids.some((k) => ["running", "queued"].includes(agentStatus(k)));
  const near = parent.sessionId === state.sessionId || kids.some((k) => k.sessionId === state.sessionId);
  const open = kidsOpen.get(parent.sessionId) ?? (busy || near || kids.some((kid) => hasAgentBrowser(kid.sessionId)));
  const id = escapeAttr(parent.sessionId);
  return `
    <button type="button" class="session-kids-toggle" data-kids-toggle="${id}" aria-expanded="${open}">
      <span class="project-caret">${icon("rl-chevron", "h-2.5 w-2.5")}</span>
      <span>${kids.length} ${kids.length === 1 ? "subagent" : "subagents"}</span>
      ${busy ? `<span class="agent-dot" data-status="running" aria-hidden="true"></span>` : ""}
    </button>
    <div class="session-children" data-kids-of="${id}"${open ? "" : " hidden"}>${kids.map(childRow).join("")}</div>`;
}

function sessionRow(session: SessionInfo, kids: SessionInfo[] = []): string {
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
      ${session.damaged ? `<span class="session-damaged" title="Unreadable: ${escapeAttr(session.damaged)}">${icon("warning", "h-3 w-3")}</span>` : ""}
      ${hasAgentBrowser(session.sessionId) || kids.some((kid) => hasAgentBrowser(kid.sessionId)) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
      ${liveIndicator(session.sessionId, live)}
      ${resBadge(session.sessionId)}
      <span class="session-count">${session.messageCount}</span>
      <button data-row-menu="${escapeAttr(session.sessionId)}" class="session-del session-more" title="More" aria-haspopup="menu">
        ${icon("sl-more", "h-3.5 w-3.5")}
      </button>
    </div>${childrenBlock(session, kids)}`;
}

// renderSessions draws the sidebar like Codex: sessions nested under
// their project folder (newest project first, foldable), then a short
// Recents list across projects. Search narrows projects and hides
// Recents.
// lastSessionListHtml lets renderSessions skip the DOM work when a
// burst of status updates produces the same sidebar (the common case
// after refreshSessions).
let lastSessionListHtml = "";

export function renderSessions(): void {
  window.dispatchEvent(new Event("paladin:sessions-changed"));
  // The parent bar and pane heads show the same live agent status.
  paintParentBar();
  paintAgentTabs();
  const filter = searchInputEl.value.trim().toLowerCase();

  // Children nest under their parent; one whose parent is not listed
  // stands on its own.
  const known = new Set(state.sessions.map((s) => s.sessionId));
  const kidsOf = new Map<string, SessionInfo[]>();
  for (const session of state.sessions) {
    const parentId = session.subagent?.parentId;
    if (parentId && known.has(parentId)) {
      const children = kidsOf.get(parentId);
      if (children) children.push(session);
      else kidsOf.set(parentId, [session]);
    }
  }
  const tops = state.sessions.filter((s) => !s.subagent || !known.has(s.subagent.parentId));
  const titled = (s: SessionInfo): boolean => s.title.toLowerCase().includes(filter);
  const matches = (s: SessionInfo): boolean => titled(s) || (kidsOf.get(s.sessionId) ?? []).some(titled);
  const visible = filter ? tops.filter(matches) : tops;

  if (visible.length === 0) {
    const empty = `<p class="px-2 py-1 text-slate-400">${filter ? "no session matches" : "no sessions yet"}</p>`;
    if (empty !== lastSessionListHtml) {
      lastSessionListHtml = empty;
      sessionListEl.innerHTML = empty;
    }
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
          <span class="project-name">${cwd ? escapeHtml(shortPath(cwd)) : "Unknown folder"}</span>
          <span class="project-caret">${icon("rl-chevron", "h-3 w-3")}</span>
        </button>
        <div class="project-list"${open ? "" : " hidden"}>${group.map((s) => sessionRow(s, kidsOf.get(s.sessionId))).join("")}</div>
      </div>`;
  }

  if (!filter) {
    const recents = tops.slice(0, RECENT_COUNT);
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
          ${hasAgentBrowser(session.sessionId) || (kidsOf.get(session.sessionId) ?? []).some((kid) => hasAgentBrowser(kid.sessionId)) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
          ${liveIndicator(session.sessionId, live)}
        </div>`;
    }
    html += `</div></div>`;
  }

  const focused =
    document.activeElement instanceof HTMLInputElement && document.activeElement.matches(".session-rename");
  const changed = html !== lastSessionListHtml;
  if (changed) {
    lastSessionListHtml = html;
    sessionListEl.innerHTML = html;
  }
  if (changed && renaming) {
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
  menu.innerHTML =
    item("rename", "Rename", "tl-pencil") +
    item("fork", "Fork", "sl-fork") +
    item("fork-wt", "Fork into worktree", "code-branch") +
    `<div class="row-menu-sep"></div>` +
    item("delete", "Delete", "trash", true);
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

  // A worktree with uncommitted work is kept unless the user opts in.
  const wt = session?.worktree;
  if (wt && wrap) {
    void api
      .worktreeStatus(id, session.cwd)
      .then((st) => {
        if (state.pendingDelete !== id || !st.exists || st.changed === 0) return;
        const n = st.changed;
        confirmModalEl.querySelector("#confirmDiscardCount")!.textContent =
          `${n} uncommitted ${n === 1 ? "change" : "changes"}`;
        confirmModalEl.querySelector("#confirmDiscardBranch")!.textContent = wt.branch;
        wrap.classList.remove("hidden");
      })
      .catch(() => {});
  }
}

export function closeConfirm(): void {
  closeModal(confirmModalEl);
  state.pendingDelete = "";
}

async function deleteSession(id: string, discardWorktree = false): Promise<void> {
  if (!id) {
    return;
  }

  const session = state.sessions.find((s) => s.sessionId === id);
  const title = session?.title || "the session";

  // A parent's children are deleted with it.
  const gone = [id, ...(session?.children ?? [])];
  const before = state.sessions;
  state.sessions = state.sessions.filter((s) => !gone.includes(s.sessionId));
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
  gone.forEach(forgetSession);
  if (gone.includes(state.sessionId)) {
    newSession();
  }
  disposeSidePanes(gone);
}

export async function openSession(id: string): Promise<void> {
  const damaged = state.sessions.find((s) => s.sessionId === id)?.damaged;
  if (damaged) {
    setStatus(`This session can't be opened (${damaged}). You can still delete it.`, "error");
    return;
  }
  try {
    // Null cwd searches every workspace namespace; the session brings
    // its own cwd with it, which becomes the new working folder.
    const session = await api.openSession(id, null);

    state.sessionId = session.sessionId;
    window.dispatchEvent(new Event("paladin:session-selected"));
    void refreshRecovery("");
    state.subagent = session.subagent ?? null;
    state.cwd = session.cwd;
    paintParentBar();
    syncSidePane();
    syncPermChip();
    syncMcp();
    syncTools();
    void syncBudget();
    void reloadContextSources();

    sessionLabelEl.textContent = state.sessionId.slice(0, 8);
    paintResources();
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
      // The session's last-used model becomes the global default too,
      // so a later provider refresh (or an app restart) restores this
      // instead of falling back to another provider's model.
      persistModelChoice();
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
    await pluginCardsReady();
    if (state.sessionId !== session.sessionId) {
      abortAttach(session.sessionId);
      return;
    }
    renderHistory(snap.history);
    finishAttach(session.sessionId, snap);
    void refreshRecovery(session.sessionId);

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

// paintParentBar shows, while a subagent's session is open, which
// parent drives it and a way back there.
function paintParentBar(): void {
  const sub = state.subagent;
  parentBarEl.hidden = !sub;
  if (!sub) return;
  const parent = state.sessions.find((s) => s.sessionId === sub.parentId);
  const self = state.sessions.find((s) => s.sessionId === state.sessionId);
  parentBarEl.querySelector("#parentBarTitle")!.textContent = parent?.title || "parent session";
  const status = self ? agentStatus({ ...self, subagent: sub }) : sub.status;
  parentBarEl.querySelector("#parentBarAgent")!.innerHTML =
    `<span class="agent-type">${escapeHtml(sub.type)}</span><span class="parent-agent-name">${escapeHtml(sub.name)}</span>${agentStatusHtml(status)}`;
}

// onAgentsChanged applies an agents/changed event: a child's status
// moves in place, and an unknown child reloads the list to find it.
export function onAgentsChanged(agent: AgentInfo): void {
  const child = state.sessions.find((s) => s.sessionId === agent.agentId);
  if (!child?.subagent) {
    void refreshSessions();
    return;
  }
  child.subagent = { ...child.subagent, status: agent.status, error: agent.error, updatedAt: agent.updatedAt };
  if (state.sessionId === agent.agentId && state.subagent) {
    state.subagent = child.subagent;
  }
  renderSessions();
  autoOpenChildTabs();
}

// HISTORY_PAGE is how many transcript rows are painted at once. Long
// sessions used to fetch, parse and build every message before the
// first pixels; older rows now load on demand.
const HISTORY_PAGE = 120;
let historyStart = 0;
let historyHasMore = false;
let historyLoading = false;

export async function loadHistory(id: string): Promise<void> {
  // Null cwd searches every workspace namespace, matching openSession.
  await pluginCardsReady();
  renderHistory(await api.sessionHistoryPage(id, null, HISTORY_PAGE, 0));
}

function earlierRow(): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "history-earlier";
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "history-earlier-btn";
  btn.textContent = "Load earlier messages";
  btn.addEventListener("click", () => void loadEarlierHistory());
  wrap.append(btn);
  return wrap;
}

// loadEarlierHistory prepends the page before the first painted row.
// Row ordinals count from the start of the session, so a turn landing
// at the end meanwhile does not shift what `historyStart` points at.
async function loadEarlierHistory(): Promise<void> {
  const id = state.sessionId;
  if (!id || historyLoading || !historyHasMore) return;
  historyLoading = true;
  const btn = messagesEl.querySelector<HTMLButtonElement>(".history-earlier-btn");
  if (btn) {
    btn.disabled = true;
    btn.textContent = "Loading…";
  }
  try {
    const page = await api.sessionHistoryPage(id, null, HISTORY_PAGE, historyStart);
    if (id !== state.sessionId) return;
    const holder = document.createElement("div");
    buildHistory(holder, page.messages, !state.subagent);
    const anchor = messagesEl.querySelector(".history-earlier");
    const prevHeight = chatViewEl.scrollHeight;
    const prevTop = chatViewEl.scrollTop;
    const fragment = document.createDocumentFragment();
    while (holder.firstChild) fragment.append(holder.firstChild);
    messagesEl.insertBefore(fragment, anchor);
    anchor?.remove();
    historyStart = page.start;
    historyHasMore = page.hasMore;
    if (page.hasMore) messagesEl.insertBefore(earlierRow(), messagesEl.firstChild);
    // Keep the row the user was reading under the cursor.
    chatViewEl.scrollTop = prevTop + (chatViewEl.scrollHeight - prevHeight);
  } finally {
    historyLoading = false;
  }
}

// renderHistory paints a persisted transcript page. An older core may
// still answer with a bare array; accept both shapes.
function renderHistory(raw: HistoryPage | MessageInfo[]): void {
  const page: HistoryPage = Array.isArray(raw) ? { messages: raw, start: 0, hasMore: false, total: raw.length } : raw;
  historyStart = page.start ?? 0;
  historyHasMore = page.hasMore === true;
  historyLoading = false;
  messagesEl.innerHTML = "";
  // One layout for the whole transcript: keep the container display:none
  // while rows are appended, then paint once.
  messagesEl.style.display = "none";
  if (historyHasMore) {
    messagesEl.append(earlierRow());
  }
  // A subagent's conversation is driven by its parent: no rewinding it.
  buildHistory(messagesEl, page.messages ?? [], !state.subagent);
  void refreshChanges(state.sessionId);
  messagesEl.style.display = "";

  if (messagesEl.childElementCount === 0) {
    renderGreeting();
  }

  scrollToBottom();
}

export function newSession(): void {
  state.sessionId = "";
  window.dispatchEvent(new Event("paladin:session-selected"));
  void refreshRecovery("");
  state.subagent = null;
  paintParentBar();
  syncSidePane();
  resetDraft();
  resetBudgetDraft();
  clearUsage();
  clearTodos();
  paintResources();
  sessionLabelEl.textContent = "—";
  setWorktree(null);
  syncWorktreeChip();
  syncTools();
  void syncBudget();
  clearContextBox();
  void reloadContextSources();

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
    if (
      rowMenu &&
      !rowMenu.contains(event.target as Node) &&
      !(event.target as HTMLElement).closest("[data-row-menu]")
    ) {
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
    const kids = target.closest<HTMLElement>("[data-kids-toggle]");
    if (kids?.dataset.kidsToggle) {
      const list = sessionListEl.querySelector<HTMLElement>(`[data-kids-of="${CSS.escape(kids.dataset.kidsToggle)}"]`);
      kidsOpen.set(kids.dataset.kidsToggle, !!list?.hidden);
      renderSessions();
      return;
    }
    const child = target.closest<HTMLElement>("[data-child]");
    if (child?.dataset.child) {
      // A child works beside its parent: open its window in the pane so
      // the parent's chat stays on screen. "Open" in that window moves
      // it into the main view when that is what you want.
      void openAgentTab(child.dataset.child);
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

  parentBarEl.querySelector("#parentBackBtn")!.addEventListener("click", () => {
    if (state.subagent) void openSession(state.subagent.parentId);
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
