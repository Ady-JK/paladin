import * as api from "../../core/api";
import { escapeAttr, escapeHtml } from "../../core/format";
import { state } from "../../core/state";

type SearchHit = { path: string; line: number; text: string };

export function initIDESearch(enabled: () => boolean, openAt: (path: string, line: number) => void): void {
  const dialog = document.createElement("div");
  dialog.className = "ide-palette-backdrop";
  dialog.hidden = true;
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-label", "Search workspace");
  dialog.setAttribute("aria-modal", "true");
  dialog.innerHTML = `<div class="ide-palette ide-search"><form><input aria-label="Search text" placeholder="Search workspace text" maxlength="512" autocomplete="off"><button type="submit">Search</button></form><div class="ide-search-status" role="status"></div><div class="ide-palette-results"></div></div>`;
  document.body.append(dialog);
  const form = dialog.querySelector("form")!;
  const input = dialog.querySelector("input")!;
  const status = dialog.querySelector<HTMLElement>(".ide-search-status")!;
  const results = dialog.querySelector<HTMLElement>(".ide-palette-results")!;
  let hits: SearchHit[] = [];
  let resultRoot = "";
  let request = 0;
  let prior: HTMLElement | null = null;
  const close = (): void => {
    request++;
    dialog.hidden = true;
    prior?.focus({ preventScroll: true });
  };
  const search = async (): Promise<void> => {
    const query = input.value;
    const root = state.cwd;
    const id = ++request;
    hits = [];
    results.replaceChildren();
    if (!query.trim()) {
      status.textContent = "Enter text to search.";
      return;
    }
    status.textContent = "Searching…";
    try {
      const response = await api.ideSearch(root, query);
      if (id !== request || dialog.hidden || state.cwd !== root) return;
      hits = response.hits;
      resultRoot = root;
      status.textContent = `${hits.length} ${hits.length === 1 ? "match" : "matches"}${response.truncated ? " · results truncated" : ""}`;
      results.innerHTML = hits
        .map((hit, i) => {
          const path = hit.path.startsWith(`${root}/`) ? hit.path.slice(root.length + 1) : hit.path;
          return `<button type="button" data-search-hit="${i}" title="${escapeAttr(hit.path)}"><span><strong>${escapeHtml(path)}:${hit.line}</strong><br>${escapeHtml(hit.text)}</span></button>`;
        })
        .join("");
    } catch (error) {
      if (id === request && !dialog.hidden) status.textContent = `Search failed: ${String(error)}`;
    }
  };
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void search();
  });
  input.addEventListener("input", () => {
    request++;
    hits = [];
    results.replaceChildren();
    status.textContent = "";
  });
  results.addEventListener("click", (event) => {
    const item = (event.target as HTMLElement).closest<HTMLElement>("[data-search-hit]");
    if (!item) return;
    const hit = hits[Number(item.dataset.searchHit)];
    if (!hit || resultRoot !== state.cwd) return;
    close();
    openAt(hit.path, hit.line);
  });
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) close();
  });
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    } else if (event.key === "Tab") {
      const targets = Array.from(dialog.querySelectorAll<HTMLElement>("input, button"));
      const index = targets.indexOf(document.activeElement as HTMLElement);
      event.preventDefault();
      targets[(index + (event.shiftKey ? -1 : 1) + targets.length) % targets.length].focus();
    }
  });
  const open = (): void => {
    if (!enabled()) return;
    prior = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.hidden = false;
    if (resultRoot !== state.cwd) {
      hits = [];
      results.replaceChildren();
      status.textContent = "";
    }
    input.focus();
    input.select();
  };
  window.addEventListener("paladin:ide-search-open", open);
  document.querySelector("#ideSearchBtn")!.addEventListener("click", open);
  document.addEventListener("keydown", (event) => {
    if (
      !enabled() ||
      !(event.metaKey || event.ctrlKey) ||
      !event.shiftKey ||
      event.altKey ||
      event.key.toLowerCase() !== "f"
    )
      return;
    event.preventDefault();
    open();
  });
}
