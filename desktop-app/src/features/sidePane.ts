// sidePane.ts is the split pane beside the conversation. Its tabs are
// /btw side chats (saved per session, each with its own composer) and
// live views of subagents. Each session keeps its own set of tabs; a
// hidden tab keeps its DOM, so switching back loses nothing.

import * as api from "../core/api";
import { $, sidePaneBtn, sidePaneEl } from "../core/dom";
import { escapeAttr, escapeHtml, renderMarkdown } from "../core/format";
import { state } from "../core/state";
import type { SideChatMessage } from "../core/types";
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

type Tab = ChatTab | AgentTab;
type TabSet = { tabs: Tab[]; active: string };

const tabsEl = $("#paneTabs");
const bodyEl = $("#paneBody");
const historyBtn = $<HTMLButtonElement>("#paneHistoryBtn");
const menuEl = $("#paneHistoryMenu");

const sets = new Map<string, TabSet>();
let paneOpen = false;
let nextKey = 0;

function currentSet(): TabSet | null {
  if (!state.sessionId) return null;
  let set = sets.get(state.sessionId);
  if (!set) {
    set = { tabs: [], active: "" };
    sets.set(state.sessionId, set);
  }
  return set;
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
  const glyph = tab.kind === "agent" ? icon("rl-agent", "h-3 w-3") : icon("comments", "h-3 w-3");
  const data =
    tab.kind === "agent"
      ? ` data-agent="${escapeAttr(tab.agentId)}"`
      : tab.chatId
        ? ` data-chat="${escapeAttr(tab.chatId)}"`
        : "";
  const busy = tab.kind === "chat" ? tab.busy : liveBadge(tab.agentId) === "running";
  return (
    `<div class="pane-tab${active ? " is-active" : ""}" data-key="${tab.key}"${data}>` +
    `<button type="button" role="tab" aria-selected="${active}" class="pane-tab-pick" title="${escapeAttr(tab.title)}">` +
    `${busy ? `<span class="tool-spin" aria-hidden="true"></span>` : glyph}<span class="truncate">${escapeHtml(tab.title)}</span></button>` +
    `<button type="button" class="pane-tab-close" aria-label="Close ${escapeAttr(tab.title)}" title="Close">${icon("xmark", "h-2.5 w-2.5")}</button>` +
    `</div>`
  );
}

let tabsHtml = "";

function paintTabs(): void {
  const set = currentSet();
  const html = set ? set.tabs.map((t) => tabHtml(t, t.key === set.active)).join("") : "";
  if (html === tabsHtml && tabsEl.innerHTML !== "") return;
  tabsHtml = html;
  tabsEl.innerHTML = html;
}

// render paints the open session's tabs, keeping each tab's DOM.
function render(): void {
  sidePaneEl.hidden = !paneOpen;
  sidePaneBtn.setAttribute("aria-pressed", String(paneOpen));
  sidePaneBtn.classList.toggle("is-on", paneOpen);
  if (!paneOpen) {
    closeMenu();
    tabsEl.innerHTML = "";
    bodyEl.replaceChildren();
    return;
  }
  const set = currentSet();
  if (!set) {
    tabsEl.innerHTML = "";
    bodyEl.innerHTML = `<div class="side-empty"><span class="side-empty-ico">${icon("comments", "h-5 w-5")}</span><p class="side-empty-title">Side chat</p><p class="side-empty-text">Send a message first: side chats are about a conversation.</p></div>`;
    return;
  }
  if (set.tabs.length === 0) {
    const tab = newChatTab();
    set.tabs.push(tab);
    set.active = tab.key;
  }
  if (!activeTab(set)) set.active = set.tabs[0].key;
  paintTabs();
  bodyEl.replaceChildren(...set.tabs.map((t) => t.body));
  for (const tab of set.tabs) {
    tab.body.hidden = tab.key !== set.active;
  }
  const tab = activeTab(set)!;
  if (tab.kind === "agent") {
    paintHead(tab);
    void loadAgent(tab);
  }
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
  render();
}

function closeTab(key: string): void {
  const set = currentSet();
  if (!set) return;
  const at = set.tabs.findIndex((t) => t.key === key);
  if (at < 0) return;
  set.tabs.splice(at, 1);
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
    tab.body.remove();
  };
  for (const [id, set] of sets) {
    if (gone.has(id)) {
      set.tabs.forEach(drop);
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
  closeMenu();
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
    menuEl.innerHTML = `<p class="pane-menu-note">No conversation yet.</p>`;
    return;
  }
  menuEl.innerHTML = `<p class="pane-menu-note">Loading…</p>`;
  try {
    const chats = (await api.listSideChats(sessionId, state.cwd || null)) ?? [];
    if (state.sessionId !== sessionId || menuEl.hidden) return;
    menuEl.innerHTML = chats.length
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
    menuEl.innerHTML = `<p class="pane-menu-note side-error">${escapeHtml(errorText(error))}</p>`;
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
  if (!menuEl.querySelector(".pane-hist-item")) {
    menuEl.innerHTML = `<p class="pane-menu-note">No saved side chats.</p>`;
  }
  const set = currentSet();
  const open = set?.tabs.find((t) => t.kind === "chat" && t.chatId === chatId);
  if (open) closeTab(open.key);
}

export function initSidePane(): void {
  sidePaneBtn.addEventListener("click", () => (paneOpen ? closePane() : showPane()));
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
    if (menuEl.hidden) void openMenu();
    else closeMenu();
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
    const set = currentSet();
    if (!key || !set) return;
    if (target.closest(".pane-tab-close")) {
      closeTab(key);
    } else {
      set.active = key;
      render();
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
}
