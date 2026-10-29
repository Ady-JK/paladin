// live.ts keeps every session's turns live at once. The sidecar runs
// turns in the background and publishes their events for all sessions;
// this module routes them by session into per-session live roots that
// stay mounted while the session is on screen and keep updating,
// detached, while it is not. Opening a session re-attaches: persisted
// history from the sidecar plus whatever is still running or queued.

import * as api from "../core/api";
import { continueBtn, messagesEl, pauseBtn, sendBtn, statusDetailEl } from "../core/dom";
import { state } from "../core/state";
import type { AttachSnapshot, ToolInfo, TurnEvent, TurnInfo } from "../core/types";
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

export type LiveBadge = "" | "running" | "queued" | "done" | "error" | "input" | "paused";

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
  badge: "" | "done" | "error" | "paused";
  asks: Map<string, ToolInfo>;
  paused: boolean;
  // Events that land while an attach is in flight wait here.
  attaching: TurnEvent[] | null;
  // Recently finished turn ids: a late submit reply must not revive them.
  finished: Set<string>;
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
      paused: false,
      attaching: null,
      finished: new Set(),
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

// syncComposer shows the visible session's controls: pause while it
// works, continue when its last turn hit the round cap. Send stays live
// so a follow-up queues behind the running turn.
export function syncComposer(): void {
  const e = state.sessionId ? entries.get(state.sessionId) : undefined;
  const busy = !!e && e.running !== null;
  state.busy = busy;
  pauseBtn.classList.toggle("hidden", !busy);
  sendBtn.disabled = false;
  sendBtn.classList.remove("hidden");
  sendBtn.title = busy ? "Queue after the running turn" : "Send";
  continueBtn.classList.toggle("hidden", !(e?.paused && !busy && e.queued.length === 0));
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
  e.running = turn;
  e.paused = false;
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
    }
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
  e.paused = !!turn.needsContinue && !failure;
  const quiet = turn.cancelled || turn.stopped;
  if (shown) {
    e.badge = "";
    if (failure) {
      setStatus(failure, "error");
    } else if (quiet) {
      setStatus("stopped", "ok");
      statusDetailEl.textContent = "STOPPED";
    } else if (turn.needsContinue) {
      toast("Paused at the round cap — continue?");
      setStatus("paused", "ok");
      statusDetailEl.textContent = "PAUSED";
    } else {
      setStatus("ready", "ok");
      statusDetailEl.textContent = "READY";
    }
    syncComposer();
    // Tokens bill even on failed turns; todos may have settled after
    // the last live event.
    void refreshUsage();
    void refreshTodos();
  } else if (!quiet) {
    e.badge = failure ? "error" : turn.needsContinue ? "paused" : "done";
    const what = failure ? "failed" : turn.needsContinue ? "paused at the round cap" : "finished";
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
  if (live && !live.persisted) {
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
  let turns: TurnInfo[] = [];
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
