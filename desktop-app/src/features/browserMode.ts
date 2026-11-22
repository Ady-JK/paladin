// browserMode.ts is Browser mode's controller: it owns the page strip and the
// page bodies when the shell (not the side pane) is showing them.
//
// The pages themselves live in sidePane.ts's one TabSet, so a page opened in
// the pane appears here and vice versa. A native page can render in only one
// surface at a time, so the pane's copy stays hidden while Browser mode owns
// the window: syncNativeSurfaces asks whoever is visible.
import { $ } from "../core/dom";
import { icon } from "../icons";
import { stripHtml, type BrowserPage } from "./browserMode/tabs";

const tabsEl = $<HTMLElement>("#browserTabs");
const bodyEl = $<HTMLElement>("#browserBody");
const barEl = $<HTMLElement>("#browserBar");

let pages: BrowserPage[] = [];
let stripCache = "";

const emptyMarkup = (): string =>
  `<div class="browser-empty"><span class="browser-empty-ico">${icon("globe", "h-5 w-5")}</span>` +
  `<p class="browser-empty-title">No pages open</p>` +
  `<p class="browser-empty-text">Open an address or a saved site from the + in the bar.</p></div>`;

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
  } else {
    bodyEl.replaceChildren();
    bodyEl.innerHTML = emptyMarkup();
  }
}

export function browserBarElement(): HTMLElement {
  return barEl;
}
