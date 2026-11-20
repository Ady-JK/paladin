// sidePane.ts is the split pane beside the conversation. Its tabs are
// /btw side chats (saved per session, each with its own composer) and
// live views of subagents. Each session keeps its own set of tabs; a
// hidden tab keeps its DOM, so switching back loses nothing.

import * as api from "../core/api";
import "@xterm/xterm/css/xterm.css";
import { $, sidePaneEl } from "../core/dom";
import { bridge, hasBridge } from "../core/bridge";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import { loadText, saveText } from "../core/storage";
import type { RunningJob, SessionChanges, SideChatMessage } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { registerChangesLink } from "./findings";
import { abortAttach, hasRunningTurn, liveBadge, liveRoot, onResync, runningTurnIndex, runningUserRow } from "./live";
import { agentStatus, openSession, refreshSessions, renderSessions } from "./sessions";
import { formatElapsed } from "./workingBar";
import { createAgentTab, loadAgent, paintHead } from "./sidePane/agentTab";
import { clip, errorText } from "./sidePane/utils";
import { createChatTab, isBlankChat, sendChat } from "./sidePane/sideChat";
import { activateTerminal, newTerminalTab, terminalEvent } from "./sidePane/terminal";
import { TerminalDock } from "./ide/terminalDock";
import { newWebTab, normalizeWebUrl, setWebStatus, webHost } from "./sidePane/webTab";
import { navigateBrowser, newBrowserTab, stopBrowserPick, updateBrowserToolbar } from "./sidePane/browserTab";
import { loadChangeDiff, newChangesTab, paintChangesTab } from "./sidePane/changesTab";
import { wireSplitResize } from "./sidePane/splitResize";
import {
  clearChangeTotals,
  paintTurnMarker,
  retainChangeTotals,
  runningTurn,
  turnTotal,
} from "./sidePane/changeMarkers";

export type { WebTab } from "./sidePane/types";
import type {
  AgentTab,
  BrowserTab,
  ChangesTab,
  ChatTab,
  JobsTab,
  Tab,
  TabSet,
  TerminalTab,
  WebTab,
} from "./sidePane/types";

const tabsEl = $("#paneTabs");
const drawerTabsEl = $("#paneDrawerTabs");
const tabsDrawerEl = $("#paneTabsDrawer");
const tabsDrawerCloseBtn = $<HTMLButtonElement>("#paneTabsDrawerClose");
const bodyEl = $("#paneBody");
const historyBtn = $<HTMLButtonElement>("#paneNewChatBtn");
const menuEl = $("#paneHistoryMenu");
const menuItemsEl = $("#paneHistoryItems");
const layoutBtn = $<HTMLButtonElement>("#paneLayoutBtn");
const tabsRailBtn = $<HTMLButtonElement>("#paneRailToggle");
const changesBtn = $<HTMLButtonElement>("#sessionChangesBtn");
const liveChangesBtn = $<HTMLButtonElement>("#liveChanges");
const runningJobsBtn = $<HTMLButtonElement>("#runningJobsBtn");
const runningJobsCount = $("#runningJobsCount");
const agentView = $("#agentView");
const layoutKey = "paladin.paneLayout";

export const STANDALONE_PANE_SESSION = "__standalone_pane__";
// One tab strip for the window, not one per session. Switching session
// must not swap the strip out from under the user: a browser page you
// opened, or a terminal you started, is still there afterwards. Each tab
// remembers the session it was created for (tab.sessionId), which is what
// lookups below filter on.
const tabSet: TabSet = { tabs: [], active: "" };
const pendingBrowserSessions = new Set<string>();

function visibleSessionId(): string {
  return state.sessionId || STANDALONE_PANE_SESSION;
}
const changesCache = new Map<string, SessionChanges>();
let paneOpen = false;
let layoutMode: "split" | "tabs" = loadText(layoutKey) === "tabs" ? "tabs" : "split";
// The tab list is its own drawer column, opened from the rail button that
// is present in both layouts, exactly like Sessions and Files. It is not a
// strip inside the split: a column there would shrink the native page's
// surface, and a native page cannot be clipped, so the page would stick out
// past its panel while the column animated. A shell column keeps every
// native surface a plain rectangle. Agent is always the first entry.
// Each layout remembers its own choice: tabs layout navigates through the
// drawer so it starts open; split layout has the horizontal strip, so the
// drawer starts closed.
const tabsDrawerKeys = { split: "paladin.tabsDrawer.split", tabs: "paladin.tabsDrawer" } as const;
const tabsDrawerOpen = {
  split: loadText(tabsDrawerKeys.split) === "open",
  tabs: loadText(tabsDrawerKeys.tabs) !== "closed",
};
// Split layout always shows the right pane: the agent is the left half and
// the right half is where terminals, pages, changes and side chats live. A
// user who wants the agent at full width switches to the tabs layout. IDE
// mode has its own arrangement and keeps its own rules.
paneOpen = layoutMode === "split";
let nextKey = 0;
let ideTerminalMode = false;
let ideBrowserKey = "";
let terminalDock: TerminalDock | null = null;
const isBrowserPage = (tab: Tab): tab is BrowserTab | WebTab => tab.kind === "browser" || tab.kind === "web";

export function activateIDEFile(): void {
  if (!ideBrowserKey) return;
  ideBrowserKey = "";
  window.dispatchEvent(new CustomEvent("paladin:ide-browser-active", { detail: false }));
  render();
}

function activateIDEPage(key: string): void {
  window.dispatchEvent(new Event("paladin:ide-hide-explorer"));
  ideBrowserKey = key;
  window.dispatchEvent(new CustomEvent("paladin:ide-browser-active", { detail: true }));
  render();
}

function renderIDEPages(set: TabSet | null): void {
  const tabs = document.getElementById("ideBrowserTabs");
  const area = document.getElementById("ideBrowserArea");
  if (!tabs || !area) return;
  const pages = ideTerminalMode ? (set?.tabs.filter(isBrowserPage) ?? []) : [];
  if (!pages.some((tab) => tab.key === ideBrowserKey)) {
    if (ideBrowserKey) window.dispatchEvent(new CustomEvent("paladin:ide-browser-active", { detail: false }));
    ideBrowserKey = "";
  }
  tabs.innerHTML = pages
    .map(
      (tab) =>
        `<div class="ide-tab${tab.key === ideBrowserKey ? " is-active" : ""}"><button type="button" class="ide-tab-select" data-ide-page="${escapeAttr(tab.key)}" title="${escapeAttr(tab.title)}">${escapeHtml(tab.title)}</button><button type="button" class="ide-tab-close" data-ide-page-close="${escapeAttr(tab.key)}" aria-label="Close ${escapeAttr(tab.title)}">×</button></div>`,
    )
    .join("");
  if (area.childElementCount !== pages.length || pages.some((tab, index) => area.children[index] !== tab.body)) {
    area.replaceChildren(...pages.map((tab) => tab.body));
  }
  for (const page of pages) {
    page.body.hidden = page.key !== ideBrowserKey;
    if (page.kind === "browser" && page.body.hidden) stopBrowserPick(page);
  }
  area.hidden = !ideBrowserKey;
}
function openTerminalTab(sessionId: string, cwd = state.cwd): TerminalTab {
  const set = browserSet(sessionId);
  const visible = visibleSessionId() === sessionId && state.cwd === cwd;
  const owner = ideTerminalMode && visible ? STANDALONE_PANE_SESSION : sessionId;
  const tab = newTerminalTab(owner, cwd, `t${++nextKey}`);
  set.tabs.push(tab);
  if (ideTerminalMode && visible) {
    terminalDock?.show(tab.key);
  } else if (visible || visibleSessionId() !== sessionId) {
    set.active = tab.key;
    if (visible) showPane();
  }
  return tab;
}

export function setIDETerminalMode(on: boolean): void {
  if (!terminalDock) {
    const area = $("#ideBrowserArea");
    new ResizeObserver(syncNativeSurfaces).observe(area);
    $("#ideBrowserTabs").addEventListener("click", (event) => {
      const target = event.target as HTMLElement;
      const close = target.closest<HTMLElement>("[data-ide-page-close]")?.dataset.idePageClose;
      const select = target.closest<HTMLElement>("[data-ide-page]")?.dataset.idePage;
      if (close) closeTab(close);
      else if (select) activateIDEPage(select);
    });
    terminalDock = new TerminalDock({
      workspace: () => state.cwd,
      // The dock shows every terminal running in this workspace, whichever
      // chat started it: a shell outlives the conversation that opened it,
      // and switching chat must not hide a command that is still running.
      terminals: () =>
        tabSet.tabs.filter((tab): tab is TerminalTab => tab.kind === "terminal" && tab.cwd === state.cwd),
      create: () => openTerminalTab(visibleSessionId()),
      close: (key) => closeTab(key),
    });
  }
  const set = currentSet();
  const selected = activeTab(set);
  if (on && selected && isBrowserPage(selected) && paneOpen) {
    window.dispatchEvent(new Event("paladin:ide-hide-explorer"));
    paneOpen = false;
    ideBrowserKey = selected.key;
    window.dispatchEvent(new CustomEvent("paladin:ide-browser-active", { detail: true }));
  }
  if (on && selected?.kind === "terminal" && paneOpen) {
    paneOpen = false;
    terminalDock.show(selected.key);
  } else if (!on && terminalDock.isOpen() && terminalDock.active() && !paneOpen && set) {
    set.active = terminalDock.active()!.key;
    paneOpen = true;
  }
  if (!on && ideBrowserKey && set) {
    set.active = ideBrowserKey;
    paneOpen = true;
  }
  ideTerminalMode = on;
  // IDE mode lays out its own panels, and its terminals and pages live in the
  // dock, so the pane is only there when something else is open in it.
  if (on && !tabSet.tabs.some((tab) => tab.kind !== "terminal" && !isBrowserPage(tab))) paneOpen = false;
  if (!on && layoutMode === "split") paneOpen = true;
  hideLiveChanges(state.sessionId);
  terminalDock.setEnabled(on);
  render();
  if (state.sessionId) void refreshChanges(state.sessionId);
}

