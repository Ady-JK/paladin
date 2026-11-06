// web.ts owns the saved sites behind the integrated browser and the Web
// menu. The tabs themselves live in the side pane (split beside the
// agent, or full width in the tabs layout); this module only stores the
// list, edits it and asks the pane to open a site.

import { $, siteModalEl, siteNameEl, siteUrlEl } from "../core/dom";
import { escapeAttr, escapeHtml } from "../core/format";
import { state } from "../core/state";
import { loadJson, loadText, saveJson, saveText } from "../core/storage";
import type { WebSite } from "../core/types";
import { icon } from "../icons";
import { closeMenu, closeModal, isMenuOpen, openModal, toggleMenu } from "../ui/anim";
import { toast } from "../ui/toast";
import { closeWebTabsForUrl, openWebTab } from "./sidePane";

const SITES_KEY = "paladin.sites";
// SEEDED_KEY records that the defaults were offered once. Without it,
// deleting a default would bring it back on the next launch.
const SEEDED_KEY = "paladin.sites.seeded";

// DEFAULT_SITES seeds a fresh profile and an existing profile that has
// never been seeded. They are ordinary entries: once written they live in
// the same list as user-added pages, so every one of them can be removed.
const DEFAULT_SITES: WebSite[] = [
  { name: "Gemini", url: "https://gemini.google.com/" },
  { name: "ChatGPT", url: "https://chatgpt.com/" },
  { name: "Claude", url: "https://claude.ai/" },
  { name: "DeepSeek", url: "https://chat.deepseek.com/" },
];

let button: HTMLButtonElement | null = null;
let menu: HTMLElement | null = null;
let listEl: HTMLElement | null = null;

function normalizeSiteUrl(raw: string): string {
  try {
    return new URL(raw).toString();
  } catch {
    return raw;
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
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
    if (names.has(site.name.toLowerCase()) || urls.has(url)) continue;
    state.customSites.push({ name: site.name, url });
    names.add(site.name.toLowerCase());
    urls.add(url);
    added = true;
  }
  if (added) persistSites();
  saveText(SEEDED_KEY, "1");
}

function persistSites(): void {
  saveJson(SITES_KEY, state.customSites);
}

function rowsHtml(): string {
  if (!state.customSites.length) {
    return `<p class="pane-menu-note">No saved sites. Add one, or open an address.</p>`;
  }
  return state.customSites
    .map(
      (site) =>
        `<div class="web-site-row">` +
        `<button type="button" role="menuitem" class="web-site-open" data-web-open="${escapeAttr(site.url)}" data-web-title="${escapeAttr(site.name)}" title="${escapeAttr(site.name)} \u00b7 ${escapeAttr(hostOf(site.url))}">` +
        `<span class="web-site-ico">${icon("globe", "h-3.5 w-3.5")}</span>` +
        `<span class="web-site-text"><span class="web-site-name">${escapeHtml(site.name)}</span><span class="web-site-host">${escapeHtml(hostOf(site.url))}</span></span>` +
        `</button>` +
        `<button type="button" class="web-site-del" data-web-del="${escapeAttr(site.url)}" title="Remove ${escapeAttr(site.name)}" aria-label="Remove ${escapeAttr(site.name)}">${icon("xmark", "h-3 w-3")}</button>` +
        `</div>`,
    )
    .join("");
}

function paintMenu(): void {
  if (!listEl) return;
  listEl.innerHTML = rowsHtml();
}

function sessionOrToast(): string {
  if (state.sessionId) return state.sessionId;
  toast("Send a message first: the browser lives beside a conversation.", "error");
  return "";
}

function openSite(url: string, title: string): void {
  const sessionId = sessionOrToast();
  closeWebMenu();
  if (!sessionId) return;
  openWebTab(sessionId, url, title);
}

function closeWebMenu(): void {
  if (!menu) return;
  if (isMenuOpen(menu)) closeMenu(menu);
  button?.setAttribute("aria-expanded", "false");
}

function removeSite(url: string): void {
  const site = state.customSites.find((s) => s.url === url);
  if (!site) return;
  state.customSites = state.customSites.filter((s) => s.url !== url);
  persistSites();
  closeWebTabsForUrl(url);
  paintMenu();
  toast(`Removed ${site.name}`);
}

