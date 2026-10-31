// sidePane.ts is the split pane beside the conversation. Its tabs are
// /btw side chats (saved per session, each with its own composer) and
// live views of subagents. Each session keeps its own set of tabs; a
// hidden tab keeps its DOM, so switching back loses nothing.

import * as api from "../core/api";
import type { Terminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { $, inputEl, sidePaneBtn, sidePaneEl } from "../core/dom";
import { bridge, hasBridge } from "../core/bridge";
import { escapeAttr, escapeHtml, renderMarkdown } from "../core/format";
import { state } from "../core/state";
import { loadText, saveText } from "../core/storage";
import type { ChangedFile, SessionChanges, SideChatMessage } from "../core/types";
import { icon } from "../icons";
import { toast } from "../ui/toast";
import { abortAttach, beginAttach, finishAttach, liveBadge, liveRoot, onResync } from "./live";
import { agentStatus, agentStatusHtml, buildHistory, openSession, refreshSessions } from "./sessions";

type ChatTab = {
  kind: "chat";
  key: string;
  chatId: string | null;
  title: string;
  body: HTMLElement;
  busy: boolean;
};

type AgentTab = {
  kind: "agent";
  key: string;
  agentId: string;
  title: string;
  body: HTMLElement;
  // loading is the attach in flight, so two paints never race.
  loading: Promise<void> | null;
  // head is the last painted head, so unchanged repaints are skipped.
  head: string;
};

type BrowserTab = {
  kind: "browser";
  key: string;
  sessionId: string;
  title: string;
  body: HTMLElement;
  url: string;
  picking: boolean;
  pickToken: number;
  noteCount: number;
  passing: boolean;
};

type TerminalTab = {
  kind: "terminal";
  key: string;
  sessionId: string;
  cwd: string;
  title: string;
  body: HTMLElement;
  term: Terminal | null;
  fit: FitAddon | null;
  pendingText: string[];
  terminalId: string;
  starting: Promise<void> | null;
  closed: boolean;
};

type ChangesTab = {
  kind: "changes";
  key: string;
  sessionId: string;
  title: string;
  body: HTMLElement;
  turn: number | null;
  selected: string | null;
  request: number;
};

type Tab = ChatTab | AgentTab | BrowserTab | ChangesTab | TerminalTab;
type TabSet = { tabs: Tab[]; active: string };

const tabsEl = $("#paneTabs");
const stripEl = tabsEl.parentElement!;
const bodyEl = $("#paneBody");
const historyBtn = $<HTMLButtonElement>("#paneHistoryBtn");
const menuEl = $("#paneHistoryMenu");
const menuItemsEl = $("#paneHistoryItems");
const layoutBtn = $<HTMLButtonElement>("#paneLayoutBtn");
const changesBtn = $<HTMLButtonElement>("#sessionChangesBtn");
const titleBar = $("#titleBar");
const agentView = $("#agentView");
const layoutKey = "paladin.paneLayout";

const sets = new Map<string, TabSet>();
const changesCache = new Map<string, SessionChanges>();
let paneOpen = false;
let layoutMode: "split" | "tabs" = loadText(layoutKey) === "tabs" ? "tabs" : "split";
let nextKey = 0;
const pendingTerminalData = new Map<string, string[]>();

function terminalEvent(raw: unknown): void {
  if (!raw || typeof raw !== "object") return;
  const e = raw as { event?: string; sessionId?: string; terminalId?: string; data?: string; exitCode?: number };
  if (!e.event?.startsWith("terminal/") || !e.terminalId || !e.sessionId) return;
  const tab = sets
    .get(e.sessionId)
    ?.tabs.find((t): t is TerminalTab => t.kind === "terminal" && t.terminalId === e.terminalId);
  if (e.event === "terminal/data" && e.data) {
    if (!tab) {
      const queued = pendingTerminalData.get(e.terminalId) ?? [];
      if (queued.length < 128) queued.push(e.data);
      pendingTerminalData.set(e.terminalId, queued);
      return;
    }
    tab.term?.write(Uint8Array.from(atob(e.data), (char) => char.charCodeAt(0)));
  } else if (e.event === "terminal/exit" && tab) {
    tab.term?.write(`\r\n[terminal exited${e.exitCode ? `: ${e.exitCode}` : ""}]\r\n`);
    tab.terminalId = "";
    tab.closed = true;
  } else if (e.event === "terminal/exit") {
    pendingTerminalData.delete(e.terminalId);
  }
}

function newTerminalTab(sessionId: string, cwd: string): TerminalTab {
  const body = document.createElement("div");
  body.className = "pane-terminal";
  body.innerHTML = `<div class="terminal-heading"><span>${icon("terminal", "h-3.5 w-3.5")} Terminal</span><small>${escapeHtml(cwd)}</small></div><div class="terminal-surface" aria-label="Interactive terminal"></div>`;
  const tab: TerminalTab = {
    kind: "terminal",
    key: `t${++nextKey}`,
    sessionId,
    cwd,
    title: "Terminal",
    body,
    term: null,
    fit: null,
    pendingText: [],
    terminalId: "",
    starting: null,
    closed: false,
  };
  void Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]).then(
    ([{ Terminal }, { FitAddon }]) => {
      if (tab.closed) return;
      const term = new Terminal({
        cursorBlink: true,
        fontFamily: '"JetBrains Mono", ui-monospace, monospace',
        fontSize: 12,
        scrollback: 5000,
        theme: { background: "#17171a", foreground: "#e9e9ed", cursor: "#8faafa" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      tab.term = term;
      tab.fit = fit;
      term.onData((data) => {
        if (tab.terminalId) void api.terminalWrite(tab.sessionId, tab.terminalId, data).catch(() => undefined);
      });
      term.onResize(({ cols, rows }) => {
        if (tab.terminalId) void api.terminalResize(tab.sessionId, tab.terminalId, cols, rows).catch(() => undefined);
      });
      for (const text of tab.pendingText) term.write(text);
      tab.pendingText = [];
      if (!tab.body.hidden) activateTerminal(tab);
    },
    (error) => {
      body.querySelector<HTMLElement>(".terminal-surface")!.textContent =
        `Terminal could not load: ${errorText(error)}`;
    },
  );
  return tab;
}

function activateTerminal(tab: TerminalTab): void {
  if (!tab.term || !tab.fit) return;
  const term = tab.term;
  const fit = tab.fit;
  const surface = tab.body.querySelector<HTMLElement>(".terminal-surface")!;
  if (!surface.querySelector(".xterm")) term.open(surface);
  if (!tab.body.hidden) fit.fit();
  if (tab.starting || tab.terminalId || tab.closed) return;
  tab.starting = api
    .terminalStart(tab.sessionId, tab.cwd, Math.max(term.cols, 20), Math.max(term.rows, 5))
    .then((id) => {
      if (tab.closed) {
        void api.terminalClose(tab.sessionId, id);
        return;
      }
      tab.terminalId = id;
      for (const chunk of pendingTerminalData.get(id) ?? []) {
        term.write(Uint8Array.from(atob(chunk), (char) => char.charCodeAt(0)));
      }
      pendingTerminalData.delete(id);
      term.focus();
    })
    .catch((error) => term.write(`\r\nTerminal could not start: ${errorText(error)}\r\n`))
    .finally(() => {
      tab.starting = null;
    });
}

function openTerminalTab(sessionId: string, cwd = state.cwd): TerminalTab {
  const set = browserSet(sessionId);
  const tab = newTerminalTab(sessionId, cwd);
  set.tabs.push(tab);
  set.active = tab.key;
  if (state.sessionId === sessionId) showPane();
  return tab;
}

export function showTerminalResult(sessionId: string, cwd: string, command: string, result: api.TerminalResult): void {
  const set = browserSet(sessionId);
  const tab = set.tabs.find((t): t is TerminalTab => t.kind === "terminal") ?? openTerminalTab(sessionId, cwd);
  set.active = tab.key;
  if (state.sessionId === sessionId) showPane();
  const text = `\r\n$ ${command}\r\n${result.output.replace(/\n/g, "\r\n")}${result.truncated ? "\r\n[output truncated]" : ""}\r\n[exit ${result.exitCode}]\r\n`;
  if (tab.term) tab.term.write(text);
  else tab.pendingText.push(text);
}

function currentSet(): TabSet | null {
  if (!state.sessionId) return null;
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

function newBrowserTab(sessionId: string): BrowserTab {
  const body = document.createElement("div");
  body.className = "pane-browser";
  body.innerHTML = `<form class="browser-toolbar"><input class="browser-url" type="url" inputmode="url" spellcheck="false" placeholder="http://localhost:8080" aria-label="Local browser URL" /><button class="browser-open" type="submit">Open</button><button class="browser-annotate" type="button" aria-label="Comment on elements" aria-pressed="false" title="Comment on elements">${icon("comments", "h-4 w-4")}<span>Comment</span></button><button class="browser-pass" type="button" disabled>Pass to agent</button></form><p class="browser-status" role="status">Open a local development server to preview it here.</p><div class="browser-surface" aria-label="Local browser page"></div>`;
  const tab: BrowserTab = {
    kind: "browser",
    key: `t${++nextKey}`,
    sessionId,
    title: "Browser",
    body,
    url: "",
    picking: false,
    pickToken: 0,
    noteCount: 0,
    passing: false,
  };
  body.querySelector<HTMLFormElement>("form")!.addEventListener("submit", (event) => {
    event.preventDefault();
    const url = body.querySelector<HTMLInputElement>(".browser-url")!.value.trim();
    navigateBrowser(tab, url);
  });
  body.querySelector<HTMLButtonElement>(".browser-annotate")!.addEventListener("click", () => {
    if (tab.picking) stopBrowserPick(tab);
    else void startBrowserPick(tab);
  });
  body
    .querySelector<HTMLButtonElement>(".browser-pass")!
    .addEventListener("click", () => void passBrowserComments(tab));
  return tab;
}

function stopBrowserPick(tab: BrowserTab): void {
  if (!tab.picking) return;
  tab.picking = false;
  tab.pickToken++;
  void bridge().browser?.cancelPick(tab.sessionId);
  updateBrowserToolbar(tab);
}

async function startBrowserPick(tab: BrowserTab): Promise<void> {
  if (!hasBridge() || !bridge().browser?.pick) {
    tab.body.querySelector<HTMLElement>(".browser-status")!.textContent =
      "Element selection needs the desktop browser.";
    return;
  }
  tab.picking = true;
  const token = ++tab.pickToken;
  updateBrowserToolbar(tab);
  await bridge().browser!.cancelPick(tab.sessionId);
  if (!tab.picking || token !== tab.pickToken) return;
  while (tab.picking && token === tab.pickToken) {
    try {
      const element = await bridge().browser!.pick(tab.sessionId);
      if (!tab.picking || token !== tab.pickToken || !element) break;
    } catch (error) {
      if (token === tab.pickToken)
        tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
      break;
    }
  }
  if (token === tab.pickToken) {
    tab.picking = false;
    updateBrowserToolbar(tab);
  }
}

function updateBrowserToolbar(tab: BrowserTab): void {
  const toggle = tab.body.querySelector<HTMLButtonElement>(".browser-annotate")!;
  toggle.setAttribute("aria-pressed", String(tab.picking));
  toggle.title = tab.picking ? "Stop selecting elements" : "Comment on elements";
  const pass = tab.body.querySelector<HTMLButtonElement>(".browser-pass")!;
  pass.disabled = tab.passing || tab.noteCount === 0;
  pass.textContent = tab.noteCount ? `Pass to agent (${tab.noteCount})` : "Pass to agent";
}

async function passBrowserComments(tab: BrowserTab): Promise<void> {
  if (tab.passing || !hasBridge() || !bridge().browser?.getNotes) return;
  tab.passing = true;
  updateBrowserToolbar(tab);
  stopBrowserPick(tab);
  try {
    const notes = await bridge().browser!.getNotes(tab.sessionId);
    if (!notes.length || state.sessionId !== tab.sessionId) return;
    const lines = notes.map(
      (note, index) =>
        `${index + 1}. URL: ${note.url}\n   Element: <${note.tag}>${note.label || note.text ? ` ${JSON.stringify(note.label || note.text)}` : ""}\n   CSS selector: ${note.selector}\n   Change: ${note.comment}`,
    );
    const feedback = `Browser UI feedback:\n${lines.join("\n")}`;
    inputEl.value = `${inputEl.value.trimEnd()}${inputEl.value.trim() ? "\n\n" : ""}${feedback}`;
    inputEl.dispatchEvent(new Event("input", { bubbles: true }));
    await bridge().browser!.clearNotes(tab.sessionId);
    if (layoutMode === "tabs") closePane();
    inputEl.focus();
  } catch (error) {
    tab.body.querySelector<HTMLElement>(".browser-status")!.textContent = errorText(error);
  } finally {
    tab.passing = false;
    updateBrowserToolbar(tab);
  }
}

function navigateBrowser(tab: BrowserTab, url: string): void {
  stopBrowserPick(tab);
  const status = tab.body.querySelector<HTMLElement>(".browser-status")!;
  if (!url) {
    status.textContent = "Enter a localhost URL.";
    return;
  }
  tab.body.querySelector<HTMLInputElement>(".browser-url")!.value = url;
  if (!hasBridge() || !bridge().browser) {
    status.textContent = "The desktop browser is unavailable.";
    return;
  }
  status.textContent = "Opening…";
  void bridge()
    .browser!.open(tab.sessionId, url)
    .then(
      () => {
        tab.url = url;
        status.textContent = url;
        syncNativeBrowser();
      },
      (error: unknown) => {
        status.textContent = errorText(error);
      },
    );
}

function openBrowserTab(sessionId: string, url = ""): void {
  const set = browserSet(sessionId);
  let tab = set.tabs.find((t): t is BrowserTab => t.kind === "browser");
  if (!tab) {
    tab = newBrowserTab(sessionId);
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
  set.active = tab.key;
  if (state.sessionId === sessionId && state.sideView === "agent") showPane();
}

// Chat links use the same one-browser-per-session view as the agent tool.
export function openLocalBrowserURL(url: string): void {
  if (!state.sessionId || state.sideView !== "agent") {
    toast("Start a conversation to open a local browser.", "error");
    return;
  }
  const set = browserSet(state.sessionId);
  openBrowserTab(state.sessionId, url);
  const tab = set.tabs.find((t): t is BrowserTab => t.kind === "browser")!;
  navigateBrowser(tab, url);
}

function browserEvent(raw: unknown): void {
  if (!raw || typeof raw !== "object") return;
  const e = raw as { event?: string; sessionId?: string; url?: string; count?: number };
  if (!e.sessionId) return;
  if (e.event === "browser/opened") {
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
    stopBrowserPick(tab);
    if (state.sessionId === e.sessionId) closeTab(tab.key, false);
    else {
      const set = sets.get(e.sessionId)!;
      set.tabs = set.tabs.filter((t) => t !== tab);
      tab.body.remove();
    }
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

function paintTurnMarker(row: HTMLElement, turn: number, files: ChangedFile[]): void {
  let button = row.querySelector<HTMLButtonElement>(".turn-changes");
  if (!files.length) {
    button?.remove();
    return;
  }
  if (!button) {
    button = document.createElement("button");
    button.type = "button";
    button.className = "turn-changes";
    row.prepend(button);
  }
  button.dataset.changeTurn = String(turn);
  button.title = "View this turn's file changes";
  button.setAttribute(
    "aria-label",
    `View ${files.length} ${files.length === 1 ? "file" : "files"} changed in this turn`,
  );
  button.innerHTML = `${icon("tl-file", "h-3.5 w-3.5")}<span>${files.length} ${files.length === 1 ? "file" : "files"}</span>`;
}

let changeRequest = 0;

// Refresh after a tool settles or a session is opened. Persisted user
// rows carry transcript indices; the current live row is passed in.
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
    paintChangesHeader(data);
    const byTurn = new Map(data.turns.map((turn) => [turn.turn, turn.files]));
    for (const row of Array.from($("#messages").querySelectorAll<HTMLElement>("[data-turn-index]"))) {
      const turn = Number(row.dataset.turnIndex);
      paintTurnMarker(row, turn, byTurn.get(turn) ?? []);
    }
    if (liveRow && callId) {
      const turn = data.turns.find((item) => item.callIds?.includes(callId));
      if (turn) paintTurnMarker(liveRow, turn.turn, turn.files);
    }
    const tab = sets.get(sessionId)?.tabs.find((t): t is ChangesTab => t.kind === "changes");
    if (tab) paintChangesTab(tab, data);
  } catch {
    if (sessionId === state.sessionId && request === changeRequest) paintChangesHeader(undefined);
  }
}

function newChangesTab(sessionId: string): ChangesTab {
  const body = document.createElement("div");
  body.className = "pane-changes";
  body.innerHTML = `<div class="change-scope"></div><div class="change-files"></div><div class="change-diff" role="region" aria-label="File diff">Select a file to view its changes.</div>`;
  const tab: ChangesTab = {
    kind: "changes",
    key: `t${++nextKey}`,
    sessionId,
    title: "Changes",
    body,
    turn: null,
    selected: null,
    request: 0,
  };
  body.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("[data-change-all]")) {
      tab.turn = null;
      tab.selected = null;
      paintChangesTab(tab, changesCache.get(sessionId));
      return;
    }
    const file = target.closest<HTMLElement>("[data-change-file]")?.dataset.changeFile;
    if (file) {
      tab.selected = file;
      paintChangesTab(tab, changesCache.get(sessionId));
      void loadChangeDiff(tab, file);
    }
  });
  return tab;
}

function paintChangesTab(tab: ChangesTab, data: SessionChanges | undefined): void {
  const files = tab.turn === null ? data?.files : data?.turns.find((t) => t.turn === tab.turn)?.files;
  tab.body.querySelector<HTMLElement>(".change-scope")!.innerHTML =
    `<span>${tab.turn === null ? "Session changes" : "Turn changes"}</span>` +
    (tab.turn === null ? "" : `<button type="button" data-change-all>All session changes</button>`);
  const list = tab.body.querySelector<HTMLElement>(".change-files")!;
  if (!data) {
    list.textContent = "Loading file changes…";
  } else if (!data.available) {
    list.textContent = "File snapshots are unavailable for this session.";
  } else if (!files?.length) {
    list.textContent = "No file changes recorded.";
  } else {
    list.innerHTML = files
      .map(
        (f) =>
          `<button type="button" class="change-file${tab.selected === f.path ? " is-active" : ""}" data-change-file="${escapeAttr(f.path)}" title="${escapeAttr(f.path)}">` +
          `${icon("tl-file", "h-3.5 w-3.5")}<span>${escapeHtml(f.path)}</span><small>${f.status}</small></button>`,
      )
      .join("");
  }
  if (tab.selected && !files?.some((f) => f.path === tab.selected)) tab.selected = null;
  if (!tab.selected)
    tab.body.querySelector<HTMLElement>(".change-diff")!.textContent = "Select a file to view its changes.";
}

async function loadChangeDiff(tab: ChangesTab, path: string): Promise<void> {
  const request = ++tab.request;
  const preview = tab.body.querySelector<HTMLElement>(".change-diff")!;
  preview.textContent = "Loading diff…";
  try {
    const diff = await api.sessionDiff(tab.sessionId, null, tab.turn, path);
    if (request !== tab.request || tab.selected !== path) return;
    preview.replaceChildren();
    if (!diff.patch) {
      preview.textContent = "No text diff available for this file.";
      return;
    }
    const lines = diff.patch.split("\n");
    const fragment = document.createDocumentFragment();
    for (const line of lines.slice(0, 5000)) {
      const el = document.createElement("div");
      el.className =
        line.startsWith("+") && !line.startsWith("+++")
          ? "change-add"
          : line.startsWith("-") && !line.startsWith("---")
            ? "change-remove"
            : line.startsWith("@@")
              ? "change-hunk"
              : "";
      el.textContent = line || " ";
      fragment.append(el);
    }
    preview.append(fragment);
    if (diff.truncated || lines.length > 5000) preview.append("\nDiff preview truncated.");
  } catch (error) {
    if (request === tab.request) preview.textContent = `Could not load diff: ${errorText(error)}`;
  }
}

function openChangesTab(turn: number | null): void {
  const sessionId = state.sessionId;
  if (!sessionId) return;
  const set = currentSet()!;
  let tab = set.tabs.find((t): t is ChangesTab => t.kind === "changes");
  if (!tab) {
    tab = newChangesTab(sessionId);
    set.tabs.push(tab);
  }
  tab.turn = turn;
  tab.selected = null;
  set.active = tab.key;
  showPane();
  paintChangesTab(tab, changesCache.get(sessionId));
  void refreshChanges(sessionId);
}

function syncNativeBrowser(): void {
  if (!hasBridge() || !bridge().browser) return;
  const set = state.sessionId ? sets.get(state.sessionId) : undefined;
  const tab = set && activeTab(set);
  const modalOpen = ["settingsModal", "sessionModal", "confirmModal", "siteModal", "askModal"].some((id) => {
    const el = document.getElementById(id);
    return el && !el.classList.contains("hidden");
  });
  if (!paneOpen || state.sideView !== "agent" || tab?.kind !== "browser" || modalOpen) {
    void bridge().browser!.show(null);
    return;
  }
  const surface = tab.body.querySelector<HTMLElement>(".browser-surface")!;
  const rect = surface.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) {
    void bridge().browser!.show(null);
    return;
  }
  void bridge().browser!.setBounds({ x: rect.left, y: rect.top, width: rect.width, height: rect.height });
  void bridge().browser!.show(tab.sessionId);
}