// Standalone terminals keep their PTY owner while following the visible
// chat. With one strip per window there is nothing to migrate: the tab is
// already here, so "following" is just re-tagging it to the session now on
// screen, for the same workspace.
function attachStandaloneTerminals(sessionId: string): void {
  if (!sessionId || sessionId === STANDALONE_PANE_SESSION) return;
  for (const tab of tabSet.tabs) {
    if (tab.kind !== "terminal" || tab.sessionId !== STANDALONE_PANE_SESSION) continue;
    if (tab.cwd !== state.cwd) continue;
    tab.sessionId = sessionId;
  }
}

export function showTerminalResult(sessionId: string, cwd: string, command: string, result: api.TerminalResult): void {
  // A standalone result belongs to whichever tab is running in that
  // workspace; find the tab itself rather than a set that owns it.
  const standalone = sessionId === STANDALONE_PANE_SESSION;
  const match = tabSet.tabs.find((tab): tab is TerminalTab => tab.kind === "terminal" && tab.cwd === cwd);
  const paneSession = standalone
    ? state.cwd === cwd
      ? visibleSessionId()
      : (match?.sessionId ?? sessionId)
    : sessionId;
  const set = browserSet(paneSession);
  const visible = visibleSessionId() === paneSession && state.cwd === cwd;
  const selected = ideTerminalMode && visible ? terminalDock?.active() : undefined;
  const tab =
    (selected?.cwd === cwd ? selected : undefined) ??
    set.tabs.find((t): t is TerminalTab => t.kind === "terminal" && t.cwd === cwd) ??
    openTerminalTab(paneSession, cwd);
  if (ideTerminalMode && visible) terminalDock?.show(tab.key);
  else if (visible || visibleSessionId() !== paneSession) {
    set.active = tab.key;
    if (visible) showPane();
  }
  const text = `\r\n$ ${command}\r\n${result.output.replace(/\n/g, "\r\n")}${result.truncated ? "\r\n[output truncated]" : ""}\r\n[exit ${result.exitCode}]\r\n`;
  if (tab.term) tab.term.write(text);
  else tab.pendingText.push(text);
}

// currentSet is the window's tab strip. It always exists: with one set
// there is no "this session has no tabs yet" state to special-case.
function currentSet(): TabSet {
  return tabSet;
}

// browserSet is kept as a name because call sites read as "the set that
// belongs to this session"; with one strip per window it is the same one.
function browserSet(_sessionId: string): TabSet {
  return tabSet;
}

// tabsFor narrows the strip to the tabs created for one session.
function tabsFor(sessionId: string): Tab[] {
  return tabSet.tabs.filter((tab) => tab.sessionId === sessionId);
}

function openBrowserTab(sessionId: string, url = ""): void {
  const set = browserSet(sessionId);
  let tab = set.tabs.find((t): t is BrowserTab => t.kind === "browser");
  if (!tab) {
    tab = newBrowserTab(sessionId, `t${++nextKey}`, syncNativeSurfaces, () => {
      if (layoutMode === "tabs") closePane();
    });
    set.tabs.push(tab);
    if (hasBridge() && bridge().browser?.getNotes) {
      const created = tab;
      void bridge()
        .browser!.getNotes(sessionId)
        .then((notes) => {
          created.noteCount = notes.length;
          updateBrowserToolbar(created);
        })
        .catch(() => undefined);
    }
  }
  if (url) tab.body.querySelector<HTMLInputElement>(".browser-url")!.value = url;
  if (state.sessionId === sessionId) {
    pendingBrowserSessions.delete(sessionId);
    if (ideTerminalMode) activateIDEPage(tab.key);
    else {
      set.active = tab.key;
      showPane();
    }
  } else {
    set.active = tab.key;
    pendingBrowserSessions.add(sessionId);
  }
  renderSessions();
}

export function hasAgentBrowser(sessionId: string): boolean {
  return tabsFor(sessionId).some((tab) => tab.kind === "browser");
}

// Chat links use the same one-browser-per-session view as the agent tool.
export function openLocalBrowserURL(url: string): void {
  if (!state.sessionId) {
    toast("Start a conversation to open a local browser.", "error");
    return;
  }
  const set = browserSet(state.sessionId);
  openBrowserTab(state.sessionId, url);
  const tab = set.tabs.find((t): t is BrowserTab => t.kind === "browser")!;
  navigateBrowser(tab, url, syncNativeSurfaces);
}

function browserEvent(raw: unknown): void {
  if (!raw || typeof raw !== "object") return;
  const e = raw as { event?: string; sessionId?: string; url?: string; count?: number };
  if (!e.sessionId) return;
  if (e.event === "browser/opened" || e.event === "browser/activity") {
    openBrowserTab(e.sessionId, e.url ?? "");
    return;
  }
  const tab = tabsFor(e.sessionId).find((t): t is BrowserTab => t.kind === "browser");
  if (!tab) return;
  if (e.event === "browser/annotations") {
    tab.noteCount = Math.max(0, e.count ?? 0);
    updateBrowserToolbar(tab);
    return;
  }
  if (e.event === "browser/closed") {
    pendingBrowserSessions.delete(e.sessionId);
    stopBrowserPick(tab);
    if (state.sessionId === e.sessionId) closeTab(tab.key, false);
    else {
      tabSet.tabs = tabSet.tabs.filter((t) => t !== tab);
      tab.body.remove();
    }
    renderSessions();
  } else if (e.event === "browser/navigated" && e.url) {
    if (tab.url && tab.url !== e.url) stopBrowserPick(tab);
    tab.url = e.url;
    const input = tab.body.querySelector<HTMLInputElement>(".browser-url")!;
    if (document.activeElement !== input) input.value = e.url;
    tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = e.url;
  }
}

function paintChangesHeader(data: SessionChanges | undefined): void {
  const count = data?.files.length ?? 0;
  $("#sessionChangesCount").textContent = count ? String(count) : "";
  if (!ideTerminalMode) changesBtn.title = `View ${count} ${count === 1 ? "file" : "files"} changed in this session`;
}

let changeRequest = 0;
let floatingChange: { sessionId: string; turn: number } | null = null;

export function hideLiveChanges(sessionId: string): void {
  if (floatingChange?.sessionId !== sessionId) return;
  floatingChange = null;
  liveChangesBtn.hidden = true;
}

function paintLiveChanges(
  sessionId: string,
  turn: SessionChanges["turns"][number] | null,
  total: Awaited<ReturnType<typeof turnTotal>>,
): void {
  if (ideTerminalMode || sessionId !== state.sessionId || !hasRunningTurn(sessionId) || !turn?.files.length) {
    hideLiveChanges(sessionId);
    return;
  }
  floatingChange = { sessionId, turn: turn.turn };
  liveChangesBtn.innerHTML =
    `${turn.files.length} ${turn.files.length === 1 ? "file" : "files"} changed` +
    (total ? ` <span class="tool-add">+${total.added}</span> <span class="tool-del">-${total.removed}</span>` : "");
  liveChangesBtn.hidden = false;
}

// Refresh after a tool settles or a session is opened. Persisted user
// rows carry transcript indices; the current live row is passed in.
// noteToolChange coalesces the live changes marker after a mutating
// tool run. A busy turn used to call refreshChanges on every command,
// and each call re-reads every recorded step (two git invocations per
// step) even when the command changed nothing.
let toolChangeTimer = 0;
let toolChangePending: { sessionId: string; liveRow: HTMLElement | null; callId?: string } | null = null;
export function noteToolChange(sessionId: string, liveRow: HTMLElement | null | undefined, callId?: string): void {
  clearChangeTotals([sessionId]);
  toolChangePending = { sessionId, liveRow: liveRow ?? null, callId };
  if (toolChangeTimer) return;
  toolChangeTimer = window.setTimeout(() => {
    toolChangeTimer = 0;
    const pending = toolChangePending;
    toolChangePending = null;
    if (pending) void refreshChanges(pending.sessionId, pending.liveRow, pending.callId);
  }, 400);
}

