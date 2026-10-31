// sites.ts owns the agent/chat side view and the external chat pages.
// It embeds Gemini/ChatGPT-style pages and tracks the embedded view.

import * as api from "../core/api";
import { closeModal, openModal } from "../ui/anim";
import {
  agentTabEl,
  agentViewEl,
  chatEntriesEl,
  chatTabEl,
  externalFallbackEl,
  externalFallbackLink,
  externalHostEl,
  externalViewEl,
  headerTitleEl,
  inputEl,
  pageReloadBtnEl,
  sessionModalEl,
  settingsModalEl,
  confirmModalEl,
  sidebarEl,
  siteModalEl,
  siteNameEl,
  siteUrlEl,
  paintIcons,
  $,
} from "../core/dom";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import { hasBridge } from "../core/bridge";
import type { ChatEntry, SideView } from "../core/types";
import { toast } from "../ui/toast";
import { icon } from "../icons";
import { loadJson, loadText, saveJson, saveText } from "../core/storage";

const VIEW_KEY = "paladin.view";

// DEFAULT_SITES seeds a fresh profile and an existing profile that has
// never been seeded. They are ordinary entries: once written they live in
// the same list as user-added pages, so every one of them can be removed.
const DEFAULT_SITES: { name: string; url: string }[] = [
  { name: "Gemini", url: "https://gemini.google.com" },
  { name: "ChatGPT", url: "https://chatgpt.com" },
  { name: "Claude", url: "https://claude.ai" },
  { name: "DeepSeek", url: "https://chat.deepseek.com" },
];

const SITES_KEY = "paladin.sites";

// SEEDED_KEY records that the defaults were offered once. Without it,
// deleting a default would bring it back on the next launch.
const SEEDED_KEY = "paladin.sites.seeded";

function normalizeSiteUrl(raw: string): string {
  try {
    return new URL(raw).toString();
  } catch {
    return raw;
  }
}

function chatEntries(): ChatEntry[] {
  return state.customSites.map((s) => ({
    id: `site:${s.name}`,
    title: s.name,
    url: s.url,
  }));
}

function restoreSites(): void {
  const saved = loadJson(SITES_KEY);
  if (Array.isArray(saved)) {
    state.customSites = (saved as ({ name?: unknown; url?: unknown } | null)[])
      .filter((s) => typeof s?.name === "string" && typeof s?.url === "string")
      .map((s) => ({ name: (s!.name as string).trim(), url: (s!.url as string).trim() }))
      .filter((s) => s.name && s.url.toLowerCase().startsWith("https://"));
  }

  // Seed once. An existing profile keeps its own pages and gains any
  // default it is missing; a profile where the user deleted defaults keeps
  // them deleted because the flag is already set.
  if (loadText(SEEDED_KEY) === "1") {
    return;
  }

  const urls = new Set(state.customSites.map((s) => normalizeSiteUrl(s.url)));
  const names = new Set(state.customSites.map((s) => s.name.toLowerCase()));
  let added = false;
  for (const site of DEFAULT_SITES) {
    const url = normalizeSiteUrl(site.url);
    if (names.has(site.name.toLowerCase()) || urls.has(url)) {
      continue;
    }
    state.customSites.push({ name: site.name, url });
    names.add(site.name.toLowerCase());
    urls.add(url);
    added = true;
  }
  if (added) {
    persistSites();
  }
  saveText(SEEDED_KEY, "1");
}

function persistSites(): void {
  saveJson(SITES_KEY, state.customSites);
}

function paintSideTabs(): void {
  agentTabEl.classList.toggle("is-active", state.sideView === "agent");
  chatTabEl.classList.toggle("is-active", state.sideView === "chat");
  agentTabEl.setAttribute("aria-selected", String(state.sideView === "agent"));
  chatTabEl.setAttribute("aria-selected", String(state.sideView === "chat"));
}