function activeTab(set: TabSet): Tab | undefined {
  return set.tabs.find((t) => t.key === set.active);
}

function errorText(error: unknown): string {
  return String(error instanceof Error ? error.message : error).replace(/^Error:\s*/, "");
}

function clip(text: string, max = 40): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

// ---- side chats ----

function chatMessage(msg: Pick<SideChatMessage, "role" | "content" | "model">): HTMLElement {
  const el = document.createElement("div");
  el.className = "side-msg";
  el.dataset.role = msg.role;
  el.dataset.state = "done";
  if (msg.role === "user") {
    el.textContent = msg.content;
  } else {
    el.innerHTML =
      `<div class="md">${renderMarkdown(msg.content)}</div>` +
      (msg.model ? `<div class="side-model">${escapeHtml(msg.model)}</div>` : "");
  }
  return el;
}

function newChatTab(chatId: string | null = null, title = "Side chat", messages: SideChatMessage[] = []): ChatTab {
  const body = document.createElement("div");
  body.className = "side-chat";
  body.innerHTML =
    `<div class="side-msgs" role="log" aria-live="polite">` +
    `<div class="side-empty"${messages.length ? " hidden" : ""}>` +
    `<span class="side-empty-ico">${icon("comments", "h-5 w-5")}</span>` +
    `<p class="side-empty-title">Side chat</p>` +
    `<p class="side-empty-text">Ask about this conversation without adding to it. Answers use no tools.</p>` +
    `</div></div>` +
    `<form class="side-composer">` +
    `<textarea class="side-input" rows="1" placeholder="Ask a side question…" aria-label="Side question"></textarea>` +
    `<button type="submit" class="side-send" title="Send" aria-label="Send">${icon("arrow-up", "h-3.5 w-3.5")}</button>` +
    `</form>`;
  const list = body.querySelector<HTMLElement>(".side-msgs")!;
  for (const msg of messages) list.append(chatMessage(msg));
  const tab: ChatTab = { kind: "chat", key: `t${++nextKey}`, chatId, title, body, busy: false };
  if (chatId) body.dataset.chat = chatId;

  const input = body.querySelector<HTMLTextAreaElement>(".side-input")!;
  const send = () => {
    const question = input.value.trim();
    if (!question || tab.busy) return;
    input.value = "";
    void sendChat(tab, question);
  };
  body.querySelector("form")!.addEventListener("submit", (event) => {
    event.preventDefault();
    send();
  });
  input.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      send();
    }
  });
  return tab;
}