export async function refreshChanges(
  sessionId = state.sessionId,
  liveRow?: HTMLElement | null,
  callId?: string,
): Promise<void> {
  if (!sessionId) {
    paintChangesHeader(undefined);
    return;
  }
  const request = ++changeRequest;
  try {
    const data = await api.sessionChanges(sessionId, null);
    changesCache.set(sessionId, data);
    if (sessionId !== state.sessionId || request !== changeRequest) return;
    retainChangeTotals(sessionId, data.turns);
    paintChangesHeader(data);
    window.dispatchEvent(new CustomEvent("paladin:changes-updated", { detail: { sessionId, data } }));
    const byTurn = new Map(data.turns.map((turn) => [turn.turn, turn.files]));
    const totals = new Map(
      await Promise.all(data.turns.map(async (turn) => [turn.turn, await turnTotal(sessionId, turn)] as const)),
    );
    if (sessionId !== state.sessionId || request !== changeRequest) return;
    const activeRow = runningUserRow(sessionId);
    const sameLiveRow = !liveRow || !activeRow || liveRow === activeRow;
    const activeIndex = activeRow?.dataset.turnIndex ?? runningTurnIndex(sessionId);
    const activeTurn = hasRunningTurn(sessionId)
      ? (data.turns.find((item) => sameLiveRow && item.callIds?.includes(callId ?? "")) ??
        data.turns.find((item) => activeIndex !== undefined && item.turn === Number(activeIndex)) ??
        runningTurn(data.turns))
      : null;
    paintLiveChanges(sessionId, activeTurn, activeTurn ? (totals.get(activeTurn.turn) ?? null) : null);
    for (const row of Array.from($("#messages").querySelectorAll<HTMLElement>("[data-turn-index]"))) {
      const turn = Number(row.dataset.turnIndex);
      if (!ideTerminalMode && (row === activeRow || (activeTurn && turn === activeTurn.turn))) {
        paintTurnMarker(row, turn, []);
        continue;
      }
      paintTurnMarker(row, turn, byTurn.get(turn) ?? [], totals.get(turn));
    }
    if (!ideTerminalMode && activeRow && activeTurn) paintTurnMarker(activeRow, activeTurn.turn, []);
    if (liveRow && (ideTerminalMode || sameLiveRow) && (callId || !hasRunningTurn(sessionId))) {
      const turn = data.turns.find((item) => callId && item.callIds?.includes(callId)) ?? runningTurn(data.turns);
      if (turn && (ideTerminalMode || !activeRow))
        paintTurnMarker(liveRow, turn.turn, turn.files, totals.get(turn.turn));
      else if (turn && activeRow) paintTurnMarker(liveRow, turn.turn, []);
    }
    const tab = tabsFor(sessionId).find((t): t is ChangesTab => t.kind === "changes");
    if (tab) paintChangesTab(tab, data);
  } catch {
    if (sessionId === state.sessionId && request === changeRequest) paintChangesHeader(undefined);
  }
}

// hasSessionChange reports whether the session changed rel (a path
// relative to its workspace), from the cached change list.
function hasSessionChange(sessionId: string, rel: string): boolean {
  return !!changesCache.get(sessionId)?.files.some((f) => f.path === rel);
}

// openChangeAt opens the session's Changes tab on rel with lines
// start..end marked (a review finding).
function openChangeAt(sessionId: string, rel: string, start: number, end: number): void {
  if (sessionId !== state.sessionId) return;
  openChangesTab(null);
  const tab = currentSet()?.tabs.find((t): t is ChangesTab => t.kind === "changes");
  if (!tab) return;
  tab.selected = rel;
  tab.mark = { path: rel, start, end };
  paintChangesTab(tab, changesCache.get(sessionId));
  void loadChangeDiff(tab, rel);
}

function openChangesTab(turn: number | null, selectFirst = false): void {
  const sessionId = state.sessionId;
  if (!sessionId) return;
  const set = currentSet();
  let tab = set.tabs.find((t): t is ChangesTab => t.kind === "changes");
  if (!tab) {
    tab = newChangesTab(sessionId, `t${++nextKey}`, () => changesCache.get(sessionId));
    set.tabs.push(tab);
  }
  tab.turn = turn;
  const cached = changesCache.get(sessionId);
  const files = turn === null ? cached?.files : cached?.turns.find((item) => item.turn === turn)?.files;
  tab.selected = selectFirst ? (files?.[0]?.path ?? null) : null;
  tab.mark = null;
  set.active = tab.key;
  showPane();
  paintChangesTab(tab, cached);
  if (tab.selected) void loadChangeDiff(tab, tab.selected);
  void refreshChanges(sessionId);
}

// ---- running commands ----

let runningJobs: RunningJob[] = [];

function jobsTab(sessionId: string): JobsTab | undefined {
  return tabsFor(sessionId).find((t): t is JobsTab => t.kind === "jobs");
}

function newJobsTab(sessionId: string): JobsTab {
  const body = document.createElement("div");
  body.className = "pane-jobs";
  body.innerHTML =
    `<div class="jobs-head"><span>Running commands</span><button type="button" class="jobs-refresh" title="Refresh">${icon("rl-pulse", "h-3.5 w-3.5")}</button></div>` +
    `<div class="jobs-list"></div>` +
    `<p class="jobs-note">Stop anything the agent left running. The agent is told which command you stopped.</p>`;
  const tab: JobsTab = { kind: "jobs", key: `t${++nextKey}`, sessionId, title: "Running", body };
  body.querySelector<HTMLButtonElement>(".jobs-refresh")!.addEventListener("click", () => void refreshRunningJobs());
  body.addEventListener("click", (event) => {
    const id = (event.target as HTMLElement).closest<HTMLElement>("[data-kill]")?.dataset.kill;
    if (id) void killRunningJob(id);
  });
  return tab;
}

function paintJobsTab(tab: JobsTab): void {
  const list = tab.body.querySelector<HTMLElement>(".jobs-list")!;
  const mine = runningJobs.filter((job) => job.sessionId === tab.sessionId);
  if (!mine.length) {
    list.innerHTML = `<p class="jobs-empty">Nothing is running.</p>`;
    return;
  }
  list.innerHTML = mine
    .map(
      (job) =>
        `<div class="job-row">` +
        `<div class="job-main"><code class="job-cmd" title="${escapeAttr(job.command)}">${escapeHtml(job.command)}</code>` +
        `<span class="job-meta">${escapeHtml(formatElapsed(job.elapsedMs))}${job.auto ? " · auto" : ""}</span></div>` +
        `<button type="button" class="job-kill" data-kill="${escapeAttr(job.id)}" title="Stop this command">${icon("pause", "h-3 w-3")}<span>Stop</span></button>` +
        `</div>`,
    )
    .join("");
}

async function refreshRunningJobs(): Promise<void> {
  try {
    runningJobs = (await api.runningJobs()) ?? [];
  } catch {
    return;
  }
  const mine = runningJobs.filter((job) => job.sessionId === state.sessionId);
  runningJobsCount.textContent = mine.length ? String(mine.length) : "";
  runningJobsBtn.classList.toggle("hidden", mine.length === 0);
  runningJobsBtn.title = mine.length
    ? `${mine.length} command${mine.length === 1 ? "" : "s"} running`
    : "Running commands";
  const tab = state.sessionId ? jobsTab(state.sessionId) : undefined;
  if (tab) paintJobsTab(tab);
}

async function killRunningJob(id: string): Promise<void> {
  try {
    await api.killJob(id);
  } catch (error) {
    toast(`Could not stop the command: ${errorText(error)}`, "error");
    return;
  }
  toast("Stopped the command; the agent will be told.");
  void refreshRunningJobs();
}

function openJobsTab(): void {
  const sessionId = state.sessionId;
  if (!sessionId) return;
  const set = currentSet();
  if (!set) return;
  let tab = jobsTab(sessionId);
  if (!tab) {
    tab = newJobsTab(sessionId);
    set.tabs.push(tab);
  }
  set.active = tab.key;
  showPane();
  paintJobsTab(tab);
  void refreshRunningJobs();
}

// ---- integrated web tabs ----

// visibleWeb is the view currently attached, so repeated renders only
// move its bounds instead of telling main to show it again.
let visibleWeb = "";

// webState follows a web tab's real URL, page title and history. Main
// reports it after a link click or SPA route change, so showing the tab
// again never navigates it back to the address it was opened with.
function webState(raw: unknown): void {
  if (!raw || typeof raw !== "object") return;
  const stateEvent = raw as {
    name?: unknown;
    url?: unknown;
    title?: unknown;
    canGoBack?: unknown;
    canGoForward?: unknown;
  };
  if (typeof stateEvent.name !== "string") return;
  const tab = tabSet.tabs.find((t): t is WebTab => t.kind === "web" && t.viewName === stateEvent.name);
  if (!tab) return;
  if (typeof stateEvent.url === "string" && /^https?:/i.test(stateEvent.url) && stateEvent.url !== tab.url) {
    tab.url = stateEvent.url;
    const input = tab.body.querySelector<HTMLInputElement>(".web-url");
    if (input && document.activeElement !== input) input.value = stateEvent.url;
    setWebStatus(tab, stateEvent.url);
  }
  if (typeof stateEvent.title === "string" && stateEvent.title.trim()) tab.title = stateEvent.title.trim();
  const back = tab.body.querySelector<HTMLButtonElement>(".web-back");
  const forward = tab.body.querySelector<HTMLButtonElement>(".web-forward");
  if (back) back.disabled = stateEvent.canGoBack !== true;
  if (forward) forward.disabled = stateEvent.canGoForward !== true;
  paintTabs();
  if (ideTerminalMode) renderIDEPages(currentSet());
}

