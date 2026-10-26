// sites.ts owns the agent/chat side view and the external chat pages.
// It embeds Gemini/ChatGPT-style pages and tracks the embedded view.

import * as api from "../core/api";
import {
  agentTabEl,
  agentViewEl,
  chatEntriesEl,
  chatListEl,
  chatTabEl,
  daemonWrapEl,
  externalFallbackEl,
  externalFallbackLink,
  externalHostEl,
  externalViewEl,
  headerTitleEl,
  inputEl,
  newSessionBtnLabelEl,
  pageReloadBtnEl,
  searchWrapEl,
  sessionListEl,
  sessionModalEl,
  settingsModalEl,
  confirmModalEl,
  siteModalEl,
  siteNameEl,
  siteUrlEl,
  tabsWrapEl,
  workspaceWrapEl,
  paintIcons,
  $,
} from "../core/dom";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import { hasTauri } from "../core/tauri";
import type { ChatEntry, SideView } from "../core/types";
import { toast } from "../ui/toast";
import { icon } from "../icons";

// VIEW_KEY is a const in this module.
// It supports the desktop app frontend.
const VIEW_KEY = "paladin.view";

// BUILT_IN_PAGES is a const in this module.
// It supports the desktop app frontend.
const BUILT_IN_PAGES: ChatEntry[] = [
  { id: "gemini", title: "Gemini", url: "https://gemini.google.com", builtin: true },
  { id: "chatgpt", title: "ChatGPT", url: "https://chatgpt.com", builtin: true },
];

// SITES_KEY is a const in this module.
// It supports the desktop app frontend.
const SITES_KEY = "paladin.sites";

// chatEntries is a function in this module.
// It supports the desktop app frontend.
function chatEntries(): ChatEntry[] {
  return [
    ...BUILT_IN_PAGES,
    ...state.customSites.map((s) => ({
      id: `site:${s.name}`,
      title: s.name,
      url: s.url,
      builtin: false,
    })),
  ];
}

// restoreSites is a function in this module.
// It supports the desktop app frontend.
function restoreSites(): void {
  try {
    const saved = JSON.parse(localStorage.getItem(SITES_KEY) ?? "null") as {
      name?: string;
      url?: string;
    }[] | null;
    if (Array.isArray(saved)) {
      state.customSites = saved
        .filter((s) => typeof s?.name === "string" && typeof s?.url === "string")
        .map((s) => ({ name: s.name!.trim(), url: s.url!.trim() }))
        .filter((s) => s.name && s.url.toLowerCase().startsWith("https://"));
    }
  } catch {
  }
}

// persistSites is a function in this module.
// It supports the desktop app frontend.
function persistSites(): void {
  try {
    localStorage.setItem(SITES_KEY, JSON.stringify(state.customSites));
  } catch {
  }
}

// paintSideTabs is a function in this module.
// It supports the desktop app frontend.
function paintSideTabs(): void {
  const active =
    "flex items-center justify-center gap-1.5 rounded bg-white px-2 py-1.5 text-xs font-medium text-slate-900 dark:bg-slate-800 dark:text-slate-100";
  const idle =
    "flex items-center justify-center gap-1.5 rounded px-2 py-1.5 text-xs font-medium text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200";
  agentTabEl.className = state.sideView === "agent" ? active : idle;
  chatTabEl.className = state.sideView === "chat" ? active : idle;
  agentTabEl.setAttribute("aria-selected", String(state.sideView === "agent"));
  chatTabEl.setAttribute("aria-selected", String(state.sideView === "chat"));
}

