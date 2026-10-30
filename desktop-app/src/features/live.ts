// live.ts keeps every session's turns live at once. The sidecar runs
// turns in the background and publishes their events for all sessions;
// this module routes them by session into per-session live roots that
// stay mounted while the session is on screen and keep updating,
// detached, while it is not. Opening a session re-attaches: persisted
// history from the sidecar plus whatever is still running or queued.

import * as api from "../core/api";
import { messagesEl, pauseBtn, sendBtn, statusDetailEl, steerBtn } from "../core/dom";
import { state } from "../core/state";
import type { Approval, AttachSnapshot, ToolInfo, TurnEvent, TurnInfo } from "../core/types";
import { setStatus } from "../ui/status";
import { toast } from "../ui/toast";
import { escapeHtml } from "../core/format";
import { createTurn, dismissGreeting, followTail, scrollToBottom } from "./chatView";
import { TurnView } from "./turnView";
import { handleAskToolEvent } from "./ask";
import { handleTodoToolEvent, refreshTodos } from "./todos";
import { refreshUsage } from "./usage";
import { refreshSessions, renderSessions } from "./sessions";
import { refreshSkillsCache } from "./skills";
import { refreshWorktreeBar, syncWorktreeChip } from "./worktrees";
import { syncBudget } from "./budget";
import { PHASE_TEXT, formatElapsed, workingBar, workingLabel, type WorkingPhase } from "./workingBar";

export type LiveBadge = "" | "running" | "queued" | "done" | "error" | "input" | "approval";

type Entry = {
  sessionId: string;
  // root holds this session's live rows; display:contents, so its rows
  // lay out as direct children of #messages.
  root: HTMLElement;
  running: TurnInfo | null;
  queued: TurnInfo[];
  views: Map<string, TurnView>;
  users: Map<string, HTMLElement>;
  // Outcome badge for a session finished while not on screen.
  badge: "" | "done" | "error";
  asks: Map<string, ToolInfo>;
  // Approval cards still waiting on the user, by call id.
  approvals: Map<string, Approval>;
  // The sidecar said a turn awaits approval before we saw its events.
  awaitingHint: boolean;
  // Events that land while an attach is in flight wait here.
  attaching: TurnEvent[] | null;
  // Recently finished turn ids: a late submit reply must not revive them.
  finished: Set<string>;
  // What the running turn is doing, for the working bar: tools still
  // running (id → label) and whether the reply is streaming.
  tools: Map<string, string>;
  phase: WorkingPhase;
  since: number;
};

const entries = new Map<string, Entry>();
let wired = false;
let sidebarQueued = false;

function entry(sessionId: string): Entry {
  let e = entries.get(sessionId);
  if (!e) {
    const root = document.createElement("div");
    root.className = "live-root";
    root.dataset.liveSession = sessionId;
    e = {
      sessionId,
      root,
      running: null,
      queued: [],
      views: new Map(),
      users: new Map(),
      badge: "",
      asks: new Map(),
      approvals: new Map(),
      awaitingHint: false,
      attaching: null,
      finished: new Set(),
      tools: new Map(),
      phase: "thinking",
      since: 0,
    };
    entries.set(sessionId, e);
  }
  return e;
}

function visible(sessionId: string): boolean {
  return !!sessionId && sessionId === state.sessionId;
}

function titleOf(sessionId: string): string {
  return state.sessions.find((s) => s.sessionId === sessionId)?.title || sessionId.slice(0, 8);
}

function sidebar(): void {
  if (sidebarQueued) return;
  sidebarQueued = true;
  queueMicrotask(() => {
    sidebarQueued = false;
    renderSessions();
  });
}

// mount puts the visible session's live rows on screen (after history).
function mount(e: Entry): void {
  if (!visible(e.sessionId) || e.root.isConnected) return;
  if (e.root.childElementCount > 0) {
    dismissGreeting();
  }
  messagesEl.append(e.root);
}

// liveBadge is the sidebar indicator for a session.
export function liveBadge(sessionId: string): LiveBadge {
  const e = entries.get(sessionId);
  if (!e) return "";
  if (e.running && (e.approvals.size > 0 || e.awaitingHint)) return "approval";
  if (e.asks.size > 0 && !visible(sessionId)) return "input";
  if (e.running) return "running";
  if (e.queued.length > 0) return "queued";
  return e.badge;
}

export function queuedCount(sessionId: string): number {
  return entries.get(sessionId)?.queued.length ?? 0;
}

export function isBusy(sessionId: string): boolean {
  const e = entries.get(sessionId);
  return !!e && (e.running !== null || e.queued.length > 0);
}

