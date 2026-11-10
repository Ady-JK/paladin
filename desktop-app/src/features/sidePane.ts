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
import { abortAttach, liveBadge, liveRoot, onResync } from "./live";
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
import {
  clearChangeTotals,
  paintTurnMarker,
  retainChangeTotals,
  runningTurn,
  turnTotal,
} from "./sidePane/changeMarkers";

export type { PluginTab, WebTab } from "./sidePane/types";
import type {
  AgentTab,
  BrowserTab,
  ChangesTab,
  ChatTab,
  JobsTab,
  PluginTab,
  Tab,
  TabSet,
  TerminalTab,
  WebTab,
} from "./sidePane/types";

const tabsEl = $("#paneTabs");
const stripEl = tabsEl.parentElement!;
const bodyEl = $("#paneBody");
const historyBtn = $<HTMLButtonElement>("#paneNewChatBtn");
const menuEl = $("#paneHistoryMenu");
const menuItemsEl = $("#paneHistoryItems");
const layoutBtn = $<HTMLButtonElement>("#paneLayoutBtn");
const changesBtn = $<HTMLButtonElement>("#sessionChangesBtn");
const runningJobsBtn = $<HTMLButtonElement>("#runningJobsBtn");
const runningJobsCount = $("#runningJobsCount");
const titleBar = $("#titleBar");
const agentView = $("#agentView");
const layoutKey = "paladin.paneLayout";

export const STANDALONE_PANE_SESSION = "__standalone_pane__";
const sets = new Map<string, TabSet>();
const pendingBrowserSessions = new Set<string>();

function visibleSessionId(): string {
  return state.sessionId || STANDALONE_PANE_SESSION;
}
const changesCache = new Map<string, SessionChanges>();
let paneOpen = false;
let layoutMode: "split" | "tabs" = loadText(layoutKey) === "tabs" ? "tabs" : "split";
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
      terminals: () =>
        sets
          .get(visibleSessionId())
          ?.tabs.filter((tab): tab is TerminalTab => tab.kind === "terminal" && tab.cwd === state.cwd) ?? [],
      create: () => openTerminalTab(visibleSessionId()),
      close: (key) => closeTab(key),
    });
    $("#ideTerminalToggle").addEventListener("click", () => terminalDock?.toggle());
  }
  const set = sets.get(visibleSessionId());
  const selected = set && activeTab(set);
  if (on && selected && isBrowserPage(selected) && paneOpen) {
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
  terminalDock.setEnabled(on);
  render();
}

// Standalone terminals keep their PTY owner while following the visible chat.
function attachStandaloneTerminals(sessionId: string): void {
  const targetId = sessionId || STANDALONE_PANE_SESSION;
  for (const [sourceId, source] of [...sets]) {
    if (sourceId === targetId) continue;
    const terminals = source.tabs.filter(
      (tab): tab is TerminalTab =>
        tab.kind === "terminal" && tab.sessionId === STANDALONE_PANE_SESSION && tab.cwd === state.cwd,
    );
    if (!terminals.length) continue;
    const target = browserSet(targetId);
    target.tabs.push(...terminals);
    if (terminals.some((tab) => tab.key === source.active)) target.active = source.active;
    else if (!target.active) target.active = terminals[0].key;
    const moving = new Set(terminals.map((tab) => tab.key));
    source.tabs = source.tabs.filter((tab) => !moving.has(tab.key));
    if (!source.tabs.length) sets.delete(sourceId);
    else if (!source.tabs.some((tab) => tab.key === source.active)) source.active = source.tabs[0].key;
  }
}

