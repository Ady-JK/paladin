// browserTabs.ts paints Browser mode's horizontal page strip.
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