function navigateWeb(tab: WebTab, raw: string): void {
  const input = tab.body.querySelector<HTMLInputElement>(".web-url")!;
  const url = normalizeWebUrl(raw);
  if (!url) {
    setWebStatus(tab, "Only secure https addresses can be opened.");
    input.focus();
    return;
  }
  tab.url = url;
  tab.title = webHost(url);
  input.value = url;
  setWebStatus(tab, "Loading…");
  paintTabs();
  if (ideTerminalMode) renderIDEPages(currentSet());
  // Force the next sync through ensure so an already-visible tab actually
  // navigates instead of only moving its bounds.
  visibleWeb = "";
  syncNativeSurfaces();
}

// openWebTab opens one site in the current session's side pane. A saved
// site keeps its name; an address shows its host. Opening the same URL
// again focuses the tab already showing it.
export function openWebTab(sessionId: string, url: string, title = ""): WebTab | null {
  if (!sessionId) return null;
  const set = browserSet(sessionId);
  if (url) {
    const existing = set.tabs.find((t): t is WebTab => t.kind === "web" && t.url === url);
    if (existing) {
      if (ideTerminalMode && visibleSessionId() === sessionId) activateIDEPage(existing.key);
      else {
        set.active = existing.key;
        if (visibleSessionId() === sessionId) showPane();
      }
      return existing;
    }
  }
  const tab = newWebTab(sessionId, url, title, `t${++nextKey}`, navigateWeb);
  set.tabs.push(tab);
  if (ideTerminalMode && visibleSessionId() === sessionId) activateIDEPage(tab.key);
  else {
    set.active = tab.key;
    if (visibleSessionId() === sessionId) showPane();
  }
  if (!url) requestAnimationFrame(() => tab.body.querySelector<HTMLInputElement>(".web-url")?.focus());
  return tab;
}

// closeWebTabsForUrl closes every tab showing a URL, e.g. after the user
// deletes that saved site.
export function closeWebTabsForUrl(url: string): void {
  if (!url) return;
  for (const tab of [...tabSet.tabs]) {
    if (tab.kind === "web" && tab.url === url) closeTab(tab.key, true, tab.sessionId);
  }
}

function modalCoveringPage(): boolean {
  const explorer = document.querySelector<HTMLElement>(".ide-explorer");
  if (explorer && getComputedStyle(explorer).visibility !== "hidden") return true;
  return ["settingsModal", "sessionModal", "confirmModal", "siteModal", "askModal"].some((id) => {
    const el = document.getElementById(id);
    return el && !el.classList.contains("hidden");
  });
}

// ---- native pages follow their surface while it moves ----
//
// Electron positions a WebContentsView by integer pixel bounds in another
// process, and a view cannot be clipped to a rounded, sliding panel: it does
// not inherit a CSS transition. Without help, the 200ms a panel spends
// opening or closing leaves the page pinned at its old rectangle, sticking
// out past the panel it belongs to and painting over its neighbours. So
// while the surface is moving, its bounds are sent once per frame. Nothing
// is sent for a frame that did not move, and the loop stops a while after
// the rectangle settles.
let followFrame = 0;
let followSurfaceEl: HTMLElement | null = null;
let followApply: ((bounds: PageBounds) => void) | null = null;

type PageBounds = { x: number; y: number; width: number; height: number };

// A transition starts slow, so the first frames of an ease can look still.
// Counting a run of identical frames — not just two — is what tells "the
// panel is easing out of its rest pose" apart from "it has finished".
const FOLLOW_SETTLE_FRAMES = 10;
// However slow a panel is, the loop gives up after this, so a stuck value
// can never leave a frame loop running for the life of the window.
const FOLLOW_MAX_MS = 1500;

function stopFollow(): void {
  if (followFrame) cancelAnimationFrame(followFrame);
  followFrame = 0;
  followSurfaceEl = null;
  followApply = null;
}

// currentSurface is the native page's HTML surface while one is really on
// screen. A closed pane keeps its tab bodies in the DOM (that is how a tab
// survives switching away), so "the tab exists" is not enough: the surface
// must have a size. Following an invisible one would leave the visible page
// pinned where it was.
function currentSurface(): { el: HTMLElement; apply: (bounds: PageBounds) => void } | null {
  if (!paneOpen || !hasBridge()) return null;
  const set = currentSet();
  const tab = set && activeTab(set);
  const el =
    tab?.kind === "web"
      ? tab.body.querySelector<HTMLElement>(".web-surface")
      : tab?.kind === "browser"
        ? tab.body.querySelector<HTMLElement>(".browser-surface")
        : null;
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) return null;
  const bridgeBrowser = bridge().browser;
  if (tab?.kind === "browser" && !bridgeBrowser) return null;
  return {
    el,
    apply:
      tab?.kind === "browser"
        ? (b) => void bridgeBrowser?.setBounds(b).catch(() => undefined)
        : (b) => void api.moveExternalPage(b).catch(() => undefined),
  };
}

// nudgeFollow restarts the frame loop for whatever page is on screen. Panel
// animations are CSS transitions owned by other modules, so they are found
// through the transition events they raise rather than through calls.
function nudgeFollow(): void {
  if (followFrame) return;
  const live = currentSurface();
  if (live) followNative(live.el, live.apply);
}

// transitionrun fires once when a transition starts, including transitions
// that originate inside a panel's own subtree. Any of them can move the
// card, so any of them can move the page.
document.addEventListener("transitionrun", nudgeFollow, true);
window.addEventListener("resize", nudgeFollow);

function followNative(surface: HTMLElement, apply: (bounds: PageBounds) => void): void {
  followSurfaceEl = surface;
  followApply = apply;
  if (followFrame) return;
  let last: PageBounds | null = null;
  let steady = 0;
  const startedAt = performance.now();
  const step = (): void => {
    followFrame = requestAnimationFrame(step);
    const el = followSurfaceEl;
    const send = followApply;
    if (!el || !send || !el.isConnected || performance.now() - startedAt > FOLLOW_MAX_MS) {
      stopFollow();
      return;
    }
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) {
      stopFollow();
      return;
    }
    const next = { x: r.left, y: r.top, width: r.width, height: r.height };
    const moved =
      !last ||
      Math.abs(next.x - last.x) > 0.5 ||
      Math.abs(next.y - last.y) > 0.5 ||
      Math.abs(next.width - last.width) > 0.5 ||
      Math.abs(next.height - last.height) > 0.5;
    if (!moved) {
      if (++steady > FOLLOW_SETTLE_FRAMES) stopFollow();
      return;
    }
    steady = 0;
    last = next;
    send(next);
  };
  followFrame = requestAnimationFrame(step);
}

function hideWebSurface(): void {
  if (!visibleWeb) return;
  visibleWeb = "";
  stopFollow();
  void api.hideExternalPage().catch(() => undefined);
}

// ---- native pages under overlays ----
//
// A native page (a web tab or the agent browser) is a WebContentsView, so
// Electron draws it above every HTML element: a menu, drawer or dialog
// opened over it would end up underneath. While anything overlaps the
// page, it is frozen to a still image set as the surface's background and
// the live view is detached; once the overlay is gone the live view comes
// back and the still is dropped.

// A bare sample grid over the surface finds any overlay without a list of
// them: whatever answers a hit test there that is not the surface itself
// is on top of it.
const COVER_COLS = 7;
const COVER_ROWS = 5;
// The live view needs a moment to paint after it is re-attached; keeping
// the still this long avoids a blank flash.
const STILL_RELEASE_MS = 180;

let nativeBrowserOn = false;
let frozenSurface: HTMLElement | null = null;
let freezing = false;

function nativeOn(): boolean {
  return visibleWeb !== "" || nativeBrowserOn;
}

function overlayOver(surface: HTMLElement): boolean {
  const r = surface.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) return false;
  // The toast ignores the pointer, so a hit test cannot see it.
  const toast = document.getElementById("toast");
  if (toast && !toast.classList.contains("hidden")) {
    const t = toast.getBoundingClientRect();
    if (t.left < r.right && t.right > r.left && t.top < r.bottom && t.bottom > r.top) return true;
  }
  for (let col = 0; col < COVER_COLS; col++) {
    for (let row = 0; row < COVER_ROWS; row++) {
      const hit = document.elementFromPoint(
        r.left + ((col + 0.5) / COVER_COLS) * r.width,
        r.top + ((row + 0.5) / COVER_ROWS) * r.height,
      );
      if (hit && !surface.contains(hit)) return true;
    }
  }
  return false;
}