export function showTerminalResult(sessionId: string, cwd: string, command: string, result: api.TerminalResult): void {
  let paneSession = sessionId;
  if (sessionId === STANDALONE_PANE_SESSION) {
    if (state.cwd === cwd) paneSession = visibleSessionId();
    else {
      for (const [id, set] of sets) {
        if (set.tabs.some((tab) => tab.kind === "terminal" && tab.sessionId === sessionId && tab.cwd === cwd)) {
          paneSession = id;
          break;
        }
      }
    }
  }
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

function currentSet(): TabSet | null {
  if (!state.sessionId) return sets.get(STANDALONE_PANE_SESSION) ?? null;
  let set = sets.get(state.sessionId);
  if (!set) {
    set = { tabs: [], active: "" };
    sets.set(state.sessionId, set);
  }
  return set;
}

function browserSet(sessionId: string): TabSet {
  let set = sets.get(sessionId);
  if (!set) {
    set = { tabs: [], active: "" };
    sets.set(sessionId, set);
  }
  return set;
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
  return sets.get(sessionId)?.tabs.some((tab) => tab.kind === "browser") ?? false;
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
  const tab = sets.get(e.sessionId)?.tabs.find((t): t is BrowserTab => t.kind === "browser");
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
      const set = sets.get(e.sessionId)!;
      set.tabs = set.tabs.filter((t) => t !== tab);
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
  changesBtn.classList.toggle("hidden", count === 0);
  $("#sessionChangesCount").textContent = count ? String(count) : "";
  changesBtn.title = `View ${count} ${count === 1 ? "file" : "files"} changed in this session`;
}

let changeRequest = 0;

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
    for (const row of Array.from($("#messages").querySelectorAll<HTMLElement>("[data-turn-index]"))) {
      const turn = Number(row.dataset.turnIndex);
      paintTurnMarker(row, turn, byTurn.get(turn) ?? [], totals.get(turn));
    }
    if (liveRow && callId) {
      const turn = data.turns.find((item) => item.callIds?.includes(callId)) ?? runningTurn(data.turns);
      if (turn) paintTurnMarker(liveRow, turn.turn, turn.files, totals.get(turn.turn));
    }
    const tab = sets.get(sessionId)?.tabs.find((t): t is ChangesTab => t.kind === "changes");
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

function openChangesTab(turn: number | null): void {
  const sessionId = state.sessionId;
  if (!sessionId) return;
  const set = currentSet()!;
  let tab = set.tabs.find((t): t is ChangesTab => t.kind === "changes");
  if (!tab) {
    tab = newChangesTab(sessionId, `t${++nextKey}`, () => changesCache.get(sessionId));
    set.tabs.push(tab);
  }
  tab.turn = turn;
  tab.selected = null;
  tab.mark = null;
  set.active = tab.key;
  showPane();
  paintChangesTab(tab, changesCache.get(sessionId));
  void refreshChanges(sessionId);
}

// ---- running commands ----

let runningJobs: RunningJob[] = [];

function jobsTab(sessionId: string): JobsTab | undefined {
  return sets.get(sessionId)?.tabs.find((t): t is JobsTab => t.kind === "jobs");
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
  for (const set of sets.values()) {
    const tab = set.tabs.find((t): t is WebTab => t.kind === "web" && t.viewName === stateEvent.name);
    if (!tab) continue;
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
    return;
  }
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
  for (const [sessionId, set] of [...sets]) {
    for (const tab of [...set.tabs]) {
      if (tab.kind === "web" && tab.url === url) closeTab(tab.key, true, sessionId);
    }
  }
}

function modalCoveringPage(): boolean {
  if (document.querySelector(".plugin-overlay")) return true;
  return ["settingsModal", "sessionModal", "confirmModal", "siteModal", "askModal"].some((id) => {
    const el = document.getElementById(id);
    return el && !el.classList.contains("hidden");
  });
}

function hideWebSurface(): void {
  if (!visibleWeb) return;
  visibleWeb = "";
  void api.hideExternalPage().catch(() => undefined);
}

// syncNativeSurfaces attaches at most one native page over the side pane:
// the agent's local browser or the active web tab. Everything else is
// detached but kept alive, so switching back does not reload a chat.
function syncNativeSurfaces(): void {
  const set = sets.get(visibleSessionId());
  const idePage = ideTerminalMode && set?.tabs.find((item) => item.key === ideBrowserKey && isBrowserPage(item));
  const tab = idePage || (set && activeTab(set));
  const covered = modalCoveringPage();
  const pageVisible = !!idePage || (paneOpen && (!ideTerminalMode || !tab || !isBrowserPage(tab)));
  const web = pageVisible && !covered && tab?.kind === "web" && tab.url !== "" ? tab : null;
  const browser = pageVisible && !covered && tab?.kind === "browser" ? tab : null;
  const browserBridge = hasBridge() ? bridge().browser : undefined;

  if (!web) hideWebSurface();
  if (!hasBridge()) return;

  if (web) {
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
      void api.moveExternalPage(bounds).catch(() => undefined);
      return;
    }
    visibleWeb = web.viewName;
    setWebStatus(web, web.url);
    void api.showExternalPage(web.viewName, web.url, bounds).catch((error) => {
      visibleWeb = "";
      setWebStatus(web, errorText(error));
      document.body.classList.remove("guest-visible");
    });
    return;
  }

  if (!browser || !browserBridge) {
    if (browserBridge) void browserBridge.show(null);
    document.body.classList.remove("guest-visible");
    return;
  }
  const surface = browser.body.querySelector<HTMLElement>(".browser-surface")!;
  const rect = surface.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) {
    void browserBridge.show(null);
    document.body.classList.remove("guest-visible");
    return;
  }
  document.body.classList.add("guest-visible");
  void browserBridge.setBounds({ x: rect.left, y: rect.top, width: rect.width, height: rect.height });
  void browserBridge.show(browser.sessionId);
}