// syncComposer shows the visible session's controls: pause and steer
// while it works. Send stays live so a follow-up queues behind the
// running turn; steer adds it to the running turn instead.
export function syncComposer(): void {
  const e = state.sessionId ? entries.get(state.sessionId) : undefined;
  const busy = !!e && e.running !== null;
  state.busy = busy;
  pauseBtn.classList.toggle("hidden", !busy);
  sendBtn.disabled = false;
  sendBtn.classList.remove("hidden");
  sendBtn.title = busy ? "Queue after the running turn" : "Send";
  steerBtn.classList.toggle("hidden", !busy);
  paintWorking();
  syncWorktreeChip();
}

function queuedRow(e: Entry, turn: TurnInfo): HTMLElement {
  const { wrapper, textEl } = createTurn("user", turn.display || turn.message, { skill: turn.skill ?? null });
  wrapper.dataset.turnId = turn.turnId;
  wrapper.dataset.queued = "";
  wrapper.classList.add("turn-queued");
  const chip = document.createElement("div");
  chip.className = "queued-chip";
  chip.innerHTML =
    `<span>Queued</span>` +
    `<button type="button" data-cancel-turn="${escapeHtml(turn.turnId)}" title="Remove from the queue" aria-label="Remove queued message">✕</button>`;
  chip.querySelector("button")!.addEventListener("click", () => {
    void api.cancelTurn(turn.turnId).catch((error) => toast(`Could not cancel: ${String(error)}`, "error"));
  });
  textEl.prepend(chip);
  e.users.set(turn.turnId, wrapper);
  return wrapper;
}

function promote(row: HTMLElement): void {
  delete row.dataset.queued;
  row.classList.remove("turn-queued");
  row.querySelector(".queued-chip")?.remove();
}

function newView(e: Entry): TurnView {
  return new TurnView({
    sessionId: e.sessionId,
    visible: () => visible(e.sessionId),
    tool: (tool) => onTool(e, tool),
  });
}

function onTool(e: Entry, tool: ToolInfo): void {
  if (tool.name === "ask") {
    if (tool.status === "running") {
      if (visible(e.sessionId) && !e.attaching) {
        handleAskToolEvent(e.sessionId, tool);
      } else {
        e.asks.set(tool.id, tool);
        sidebar();
      }
    } else {
      e.asks.delete(tool.id);
      handleAskToolEvent(e.sessionId, tool);
      sidebar();
    }
  }
  if (tool.name === "todo" && visible(e.sessionId)) {
    handleTodoToolEvent(tool);
  }
}

// startTurn renders a running turn: its user row (a queued bubble is
// promoted in place) and a fresh agent view right after it.
function startTurn(e: Entry, turn: TurnInfo): TurnView {
  e.queued = e.queued.filter((q) => q.turnId !== turn.turnId);
  if (e.running?.turnId !== turn.turnId) {
    e.tools.clear();
    e.approvals.clear();
    e.awaitingHint = !!turn.awaitingApproval;
    e.phase = "thinking";
    e.since = 0;
  }
  e.running = turn;
  let anchor: HTMLElement | null = e.users.get(turn.turnId) ?? null;
  if (anchor) {
    promote(anchor);
  } else if (!turn.resume) {
    anchor = createTurn("user", turn.display || turn.message, { skill: turn.skill ?? null }).wrapper;
    anchor.dataset.turnId = turn.turnId;
    e.users.set(turn.turnId, anchor);
    e.root.insertBefore(anchor, firstQueued(e));
  }
  let view = e.views.get(turn.turnId);
  if (!view) {
    view = newView(e);
    e.views.set(turn.turnId, view);
    if (anchor) {
      anchor.after(view.wrapper);
    } else {
      e.root.insertBefore(view.wrapper, firstQueued(e));
    }
  }
  return view;
}

function firstQueued(e: Entry): HTMLElement | null {
  return e.root.querySelector<HTMLElement>(":scope > [data-queued]");
}

