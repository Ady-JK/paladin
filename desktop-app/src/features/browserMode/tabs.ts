// browserTabs.ts paints Browser mode: the horizontal page strip in its own bar
// plus the floating vertical list, which is the same pages drawn as a drawer
// over them. The list floats so the page keeps its full width — a native page
// cannot be clipped, so the drawer covers it (the page is hidden while the
// drawer is open) rather than narrowing it.
//
// Both views read one array, so there is nothing to keep in sync.
import { escapeHtml } from "../../core/format";
import { icon } from "../../icons";

export type BrowserPage = { key: string; title: string; url: string; active: boolean };

const host = (url: string): string => {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
};

// stripHtml returns the horizontal bar's tabs, one per page.
export function stripHtml(pages: BrowserPage[]): string {
  return pages
    .map(
      (page) =>
        `<div class="browser-tab${page.active ? " is-active" : ""}" data-page="${escapeHtml(page.key)}" role="tab" aria-selected="${page.active}">` +
        `<span class="browser-tab-title">${escapeHtml(page.title || host(page.url) || "New tab")}</span>` +
        `<button type="button" class="browser-tab-close" data-page-close="${escapeHtml(page.key)}" aria-label="Close tab">${icon("xmark", "h-3 w-3")}</button>` +
        `</div>`,
    )
    .join("");
}

// drawerHtml returns the floating vertical list: full titles, the host under
// each, and a new-tab row at the end.
export function drawerHtml(pages: BrowserPage[]): string {
  const rows = pages
    .map(
      (page) =>
        `<button type="button" class="browser-drawer-row${page.active ? " is-active" : ""}" data-page="${escapeHtml(page.key)}">` +
        `<span class="browser-drawer-title">${escapeHtml(page.title || host(page.url) || "New tab")}</span>` +
        `<span class="browser-drawer-host">${escapeHtml(host(page.url) || "about:blank")}</span>` +
        `</button>`,
    )
    .join("");
  return (
    `<div class="browser-drawer-head"><strong>Tabs</strong>` +
    `<button type="button" class="browser-drawer-close" data-drawer-close aria-label="Hide tabs">${icon("xmark", "h-3.5 w-3.5")}</button></div>` +
    `<div class="browser-drawer-list">${rows || '<p class="browser-drawer-empty">No pages open.</p>'}</div>` +
    `<button type="button" class="browser-drawer-new" data-drawer-new>${icon("plus", "h-3.5 w-3.5")}<span>New tab</span></button>`
  );
}