// nativeTarget is the native page the pane wants on screen right now,
// before any overlay is taken into account.
function nativeTarget(): { web: WebTab | null; browser: BrowserTab | null; surface: HTMLElement | null } {
  const set = currentSet();
  const idePage = ideTerminalMode && set.tabs.find((item) => item.key === ideBrowserKey && isBrowserPage(item));
  const tab = idePage || activeTab(set);
  const pageVisible = !!idePage || (paneOpen && (!ideTerminalMode || !tab || !isBrowserPage(tab)));
  const web = pageVisible && tab?.kind === "web" && tab.url !== "" ? tab : null;
  const browser = pageVisible && tab?.kind === "browser" ? tab : null;
  const surface = web
    ? web.body.querySelector<HTMLElement>(".web-surface")
    : browser
      ? browser.body.querySelector<HTMLElement>(".browser-surface")
      : null;
  return { web, browser, surface };
}

function pageCovered(surface: HTMLElement | null): boolean {
  return modalCoveringPage() || (!!surface && overlayOver(surface));
}

function showStill(surface: HTMLElement, data: string): void {
  surface.style.backgroundImage = `url("${data}")`;
  surface.style.backgroundSize = "100% 100%";
  surface.style.backgroundRepeat = "no-repeat";
}

function clearStill(surface: HTMLElement): void {
  surface.style.backgroundImage = "";
  surface.style.backgroundSize = "";
  surface.style.backgroundRepeat = "";
}

function releaseStill(): void {
  const surface = frozenSurface;
  frozenSurface = null;
  if (surface) window.setTimeout(() => clearStill(surface), STILL_RELEASE_MS);
}

// freezeSurface captures the page that is still on screen, puts the still
// behind where it was, and only then lets the sync detach the live view.
async function freezeSurface(surface: HTMLElement): Promise<void> {
  if (freezing) return;
  freezing = true;
  try {
    let data: string | null = null;
    try {
      data = await api.snapshotExternalPage();
    } catch {
      data = null;
    }
    if (data) {
      try {
        const probe = new Image();
        probe.src = data;
        await probe.decode();
        if (surface.isConnected) showStill(surface, data);
      } catch {
        // Not decodable: fall back to a blank surface behind the overlay.
      }
    }
    frozenSurface = surface;
  } finally {
    freezing = false;
  }
  syncNativeSurfaces();
}

// watchOverlays re-syncs when an overlay opens or closes over the page.
// Chat streaming only touches #messages, which is never over the pane, so
// those mutations are skipped.
function watchOverlays(): void {
  let queued = false;
  const check = (): void => {
    queued = false;
    const { surface } = nativeTarget();
    if (!surface && !nativeOn() && !frozenSurface) return;
    const covered = pageCovered(surface);
    // Resync whenever what should be on screen differs from what is: a page
    // that has to come back (or go under a still), or a still left behind.
    const shouldBeLive = !!surface && !covered;
    if (shouldBeLive !== nativeOn() || (!covered && frozenSurface !== null)) syncNativeSurfaces();
  };
  const schedule = (records: MutationRecord[]): void => {
    if (queued) return;
    if (records.every((r) => r.target instanceof Element && r.target.closest("#messages"))) return;
    queued = true;
    requestAnimationFrame(check);
  };
  new MutationObserver(schedule).observe(document.body, {
    subtree: true,
    attributes: true,
    attributeFilter: ["class", "hidden", "style", "open", "inert", "aria-expanded"],
    childList: true,
  });
}

// syncNativeSurfaces attaches at most one native page over the side pane:
// the agent's local browser or the active web tab. Everything else is
// detached but kept alive, so switching back does not reload a chat.
function syncNativeSurfaces(): void {
  const { web: wantWeb, browser: wantBrowser, surface } = nativeTarget();
  const covered = pageCovered(surface);
  // Something is about to cover a page that is on screen: freeze it first,
  // then this runs again with the still in place.
  if (covered && surface && nativeOn() && frozenSurface !== surface) {
    void freezeSurface(surface);
    return;
  }
  if (!covered) releaseStill();

  const web = covered ? null : wantWeb;
  const browser = covered ? null : wantBrowser;
  const browserBridge = hasBridge() ? bridge().browser : undefined;

  if (!web) hideWebSurface();
  if (!hasBridge()) return;

  if (web) {
    nativeBrowserOn = false;
    if (browserBridge) void browserBridge.show(null);
    const surface = web.body.querySelector<HTMLElement>(".web-surface");
    const rect = surface?.getBoundingClientRect();
    if (!rect || rect.width < 1 || rect.height < 1) {
      hideWebSurface();
      document.body.classList.remove("guest-visible");
      return;
    }
    document.body.classList.add("guest-visible");
    const bounds = { x: rect.left, y: rect.top, width: rect.width, height: rect.height };
    if (visibleWeb === web.viewName) {
      followNative(surface!, (next) => {
        void api.moveExternalPage(next).catch(() => undefined);
      });
      return;
    }
    visibleWeb = web.viewName;
    setWebStatus(web, web.url);
    void api.showExternalPage(web.viewName, web.url, bounds).catch((error) => {
      visibleWeb = "";
      stopFollow();
      setWebStatus(web, errorText(error));
      document.body.classList.remove("guest-visible");
    });
    return;
  }

  if (!browser || !browserBridge) {
    nativeBrowserOn = false;
    stopFollow();
    void browserBridge?.show(null);
    document.body.classList.remove("guest-visible");
    return;
  }
  const browserSurface = browser.body.querySelector<HTMLElement>(".browser-surface")!;
  const rect = browserSurface.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) {
    nativeBrowserOn = false;
    stopFollow();
    void browserBridge.show(null);
    document.body.classList.remove("guest-visible");
    return;
  }
  document.body.classList.add("guest-visible");
  nativeBrowserOn = true;
  void browserBridge.setBounds({ x: rect.left, y: rect.top, width: rect.width, height: rect.height });
  void browserBridge.show(browser.sessionId);
  followNative(browserSurface, (next) => {
    void browserBridge.setBounds(next).catch(() => undefined);
  });
}

function activeTab(set: TabSet): Tab | undefined {
  return set.tabs.find((t) => t.key === set.active);
}

// ---- side chats ----

function newChatTab(chatId: string | null = null, title = "Side chat", messages: SideChatMessage[] = []): ChatTab {
  return createChatTab(`t${++nextKey}`, paintTabs, visibleSessionId(), chatId, title, messages);
}

// blankChatTab shows the pane on an empty side chat: the one on
// screen, else a new one.
function blankChatTab(set: TabSet): ChatTab {
  let tab = activeTab(set);
  if (!isBlankChat(tab)) {
    tab = newChatTab();
    set.tabs.push(tab);
  }
  set.active = tab.key;
  showPane();
  return tab;
}

// openSideChat runs a bare `/btw`: an empty side chat, ready to type in.
function hasMainMessage(): boolean {
  return !!state.sessionId && !!document.querySelector("#messages .bubble-user");
}

function canOpenSideChat(): boolean {
  if (hasMainMessage()) return true;
  toast("Send a message in the main chat before starting a side chat.", "error");
  return false;
}

export function openSideChat(): void {
  if (!canOpenSideChat()) return;
  const set = currentSet();
  if (!set) return;
  blankChatTab(set).body.querySelector<HTMLTextAreaElement>(".side-input")!.focus();
}

// askSideQuestion runs `/btw <question>` in a side chat tab.
export function askSideQuestion(question: string): void {
  if (!canOpenSideChat()) return;
  const set = currentSet();
  if (!set) return;
  void sendChat(blankChatTab(set), question, paintTabs);
}

// ---- subagent tabs ----

function newAgentTab(agentId: string): AgentTab {
  return createAgentTab(agentId, `t${++nextKey}`, closePane);
}

// openAgentTab shows a subagent in the pane beside its parent. With no
// session open yet, the parent opens first so the child has somewhere to
// belong.
export async function openAgentTab(agentId: string): Promise<void> {
  if (!state.sessions.some((s) => s.sessionId === agentId)) {
    await refreshSessions();
  }
  if (!state.sessions.some((s) => s.sessionId === agentId)) {
    const parentId = state.sessions.find((s) => s.sessionId === state.sessionId)?.subagent?.parentId ?? state.sessionId;
    const matches = state.sessions.filter((s) => s.subagent?.parentId === parentId && s.sessionId.startsWith(agentId));
    const fullId = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(agentId);
    if (matches.length === 1) agentId = matches[0].sessionId;
    else if (matches.length || !fullId) {
      toast(matches.length ? "Subagent ID is ambiguous" : "Subagent not found", "error");
      return;
    }
  }
  if (agentId === state.sessionId) return;
  const parentId = state.sessions.find((s) => s.sessionId === agentId)?.subagent?.parentId;
  if (parentId && state.sessionId !== parentId) {
    await openSession(parentId);
  }
  const set = currentSet();
  if (!set) return;
  dismissedAgents.delete(agentId);
  let tab = set.tabs.find((t) => t.kind === "agent" && t.agentId === agentId);
  if (!tab) {
    tab = newAgentTab(agentId);
    set.tabs.push(tab);
  }
  set.active = tab.key;
  showPane();
}

// dismissedAgents remembers agent tabs the user closed, so auto-open
// never brings the same child back on its next status change.
const dismissedAgents = new Set<string>();