function apply(e: Entry, ev: TurnEvent): void {
  const turn = ev.turn;
  switch (ev.event) {
    case "turn/queued": {
      if (!turn || e.finished.has(turn.turnId) || e.running?.turnId === turn.turnId) return;
      if (!e.queued.some((q) => q.turnId === turn.turnId)) {
        e.queued.push(turn);
      }
      if (!e.users.has(turn.turnId) && !turn.resume) {
        e.root.append(queuedRow(e, turn));
      }
      if (visible(e.sessionId)) {
        mount(e);
        followTail();
        syncComposer();
      }
      sidebar();
      return;
    }
    case "turn/start": {
      if (!turn || e.finished.has(turn.turnId)) return;
      const view = startTurn(e, turn);
      view.lastSeq = Math.max(view.lastSeq, ev.seq || 0);
      if (visible(e.sessionId)) {
        mount(e);
        followTail();
        setStatus("thinking…", "busy");
        syncComposer();
      }
      sidebar();
      return;
    }
    case "turn/done": {
      if (!turn) return;
      finishTurn(e, turn);
      return;
    }
    default: {
      const view = e.views.get(ev.turnId);
      view?.apply(ev);
      if (e.running?.turnId === ev.turnId && track(e, ev) && visible(e.sessionId)) {
        paintWorking();
      }
    }
  }
}

// track folds a live event into the entry's activity. True when the
// working bar text may have changed.
function track(e: Entry, ev: TurnEvent): boolean {
  switch (ev.event) {
    case "chat/tool": {
      const tool = ev.tool;
      if (!tool) return false;
      if (tool.status === "running") e.tools.set(tool.id, workingLabel(tool));
      else e.tools.delete(tool.id);
      if (e.tools.size === 0) e.phase = "thinking";
      return true;
    }
    case "chat/delta":
      if (e.phase === "writing" && e.tools.size === 0) return false;
      e.phase = "writing";
      return true;
    case "chat/reasoning":
      if (e.phase === "thinking") return false;
      e.phase = "thinking";
      return true;
    case "chat/retry":
      e.phase = "retrying";
      return true;
    case "chat/compact":
      e.phase = "compacting";
      return true;
    case "chat/approval":
      return noteApproval(e, ev.approval);
    case "chat/steer":
      e.phase = "thinking";
      return true;
    case "chat/budget":
      if (visible(e.sessionId)) void syncBudget();
      return false;
  }
  return false;
}

// noteApproval keeps the entry's waiting set in step with the cards.
function noteApproval(e: Entry, ap: Approval | undefined): boolean {
  if (!ap?.id) return false;
  e.awaitingHint = false;
  if (ap.state === "pending") e.approvals.set(ap.id, ap);
  else e.approvals.delete(ap.id);
  sidebar();
  return true;
}

// ticker refreshes the working bar's elapsed time once a second.
let ticker = 0;

// paintWorking shows what the visible session's running turn is doing:
// a spinner, how long it has run, and its current step. It stays up for
// the whole turn so a quiet stretch never looks like a stall.
function paintWorking(): void {
  const el = workingBar();
  const e = state.sessionId ? entries.get(state.sessionId) : undefined;
  const turn = e?.running;
  if (!e || !turn) {
    el.classList.add("hidden");
    window.clearInterval(ticker);
    ticker = 0;
    return;
  }
  if (!e.since) {
    const started = turn.startedAt ? Date.parse(turn.startedAt) : NaN;
    e.since = Number.isFinite(started) ? started : Date.now();
  }
  const labels = [...e.tools.values()];
  const waiting = e.approvals.size > 0 || e.awaitingHint;
  const what = waiting
    ? "Waiting for approval"
    : turn.compact
      ? "Compacting context"
      : labels.length
        ? labels[labels.length - 1]
        : PHASE_TEXT[e.phase];
  el.classList.remove("hidden");
  el.dataset.phase = waiting ? "approval" : labels.length ? "tool" : e.phase;
  el.querySelector(".working-title")!.textContent = turn.mode === "plan" ? "Planning" : "Working";
  el.querySelector(".working-time")!.textContent = formatElapsed(Date.now() - e.since);
  const whatEl = el.querySelector<HTMLElement>(".working-what")!;
  whatEl.textContent = what;
  whatEl.title = what;
  const more = labels.length > 1 ? `+${labels.length - 1} more` : "";
  const queued = e.queued.length ? `${e.queued.length} queued` : "";
  el.querySelector(".working-more")!.textContent = [more, queued].filter(Boolean).join(" · ");
  if (!ticker) {
    ticker = window.setInterval(() => {
      const cur = state.sessionId ? entries.get(state.sessionId) : undefined;
      if (!cur?.running) {
        paintWorking();
        return;
      }
      el.querySelector(".working-time")!.textContent = formatElapsed(Date.now() - cur.since);
    }, 1000);
  }
}