function buildMenu(): void {
  const actions = document.querySelector<HTMLElement>("#paneActions");
  if (!actions || button) return;
  button = document.createElement("button");
  button.id = "paneWebBtn";
  button.type = "button";
  button.className = "pane-btn";
  button.title = "Web: saved sites and any address";
  button.setAttribute("aria-label", "Web browser");
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");
  button.innerHTML = icon("rl-chat", "h-4 w-4");

  menu = document.createElement("div");
  menu.id = "webMenu";
  menu.className = "pane-menu web-menu";
  menu.setAttribute("role", "menu");
  menu.classList.add("hidden");
  menu.innerHTML =
    `<div class="web-menu-head"><span>Web</span><span class="web-menu-hint">Gemini, ChatGPT and any site</span></div>` +
    `<div class="web-menu-list"></div>` +
    `<button type="button" role="menuitem" class="web-menu-action" data-web-address>${icon("plus", "h-3.5 w-3.5")}<span>Open an address…</span></button>` +
    `<button type="button" role="menuitem" class="web-menu-action" data-web-new>${icon("plus", "h-3.5 w-3.5")}<span>New saved site…</span></button>`;
  listEl = menu.querySelector<HTMLElement>(".web-menu-list");
  actions.insertBefore(button, actions.querySelector("#paneBrowserBtn") ?? null);
  actions.append(menu);

  button.addEventListener("click", (event) => {
    event.stopPropagation();
    if (!menu) return;
    const open = toggleMenu(menu);
    button?.setAttribute("aria-expanded", String(open));
    if (open) {
      menu.style.left = `${button?.offsetLeft ?? 0}px`;
      paintMenu();
    }
  });
  menu.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const open = target.closest<HTMLElement>("[data-web-open]");
    if (open?.dataset.webOpen) {
      openSite(open.dataset.webOpen, open.dataset.webTitle ?? "");
      return;
    }
    const del = target.closest<HTMLElement>("[data-web-del]");
    if (del?.dataset.webDel) {
      removeSite(del.dataset.webDel);
      return;
    }
    if (target.closest("[data-web-address]")) {
      const sessionId = sessionOrToast();
      closeWebMenu();
      if (sessionId) openWebTab(sessionId, "", "New tab");
      return;
    }
    if (target.closest("[data-web-new]")) {
      closeWebMenu();
      openNewSite();
    }
  });
  document.addEventListener("mousedown", (event) => {
    if (!menu || !isMenuOpen(menu)) return;
    const target = event.target as Node;
    if (!menu.contains(target) && !button?.contains(target)) closeWebMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeWebMenu();
  });
  paintMenu();
}

// openNewSite opens the add-site dialog; the Escape handler in main calls
// closeNewSite.
function openNewSite(): void {
  siteNameEl.value = "";
  siteUrlEl.value = "";
  openModal(siteModalEl);
  siteNameEl.focus();
}

export function closeNewSite(): void {
  closeModal(siteModalEl);
}

function addSite(): void {
  const name = siteNameEl.value.trim();
  let raw = siteUrlEl.value.trim();

  if (!name) {
    toast("Give the site a name.", "error");
    siteNameEl.focus();
    return;
  }
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) raw = `https://${raw}`;

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
  const taken = state.customSites.some(
    (s) => s.name.toLowerCase() === name.toLowerCase() || normalizeSiteUrl(s.url) === normalized,
  );
  if (taken) {
    toast("A site with that name or URL is already listed.", "error");
    return;
  }

  state.customSites.push({ name, url: normalized });
  persistSites();
  closeNewSite();
  paintMenu();
  if (state.sessionId) openWebTab(state.sessionId, normalized, name);
  toast(`Added ${name}`);
}

// initWeb restores the saved sites, builds the Web menu and wires the
// add-site dialog.
export function initWeb(): void {
  restoreSites();
  buildMenu();

  $("#siteCloseBtn").addEventListener("click", closeNewSite);
  $("#siteCancelBtn").addEventListener("click", closeNewSite);
  $("#siteAddBtn").addEventListener("click", addSite);
  siteModalEl.addEventListener("click", (event) => {
    if (event.target === siteModalEl) closeNewSite();
  });
  for (const field of [siteNameEl, siteUrlEl]) {
    field.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        addSite();
      }
    });
  }
}