// paintChatList is a function in this module.
// It supports the desktop app frontend.
export function paintChatList(): void {
  let html = "";
  for (const entry of chatEntries()) {
    const picked = entry.id === state.chatPage;
    html += `
      <div class="pick-row flex items-center gap-1">
        <button
          data-pick="${escapeAttr(entry.id)}"
          class="min-w-0 flex-1 rounded-md border p-1.5 text-left ${
            picked
              ? "border-indigo-200 bg-indigo-50/80 dark:border-indigo-800 dark:bg-indigo-950/40"
              : "border-transparent hover:bg-slate-100 dark:hover:bg-slate-800/60"
          }"
          title="${escapeAttr(entry.url)}"
        >
          <span class="flex items-center gap-2">
            <span class="flex-shrink-0 text-accent-500">${icon("globe", "h-3.5 w-3.5")}</span>
            <span class="min-w-0 flex-1">
              <span class="block truncate font-medium text-slate-700 dark:text-slate-300">${escapeHtml(entry.title)}</span>
              <span class="block truncate text-[10px] text-slate-400">${escapeHtml(hostOf(entry.url))}</span>
            </span>
          </span>
        </button>
        <button
          data-open="${escapeAttr(entry.id)}"
          class="flex-shrink-0 rounded-md p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800 dark:hover:text-slate-200"
          title="Open ${escapeAttr(entry.title)} in the system browser"
        >
          ${icon("external")}
        </button>
        ${
          entry.builtin
            ? ""
            : `<button
          data-del="${escapeAttr(entry.id)}"
          class="flex-shrink-0 rounded-md p-1.5 text-slate-400 hover:bg-agent-rose/10 hover:text-agent-rose"
          title="Remove ${escapeAttr(entry.title)}"
        >
          ${icon("trash")}
        </button>`
        }
      </div>`;
  }

  if (chatEntries().length === 0) {
    html = `<p class="px-2 py-1 text-slate-400">no pages yet</p>`;
  }

  chatEntriesEl.innerHTML = html;
  paintIcons(chatEntriesEl);
}

// hostOf is a function in this module.
// It supports the desktop app frontend.
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

// currentChatPage is a function in this module.
// It supports the desktop app frontend.
export function currentChatPage(): ChatEntry | null {
  return chatEntries().find((e) => e.id === state.chatPage) ?? null;
}

// persistView is a function in this module.
// It supports the desktop app frontend.
function persistView(): void {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify({ view: state.sideView, page: state.chatPage }));
  } catch {
  }
}

// restoreView is a function in this module.
// It supports the desktop app frontend.
function restoreView(): void {
  try {
    const saved = JSON.parse(localStorage.getItem(VIEW_KEY) ?? "null") as {
      view?: SideView;
      page?: string;
    } | null;
    if (saved?.view === "chat") {
      state.sideView = "chat";
    }
    if (typeof saved?.page === "string") {
      state.chatPage = saved.page;
    }
  } catch {
  }
}

// anyModalOpen is a function in this module.
// It supports the desktop app frontend.
function anyModalOpen(): boolean {
  return (
    !settingsModalEl.classList.contains("hidden") ||
    !sessionModalEl.classList.contains("hidden") ||
    !confirmModalEl.classList.contains("hidden") ||
    !siteModalEl.classList.contains("hidden")
  );
}

// externalWanted is a function in this module.
// It supports the desktop app frontend.
function externalWanted(): boolean {
  return state.sideView === "chat" && state.chatPage !== "" && hasTauri() && !anyModalOpen();
}

// externalRect is a function in this module.
// It supports the desktop app frontend.
function externalRect(): { x: number; y: number; width: number; height: number } {
  const rect = externalHostEl.getBoundingClientRect();
  return { x: rect.left, y: rect.top, width: rect.width, height: rect.height };
}

export async function refreshExternalVisibility(): Promise<void> {
  const page = currentChatPage();

  if (!hasTauri()) {
    const on = state.sideView === "chat" && page !== null;
    externalFallbackEl.classList.toggle("hidden", !on);
    externalFallbackEl.classList.toggle("flex", on);
    return;
  }

  if (externalWanted() && page) {
    defusePageDrop();
    try {
      await api.showExternalPage(page.url, externalRect());
    } catch (error) {
      toast(String(error), "error");
      return;
    }
    if (state.pageUrlShown && state.pageUrlShown !== page.url) {
      try {
        await api.navigateExternalPage(page.url);
      } catch (error) {
        toast(String(error), "error");
      }
    }
    state.pageUrlShown = page.url;
    return;
  }

  armPageDrop();
  try {
    await api.hideExternalPage();
  } catch {
  }
}

