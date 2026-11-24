// sessions.ts owns the session list and transcripts.
// It renders the sidebar and loads history. No event stream in v1;

import * as api from "../core/api";
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
import { bare, escapeAttr, escapeHtml, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import { CODEX } from "../core/harness";
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
import { openedHarness, previewHarness, setHarness } from "../adapters/codex/harness";
import { abortAttach, beginAttach, finishAttach, forgetSession, liveBadge, queuedCount, syncComposer } from "./live";
import { STARTING_CHANGED, startingIn } from "./startProgress";
import { forkSessionById, renameSessionById } from "./sessionActions";
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

// refreshSeq orders overlapping refreshes. A burst of subagent turns can
// start several listSessions calls; without this an older reply that lands
// late paints over a newer one.
let refreshSeq = 0;

// emptyStreak counts consecutive replies that listed nothing. The sidebar
// only clears a populated list once an empty answer is confirmed, so one
// read that races a namespace rewrite cannot empty the pane.
let emptyStreak = 0;
const EMPTY_CONFIRMATIONS = 2;

export async function refreshSessions(): Promise<void> {
  // The sidebar shows every workspace's sessions, not just the current
  // folder's: filtering by cwd hid old sessions whenever the workspace
  // changed (e.g. after New Session picked another folder).
  const seq = ++refreshSeq;
  try {
    const list = await api.listSessions(null, 0);
    if (seq !== refreshSeq) return; // a newer refresh owns the list now
    if (list.length === 0 && state.sessions.length > 0) {
      // Sessions do not vanish on their own: an empty answer while the
      // pane shows sessions is a read taken mid-write. Hold the current
      // list until a second reply agrees, then clear for real.
      if (++emptyStreak < EMPTY_CONFIRMATIONS) return;
    }
    emptyStreak = 0;
    state.sessions = list;
  } catch (error) {
    console.warn(`refreshSessions failed: ${String(error)}`);
    // Keep what the sidebar already shows. A transient failure (a session
    // folder being written while the list is read, or the host restarting
    // mid-turn) must never look like "no sessions yet".
    if (seq === refreshSeq && state.sessions.length > 0) return;
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

// kidsOpen remembers a parent's children toggle the user clicked. The
// list is collapsed by default: a long run can spawn many subagents, and
// keeping them all on screen pushes the rest of the sidebar away. The
// toggle keeps a dot while any child is at work, so activity stays
// visible without the list being open.
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
  // A child can be a fork too (spawn_agent with fork:"all"), so it wears
  // the same glyph the parent rows use.
  const forked = child.forkedFrom
    ? `<span class="session-forked" title="Forked from another session" aria-label="Forked">${icon("sl-fork", "h-3 w-3")}</span>`
    : "";
  return `
    <button type="button" data-child="${escapeAttr(child.sessionId)}" class="session-child${active ? " is-active" : ""}" title="${escapeAttr(sub.task || sub.name)}">
      <span class="child-ico">${icon("rl-agent", "h-3 w-3")}</span>
      <span class="child-name truncate">${escapeHtml(sub.name || child.title)}</span>
      ${forked}
      <span class="agent-type">${escapeHtml(sub.type)}</span>
      ${hasAgentBrowser(child.sessionId) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
      ${agentStatusHtml(agentStatus(child))}
    </button>`;
}

function childrenBlock(parent: SessionInfo, kids: SessionInfo[], tree?: sessionTree): string {
  if (kids.length === 0) return "";
  const busy = kids.some((k) => ["running", "queued"].includes(agentStatus(k)));
  // Collapsed unless the user opened it for this parent.
  const open = kidsOpen.get(parent.sessionId) ?? false;
  const id = escapeAttr(parent.sessionId);
  // A child can itself have forked chats; they hang under the child, so a
  // fork of a subagent is still reachable.
  const row = (kid: SessionInfo): string => `${childRow(kid)}${tree ? forkRows(kid, tree, 1) : ""}`;
  return `
    <button type="button" class="session-kids-toggle" data-kids-toggle="${id}" aria-expanded="${open}">
      <span class="project-caret">${icon("rl-chevron", "h-2.5 w-2.5")}</span>
      <span>${kids.length} ${kids.length === 1 ? "subagent" : "subagents"}</span>
      ${busy ? `<span class="agent-dot" data-status="running" aria-hidden="true"></span>` : ""}
    </button>
    <div class="session-children" data-kids-of="${id}"${open ? "" : " hidden"}>${kids.map(row).join("")}</div>`;
}

type Engine = "paladin" | "codex" | "claude";

// ENGINES is the order a project lists its sections in; every session
// belongs to exactly one (anything that is not Codex or Claude is Paladin's).
const ENGINES: ReadonlyArray<{ id: Engine; label: string }> = [
  { id: "paladin", label: "Paladin" },
  { id: "codex", label: "Codex" },
  { id: "claude", label: "Claude Code" },
];

function engineOf(session: SessionInfo): Engine {
  return session.harness === CODEX ? "codex" : session.harness === "claude" ? "claude" : "paladin";
}

// engineChip says which engine runs a session: Codex or Claude Code (the
// user's own CLIs) or Paladin's agent.
function engineChip(session: SessionInfo): string {
  const engine = engineOf(session);
  const title =
    engine === "codex"
      ? "Runs on your Codex CLI"
      : engine === "claude"
        ? "Runs on your Claude Code CLI"
        : "Runs on Paladin's agent";
  const label = ENGINES.find((e) => e.id === engine)?.label ?? "Paladin";
  return `<span class="session-harness" data-engine="${engine}" title="${title}">${label}</span>`;
}

// projectAddButton is a folder's "+": it opens the engine menu, and shows a
// spinner (and refuses a second start) while an engine is starting there.
function projectAddButton(cwd: string): string {
  const name = escapeAttr(shortPath(cwd));
  const starting = startingIn(cwd);
  if (starting) {
    const label = `Starting ${escapeAttr(starting)}…`;
    return `<button type="button" class="project-add is-starting" data-new-in="${escapeAttr(cwd)}" title="${label}" aria-label="${label}" aria-busy="true" disabled><span class="live-spinner start-spin" aria-hidden="true"></span></button>`;
  }
  return `<button type="button" class="project-add" data-new-in="${escapeAttr(cwd)}" title="New session in ${name}" aria-label="New session in ${name}" aria-haspopup="menu">${icon("plus", "h-3.5 w-3.5")}</button>`;
}

// engineSections splits one project's sessions into a labelled section per
// engine, so a folder worked on with several agents stays readable. Empty
// sections are not drawn.
function engineSections(group: SessionInfo[], row: (session: SessionInfo) => string): string {
  return ENGINES.map(({ id, label }) => {
    const rows = group.filter((s) => engineOf(s) === id);
    if (rows.length === 0) return "";
    return `<div class="project-engine" data-engine-group="${id}"><div class="project-engine-label">${label}</div>${rows.map(row).join("")}</div>`;
  }).join("");
}

function sessionRow(session: SessionInfo, kids: SessionInfo[] = [], tree?: sessionTree): string {
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
      ${engineChip(session)}
      ${session.forkedFrom ? `<span class="session-forked" title="Forked from another session" aria-label="Forked">${icon("sl-fork", "h-3 w-3")}</span>` : ""}
      ${session.worktree ? `<span class="session-branch" title="Worktree ${escapeAttr(session.worktree.path)}">${icon("code-branch", "h-2.5 w-2.5")}<span>${escapeHtml(session.worktree.branch.replace(/^paladin\//, ""))}</span></span>` : ""}
      ${session.damaged ? `<span class="session-damaged" title="Unreadable: ${escapeAttr(session.damaged)}">${icon("warning", "h-3 w-3")}</span>` : ""}
      ${hasAgentBrowser(session.sessionId) || kids.some((kid) => hasAgentBrowser(kid.sessionId)) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
      ${liveIndicator(session.sessionId, live)}
      ${resBadge(session.sessionId)}
      <button data-row-menu="${escapeAttr(session.sessionId)}" class="session-del session-more" title="More" aria-haspopup="menu">
        ${icon("sl-more", "h-3.5 w-3.5")}
      </button>
    </div>${childrenBlock(session, kids, tree)}`;
}

// sessionTree is the sidebar's shape: every session nests under the one
// it came from. A fork hangs under the chat it forked, a subagent under
// the chat that spawned it, and the two nest further (a fork's subagents
// under the fork), like folders in an explorer.
type sessionTree = {
  // forksOf maps a session id to the chats forked from it.
  forksOf: Map<string, SessionInfo[]>;
  // agentsOf maps a session id to the subagents it spawned.
  agentsOf: Map<string, SessionInfo[]>;
  // tops are the sessions drawn at a project's top level: everything
  // whose parent is not in the list.
  tops: SessionInfo[];
};

// buildSessionTree links the flat list into that shape. A child whose
// parent is missing from the list (a deleted parent, a truncated page)
// stands on its own instead of vanishing. A parent cycle is cut the same
// way, so a corrupt pair of links can never hide both sessions.
//
// The result must not depend on the order sessions arrive in: the core
// sends them interleaved by time, so a fork of a fork can be listed
// BEFORE the chat it forked from. Only the upward loop check decides.
function buildSessionTree(sessions: SessionInfo[]): sessionTree {
  const known = new Set(sessions.map((s) => s.sessionId));
  const byId = new Map(sessions.map((s) => [s.sessionId, s]));
  const forksOf = new Map<string, SessionInfo[]>();
  const agentsOf = new Map<string, SessionInfo[]>();
  const parentOf = (session: SessionInfo): string => session.forkedFrom || session.subagent?.parentId || "";
  // Walk up to see whether a chain of links loops back to this session.
  // Depth is bounded by the list size, so this cannot spin.
  const loopsBack = (session: SessionInfo): boolean => {
    let cur = session;
    for (let step = 0; step <= sessions.length; step++) {
      const parent = parentOf(cur);
      if (!parent || !known.has(parent)) return false;
      if (parent === session.sessionId) return true;
      const next = byId.get(parent);
      if (!next) return false;
      cur = next;
    }
    return true;
  };
  const tops: SessionInfo[] = [];
  for (const session of sessions) {
    const parentId = parentOf(session);
    if (!parentId || !known.has(parentId) || loopsBack(session)) {
      tops.push(session);
      continue;
    }
    const into = session.forkedFrom ? forksOf : agentsOf;
    const siblings = into.get(parentId);
    if (siblings) siblings.push(session);
    else into.set(parentId, [session]);
  }
  // Safety net: everything must be reachable from a top row. A relation
  // the renderers do not walk (a new kind of child, a shape this tree did
  // not anticipate) would otherwise hide a session entirely, so anything
  // unreachable is promoted to a top row rather than lost.
  const reachable = new Set<string>();
  const walk = (session: SessionInfo, depth: number): void => {
    if (depth > sessions.length || reachable.has(session.sessionId)) return;
    reachable.add(session.sessionId);
    for (const child of [...(forksOf.get(session.sessionId) ?? []), ...(agentsOf.get(session.sessionId) ?? [])]) {
      walk(child, depth + 1);
    }
  };
  for (const top of tops) walk(top, 0);
  const reachTops = tops.filter((s) => reachable.has(s.sessionId));
  for (const session of sessions) {
    if (!reachable.has(session.sessionId)) reachTops.push(session);
  }
  return { forksOf, agentsOf, tops: reachTops };
}

// ---- forks and subagents under their parent ----

// forkRows draws a parent's forks, recursively: each fork carries its own
// forks and subagents beneath it. Unlike the subagent list (collapsed by
// default), forks are always shown — a fork is a chat the user made on
// purpose, and hiding it would leave two copies of one title with no
// visible relation.
function forkRows(parent: SessionInfo, tree: sessionTree, depth: number): string {
  const forks = tree.forksOf.get(parent.sessionId) ?? [];
  if (forks.length === 0) return "";
  // A fork carries its own subagents and its own forks beneath it.
  const body = forks
    .map(
      (fork) =>
        `${forkRow(fork)}${childrenBlock(fork, tree.agentsOf.get(fork.sessionId) ?? [], tree)}${forkRows(fork, tree, depth + 1)}`,
    )
    .join("");
  return `<div class="session-forks" data-forks-of="${escapeAttr(parent.sessionId)}" style="--fork-depth:${depth}">${body}</div>`;
}

// forkRow is one forked chat, nested one level in. It carries the same
// actions as a top-level row (open, rename, fork again, delete).
function forkRow(session: SessionInfo): string {
  const active = session.sessionId === state.sessionId;
  const title = session.title || "untitled session";
  const live = liveBadge(session.sessionId);
  return `
    <div${live ? ` data-live="${live}"` : ""} class="session-row session-fork-row group${active ? " is-active" : ""}" data-fork-of="${escapeAttr(session.forkedFrom ?? "")}">
      <span class="fork-guide" aria-hidden="true">${icon("sl-fork", "h-3 w-3")}</span>
      ${
        renaming?.id === session.sessionId
          ? `<input class="session-rename" data-rename="${escapeAttr(session.sessionId)}" value="${escapeHtml(renaming.draft)}" spellcheck="false" aria-label="Session title" />`
          : `<button data-session="${escapeAttr(session.sessionId)}" class="session-pick" title="${escapeHtml(title)}">
        <span class="truncate">${escapeHtml(title)}</span>
      </button>`
      }
      ${engineChip(session)}
      ${hasAgentBrowser(session.sessionId) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
      ${liveIndicator(session.sessionId, live)}
      ${resBadge(session.sessionId)}
      <button data-row-menu="${escapeAttr(session.sessionId)}" class="session-del session-more" title="More" aria-haspopup="menu">
        ${icon("sl-more", "h-3.5 w-3.5")}
      </button>
    </div>`;
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

  const tree = buildSessionTree(state.sessions);
  const { tops } = tree;
  // A search matches a whole subtree: typing a fork's title must keep the
  // chat it was forked from, or the fork would show with no parent.
  const titled = (s: SessionInfo): boolean => s.title.toLowerCase().includes(filter);
  const subtreeMatches = (s: SessionInfo, seen: Set<string> = new Set()): boolean => {
    if (titled(s)) return true;
    if (seen.has(s.sessionId)) return false;
    seen.add(s.sessionId);
    const kin = [...(tree.forksOf.get(s.sessionId) ?? []), ...(tree.agentsOf.get(s.sessionId) ?? [])];
    return kin.some((child) => subtreeMatches(child, seen));
  };
  const visible = filter ? tops.filter((s) => subtreeMatches(s)) : tops;

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
        <div class="project-row">
          <button class="project-head" data-fold="${escapeAttr(cwd)}" title="${escapeAttr(cwd)}" aria-expanded="${open}">
            <span class="project-ico">${icon("rl-folder", "h-4 w-4")}</span>
            <span class="project-name">${cwd ? escapeHtml(shortPath(cwd)) : "Unknown folder"}</span>
            <span class="project-caret">${icon("rl-chevron", "h-3 w-3")}</span>
          </button>
          ${cwd ? projectAddButton(cwd) : ""}
        </div>
        <div class="project-list"${open ? "" : " hidden"}>${engineSections(group, (s) => sessionRow(s, tree.agentsOf.get(s.sessionId), tree) + forkRows(s, tree, 1))}</div>
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
          ${engineChip(session)}
          ${hasAgentBrowser(session.sessionId) || (tree.agentsOf.get(session.sessionId) ?? []).some((kid) => hasAgentBrowser(kid.sessionId)) || (tree.forksOf.get(session.sessionId) ?? []).some((kid) => hasAgentBrowser(kid.sessionId)) ? `<span title="Agent browser open" aria-label="Agent browser open">${icon("globe", "h-3 w-3")}</span>` : ""}
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

let openGeneration = 0;

let syncingSession = false;

// External Codex clients write the same thread without sending events through
// Paladin's app-server. Refresh the visible idle session without resetting the
// composer, selected model, or history when nothing changed.
async function syncLinkedSession(): Promise<void> {
  if (syncingSession || document.hidden || !state.harness || state.busy || !state.sessionId) return;
  const id = state.sessionId;
  const generation = openGeneration;
  syncingSession = true;
  try {
    const changed = await api.syncSession(id, state.cwd || null);
    if (changed && id === state.sessionId && generation === openGeneration) {
      await reattachSession(id, generation);
      void refreshSessions();
    }
  } catch (error) {
    console.debug("Linked session sync will retry", error);
  } finally {
    syncingSession = false;
  }
}

// reattachSession reads the visible session's history again without
// reopening it: the composer, model, panes and chips stay as they are, and a
// reader scrolled up stays where they were. The two callers are a linked
// agent's periodic sync and a "session/refresh" (a background command ended).
export async function reattachSession(id: string, generation = openGeneration): Promise<void> {
  if (id !== state.sessionId || generation !== openGeneration) return;
  const fromBottom = messagesEl.scrollHeight - messagesEl.scrollTop - messagesEl.clientHeight;
  beginAttach(id);
  try {
    const snap = await api.attachSession(id, 0, state.cwd);
    if (id !== state.sessionId || generation !== openGeneration) {
      abortAttach(id);
      return;
    }
    renderHistory(snap.history);
    finishAttach(id, snap);
    // A reader who had scrolled up keeps their place; one at the bottom follows.
    if (fromBottom > 80) {
      messagesEl.scrollTop = Math.max(0, messagesEl.scrollHeight - messagesEl.clientHeight - fromBottom);
    }
  } catch (error) {
    abortAttach(id);
    throw error;
  }
}

// showSwitching tells the reader another chat is on its way: the old transcript
// dims and an "Opening chat…" pill fades in after a beat (CSS), so a quick
// switch never flashes it and a slow one never looks frozen.
function showSwitching(label: string, id: string): void {
  chatViewEl.classList.add("is-switching");
  // The sidebar selection moves to the clicked chat now, not when it has opened.
  document.querySelectorAll<HTMLElement>("#sidebar .is-pending").forEach((row) => row.classList.remove("is-pending"));
  document.querySelectorAll<HTMLElement>("#sidebar [data-session]").forEach((pick) => {
    if (pick.dataset.session === id) pick.closest(".session-row, .recent-row")?.classList.add("is-pending");
  });
  const pill = document.getElementById("chatLoading");
  if (!pill) return;
  pill.querySelector("#chatLoadingText")!.textContent = label;
  pill.hidden = false;
}

function hideSwitching(): void {
  chatViewEl.classList.remove("is-switching");
  document.querySelectorAll<HTMLElement>("#sidebar .is-pending").forEach((row) => row.classList.remove("is-pending"));
  const pill = document.getElementById("chatLoading");
  if (pill) pill.hidden = true;
}

function switchingLabel(id: string): string {
  switch (state.sessions.find((s) => s.sessionId === id)?.harness) {
    case "claude":
      return "Opening Claude Code chat…";
    case "codex":
      return "Opening Codex chat…";
    default:
      return "Opening chat…";
  }
}

export async function openSession(id: string): Promise<void> {
  const generation = ++openGeneration;
  const damaged = state.sessions.find((s) => s.sessionId === id)?.damaged;
  if (damaged) {
    hideSwitching();
    setStatus(`This session can't be opened (${damaged}). You can still delete it.`, "error");
    return;
  }
  showSwitching(switchingLabel(id), id);
  try {
    // Use the listed workspace to avoid searching every namespace.
    const session = await api.openSession(id, state.sessions.find((item) => item.sessionId === id)?.cwd || null);
    if (generation !== openGeneration) return;

    state.sessionId = session.sessionId;
    window.dispatchEvent(new Event("paladin:session-selected"));
    void refreshRecovery("");
    state.subagent = session.subagent ?? null;
    state.cwd = session.cwd;
    paintParentBar();
    syncSidePane();
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

    // An engine's catalog (models, commands) comes from launching its CLI,
    // which takes seconds. The composer takes the session's look now and the
    // history is read in parallel; the catalog finishes in the background.
    previewHarness(session.harness, session.model);
    const catalog = openedHarness(session.harness, session.model);
    // A Paladin chat has no catalog to wait for, so its provider and model
    // are applied right here before the history is read.
    if (!session.harness) await catalog;
    if (generation !== openGeneration) return;
    if (session.harness) {
      // The harness picker is already showing the session's model.
    } else if (session.provider) {
      state.selectedProvider = session.provider;
      state.selectedLab = session.model ? splitModel(session.model).lab : "";
      state.selectedModel = session.model;
      // The session's last-used model becomes the global default too,
      // so a later provider refresh (or an app restart) restores this
      // instead of falling back to another provider's model.
      persistModelChoice();
      syncModelSelects();
    } else {
      modelSelectLabelEl.textContent = session.model ? bare(splitModel(session.model).short) : "default";
    }

    // Attach is atomic: persisted history plus the live turn (if it has
    // not landed yet) and the queue, so nothing shows twice or not at
    // all. Events arriving meanwhile are buffered and replayed.
    beginAttach(session.sessionId);
    let snap;
    try {
      snap = await api.attachSession(session.sessionId, 0, session.cwd);
    } catch (error) {
      abortAttach(session.sessionId);
      throw error;
    }
    if (generation !== openGeneration || state.sessionId !== session.sessionId) {
      // The user moved on while this loaded; keep the buffer flowing.
      abortAttach(session.sessionId);
      return;
    }
    renderHistory(snap.history);
    finishAttach(session.sessionId, snap);
    hideSwitching();
    void refreshRecovery(session.sessionId);

    // The ledger persists across restarts, so opening a session shows
    // its last-known totals immediately. The todo hat shows its plan.
    void refreshUsage();
    void refreshTodos();
    if (!state.busy) {
      setStatus("ready", "ok");
    }
    // Last: the engine's model list and commands, whenever its CLI is up.
    await catalog;
  } catch (error) {
    if (generation === openGeneration) {
      hideSwitching();
      setStatus(String(error), "error");
    }
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
// first pixels; older rows now load on demand. The core turn-aligns
// every window, so each page is a whole number of turn groups.
const HISTORY_PAGE = 120;
let historyStart = 0;
let historyHasMore = false;
let historyLoading = false;
let prefetchedHistory: { sessionId: string; before: number; page: Promise<HistoryPage> } | null = null;
// Turn groups fetched but not yet painted. Scroll-to-top pops one group
// per hit, so climbing into deep history happens one question at a time
// instead of 120 rows at a jump. The queue is newest-first: the group
// nearest the painted top is shifted from the head.
let earlierGroups: MessageInfo[][] = [];

// splitTurnGroups cuts a page into whole turn groups, the same way
// buildHistory groups rows: a user row opens a new group. The page is
// turn-aligned already (see the core's HistoryPageStats), so every
// group here is complete.
function splitTurnGroups(messages: MessageInfo[]): MessageInfo[][] {
  const groups: MessageInfo[][] = [];
  let current: MessageInfo[] = [];
  for (const message of messages) {
    if (message.role === "user" && current.length) {
      groups.push(current);
      current = [];
    }
    current.push(message);
  }
  if (current.length) groups.push(current);
  return groups;
}

function prefetchEarlierHistory(sessionId: string): void {
  if (!historyHasMore || !sessionId) {
    prefetchedHistory = null;
    return;
  }
  const before = historyStart;
  const page = api.sessionHistoryPage(sessionId, state.cwd || null, HISTORY_PAGE, before);
  // Handle a failed speculative request; a click can retry it normally.
  void page.catch(() => {
    if (prefetchedHistory?.page === page) prefetchedHistory = null;
  });
  prefetchedHistory = { sessionId, before, page };
}

export async function loadHistory(id: string): Promise<void> {
  const generation = openGeneration;
  const cwd = state.cwd || null;
  const page = await api.sessionHistoryPage(id, cwd, HISTORY_PAGE, 0);
  if (generation === openGeneration && id === state.sessionId) renderHistory(page);
}

// earlierRow is the hairline at the top of the transcript. Loading is
// automatic on scroll, so it is not a button any more: it says how much
// older history remains, and a click still loads the next turn for the
// rare case scrolling is impossible (a zero-height view, for instance).
function earlierRow(): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "history-earlier";
  wrap.textContent = "";
  wrap.addEventListener("click", () => void loadEarlierTurn());
  return wrap;
}

function paintEarlierRow(): void {
  const wrap = messagesEl.querySelector<HTMLElement>(".history-earlier");
  if (!wrap) return;
  const left = earlierGroups.length;
  if (historyLoading) wrap.textContent = "Loading…";
  else if (historyHasMore)
    wrap.textContent = `${left ? `${left} turn${left === 1 ? "" : "s"} ready · ` : ""}scroll for earlier messages`;
  else wrap.textContent = "";
}

// fetchEarlierPage brings in the next page of older rows and parks its
// turn groups in the queue. Row ordinals count from the start of the
// session, so a turn landing at the end meanwhile does not shift what
// `historyStart` points at.
async function fetchEarlierPage(): Promise<void> {
  const id = state.sessionId;
  if (!id || historyLoading || !historyHasMore || earlierGroups.length) return;
  const generation = openGeneration;
  historyLoading = true;
  paintEarlierRow();
  const cwd = state.cwd || null;
  const before = historyStart;
  try {
    const cached = prefetchedHistory;
    let page: HistoryPage;
    if (cached?.sessionId === id && cached.before === before) {
      page = await cached.page.catch(() => api.sessionHistoryPage(id, cwd, HISTORY_PAGE, before));
    } else {
      page = await api.sessionHistoryPage(id, cwd, HISTORY_PAGE, before);
    }
    if (generation !== openGeneration || id !== state.sessionId) return;
    // Groups nearest the painted top must prepend first, so the queue
    // is newest-first and each hit shifts the head.
    earlierGroups = splitTurnGroups(page.messages).reverse();
    historyStart = page.start;
    historyHasMore = page.hasMore;
    if (!page.hasMore) {
      messagesEl.querySelector(".history-earlier")?.remove();
    }
    prefetchEarlierHistory(id);
  } catch (error) {
    if (generation === openGeneration && id === state.sessionId) {
      toast(`Could not load earlier messages: ${String(error)}`, "error");
    }
  } finally {
    if (generation === openGeneration) {
      historyLoading = false;
      paintEarlierRow();
      // A scroll-hit that arrived while this request was in flight
      // could not act: with scrollTop already 0 no new scroll event
      // will ever come, so its turn is prepended here instead of
      // leaving the user stuck at the top.
      if (pendingTopHit && earlierGroups.length) {
        pendingTopHit = false;
        void loadEarlierTurn();
      }
    }
  }
}

// pendingTopHit records a scroll-hit that arrived while a page fetch was
// already in flight. The view is already at the top, so no further
// scroll event would ever fire; the fetch serves it when it lands.
let pendingTopHit = false;

// loadEarlierTurn prepends exactly one whole turn group — the one just
// above the current top — and fetches the next page when the queue runs
// dry. Scroll-to-top calls this, so climbing into deep history happens
// one question at a time.
async function loadEarlierTurn(): Promise<void> {
  if (!state.sessionId) return;
  if (historyLoading) {
    if (historyHasMore || earlierGroups.length) pendingTopHit = true;
    return;
  }
  if (!earlierGroups.length) {
    if (!historyHasMore) return;
    await fetchEarlierPage();
    if (!earlierGroups.length) return;
  }
  const group = earlierGroups.shift()!;
  const generation = openGeneration;
  const holder = document.createElement("div");
  buildHistory(holder, group, !state.subagent);
  const anchor = messagesEl.querySelector(".history-earlier");
  const prevHeight = chatViewEl.scrollHeight;
  const prevTop = chatViewEl.scrollTop;
  const fragment = document.createDocumentFragment();
  while (holder.firstChild) fragment.append(holder.firstChild);
  messagesEl.insertBefore(fragment, anchor);
  // Keep the row the user was reading under the cursor.
  chatViewEl.scrollTop = prevTop + (chatViewEl.scrollHeight - prevHeight);
  if (generation !== openGeneration) return;
  paintEarlierRow();
  // Warm the next page while the user reads this one.
  if (!earlierGroups.length && historyHasMore) void fetchEarlierPage();
}

// renderHistory paints a persisted transcript page. An older core may
// still answer with a bare array; accept both shapes.
function renderHistory(raw: HistoryPage | MessageInfo[]): void {
  const page: HistoryPage = Array.isArray(raw) ? { messages: raw, start: 0, hasMore: false, total: raw.length } : raw;
  historyStart = page.start ?? 0;
  historyHasMore = page.hasMore === true;
  historyLoading = false;
  prefetchedHistory = null;
  earlierGroups = [];
  messagesEl.innerHTML = "";
  // One layout for the whole transcript: keep the container display:none
  // while rows are appended, then paint once.
  messagesEl.style.display = "none";
  if (historyHasMore) {
    messagesEl.append(earlierRow());
    paintEarlierRow();
  }
  // A subagent's conversation is driven by its parent: no rewinding it.
  buildHistory(messagesEl, page.messages ?? [], !state.subagent);
  void refreshChanges(state.sessionId);
  messagesEl.style.display = "";

  if (messagesEl.childElementCount === 0) {
    renderGreeting();
  }

  scrollToBottom();
  prefetchEarlierHistory(state.sessionId);
}

export function newSession(): void {
  openGeneration++;
  hideSwitching();
  setHarness("");
  state.sessionId = "";
  window.dispatchEvent(new Event("paladin:session-selected"));
  void refreshRecovery("");
  state.subagent = null;
  paintParentBar();
  syncSidePane();
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
  void api.startCodexDiscovery().catch((error: unknown) => console.debug("Codex discovery unavailable", error));
  void api.startClaudeDiscovery().catch((error: unknown) => console.debug("Claude discovery unavailable", error));
  window.setInterval(() => void syncLinkedSession(), 3000);
  window.addEventListener("focus", () => void syncLinkedSession());
  // A folder's + shows a spinner while an engine starts there.
  window.addEventListener(STARTING_CHANGED, renderSessions);
  document.addEventListener("visibilitychange", () => void syncLinkedSession());
  // Scrolling to the top of the transcript loads the next older turn by
  // itself: the old "Load earlier messages" button is gone, so deep
  // history reads like one continuous climb. One hit prepends exactly
  // one whole turn group (see loadEarlierTurn).
  chatViewEl.addEventListener("scroll", () => {
    if (chatViewEl.scrollTop <= 0 && (historyHasMore || earlierGroups.length)) {
      void loadEarlierTurn();
    }
  });
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