// ---- plugin panels ----

export type PluginTabSpec = {
  sessionId: string;
  panelId: string;
  plugin: string;
  view: string;
  title: string;
  url: string;
  workspace: string;
};

// preparePluginPanel closes the oldest plugin panels until there is room
// for one more. Main enforces the same caps; closing here avoids an open
// that main would refuse.
export function preparePluginPanel(sessionId: string): void {
  const set = sets.get(sessionId);
  const mine = set?.tabs.filter((t): t is PluginTab => t.kind === "plugin") ?? [];
  if (mine.length >= 2) {
    const oldest = [...mine].sort((a, b) => a.openedAt - b.openedAt)[0];
    if (oldest) closeTab(oldest.key, true, sessionId);
  }
  const all: PluginTab[] = [];
  for (const s of sets.values()) {
    for (const tab of s.tabs) if (tab.kind === "plugin") all.push(tab);
  }
  if (all.length >= 4) {
    const oldest = all.sort((a, b) => a.openedAt - b.openedAt)[0];
    if (oldest) closeTab(oldest.key, true, oldest.sessionId);
  }
}

export function pluginTabList(): PluginTab[] {
  const out: PluginTab[] = [];
  for (const set of sets.values()) {
    for (const tab of set.tabs) if (tab.kind === "plugin") out.push(tab);
  }
  return out;
}

export function openPluginTab(spec: PluginTabSpec): PluginTab {
  const set = browserSet(spec.sessionId);
  const existing = set.tabs.find((t): t is PluginTab => t.kind === "plugin" && t.panelId === spec.panelId);
  if (existing) {
    set.active = existing.key;
    if (visibleSessionId() === spec.sessionId) showPane();
    return existing;
  }
  const body = document.createElement("div");
  body.className = "pane-plugin";
  const iframe = document.createElement("iframe");
  iframe.className = "plugin-frame";
  iframe.setAttribute("sandbox", "allow-scripts");
  iframe.setAttribute("referrerpolicy", "no-referrer");
  iframe.setAttribute("title", spec.title);
  iframe.src = spec.url;
  const status = document.createElement("div");
  status.className = "plugin-panel-status";
  status.textContent = "Loading plugin panel…";
  body.append(status, iframe);
  const tab: PluginTab = {
    kind: "plugin",
    key: `t${++nextKey}`,
    sessionId: spec.sessionId,
    panelId: spec.panelId,
    plugin: spec.plugin,
    view: spec.view,
    title: spec.title,
    workspace: spec.workspace,
    body,
    iframe,
    openedAt: Date.now(),
    stale: false,
  };
  const timer = window.setTimeout(() => {
    status.textContent = "Plugin UI failed to load: no response";
  }, 10_000);
  iframe.addEventListener("load", () => {
    window.clearTimeout(timer);
    status.remove();
  });
  set.tabs.push(tab);
  set.active = tab.key;
  if (visibleSessionId() === spec.sessionId) showPane();
  return tab;
}