// paintSideVisibility is a function in this module.
// It supports the desktop app frontend.
export function paintSideVisibility(): void {
  const agent = state.sideView === "agent";
  workspaceWrapEl.classList.toggle("hidden", state.collapsed);
  tabsWrapEl.classList.toggle("hidden", state.collapsed);
  searchWrapEl.classList.toggle("hidden", state.collapsed || !agent);
  sessionListEl.classList.toggle("hidden", state.collapsed || !agent);
  chatListEl.classList.toggle("hidden", state.collapsed || agent);
  daemonWrapEl.classList.toggle("hidden", state.collapsed);
}

// setSideView is a function in this module.
// It supports the desktop app frontend.
export function setSideView(view: SideView): void {
  state.sideView = view;
  persistView();
  paintSideTabs();

  const agent = view === "agent";
  paintSideVisibility();

  agentViewEl.classList.toggle("hidden", !agent);
  agentViewEl.classList.toggle("flex", agent);
  externalViewEl.classList.toggle("hidden", agent);
  externalViewEl.classList.toggle("flex", !agent);

  headerTitleEl.textContent = agent ? "Agent Chat" : (currentChatPage()?.title ?? "Chat");
  pageReloadBtnEl.classList.toggle("hidden", agent || !hasTauri());

  newSessionBtnLabelEl.textContent = agent ? "New Session" : "New Site";
  $("#newSessionBtn").title = agent ? "Start a new session" : "Add another chat page";
  if (!agent) {
    paintChatList();
  } else {
    inputEl.focus();
  }

  void refreshExternalVisibility();
}

// dropTimer destroys the hidden page after a minute idle so its RAM
// (JS heap, decoded images, GPU buffers) goes back to zero. Logins live
// in the shared data store on disk, so the next show signs straight in.
let dropTimer = 0;

function armPageDrop(): void {
  window.clearTimeout(dropTimer);
  dropTimer = window.setTimeout(() => {
    if (externalWanted()) {
      return;
    }
    api.dropExternalPage().catch(() => undefined);
  }, 60_000);
}

function defusePageDrop(): void {
  window.clearTimeout(dropTimer);
}

// selectChatEntry is a function in this module.
// It supports the desktop app frontend.
export function selectChatEntry(id: string): void {
  const picked = chatEntries().find((e) => e.id === id);
  if (!picked) {
    return;
  }
  state.chatPage = picked.id;
  persistView();
  paintChatList();
  externalFallbackLink.href = picked.url;
  externalFallbackLink.textContent = `Open ${picked.title}`;
  headerTitleEl.textContent = picked.title;
  void refreshExternalVisibility();
  trackExternalFlight();
}

// openNewSite is a function in this module.
// It supports the desktop app frontend.
export function openNewSite(): void {
  siteNameEl.value = "";
  siteUrlEl.value = "";
  siteModalEl.classList.remove("hidden");
  siteModalEl.classList.add("flex");
  void refreshExternalVisibility();
  siteNameEl.focus();
}

// closeNewSite is a function in this module.
// It supports the desktop app frontend.
export function closeNewSite(): void {
  siteModalEl.classList.add("hidden");
  siteModalEl.classList.remove("flex");
  void refreshExternalVisibility();
}

// addSite is a function in this module.
// It supports the desktop app frontend.
export function addSite(): void {
  const name = siteNameEl.value.trim();
  let raw = siteUrlEl.value.trim();

  if (!name) {
    toast("Give the site a name.", "error");
    siteNameEl.focus();
    return;
  }

  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) {
    raw = `https://${raw}`;
  }

  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    toast("That is not a valid URL.", "error");
    siteUrlEl.focus();
    return;
  }

  if (url.protocol !== "https:") {
    toast("Only secure https addresses can be added.", "error");
    siteUrlEl.focus();
    return;
  }

  const normalized = url.toString();
  const taken = chatEntries().some(
    (e) => e.title.toLowerCase() === name.toLowerCase() || e.url === normalized,
  );
  if (taken) {
    toast("A page with that name or URL is already listed.", "error");
    return;
  }

  state.customSites.push({ name, url: normalized });
  persistSites();
  closeNewSite();
  paintChatList();
  selectChatEntry(`site:${name}`);
  toast(`Added ${name}`);
}