function finishTurn(e: Entry, turn: TurnInfo): void {
  e.finished.add(turn.turnId);
  if (e.finished.size > 64) {
    e.finished.delete(e.finished.values().next().value as string);
  }
  const wasQueued = e.running?.turnId !== turn.turnId;
  e.queued = e.queued.filter((q) => q.turnId !== turn.turnId);
  const shown = visible(e.sessionId);
  if (wasQueued && turn.cancelled) {
    e.users.get(turn.turnId)?.remove();
    e.users.delete(turn.turnId);
    if (shown) syncComposer();
    sidebar();
    return;
  }
  e.running = null;
  e.tools.clear();
  e.approvals.clear();
  e.awaitingHint = false;
  e.since = 0;
  for (const [id, ask] of e.asks) {
    // A turn that ended cannot still be waiting for an answer.
    handleAskToolEvent(e.sessionId, { ...ask, status: "failed" });
    e.asks.delete(id);
  }
  const view = e.views.get(turn.turnId);
  e.views.delete(turn.turnId);
  e.users.delete(turn.turnId);
  const failure = view ? view.finish(turn) : turn.error || null;
  if (failure) {
    const err = createTurn("error", failure.startsWith("Error") ? failure : `Error: ${failure}`).wrapper;
    if (view?.wrapper.isConnected || view?.wrapper.parentElement) {
      view.wrapper.after(err);
    } else {
      e.root.insertBefore(err, firstQueued(e));
    }
    if (turn.skill && /not found: skill/i.test(failure)) {
      refreshSkillsCache();
    }
  }
  const quiet = turn.cancelled || turn.stopped;
  if (shown) {
    e.badge = "";
    if (failure) {
      setStatus(failure, "error");
    } else if (quiet) {
      setStatus("stopped", "ok");
      statusDetailEl.textContent = "STOPPED";
    } else {
      setStatus("ready", "ok");
      statusDetailEl.textContent = "READY";
    }
    syncComposer();
    // Tokens bill even on failed turns; todos may have settled after
    // the last live event.
    void refreshUsage();
    void syncBudget();
    void refreshTodos();
    void refreshWorktreeBar();
  } else if (!quiet) {
    e.badge = failure ? "error" : "done";
    const what = failure ? "failed" : "finished";
    toast(`“${titleOf(e.sessionId)}” ${what}`, failure ? "error" : "ok");
  }
  void refreshSessions();
}

// noteSubmitted reconciles a submit reply with the event stream: the
// events normally arrive first, but nothing breaks if they do not.
export function noteSubmitted(info: TurnInfo): void {
  const e = entry(info.sessionId);
  if (e.attaching || e.finished.has(info.turnId)) return;
  if (info.state === "running" && e.running?.turnId !== info.turnId) {
    apply(e, { event: "turn/start", seq: 0, sessionId: info.sessionId, turnId: info.turnId, turn: info });
  } else if (info.state === "queued" && !e.queued.some((q) => q.turnId === info.turnId)) {
    apply(e, { event: "turn/queued", seq: 0, sessionId: info.sessionId, turnId: info.turnId, turn: info });
  }
}

function onEvent(ev: TurnEvent): void {
  if (ev?.event === "host/restarted") {
    void recoverFromRestart();
    return;
  }
  if (!ev || typeof ev.sessionId !== "string" || !ev.sessionId) return;
  const e = entry(ev.sessionId);
  if (e.attaching) {
    e.attaching.push(ev);
    return;
  }
  try {
    apply(e, ev);
  } catch (error) {
    console.error("live event failed", ev.event, error);
  }
}

// recoverFromRestart runs after the agent host crashed and was
// restarted: its running and queued turns died with it, so they are
// ended here before re-reading what the new host knows.
async function recoverFromRestart(): Promise<void> {
  for (const e of entries.values()) {
    const dropped = e.queued.length;
    for (const queued of [...e.queued]) {
      finishTurn(e, { ...queued, state: "done", cancelled: true });
    }
    if (e.running) {
      const lost = dropped > 0 ? ` and ${dropped} queued message${dropped === 1 ? " was" : "s were"} dropped` : "";
      finishTurn(e, { ...e.running, state: "done", error: `agent host restarted: this turn was interrupted${lost}` });
    }
  }
  await bootLive();
}

// beginAttach starts buffering a session's events while its snapshot
// is fetched.
export function beginAttach(sessionId: string): void {
  entry(sessionId).attaching = [];
}

// abortAttach releases the buffer when the snapshot could not be read.
export function abortAttach(sessionId: string): void {
  const e = entry(sessionId);
  const buffered = e.attaching ?? [];
  e.attaching = null;
  for (const ev of buffered) apply(e, ev);
}