// autoOpenChildTabs gives every working child its own window, without
// stealing focus from the parent's chat: the tab is simply there when
// the user opens the pane.
export function autoOpenChildTabs(): void {
  const set = currentSet();
  const parentId = state.sessionId;
  if (!set || !parentId) return;
  let added = false;
  for (const kid of state.sessions) {
    if (kid.subagent?.parentId !== parentId || dismissedAgents.has(kid.sessionId)) continue;
    const status = agentStatus(kid);
    if (status !== "running" && status !== "queued") continue;
    if (set.tabs.some((t) => t.kind === "agent" && t.agentId === kid.sessionId)) continue;
    set.tabs.push(newAgentTab(kid.sessionId));
    added = true;
  }
  if (added) render();
}

// paintAgentTabs refreshes the heads of the agent tabs on screen.
export function paintAgentTabs(): void {
  if (!state.sessionId) return;
  for (const tab of tabsFor(state.sessionId)) {
    if (tab.kind === "agent") paintHead(tab);
  }
  if (paneOpen) paintTabs();
}

// ---- the pane ----

function tabHtml(tab: Tab, active: boolean): string {
  const glyph =
    tab.kind === "agent"
      ? icon("rl-agent", "h-3 w-3")
      : tab.kind === "browser"
        ? icon("globe", "h-3 w-3")
        : tab.kind === "web"
          ? icon("rl-chat", "h-3 w-3")
          : tab.kind === "terminal"
            ? icon("terminal", "h-3 w-3")
            : tab.kind === "jobs"
              ? icon("terminal", "h-3 w-3")
              : tab.kind === "changes"
                ? icon("tl-file", "h-3 w-3")
                : icon("comments", "h-3 w-3");
  const data =
    tab.kind === "agent"
      ? ` data-agent="${escapeAttr(tab.agentId)}"`
      : tab.kind === "browser"
        ? ` data-browser="${escapeAttr(tab.sessionId)}"`
        : tab.kind === "web"
          ? ` data-web="${escapeAttr(tab.viewName)}"`
          : tab.kind === "chat" && tab.chatId
            ? ` data-chat="${escapeAttr(tab.chatId)}"`
            : "";
  const busy = tab.kind === "chat" ? tab.busy : tab.kind === "agent" && liveBadge(tab.agentId) === "running";
  return (
    `<div class="pane-tab${active ? " is-active" : ""}" data-key="${tab.key}"${data}>` +
    `<button type="button" role="tab" aria-selected="${active}" class="pane-tab-pick" title="${escapeAttr(tab.title)}">` +
    `${busy ? `<span class="tool-spin" aria-hidden="true"></span>` : glyph}<span class="truncate">${escapeHtml(tab.title)}</span></button>` +
    `<button type="button" class="pane-tab-close" aria-label="Close ${escapeAttr(tab.title)}" title="Close">${icon("xmark", "h-2.5 w-2.5")}</button>` +
    `</div>`
  );
}

// mainTabHtml is the Agent entry: the conversation, pinned at the top of
// the drawer list. It is not in the split pane's strip, because the agent is
// the left half there and never needs a tab. Selecting it shows the chat
// (full width in tabs layout) and keeps the list, the way a browser keeps
// its tab bar when you pick the first tab.
function mainTabHtml(): string {
  const active = layoutMode === "split" || !paneOpen;
  return `<div class="pane-tab pane-main-tab${active ? " is-active" : ""}" data-key="main"><button type="button" role="tab" aria-selected="${active}" class="pane-tab-pick" title="Agent chat">${icon("rl-agent", "h-3 w-3")}<span>Agent</span></button></div>`;
}

const emptyPaneEl = document.createElement("div");
emptyPaneEl.className = "side-empty";
emptyPaneEl.innerHTML = `<span class="side-empty-ico">${icon("layout-tabs", "h-5 w-5")}</span><p class="side-empty-title">Nothing open</p><p class="side-empty-text">Open a terminal, a page, the changes or a side chat from the toolbar or the rail.</p>`;

let stripHtml: string | null = null;
let drawerHtml: string | null = null;

// resetTabsCache forces the next paint to rewrite both tab lists.
function resetTabsCache(): void {
  stripHtml = null;
  drawerHtml = null;
}

// paintTabs writes the two views of one tab set: the horizontal strip
// inside the split pane (no Agent entry), and the drawer's vertical list
// (Agent first).
function paintTabs(): void {
  const set = currentSet();
  const tabs = set.tabs
    .filter((t) => !ideTerminalMode || (t.kind !== "terminal" && !isBrowserPage(t)))
    .map((t) => tabHtml(t, paneOpen && t.key === set.active))
    .join("");
  const drawer = mainTabHtml() + tabs;
  if (tabs !== stripHtml) {
    stripHtml = tabs;
    tabsEl.innerHTML = tabs;
  }
  if (drawer !== drawerHtml) {
    drawerHtml = drawer;
    drawerTabsEl.innerHTML = drawer;
  }
}

// render paints the open session's tabs, keeping each tab's DOM.
function render(): void {
  if (ideTerminalMode) terminalDock?.sync();
  else {
    const toggle = $("#ideTerminalToggle");
    const set = currentSet();
    const visible = paneOpen && !!set && activeTab(set)?.kind === "terminal";
    toggle.setAttribute("aria-pressed", String(visible));
    toggle.setAttribute("aria-label", visible ? "Hide terminal" : "Show terminal");
    toggle.title = visible ? "Hide terminal" : "Show terminal";
  }
  renderIDEPages(currentSet());
  // The drawer list and the split pane's strip are separate elements, so
  // both can be on screen at once. Tabs layout has no strip in the pane: the
  // drawer is its only navigation.
  const drawerOpen = tabsDrawerOpen[layoutMode];
  tabsDrawerEl.classList.toggle("is-closed", !drawerOpen);
  tabsDrawerEl.hidden = false;
  tabsDrawerEl.inert = !drawerOpen;
  tabsRailBtn.setAttribute("aria-pressed", String(drawerOpen));
  tabsRailBtn.setAttribute("aria-expanded", String(drawerOpen));
  const tabsLabel = drawerOpen ? "Hide browser and open tabs" : "Show browser and open tabs";
  tabsRailBtn.setAttribute("aria-label", tabsLabel);
  tabsRailBtn.title = tabsLabel;
  agentView.classList.toggle("is-tab-layout", layoutMode === "tabs");
  agentView.classList.toggle("is-side-tab", layoutMode === "tabs" && paneOpen);
  sidePaneEl.hidden = !paneOpen;
  layoutBtn.dataset.layout = layoutMode;
  layoutBtn.setAttribute("aria-label", layoutMode === "tabs" ? "Switch to split layout" : "Switch to tabs layout");
  layoutBtn.title = layoutMode === "tabs" ? "Switch to split layout" : "Switch to tabs layout";
  if (!paneOpen && layoutMode === "split") {
    // The strip keeps its tabs: selecting the Agent tab shows the
    // conversation, it does not throw the other tabs away. Only the pane
    // body gives up its space, so the native page is detached.
    closeMenu();
    paintTabs();
    bodyEl.replaceChildren();
    syncNativeSurfaces();
    return;
  }
  const set = currentSet();
  if (!set) {
    paintTabs();
    bodyEl.innerHTML = `<div class="side-empty"><span class="side-empty-ico">${icon("comments", "h-5 w-5")}</span><p class="side-empty-title">Side chat</p><p class="side-empty-text">Send a message first: side chats are about a conversation.</p></div>`;
    syncNativeSurfaces();
    return;
  }
  if (paneOpen && layoutMode === "tabs" && set.tabs.length === 0 && hasMainMessage()) {
    const tab = newChatTab();
    set.tabs.push(tab);
    set.active = tab.key;
  }
  if (set.tabs.length && !activeTab(set)) set.active = set.tabs[0].key;
  paintTabs();
  const shown = set.tabs.filter((t) => !ideTerminalMode || (t.kind !== "terminal" && !isBrowserPage(t)));
  bodyEl.replaceChildren(...shown.map((t) => t.body), ...(shown.length || !paneOpen ? [] : [emptyPaneEl]));
  for (const tab of set.tabs) {
    if (ideTerminalMode && (tab.kind === "terminal" || isBrowserPage(tab))) continue;
    tab.body.hidden = !paneOpen || tab.key !== set.active;
    if (tab.kind === "browser" && tab.body.hidden) stopBrowserPick(tab);
    if (tab.kind === "terminal" && !tab.body.hidden) activateTerminal(tab);
  }
  const tab = activeTab(set);
  if (tab?.kind === "agent") {
    paintHead(tab);
    void loadAgent(tab);
  }
  if (tab?.kind === "jobs") paintJobsTab(tab);
  syncNativeSurfaces();
}

// After a resync the shown agent tab re-attaches to fetch the events
// the host dropped.
onResync(() => {
  const set = paneOpen && state.sessionId ? currentSet() : undefined;
  const tab = set && activeTab(set);
  if (tab?.kind !== "agent") return;
  liveRoot(tab.agentId).remove();
  void loadAgent(tab);
});

function showPane(): void {
  paneOpen = true;
  render();
}