// paintChatList paints the chat pages as browser-style tabs above the
// embedded page. × removes a page; the strip's + adds one.
function paintChatList(): void {
  let html = "";
  for (const entry of chatEntries()) {
    const picked = entry.id === state.chatPage;
    html += `
      <div class="chat-tab${picked ? " is-active" : ""}">
        <button
          type="button"
          role="tab"
          aria-selected="${picked}"
          data-pick="${escapeAttr(entry.id)}"
          class="chat-tab-pick"
          title="${escapeAttr(entry.title)} \u00b7 ${escapeAttr(hostOf(entry.url))}"
        >
          <span class="flex-shrink-0 text-slate-400">${icon("globe", "h-3.5 w-3.5")}</span>
          <span class="truncate">${escapeHtml(entry.title)}</span>
        </button>
        <button
          type="button"
          data-del="${escapeAttr(entry.id)}"
          class="chat-tab-close"
          title="Remove ${escapeAttr(entry.title)}"
          aria-label="Remove ${escapeAttr(entry.title)}"
        >
          ${icon("xmark", "h-3 w-3")}
        </button>
      </div>`;
  }

  if (chatEntries().length === 0) {
    html = `<p class="chat-tabs-empty">no pages yet</p>`;
  }

  chatEntriesEl.innerHTML = html;
  paintIcons(chatEntriesEl);
  const open = $("#pageOpenBtn");
  const page = currentChatPage();
  open.classList.toggle("hidden", !page);
  if (page) open.dataset.open = page.id;
  else delete open.dataset.open;
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function currentChatPage(): ChatEntry | null {
  return chatEntries().find((e) => e.id === state.chatPage) ?? null;
}

function persistView(): void {
  saveJson(VIEW_KEY, { view: state.sideView, page: state.chatPage });
}

// LEGACY_PAGE_IDS maps the old hard-coded page ids to the seeded site
// ids, so an existing session keeps the chat page it had selected.
const LEGACY_PAGE_IDS: Record<string, string> = {
  gemini: "site:Gemini",
  chatgpt: "site:ChatGPT",
};

function restoreView(): void {
  const saved = loadJson(VIEW_KEY) as { view?: unknown; page?: unknown } | null | undefined;
  if (saved?.view === "chat") {
    state.sideView = "chat";
  }
  if (typeof saved?.page === "string") {
    const page = LEGACY_PAGE_IDS[saved.page] ?? saved.page;
    if (chatEntries().some((e) => e.id === page)) {
      state.chatPage = page;
    }
  }
}

function anyModalOpen(): boolean {
  return (
    !settingsModalEl.classList.contains("hidden") ||
    !sessionModalEl.classList.contains("hidden") ||
    !confirmModalEl.classList.contains("hidden") ||
    !siteModalEl.classList.contains("hidden")
  );
}

function externalWanted(): boolean {
  return state.sideView === "chat" && state.chatPage !== "" && hasBridge() && !anyModalOpen();
}

function externalRect(): { x: number; y: number; width: number; height: number } {
  const rect = externalHostEl.getBoundingClientRect();
  return { x: rect.left, y: rect.top, width: rect.width, height: rect.height };
}

// setGuestVisible marks the shell as "a native page is on screen". The
// stylesheet uses it to freeze animations/blur so the guest renderer
// gets the whole frame budget while scrolling.
function setGuestVisible(on: boolean): void {
  document.body.classList.toggle("guest-visible", on);
}

export async function refreshExternalVisibility(): Promise<void> {
  const page = currentChatPage();

  if (!hasBridge()) {
    const on = state.sideView === "chat" && page !== null;
    setGuestVisible(false);
    externalFallbackEl.classList.toggle("hidden", !on);
    externalFallbackEl.classList.toggle("flex", on);
    return;
  }

  if (externalWanted() && page) {
    defusePageDrop();
    try {
      // ensure() navigates when the URL changed, so no second
      // navigate call: it would load the page twice.
      await api.showExternalPage(page.id, page.url, externalRect());
    } catch (error) {
      setGuestVisible(false);
      toast(String(error), "error");
      return;
    }
    setGuestVisible(true);
    state.pageUrlShown = page.url;
    shownView = page.id;
    return;
  }

  setGuestVisible(false);
  armPageDrop();
  try {
    await api.hideExternalPage();
  } catch (error) {
    console.warn(`hide page failed: ${String(error)}`);
  }
}

// paintSideVisibility shows the floating session drawer in agent mode
// only. Chat mode has no sidebar: its pages are tabs above the page.
export function paintSideVisibility(): void {
  const agent = state.sideView === "agent";
  const open = agent && !state.collapsed;
  sidebarEl.classList.toggle("hidden", !agent);
  sidebarEl.classList.toggle("is-closed", !open);
  sidebarEl.inert = !open;
  agentTabEl.setAttribute("aria-expanded", String(open));
  agentTabEl.title = !agent
    ? "Agent: the coding harness"
    : open
      ? "Agent: hide the sessions"
      : "Agent: show the sessions";
}

function setSideView(view: SideView): void {
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
  $("#paneLayoutBtn").classList.toggle("hidden", !agent);
  $("#sessionChangesBtn").classList.toggle("hidden", !agent || $("#sessionChangesCount").textContent === "");
  pageReloadBtnEl.classList.toggle("hidden", agent || !hasBridge());

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

// shownView is the page id behind the currently embedded view, so the
// idle drop timer destroys the view that was actually shown.
let shownView = "";

function armPageDrop(): void {
  window.clearTimeout(dropTimer);
  dropTimer = window.setTimeout(() => {
    if (externalWanted()) {
      return;
    }
    if (shownView) {
      const target = shownView;
      shownView = "";
      api.dropExternalPage(target).catch(() => undefined);
    }
  }, 60_000);
}

function defusePageDrop(): void {
  window.clearTimeout(dropTimer);
}

function selectChatEntry(id: string): void {
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

export function openNewSite(): void {
  siteNameEl.value = "";
  siteUrlEl.value = "";
  openModal(siteModalEl);
  void refreshExternalVisibility();
  siteNameEl.focus();
}

export function closeNewSite(): void {
  closeModal(siteModalEl);
  void refreshExternalVisibility();
}

function addSite(): void {
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
  const taken = chatEntries().some((e) => e.title.toLowerCase() === name.toLowerCase() || e.url === normalized);
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

function deleteSite(id: string): void {
  const entry = chatEntries().find((e) => e.id === id);
  if (!entry) {
    return;
  }

  state.customSites = state.customSites.filter((s) => `site:${s.name}` !== id);
  persistSites();
  // The removed page never comes back: destroy its view outright.
  api.dropExternalPage(id).catch(() => undefined);
  if (shownView === id) {
    shownView = "";
  }

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
function trackExternalFlight(): void {
  if (!hasBridge()) {
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
function reloadCurrentPage(): void {
  const page = currentChatPage();
  if (!page) {
    return;
  }
  api.reloadExternalPage(page.id).catch((error) => toast(String(error), "error"));
}

// initSites restores the side view and wires its controls.
export function initSites(): void {
  restoreSites();
  restoreView();

  // There is no separate sidebar button: Agent opens its drawer when
  // switching in, and toggles it when already there.
  agentTabEl.addEventListener("click", () => {
    if (state.sideView === "agent") {
      state.collapsed = !state.collapsed;
      paintSideVisibility();
      return;
    }
    state.collapsed = false;
    setSideView("agent");
  });
  chatTabEl.addEventListener("click", () => setSideView("chat"));

  chatEntriesEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const pick = target.closest<HTMLElement>("[data-pick]");
    if (pick?.dataset.pick) {
      selectChatEntry(pick.dataset.pick);
      return;
    }
    const del = target.closest<HTMLElement>("[data-del]");
    if (del?.dataset.del) {
      deleteSite(del.dataset.del);
    }
  });

  $("#chatAddBtn").addEventListener("click", openNewSite);
  $("#pageOpenBtn").addEventListener("click", () => {
    const page = currentChatPage();
    if (page) {
      void api.openExternalPage(page.url).catch((error) => toast(String(error), "error"));
    }
  });

  pageReloadBtnEl.addEventListener("click", () => {
    reloadCurrentPage();
  });

  if (!hasBridge()) {
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
      // Deduped by lastSentRect: chat mode has no sidebar, so this is
      // a no-op unless the outer window moved.
      trackExternalFlight();
    }, 120);
  };
  new ResizeObserver(nudgeExternal).observe(externalHostEl);
  window.addEventListener("resize", () => trackExternalFlight());

  setSideView(state.sideView);
}
