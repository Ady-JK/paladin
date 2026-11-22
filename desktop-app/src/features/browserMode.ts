// browserMode.ts is Browser mode's controller: it owns the page strip, the
// floating vertical drawer, and the page bodies when the shell (not the side
// pane) is showing them.
//
// The pages themselves live in sidePane.ts's one TabSet, so a page opened in
// the pane appears here and vice versa. A native page can render in only one
// surface at a time, so the pane's copy stays hidden while Browser mode owns
// the window: syncNativeSurfaces asks whoever is visible.
import { $ } from "../core/dom";
import { icon } from "../icons";
import { drawerHtml, stripHtml, type BrowserPage } from "./browserMode/tabs";

const tabsEl = $<HTMLElement>("#browserTabs");
const bodyEl = $<HTMLElement>("#browserBody");
const barEl = $<HTMLElement>("#browserBar");
const dockBtn = $<HTMLButtonElement>("#browserDockBtn");

let drawerEl: HTMLElement | null = null;
let pages: BrowserPage[] = [];
let stripCache = "";
let drawerCache = "";
let drawerOpen = false;

const emptyMarkup = (): string =>
  `<div class="browser-empty"><span class="browser-empty-ico">${icon("globe", "h-5 w-5")}</span>` +
  `<p class="browser-empty-title">No pages open</p>` +
  `<p class="browser-empty-text">Open an address or a saved site from the + in the bar.</p></div>`;

// mountDrawer creates the floating list once. It is a sibling of the body so
// it can sit over the page without being part of the page's layout.
function mountDrawer(): HTMLElement {
  if (!drawerEl) {
    drawerEl = document.createElement("div");
    drawerEl.id = "browserDrawer";
    drawerEl.className = "browser-drawer";
    drawerEl.hidden = true;
    drawerEl.setAttribute("role", "dialog");
    drawerEl.setAttribute("aria-label", "All tabs");
    bodyEl.parentElement!.append(drawerEl);
  }
  return drawerEl;
}

export function setBrowserPages(next: BrowserPage[], bodies: HTMLElement[]): void {
  pages = next;
  const strip = stripHtml(pages);
  if (strip !== stripCache) {
    stripCache = strip;
    tabsEl.innerHTML = strip;
  }
  // The page bodies are moved, not copied: they are the same DOM the pane uses.
  const activeBody = bodies.find((_, index) => next[index]?.active);
  if (activeBody) {
    if (activeBody.parentElement !== bodyEl) bodyEl.replaceChildren(activeBody);
  } else if (!drawerOpen) {
    bodyEl.replaceChildren();
    bodyEl.innerHTML = emptyMarkup();
  }
  if (drawerOpen) {
    const markup = drawerHtml(pages);
    if (markup !== drawerCache) {
      drawerCache = markup;
      mountDrawer().innerHTML = markup;
    }
  }
}

export function setBrowserDrawer(open: boolean): void {
  if (drawerOpen === open) return;
  drawerOpen = open;
  const drawer = mountDrawer();
  if (open) drawer.innerHTML = drawerHtml(pages);
  drawerCache = open ? drawerHtml(pages) : "";
  drawer.hidden = !open;
  drawer.inert = !open;
  // The page is hidden while the list is open: a native page cannot be dimmed
  // or clipped, so covering it means taking it off screen, not painting over it.
  bodyEl.classList.toggle("is-drawer-open", open);
  dockBtn.setAttribute("aria-expanded", String(open));
  window.dispatchEvent(new Event("paladin:browser-drawer"));
}

export function isBrowserDrawerOpen(): boolean {
  return drawerOpen;
}

// browserDrawerElement is exposed so the shell can position/aria-manage it.
export function browserDrawerElement(): HTMLElement {
  return mountDrawer();
}

export function browserBarElement(): HTMLElement {
  return barEl;
}
