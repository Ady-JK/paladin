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
import { closeWebTabsForUrl, openWebTab, STANDALONE_PANE_SESSION } from "./sidePane";

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

// menuAnchor is whatever last opened the menu. Browser mode's + is the visible
// one; the header anchor stays as a fallback for the keyboard shortcut, which
// can fire in a mode whose bar is hidden.
let menuAnchor: HTMLElement | null = null;

// The menu is positioned against the viewport, from the rectangle of whatever
// opened it. It cannot be positioned against its container: it hangs off the
// card now (Browser mode hides the header it used to live in), and a
// percentage offset inside the card means "below the whole card".
function positionMenu(anchor: HTMLElement | null = menuAnchor ?? button): void {
  if (!menu || !anchor) return;
  const rect = anchor.getBoundingClientRect();
  const width = menu.offsetWidth || 320;
  // A hidden anchor has no rectangle (the keyboard shortcut opens the menu in
  // Agent mode, where the globe is not shown). Fall back to the header, so the
  // menu appears under the composer bar rather than in the window's corner.
  const header = document.querySelector<HTMLElement>("#titleBar");
  const fallback = header && header.getBoundingClientRect().width > 0 ? header.getBoundingClientRect() : null;
  const left = rect.width > 0 ? rect.left : (fallback?.left ?? 12) + 8;
  const maxLeft = document.documentElement.clientWidth - width - 8;
  menu.style.left = `${Math.max(8, Math.min(left, maxLeft))}px`;
  menu.style.top = `${Math.round((rect.width > 0 ? rect.bottom : (fallback?.bottom ?? 0)) + 6)}px`;
}

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

function browserSession(): string {
  return state.sessionId || STANDALONE_PANE_SESSION;
}

// openWebMenuFromKey pops the personal-browser menu open, the same menu
// the toolbar's Browser button shows, so a shortcut can reach saved sites
// and the address field.
export function openWebMenuFromKey(anchor?: HTMLElement | null): void {
  if (!button) buildMenu();
  if (!button || !menu) return;
  menuAnchor = anchor ?? document.querySelector<HTMLElement>("#browserNewTab") ?? button;
  if (!isMenuOpen(menu)) toggleMenu(menu);
  button.setAttribute("aria-expanded", "true");
  positionMenu();
  paintMenu();
  menu.querySelector<HTMLElement>("[data-web-address]")?.focus();
}

function openSite(url: string, title: string): void {
  const sessionId = browserSession();
  closeWebMenu();
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
  // The trigger is the + in Browser mode's own bar, not a header button:
  // an entry is created here only so the menu has an anchor to position
  // against and a place to report its expanded state.
  button = document.createElement("button");
  button.id = "paneWebBtn";
  button.type = "button";
  button.className = "pane-btn";
  button.hidden = true;
  button.title = "Browser: bookmarks and any address";
  button.setAttribute("aria-label", "Browser");
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");
  button.innerHTML = icon("globe", "h-4 w-4");

  menu = document.createElement("div");
  menu.id = "webMenu";
  menu.className = "pane-menu web-menu";
  menu.setAttribute("role", "menu");
  menu.classList.add("hidden");
  menu.innerHTML =
    `<div class="web-menu-head"><span>Personal browser</span><span class="web-menu-hint">Bookmarks and websites</span></div>` +
    `<div class="web-menu-list"></div>` +
    `<button type="button" role="menuitem" class="web-menu-action" data-web-address>${icon("plus", "h-3.5 w-3.5")}<span>Open an address…</span></button>` +
    `<button type="button" role="menuitem" class="web-menu-action" data-web-new>${icon("plus", "h-3.5 w-3.5")}<span>New saved site…</span></button>`;
  listEl = menu.querySelector<HTMLElement>(".web-menu-list");
  actions.append(button);
  // Hidden rather than removed: positionMenu needs an anchor rectangle, and
  // the browser bar reports its own expanded state through this element.
  button.style.display = "none";
  // The menu itself hangs off the card, not the header: Browser mode hides the
  // header, and a menu inside it would be hidden with it.
  (document.querySelector("#mainCard") ?? actions).append(menu);

  window.addEventListener("resize", () => {
    if (menu && isMenuOpen(menu)) positionMenu();
  });

  button.addEventListener("click", (event) => {
    event.stopPropagation();
    if (!menu) return;
    const open = toggleMenu(menu);
    button?.setAttribute("aria-expanded", String(open));
    if (open) {
      positionMenu();
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
      const sessionId = browserSession();
      closeWebMenu();
      openWebTab(sessionId, "", "New tab");
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
  openWebTab(browserSession(), normalized, name);
  toast(`Added ${name}`);
}

// initWeb restores the saved sites, builds the Web menu and wires the
// add-site dialog.
export function initWeb(): void {
  restoreSites();
  buildMenu();
  // Browser mode's + asks for the same menu the header used to own.
  window.addEventListener("paladin:browser-menu", () => openWebMenuFromKey());

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