function isBlankChat(tab: Tab | undefined): tab is ChatTab {
  return tab?.kind === "chat" && !tab.chatId && !tab.busy && !tab.body.querySelector(".side-msg");
}

// sendChat asks one side question in tab's chat. The chat belongs to
// the session it was asked in, even if the user moves on meanwhile.
async function sendChat(tab: ChatTab, question: string): Promise<void> {
  const sessionId = state.sessionId;
  const cwd = state.cwd || null;
  const list = tab.body.querySelector<HTMLElement>(".side-msgs")!;
  const sendBtn = tab.body.querySelector<HTMLButtonElement>(".side-send")!;
  list.querySelector(".side-empty")?.setAttribute("hidden", "");
  list.querySelector(".side-error")?.remove();
  list.append(chatMessage({ role: "user", content: question }));
  const pending = document.createElement("div");
  pending.className = "side-msg";
  pending.dataset.role = "assistant";
  pending.dataset.state = "pending";
  pending.innerHTML = `<span class="btw-spinner" aria-hidden="true"></span><span class="side-wait">Thinking…</span>`;
  list.append(pending);
  list.scrollTop = list.scrollHeight;
  if (!tab.chatId && tab.title === "Side chat") {
    tab.title = clip(question);
    paintTabs();
  }
  tab.busy = true;
  sendBtn.disabled = true;
  try {
    const res = await api.sideQuestion(
      sessionId,
      cwd,
      question,
      state.selectedProvider || null,
      state.selectedModel || null,
      tab.chatId,
    );
    tab.chatId = res.chatId || tab.chatId;
    if (tab.chatId) tab.body.dataset.chat = tab.chatId;
    pending.replaceWith(chatMessage({ role: "assistant", content: res.answer, model: res.model }));
  } catch (error) {
    const err = document.createElement("div");
    err.className = "side-error";
    err.setAttribute("role", "alert");
    err.textContent = errorText(error);
    pending.replaceWith(err);
  } finally {
    tab.busy = false;
    sendBtn.disabled = false;
    list.scrollTop = list.scrollHeight;
    paintTabs();
  }
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
export function openSideChat(): void {
  const set = currentSet();
  if (!set) return;
  blankChatTab(set).body.querySelector<HTMLTextAreaElement>(".side-input")!.focus();
}

// askSideQuestion runs `/btw <question>` in a side chat tab.
export function askSideQuestion(question: string): void {
  const set = currentSet();
  if (!set) return;
  void sendChat(blankChatTab(set), question);
}

// ---- subagent tabs ----

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

function newAgentTab(agentId: string): AgentTab {
  const child = state.sessions.find((s) => s.sessionId === agentId);
  const body = document.createElement("div");
  body.className = "pane-agent";
  body.dataset.agent = agentId;
  body.innerHTML =
    `<div class="pane-agent-head"></div>` +
    `<div class="pane-agent-body"><div class="pane-agent-history"></div></div>` +
    `<div class="pane-agent-foot">Driven by its parent agent · shares the parent's workspace</div>`;
  const tab: AgentTab = {
    kind: "agent",
    key: `t${++nextKey}`,
    agentId,
    title: child?.subagent?.name || child?.title || agentId.slice(0, 8),
    body,
    loading: null,
    head: "",
  };
  const parentId = state.sessionId;
  body.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest(".pane-agent-open")) {
      void openSession(agentId);
    } else if (target.closest(".pane-agent-stop")) {
      api
        .interruptAgent(parentId, agentId)
        .catch((error: unknown) => toast(`Could not stop: ${errorText(error)}`, "error"));
    }
  });
  return tab;
}