// markPluginUpdated puts a reload banner on a panel whose plugin files
// changed; reload reopens the frame with the new entry.
export function markPluginUpdated(tab: PluginTab, reload: () => void): void {
  if (tab.stale) return;
  tab.stale = true;
  const banner = document.createElement("div");
  banner.className = "plugin-stale";
  banner.setAttribute("role", "status");
  banner.innerHTML = `<span>Plugin updated</span><button type="button">Reload</button>`;
  banner.querySelector("button")!.addEventListener("click", () => {
    banner.remove();
    reload();
  });
  tab.body.prepend(banner);
}

export function closePluginTab(panelId: string): void {
  for (const [id, set] of sets) {
    const tab = set.tabs.find((t): t is PluginTab => t.kind === "plugin" && t.panelId === panelId);
    if (tab) {
      closeTab(tab.key, true, id);
      return;
    }
  }
}

function activeTab(set: TabSet): Tab | undefined {
  return set.tabs.find((t) => t.key === set.active);
}

// ---- side chats ----

function newChatTab(chatId: string | null = null, title = "Side chat", messages: SideChatMessage[] = []): ChatTab {
  return createChatTab(`t${++nextKey}`, paintTabs, chatId, title, messages);
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
  const set = state.sessionId ? sets.get(state.sessionId) : undefined;
  if (!set) return;
  for (const tab of set.tabs) {
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
                : tab.kind === "plugin"
                  ? icon("sl-plug", "h-3 w-3")
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
            : tab.kind === "plugin"
              ? ` data-plugin="${escapeAttr(tab.plugin)}/${escapeAttr(tab.view)}"`
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

function mainTabHtml(): string {
  return `<div class="pane-tab pane-main-tab${paneOpen ? "" : " is-active"}" data-key="main"><button type="button" role="tab" aria-selected="${!paneOpen}" class="pane-tab-pick" title="Agent chat">${icon("rl-agent", "h-3 w-3")}<span>Agent</span></button></div>`;
}

let tabsHtml = "";

function paintTabs(): void {
  const set = currentSet();
  const html =
    (layoutMode === "tabs" ? mainTabHtml() : "") +
    (set
      ? set.tabs
          .filter((t) => !ideTerminalMode || (t.kind !== "terminal" && !isBrowserPage(t)))
          .map((t) => tabHtml(t, paneOpen && t.key === set.active))
          .join("")
      : "");
  if (html === tabsHtml && tabsEl.innerHTML !== "") return;
  tabsHtml = html;
  tabsEl.innerHTML = html;
}

// render paints the open session's tabs, keeping each tab's DOM.
function render(): void {
  if (ideTerminalMode) terminalDock?.sync();
  renderIDEPages(currentSet());
  if (layoutMode === "tabs") {
    titleBar.insertBefore(stripEl, titleBar.lastElementChild);
  } else {
    sidePaneEl.insertBefore(stripEl, sidePaneEl.firstElementChild);
  }
  agentView.classList.toggle("is-tab-layout", layoutMode === "tabs");
  agentView.classList.toggle("is-side-tab", layoutMode === "tabs" && paneOpen);
  sidePaneEl.hidden = !paneOpen;
  layoutBtn.dataset.layout = layoutMode;
  layoutBtn.setAttribute("aria-label", layoutMode === "tabs" ? "Switch to split layout" : "Switch to tabs layout");
  layoutBtn.title = layoutMode === "tabs" ? "Switch to split layout" : "Switch to tabs layout";
  if (!paneOpen && layoutMode === "split") {
    closeMenu();
    tabsEl.innerHTML = "";
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
  if (paneOpen && set.tabs.length === 0 && hasMainMessage()) {
    const tab = newChatTab();
    set.tabs.push(tab);
    set.active = tab.key;
  }
  if (set.tabs.length && !activeTab(set)) set.active = set.tabs[0].key;
  paintTabs();
  bodyEl.replaceChildren(
    ...set.tabs.filter((t) => !ideTerminalMode || (t.kind !== "terminal" && !isBrowserPage(t))).map((t) => t.body),
  );
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
  const set = paneOpen && state.sessionId ? sets.get(state.sessionId) : undefined;
  const tab = set && activeTab(set);
  if (tab?.kind !== "agent") return;
  liveRoot(tab.agentId).remove();
  void loadAgent(tab);
});

function showPane(): void {
  paneOpen = true;
  render();
}

function closePane(): void {
  paneOpen = false;
  closeMenu();
  render();
}

function toggleLayout(): void {
  layoutMode = layoutMode === "split" ? "tabs" : "split";
  saveText(layoutKey, layoutMode);
  tabsHtml = "";
  render();
}

// openTerminalDockFromKey toggles the IDE terminal dock; in split/tabs
// mode it falls back to adding a terminal tab, which is the same place a
// terminal shows there.
export function openTerminalDockFromKey(): void {
  if (ideTerminalMode && terminalDock) terminalDock.toggle();
  else openTerminalTab(visibleSessionId());
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

function closeTab(key: string, dropBrowser = true, sessionId = visibleSessionId()): void {
  const set = sessionId ? sets.get(sessionId) : undefined;
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
  if (removed.kind === "plugin") {
    removed.iframe.src = "about:blank";
    removed.iframe.remove();
    if (hasBridge()) void bridge().plugins?.close(removed.panelId);
  }
  if (set.tabs.length === 0) {
    if (sessionId === STANDALONE_PANE_SESSION) sets.delete(sessionId);
    if (sessionId === visibleSessionId()) {
      closePane();
    } else {
      sets.delete(sessionId);
    }
    return;
  }
  if (set.active === key) set.active = set.tabs[Math.min(at, set.tabs.length - 1)].key;
  if (sessionId === visibleSessionId()) render();
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
    if (tab.kind === "plugin") {
      tab.iframe.src = "about:blank";
      tab.iframe.remove();
      if (hasBridge()) void bridge().plugins?.close(tab.panelId);
    }
    tab.body.remove();
  };
  for (const [id, set] of sets) {
    if (gone.has(id)) {
      for (const tab of set.tabs) {
        if (tab.kind === "terminal" && tab.sessionId === STANDALONE_PANE_SESSION) {
          const standalone = browserSet(STANDALONE_PANE_SESSION);
          standalone.tabs.push(tab);
          if (!standalone.active) standalone.active = tab.key;
        } else drop(tab);
      }
      sets.delete(id);
      continue;
    }
    const keep = set.tabs.filter((t) => t.kind !== "agent" || !gone.has(t.agentId));
    set.tabs.filter((t) => !keep.includes(t)).forEach(drop);
    set.tabs = keep;
  }
  tabsHtml = "";
  render();
}

/**
 * paneSessionIds lists the sessions that hold side-pane tabs; e2e specs
 * read it to check deleted sessions leave nothing behind.
 * @public
 */
export function paneSessionIds(): string[] {
  return [...sets.keys()];
}

// syncSidePane follows the main view to another session.
export function syncSidePane(): void {
  window.dispatchEvent(new Event("paladin:workspace-changed"));
  closeMenu();
  attachStandaloneTerminals(state.sessionId);
  if (pendingBrowserSessions.delete(state.sessionId)) {
    const tab = sets.get(state.sessionId)?.tabs.find((item) => item.kind === "browser");
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
  if (hasBridge()) {
    bridge().onEvent?.(browserEvent);
    bridge().onEvent?.((event) => terminalEvent(event, sets));
    bridge().views?.onState?.(webState);
  }
  const observer = new ResizeObserver(() => {
    syncNativeSurfaces();
    const set = state.sessionId ? sets.get(state.sessionId) : null;
    const tab = set && activeTab(set);
    if (paneOpen && tab?.kind === "terminal") tab.fit?.fit();
  });
  observer.observe(sidePaneEl);
  observer.observe(bodyEl);
  window.addEventListener("resize", syncNativeSurfaces);
  window.addEventListener("paladin:plugin-overlay-changed", syncNativeSurfaces);
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
  changesBtn.addEventListener("click", () => openChangesTab(null));
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
      openChangesTab(Number(card.dataset.changeTurn));
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
  tabsEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const key = target.closest<HTMLElement>(".pane-tab")?.dataset.key;
    if (key === "main") {
      closePane();
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
  });
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