// setTabsDrawer opens or closes the "Open tabs" panel. It is a column of the
// shell, so opening it narrows the card.
//
// `remember` separates a choice from a side effect. When another panel takes
// the column slot this closes too, but that is not the user saying they want
// the tab list gone: persisting it would make the panel disappear on the
// next start because they happened to look at their sessions once.
function setTabsDrawer(open: boolean, remember = true): void {
  // A panel taking the slot closes the drawer in both layouts, so toggling
  // the layout cannot bring back a second pushed column.
  if (!open && !remember) {
    const wasOpen = tabsDrawerOpen[layoutMode];
    tabsDrawerOpen.split = false;
    tabsDrawerOpen.tabs = false;
    if (wasOpen) render();
    return;
  }
  if (tabsDrawerOpen[layoutMode] === open) return;
  tabsDrawerOpen[layoutMode] = open;
  if (remember) saveText(tabsDrawerKeys[layoutMode], open ? "open" : "closed");
  // The pushing panels share one column slot, so opening this one closes
  // the sessions drawer and the file explorer.
  if (open) {
    window.dispatchEvent(new Event("paladin:tabs-drawer-opened"));
    window.dispatchEvent(new Event("paladin:ide-hide-explorer"));
  }
  render();
}

function closePane(): void {
  // Split keeps its right pane; only tabs layout swaps it for the agent.
  if (layoutMode === "split" && !ideTerminalMode) {
    closeMenu();
    render();
    return;
  }
  paneOpen = false;
  closeMenu();
  render();
}

function toggleLayout(): void {
  layoutMode = layoutMode === "split" ? "tabs" : "split";
  saveText(layoutKey, layoutMode);
  // Tabs layout shows one body at a time, so it starts on the agent unless a
  // tab is there to show; split always has its pane.
  paneOpen = layoutMode === "split" || tabSet.tabs.length > 0;
  resetTabsCache();
  render();
}

// openTerminalDockFromKey toggles the IDE terminal dock; in split/tabs
// mode it falls back to adding a terminal tab, which is the same place a
// terminal shows there.
export function openTerminalDockFromKey(): void {
  if (ideTerminalMode && terminalDock) terminalDock.toggle();
  else openTerminalTab(visibleSessionId());
}

// The rail returns to an existing terminal instead of creating another tab.
function toggleTerminalFromRail(): void {
  if (ideTerminalMode && terminalDock) {
    terminalDock.toggle();
    return;
  }
  const set = currentSet();
  const active = set && activeTab(set);
  if (paneOpen && active?.kind === "terminal") {
    closePane();
    return;
  }
  const existing = set?.tabs.find((tab) => tab.kind === "terminal" && tab.cwd === state.cwd);
  if (set && existing) {
    set.active = existing.key;
    showPane();
  } else openTerminalTab(visibleSessionId());
}

// focusAgentFromKey brings the conversation back: it hides the side pane
// and focuses the prompt. The IDE's own toggle owns turning IDE off, so
// this stays a view switch. Focusing happens after the pane's render has
// settled: render() moves the focused side pane out of the document, and
// removing the focused node sends focus to the body first.
export function focusAgentFromKey(): void {
  closePane();
  requestAnimationFrame(() => $<HTMLTextAreaElement>("#promptInput").focus());
}

// toggleLayoutFromKey switches split <-> tabs, the same as the layout
// button in the toolbar.
export function toggleLayoutFromKey(): void {
  toggleLayout();
}

function closeTab(key: string, dropBrowser = true, _sessionId = visibleSessionId()): void {
  const set = currentSet();
  if (!set) return;
  const at = set.tabs.findIndex((t) => t.key === key);
  if (at < 0) return;
  const [removed] = set.tabs.splice(at, 1);
  if (removed.kind === "browser") {
    pendingBrowserSessions.delete(removed.sessionId);
    stopBrowserPick(removed);
    renderSessions();
  }
  if (removed.kind === "agent") {
    if (removed.loading) abortAttach(removed.agentId);
    liveRoot(removed.agentId).remove();
    dismissedAgents.add(removed.agentId);
  }
  if (removed.kind === "browser" && dropBrowser && hasBridge()) void bridge().browser?.drop(removed.sessionId);
  if (removed.kind === "web") {
    if (visibleWeb === removed.viewName) visibleWeb = "";
    removed.body.remove();
    void api.dropExternalPage(removed.viewName).catch(() => undefined);
  }
  if (removed.kind === "terminal") {
    removed.closed = true;
    if (removed.terminalId) void api.terminalClose(removed.sessionId, removed.terminalId).catch(() => undefined);
    removed.term?.dispose();
  }
  if (set.tabs.length === 0) {
    // With one strip per window, an empty strip is just a closed pane.
    if (_sessionId === visibleSessionId()) closePane();
    return;
  }
  if (set.active === key) set.active = set.tabs[Math.min(at, set.tabs.length - 1)].key;
  if (_sessionId === visibleSessionId()) render();
}

// disposeSidePanes drops the tab sets of deleted sessions, and any
// tab, in any set, showing one of them: their DOM and live rows go too.
export function disposeSidePanes(ids: string[]): void {
  const gone = new Set(ids);
  clearChangeTotals(ids);
  for (const id of ids) {
    changesCache.delete(id);
    pendingBrowserSessions.delete(id);
  }
  const drop = (tab: Tab): void => {
    if (tab.kind === "agent") {
      if (tab.loading) abortAttach(tab.agentId);
      liveRoot(tab.agentId).remove();
    }
    if (tab.kind === "browser" && hasBridge()) void bridge().browser?.drop(tab.sessionId);
    if (tab.kind === "browser") stopBrowserPick(tab);
    if (tab.kind === "web") {
      if (visibleWeb === tab.viewName) visibleWeb = "";
      void api.dropExternalPage(tab.viewName).catch(() => undefined);
    }
    if (tab.kind === "terminal") {
      tab.closed = true;
      if (tab.terminalId) void api.terminalClose(tab.sessionId, tab.terminalId).catch(() => undefined);
      tab.term?.dispose();
    }
    tab.body.remove();
  };
  // One strip: drop the tabs of deleted sessions and any subagent tab in
  // any session whose child is gone. Standalone terminals survive, the
  // same way they did when each session owned its own set.
  const keep = tabSet.tabs.filter((tab) => {
    if (tab.kind === "agent") return !gone.has(tab.agentId);
    if (tab.kind === "terminal" && tab.sessionId === STANDALONE_PANE_SESSION) return true;
    return !gone.has(tab.sessionId);
  });
  tabSet.tabs.filter((tab) => !keep.includes(tab)).forEach(drop);
  tabSet.tabs = keep;
  if (!tabSet.tabs.some((tab) => tab.key === tabSet.active)) tabSet.active = tabSet.tabs[0]?.key ?? "";
  resetTabsCache();
  render();
}

/**
 * paneSessionIds lists the sessions that hold side-pane tabs; e2e specs
 * read it to check deleted sessions leave nothing behind.
 * @public
 */
export function paneSessionIds(): string[] {
  return [...new Set(tabSet.tabs.map((tab) => tab.sessionId))];
}

// syncSidePane follows the main view to another session.
export function syncSidePane(): void {
  if (floatingChange && floatingChange.sessionId !== state.sessionId) hideLiveChanges(floatingChange.sessionId);
  window.dispatchEvent(new Event("paladin:workspace-changed"));
  closeMenu();
  attachStandaloneTerminals(state.sessionId);
  if (pendingBrowserSessions.delete(state.sessionId)) {
    const tab = tabsFor(state.sessionId).find((item) => item.kind === "browser");
    if (ideTerminalMode && tab) {
      ideBrowserKey = tab.key;
      window.dispatchEvent(new CustomEvent("paladin:ide-browser-active", { detail: true }));
    } else paneOpen = true;
  }
  paintChangesHeader(changesCache.get(state.sessionId));
  render();
  autoOpenChildTabs();
  void refreshRunningJobs();
}

// ---- saved side chats ----

function closeMenu(): void {
  menuEl.hidden = true;
  historyBtn.setAttribute("aria-expanded", "false");
}