// finishAttach rebuilds the live rows from a snapshot (history has
// already been rendered), mounts them, and replays buffered events.
export function finishAttach(sessionId: string, snap: AttachSnapshot): void {
  const e = entry(sessionId);
  const keep: HTMLElement[] = [];
  const live = snap.turn && snap.turn.state === "running" ? snap.turn : null;
  // Rows of turns that already landed in history are dropped.
  for (const [id, view] of e.views) {
    if (!live || live.turnId !== id || live.persisted) {
      e.views.delete(id);
      view.wrapper.remove();
    }
  }
  e.running = live;
  e.approvals.clear();
  e.awaitingHint = false;
  if (live && !live.persisted) {
    for (const ev of snap.events ?? []) {
      if (ev.event === "chat/approval") noteApproval(e, ev.approval);
    }
    let view = e.views.get(live.turnId);
    let user = e.users.get(live.turnId) ?? null;
    if (!view) {
      if (!live.resume && !user) {
        user = createTurn("user", live.display || live.message, { skill: live.skill ?? null }).wrapper;
        user.dataset.turnId = live.turnId;
      }
      if (user) promote(user);
      view = newView(e);
      e.views.set(live.turnId, view);
      for (const ev of snap.events ?? []) {
        view.apply(ev);
      }
      view.lastSeq = Math.max(view.lastSeq, snap.seq);
    }
    if (user) {
      e.users.set(live.turnId, user);
      keep.push(user);
    }
    keep.push(view.wrapper);
  }
  e.queued = [...(snap.queued ?? [])];
  const users = new Map<string, HTMLElement>();
  if (live && e.users.has(live.turnId)) users.set(live.turnId, e.users.get(live.turnId)!);
  for (const q of e.queued) {
    const row = e.users.get(q.turnId) ?? queuedRow(e, q);
    users.set(q.turnId, row);
    keep.push(row);
  }
  e.users = users;
  e.root.replaceChildren(...keep);
  e.badge = "";
  if (!live) {
    e.asks.clear();
  }
  const buffered = e.attaching ?? [];
  e.attaching = null;
  mount(e);
  if (e.root.childElementCount > 0) {
    scrollToBottom();
  }
  for (const ev of buffered) {
    try {
      apply(e, ev);
    } catch (error) {
      console.error("live replay failed", ev.event, error);
    }
  }
  // Questions asked while the session was hidden open now.
  for (const ask of [...e.asks.values()]) {
    e.asks.delete(ask.id);
    handleAskToolEvent(e.sessionId, ask);
  }
  if (e.running) {
    setStatus("thinking…", "busy");
  }
  syncComposer();
  sidebar();
}

// clearLanded drops rows of finished turns from the visible session
// (the transcript was cleared by hand).
export function clearLanded(sessionId: string): void {
  const e = entries.get(sessionId);
  if (!e) return;
  const keep = new Set<Node>();
  for (const v of e.views.values()) keep.add(v.wrapper);
  for (const u of e.users.values()) keep.add(u);
  for (const child of Array.from(e.root.children)) {
    if (!keep.has(child)) child.remove();
  }
}

// forgetSession drops a deleted session's live state.
export function forgetSession(sessionId: string): void {
  const e = entries.get(sessionId);
  if (!e) return;
  e.root.remove();
  entries.delete(sessionId);
}

// bootLive learns which turns are already running (the sidecar outlives
// the window) so the sidebar shows them before any session is opened.
export async function bootLive(): Promise<void> {
  let turns: TurnInfo[];
  try {
    turns = await api.listTurns();
  } catch (error) {
    console.warn(`turns failed: ${String(error)}`);
    return;
  }
  for (const t of turns ?? []) {
    const e = entry(t.sessionId);
    if (t.state === "running") {
      if (e.running?.turnId !== t.turnId) e.running = t;
      e.awaitingHint = !!t.awaitingApproval && e.approvals.size === 0;
    } else if (t.state === "queued" && !e.queued.some((q) => q.turnId === t.turnId)) {
      e.queued.push(t);
    }
  }
  sidebar();
}

// initLive subscribes to the event bus once.
export function initLive(open: (sessionId: string) => void): void {
  if (wired) return;
  wired = true;
  try {
    api.onAgentEvent(onEvent);
    api.onOpenSessionRequest((id) => open(id));
  } catch (error) {
    console.warn(`live events unavailable: ${String(error)}`);
  }
}

// takePendingOpen opens the session a notification click asked for
// while no window existed.
export async function openPendingSession(open: (sessionId: string) => void): Promise<void> {
  try {
    const id = await api.takePendingOpen();
    if (id) open(id);
  } catch {
    // No bridge support: nothing pending.
  }
}