// deleteSite is a function in this module.
// It supports the desktop app frontend.
export function deleteSite(id: string): void {
  const entry = chatEntries().find((e) => e.id === id);
  if (!entry || entry.builtin) {
    return;
  }

  state.customSites = state.customSites.filter((s) => `site:${s.name}` !== id);
  persistSites();

  if (state.chatPage === id) {
    const next = chatEntries()[0];
    state.chatPage = "";
    state.pageUrlShown = "";
    if (next) {
      selectChatEntry(next.id);
      toast(`Removed ${entry.title}, showing ${next.title} instead`);
      return;
    }
    persistView();
    headerTitleEl.textContent = "Chat";
    void refreshExternalVisibility();
  }

  paintChatList();
  toast(`Removed ${entry.title}`);
}

// trackExternalFlight re-syncs the native page rect once, on the next
// frame. Layout changes are instant now (no sidebar animation), so a
// 600ms/60fps IPC storm is pure waste: one move per settle is enough.
export function trackExternalFlight(): void {
  if (!hasTauri()) {
    return;
  }
  requestAnimationFrame(() => {
    const page = currentChatPage();
    if (page && externalWanted()) {
      const rect = externalRect();
      const key = `${rect.x},${rect.y},${rect.width},${rect.height}`;
      if (key !== state.lastSentRect) {
        state.lastSentRect = key;
        api.moveExternalPage({ ...rect }).catch(() => undefined);
      }
    }
  });
}

// reloadCurrentPage reloads the embedded chat page.
// It supports the desktop app frontend.
export function reloadCurrentPage(): void {
  if (!currentChatPage()) {
    return;
  }
  api.reloadExternalPage().catch((error) =>
    toast(String(error), "error"),
  );
}

// initSites restores the side view and wires its controls.
// It supports the desktop app frontend.
export function initSites(): void {
  restoreSites();
  restoreView();

  agentTabEl.addEventListener("click", () => setSideView("agent"));
  chatTabEl.addEventListener("click", () => setSideView("chat"));

  chatEntriesEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const pick = target.closest<HTMLElement>("[data-pick]");
    if (pick?.dataset.pick) {
      selectChatEntry(pick.dataset.pick);
      return;
    }
    const open = target.closest<HTMLElement>("[data-open]");
    if (open?.dataset.open) {
      const entry = chatEntries().find((e) => e.id === open.dataset.open);
      if (entry) {
        void api.openExternalPage(entry.url).catch((error) => toast(String(error), "error"));
      }
      return;
    }
    const del = target.closest<HTMLElement>("[data-del]");
    if (del?.dataset.del) {
      deleteSite(del.dataset.del);
    }
  });

  pageReloadBtnEl.addEventListener("click", () => {
    reloadCurrentPage();
  });

  if (!hasTauri()) {
    pageReloadBtnEl.classList.add("hidden");
  }

  $("#siteCloseBtn").addEventListener("click", closeNewSite);
  $("#siteCancelBtn").addEventListener("click", closeNewSite);
  $("#siteAddBtn").addEventListener("click", addSite);
  siteModalEl.addEventListener("click", (event) => {
    if (event.target === siteModalEl) {
      closeNewSite();
    }
  });
  for (const field of [siteNameEl, siteUrlEl]) {
    field.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        addSite();
      }
    });
  }

  let externalMoveTimer = 0;
  const nudgeExternal = (): void => {
    window.clearTimeout(externalMoveTimer);
    externalMoveTimer = window.setTimeout(() => {
      if (currentChatPage() && externalWanted()) {
        api.moveExternalPage({ ...externalRect() }).catch(
          () => undefined,
        );
      }
    }, 120);
  };
  new ResizeObserver(nudgeExternal).observe(externalHostEl);
  window.addEventListener("resize", () => trackExternalFlight());

  setSideView(state.sideView);
}