// loadAgent shows the child's history and then its live rows, which
// keep streaming. The main view can borrow the live rows; the tab takes
// them back (re-attaching) when it is shown again.
function loadAgent(tab: AgentTab): Promise<void> {
  const root = liveRoot(tab.agentId);
  const view = tab.body.querySelector<HTMLElement>(".pane-agent-body")!;
  if (root.parentElement === view || tab.loading) return tab.loading ?? Promise.resolve();
  tab.loading = (async () => {
    beginAttach(tab.agentId);
    try {
      const snap = await api.attachSession(tab.agentId);
      const history = view.querySelector<HTMLElement>(".pane-agent-history")!;
      history.replaceChildren();
      buildHistory(history, snap.history ?? [], false);
      finishAttach(tab.agentId, snap);
      if (state.sessionId !== tab.agentId) view.append(root);
      view.scrollTop = view.scrollHeight;
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

// openAgentTab shows a subagent of the open session in the pane.
async function openAgentTab(agentId: string): Promise<void> {
  const set = currentSet();
  if (!set || agentId === state.sessionId) return;
  if (!state.sessions.some((s) => s.sessionId === agentId)) {
    await refreshSessions();
  }
  let tab = set.tabs.find((t) => t.kind === "agent" && t.agentId === agentId);
  if (!tab) {
    tab = newAgentTab(agentId);
    set.tabs.push(tab);
  }
  set.active = tab.key;
  showPane();
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

// paintHead repaints only on change: live events repaint often, and a
// replaced button would swallow a click in progress.
function paintHead(tab: AgentTab): void {
  const html = agentHead(tab);
  if (html === tab.head) return;
  tab.head = html;
  tab.body.querySelector(".pane-agent-head")!.innerHTML = html;
}

// ---- the pane ----

function tabHtml(tab: Tab, active: boolean): string {
  const glyph =
    tab.kind === "agent"
      ? icon("rl-agent", "h-3 w-3")
      : tab.kind === "browser"
        ? icon("globe", "h-3 w-3")
        : tab.kind === "terminal"
          ? icon("terminal", "h-3 w-3")
          : tab.kind === "changes"
            ? icon("tl-file", "h-3 w-3")
            : icon("comments", "h-3 w-3");
  const data =
    tab.kind === "agent"
      ? ` data-agent="${escapeAttr(tab.agentId)}"`
      : tab.kind === "browser"
        ? ` data-browser="${escapeAttr(tab.sessionId)}"`
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

function mainTabHtml(): string {
  return `<div class="pane-tab pane-main-tab${paneOpen ? "" : " is-active"}" data-key="main"><button type="button" role="tab" aria-selected="${!paneOpen}" class="pane-tab-pick" title="Main chat">${icon("comments", "h-3 w-3")}<span>Chat</span></button></div>`;
}

let tabsHtml = "";

function paintTabs(): void {
  const set = currentSet();
  const html =
    (layoutMode === "tabs" ? mainTabHtml() : "") +
    (set ? set.tabs.map((t) => tabHtml(t, paneOpen && t.key === set.active)).join("") : "");
  if (html === tabsHtml && tabsEl.innerHTML !== "") return;
  tabsHtml = html;
  tabsEl.innerHTML = html;
}

// render paints the open session's tabs, keeping each tab's DOM.
function render(): void {
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
  layoutBtn.textContent = layoutMode === "tabs" ? "Split view" : "Tabs view";
  sidePaneBtn.setAttribute("aria-pressed", String(paneOpen));
  sidePaneBtn.classList.toggle("is-on", paneOpen);
  if (!paneOpen && layoutMode === "split") {
    closeMenu();
    tabsEl.innerHTML = "";
    bodyEl.replaceChildren();
    syncNativeBrowser();
    return;
  }
  const set = currentSet();
  if (!set) {
    paintTabs();
    bodyEl.innerHTML = `<div class="side-empty"><span class="side-empty-ico">${icon("comments", "h-5 w-5")}</span><p class="side-empty-title">Side chat</p><p class="side-empty-text">Send a message first: side chats are about a conversation.</p></div>`;
    syncNativeBrowser();
    return;
  }
  if (paneOpen && set.tabs.length === 0) {
    const tab = newChatTab();
    set.tabs.push(tab);
    set.active = tab.key;
  }
  if (set.tabs.length && !activeTab(set)) set.active = set.tabs[0].key;
  paintTabs();
  bodyEl.replaceChildren(...set.tabs.map((t) => t.body));
  for (const tab of set.tabs) {
    tab.body.hidden = !paneOpen || tab.key !== set.active;
    if (tab.kind === "browser" && tab.body.hidden) stopBrowserPick(tab);
    if (tab.kind === "terminal" && !tab.body.hidden) activateTerminal(tab);
  }
  const tab = activeTab(set);
  if (tab?.kind === "agent") {
    paintHead(tab);
    void loadAgent(tab);
  }
  syncNativeBrowser();
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

function closeTab(key: string, dropBrowser = true): void {
  const set = currentSet();
  if (!set) return;
  const at = set.tabs.findIndex((t) => t.key === key);
  if (at < 0) return;
  const [removed] = set.tabs.splice(at, 1);
  if (removed.kind === "browser") stopBrowserPick(removed);
  if (removed.kind === "browser" && dropBrowser && hasBridge()) void bridge().browser?.drop(removed.sessionId);
  if (removed.kind === "terminal") {
    removed.closed = true;
    if (removed.terminalId) void api.terminalClose(removed.sessionId, removed.terminalId).catch(() => undefined);
    removed.term?.dispose();
  }
  if (set.tabs.length === 0) {
    closePane();
    return;
  }
  if (set.active === key) set.active = set.tabs[Math.min(at, set.tabs.length - 1)].key;
  render();
}

// disposeSidePanes drops the tab sets of deleted sessions, and any
// tab, in any set, showing one of them: their DOM and live rows go too.
export function disposeSidePanes(ids: string[]): void {
  const gone = new Set(ids);
  const drop = (tab: Tab): void => {
    if (tab.kind === "agent") {
      if (tab.loading) abortAttach(tab.agentId);
      liveRoot(tab.agentId).remove();
    }
    if (tab.kind === "browser" && hasBridge()) void bridge().browser?.drop(tab.sessionId);
    if (tab.kind === "browser") stopBrowserPick(tab);
    if (tab.kind === "terminal") {
      tab.closed = true;
      if (tab.terminalId) void api.terminalClose(tab.sessionId, tab.terminalId).catch(() => undefined);
      tab.term?.dispose();
    }
    tab.body.remove();
  };
  for (const [id, set] of sets) {
    if (gone.has(id)) {
      set.tabs.forEach(drop);
      sets.delete(id);
      changesCache.delete(id);
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
  closeMenu();
  paintChangesHeader(changesCache.get(state.sessionId));
  render();
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
    render();
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
  if (hasBridge()) {
    bridge().onEvent?.(browserEvent);
    bridge().onEvent?.(terminalEvent);
  }
  const observer = new ResizeObserver(() => {
    syncNativeBrowser();
    const set = state.sessionId ? sets.get(state.sessionId) : null;
    const tab = set && activeTab(set);
    if (paneOpen && tab?.kind === "terminal") tab.fit?.fit();
  });
  observer.observe(sidePaneEl);
  observer.observe(bodyEl);
  window.addEventListener("resize", syncNativeBrowser);
  for (const id of ["settingsModal", "sessionModal", "confirmModal", "siteModal"]) {
    const el = document.getElementById(id);
    if (el) new MutationObserver(syncNativeBrowser).observe(el, { attributes: true, attributeFilter: ["class"] });
  }
  new MutationObserver((records) => {
    if (
      records.some((record) =>
        Array.from(record.addedNodes)
          .concat(Array.from(record.removedNodes))
          .some((node) => node instanceof HTMLElement && node.id === "askModal"),
      )
    ) {
      syncNativeBrowser();
    }
  }).observe(document.body, { childList: true });
  sidePaneBtn.addEventListener("click", () => (paneOpen ? closePane() : showPane()));
  changesBtn.addEventListener("click", () => openChangesTab(null));
  $("#messages").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLElement>("[data-change-turn]");
    if (button?.dataset.changeTurn) openChangesTab(Number(button.dataset.changeTurn));
  });
  layoutBtn.addEventListener("click", () => {
    toggleLayout();
    closeMenu();
  });
  $("#paneBrowserBtn").addEventListener("click", () => {
    if (!state.sessionId) return showPane();
    openBrowserTab(state.sessionId);
    currentSet()
      ?.tabs.find((t) => t.kind === "browser")
      ?.body.querySelector<HTMLInputElement>(".browser-url")
      ?.focus();
  });
  $("#paneTerminalBtn").addEventListener("click", () => {
    if (!state.sessionId) return showPane();
    openTerminalTab(state.sessionId);
  });
  $("#paneCloseBtn").addEventListener("click", closePane);
  $("#paneNewChatBtn").addEventListener("click", () => {
    const set = currentSet();
    if (!set) return showPane();
    const tab = newChatTab();
    set.tabs.push(tab);
    set.active = tab.key;
    showPane();
    tab.body.querySelector<HTMLTextAreaElement>(".side-input")!.focus();
  });
  historyBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    if (menuEl.hidden) {
      showPane();
      void openMenu();
    } else closeMenu();
  });
  menuEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
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