function ago(ms: number): string {
  const min = Math.round((Date.now() - ms) / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const h = Math.round(min / 60);
  return h < 24 ? `${h}h ago` : `${Math.round(h / 24)}d ago`;
}

async function openMenu(): Promise<void> {
  const sessionId = state.sessionId;
  menuEl.hidden = false;
  historyBtn.setAttribute("aria-expanded", "true");
  if (!sessionId) {
    menuItemsEl.innerHTML = `<p class="pane-menu-note">No conversation yet.</p>`;
    return;
  }
  menuItemsEl.innerHTML = `<p class="pane-menu-note">Loading…</p>`;
  try {
    const chats = (await api.listSideChats(sessionId, state.cwd || null)) ?? [];
    if (state.sessionId !== sessionId || menuEl.hidden) return;
    menuItemsEl.innerHTML = chats.length
      ? `<p class="pane-menu-label">Saved side chats</p>` +
        chats
          .map(
            (c) =>
              `<div class="pane-hist-item" data-chat="${escapeAttr(c.id)}">` +
              `<button type="button" role="menuitem" class="pane-hist-open"><span class="truncate">${escapeHtml(c.title || "Side chat")}</span>` +
              `<span class="pane-hist-meta">${ago(c.updatedAt)} · ${c.messageCount} messages</span></button>` +
              `<button type="button" class="pane-hist-del" title="Delete" aria-label="Delete ${escapeAttr(c.title || "side chat")}">${icon("trash", "h-3 w-3")}</button>` +
              `</div>`,
          )
          .join("")
      : `<p class="pane-menu-note">No saved side chats.</p>`;
  } catch (error) {
    menuItemsEl.innerHTML = `<p class="pane-menu-note side-error">${escapeHtml(errorText(error))}</p>`;
  }
}

async function reopenChat(chatId: string): Promise<void> {
  const set = currentSet();
  if (!set) return;
  closeMenu();
  const open = set.tabs.find((t) => t.kind === "chat" && t.chatId === chatId);
  if (open) {
    set.active = open.key;
    showPane();
    return;
  }
  try {
    const chat = await api.loadSideChat(state.sessionId, state.cwd || null, chatId);
    const tab = newChatTab(chat.id, clip(chat.title || "Side chat"), chat.messages ?? []);
    const blank = activeTab(set);
    if (isBlankChat(blank)) {
      set.tabs.splice(set.tabs.indexOf(blank), 1, tab);
    } else {
      set.tabs.push(tab);
    }
    set.active = tab.key;
    showPane();
  } catch (error) {
    toast(`Could not open the side chat: ${errorText(error)}`, "error");
  }
}

async function deleteChat(chatId: string, row: HTMLElement): Promise<void> {
  try {
    await api.deleteSideChat(state.sessionId, state.cwd || null, chatId);
  } catch (error) {
    toast(`Could not delete the side chat: ${errorText(error)}`, "error");
    return;
  }
  row.remove();
  if (!menuItemsEl.querySelector(".pane-hist-item")) {
    menuItemsEl.innerHTML = `<p class="pane-menu-note">No saved side chats.</p>`;
  }
  const set = currentSet();
  const open = set?.tabs.find((t) => t.kind === "chat" && t.chatId === chatId);
  if (open) closeTab(open.key);
}

export function initSidePane(): void {
  registerChangesLink({ has: hasSessionChange, open: openChangeAt });
  // The draggable border between the conversation and the pane. It is
  // created here rather than in index.html so it lives next to the pane
  // it sizes.
  const splitHandle = document.createElement("div");
  splitHandle.id = "paneSplitResize";
  splitHandle.className = "pane-split-resize";
  splitHandle.tabIndex = 0;
  splitHandle.setAttribute("role", "separator");
  splitHandle.setAttribute("aria-orientation", "vertical");
  splitHandle.setAttribute("aria-label", "Resize the side pane");
  splitHandle.title = "Resize the side pane";
  sidePaneEl.before(splitHandle);
  wireSplitResize(splitHandle, $<HTMLElement>("#agentSplit"), sidePaneEl);
  if (hasBridge()) {
    bridge().onEvent?.(browserEvent);
    bridge().onEvent?.((event) => terminalEvent(event, tabSet));
    bridge().views?.onState?.(webState);
  }
  const observer = new ResizeObserver(() => {
    syncNativeSurfaces();
    const set = state.sessionId ? currentSet() : null;
    const tab = set && activeTab(set);
    if (paneOpen && tab?.kind === "terminal") tab.fit?.fit();
  });
  observer.observe(sidePaneEl);
  observer.observe(bodyEl);
  window.addEventListener("resize", syncNativeSurfaces);
  watchOverlays();
  for (const id of ["settingsModal", "sessionModal", "confirmModal", "siteModal"]) {
    const el = document.getElementById(id);
    if (el) new MutationObserver(syncNativeSurfaces).observe(el, { attributes: true, attributeFilter: ["class"] });
  }
  new MutationObserver((records) => {
    if (
      records.some((record) =>
        Array.from(record.addedNodes)
          .concat(Array.from(record.removedNodes))
          .some((node) => node instanceof HTMLElement && node.id === "askModal"),
      )
    ) {
      syncNativeSurfaces();
    }
  }).observe(document.body, { childList: true });
  changesBtn.addEventListener("click", () => {
    if (ideTerminalMode) return;
    if (!state.sessionId) toast("Open a session to see its changes.", "error");
    else openChangesTab(null);
  });
  liveChangesBtn.addEventListener("click", () => {
    if (floatingChange?.sessionId === state.sessionId) openChangesTab(floatingChange.turn, true);
  });
  $("#ideTerminalToggle").addEventListener("click", toggleTerminalFromRail);
  runningJobsBtn.addEventListener("click", openJobsTab);
  void refreshRunningJobs();
  window.setInterval(() => {
    if (!document.hidden) void refreshRunningJobs();
  }, 4000);
  // Clicking the card expands or collapses the file list in place;
  // "View changes" still opens the diff tab. Undo keeps its own
  // button and never expands.
  $("#messages").addEventListener("click", (event) => {
    if ((event.target as HTMLElement).closest(".turn-change-undo")) return;
    const card = (event.target as HTMLElement).closest<HTMLElement>(".turn-change-card[data-change-turn]");
    if (!card?.dataset.changeTurn) return;
    if ((event.target as HTMLElement).closest(".turn-change-view")) {
      openChangesTab(Number(card.dataset.changeTurn), true);
      return;
    }
    card.classList.toggle("turn-change-collapsed");
    card.setAttribute("aria-expanded", String(!card.classList.contains("turn-change-collapsed")));
  });
  $("#messages").addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const target = event.target as HTMLElement;
    const card = target.closest<HTMLElement>(".turn-change-card[data-change-turn]");
    if (!card || target.closest("button")) return;
    event.preventDefault();
    card.classList.toggle("turn-change-collapsed");
    card.setAttribute("aria-expanded", String(!card.classList.contains("turn-change-collapsed")));
  });
  layoutBtn.addEventListener("click", () => {
    toggleLayout();
  });
  // The tab list is a drawer: the rail button opens and closes it, the way
  // the Sessions and Files buttons work. Closing it gives a page the window.
  tabsRailBtn.addEventListener("click", () => {
    setTabsDrawer(!tabsDrawerOpen[layoutMode]);
  });
  tabsDrawerCloseBtn.addEventListener("click", () => {
    setTabsDrawer(false);
    tabsRailBtn.focus();
  });
  // First Escape closes the tabs drawer, the way it closes the sessions
  // drawer: a panel that pushed the card should be dismissable from the
  // keyboard, and a second press then reaches whatever is behind it.
  tabsDrawerEl.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    event.stopPropagation();
    setTabsDrawer(false);
    tabsRailBtn.focus();
  });
  // One panel per column slot. The explorer announces that it is opening;
  // the sessions drawer announces itself the same way, and opening this one
  // closes both. Listening to each panel's *own* open event (rather than to
  // every show/hide request) is what keeps a late close animation from
  // shutting a panel the user just opened.
  window.addEventListener("paladin:sessions-drawer-opened", () => setTabsDrawer(false, false));
  window.addEventListener("paladin:explorer-opened", () => setTabsDrawer(false, false));
  $("#paneCloseBtn").addEventListener("click", closePane);
  historyBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    if (menuEl.hidden) void openMenu();
    else closeMenu();
  });
  menuEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("[data-pane-new-chat]")) {
      closeMenu();
      if (!canOpenSideChat()) return;
      const set = currentSet();
      if (!set) return;
      const tab = newChatTab();
      set.tabs.push(tab);
      set.active = tab.key;
      showPane();
      tab.body.querySelector<HTMLTextAreaElement>(".side-input")!.focus();
      return;
    }
    if (target.closest("[data-pane-new-terminal]")) {
      closeMenu();
      openTerminalTab(visibleSessionId());
      return;
    }
    const row = target.closest<HTMLElement>(".pane-hist-item");
    const chatId = row?.dataset.chat;
    if (!row || !chatId) return;
    if (target.closest(".pane-hist-del")) void deleteChat(chatId, row);
    else if (target.closest(".pane-hist-open")) void reopenChat(chatId);
  });
  const onTabListClick = (event: Event): void => {
    const target = event.target as HTMLElement;
    const key = target.closest<HTMLElement>(".pane-tab")?.dataset.key;
    if (key === "main") {
      // Agent is the left half of a split, so picking it only returns focus
      // to the prompt. In tabs layout it shows the conversation in place of
      // the page, without destroying the list.
      if (layoutMode === "tabs") closePane();
      else $<HTMLTextAreaElement>("#promptInput").focus();
      return;
    }
    const set = currentSet();
    if (!key || !set) return;
    if (target.closest(".pane-tab-close")) {
      closeTab(key);
    } else {
      set.active = key;
      showPane();
    }
  };
  tabsEl.addEventListener("click", onTabListClick);
  drawerTabsEl.addEventListener("click", onTabListClick);
  document.addEventListener("mousedown", (event) => {
    const target = event.target as Node;
    if (!menuEl.hidden && !menuEl.contains(target) && !historyBtn.contains(target)) closeMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !menuEl.hidden) closeMenu();
  });
  // Agent tool rows anywhere carry an Open button for their subagent.
  document.addEventListener("click", (event) => {
    const open = (event.target as HTMLElement).closest<HTMLElement>("[data-open-agent]");
    if (open?.dataset.openAgent) {
      event.stopPropagation();
      void openAgentTab(open.dataset.openAgent);
    }
  });
  render();
}
