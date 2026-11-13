import { errorText } from "./utils";
import { renderDiff } from "./diffView";
import * as api from "../../core/api";
import { escapeAttr, escapeHtml } from "../../core/format";
import type { SessionChanges } from "../../core/types";
import { icon } from "../../icons";
import type { ChangesTab } from "./types";

const painted = new WeakMap<HTMLElement, string>();

function paintContent(element: HTMLElement, content: string, html = false): void {
  const signature = `${html ? "html" : "text"}:${content}`;
  if (painted.get(element) === signature) return;
  if (html) element.innerHTML = content;
  else element.textContent = content;
  painted.set(element, signature);
}

export function newChangesTab(
  sessionId: string,
  key: string,
  getChanges: () => SessionChanges | undefined,
): ChangesTab {
  const body = document.createElement("div");
  body.className = "pane-changes";
  body.innerHTML = `<div class="change-scope"></div><div class="change-files"></div><div class="change-diff" role="region" aria-label="File diff">Select a file to view its changes.</div>`;
  const tab: ChangesTab = {
    kind: "changes",
    key,
    sessionId,
    title: "Changes",
    body,
    turn: null,
    selected: null,
    request: 0,
    mark: null,
  };
  body.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("[data-change-all]")) {
      tab.turn = null;
      tab.selected = null;
      tab.request++;
      paintChangesTab(tab, getChanges());
      return;
    }
    const file = target.closest<HTMLElement>("[data-change-file]")?.dataset.changeFile;
    if (file) {
      tab.selected = file;
      if (tab.mark?.path !== file) tab.mark = null;
      paintChangesTab(tab, getChanges());
      void loadChangeDiff(tab, file);
    }
  });
  return tab;
}

export function paintChangesTab(tab: ChangesTab, data: SessionChanges | undefined): void {
  const files = tab.turn === null ? data?.files : data?.turns.find((t) => t.turn === tab.turn)?.files;
  paintContent(
    tab.body.querySelector<HTMLElement>(".change-scope")!,
    `<span>${tab.turn === null ? "Session changes" : "Turn changes"}</span>` +
      (tab.turn === null ? "" : `<button type="button" data-change-all>All session changes</button>`),
    true,
  );
  if (tab.selected && !files?.some((f) => f.path === tab.selected)) {
    tab.selected = null;
    tab.request++;
  }
  const list = tab.body.querySelector<HTMLElement>(".change-files")!;
  if (!data) {
    paintContent(list, "Loading file changes…");
  } else if (!data.available) {
    paintContent(list, "File snapshots are unavailable for this session.");
  } else if (!files?.length) {
    paintContent(list, "No file changes recorded.");
  } else {
    const content = files
      .map(
        (f) =>
          `<button type="button" class="change-file${tab.selected === f.path ? " is-active" : ""}" aria-pressed="${tab.selected === f.path}" data-change-file="${escapeAttr(f.path)}" title="${escapeAttr(f.path)}">` +
          `${icon("tl-file", "h-3.5 w-3.5")}<span>${escapeHtml(f.path)}</span><small>${f.status}</small></button>`,
      )
      .join("");
    paintContent(list, content, true);
  }
  if (!tab.selected)
    tab.body.querySelector<HTMLElement>(".change-diff")!.textContent = "Select a file to view its changes.";
}

export async function loadChangeDiff(tab: ChangesTab, path: string): Promise<void> {
  const request = ++tab.request;
  const preview = tab.body.querySelector<HTMLElement>(".change-diff")!;
  preview.textContent = "Loading diff…";
  try {
    const diff = await api.sessionDiff(tab.sessionId, null, tab.turn, path);
    if (request !== tab.request || tab.selected !== path) return;
    preview.replaceChildren();
    if (!diff.patch) {
      preview.textContent = "No text diff available for this file.";
      return;
    }
    const mark = tab.mark?.path === path ? tab.mark : null;
    preview.innerHTML = renderDiff(diff.patch, path, mark, diff.truncated);
    Array.from(preview.querySelectorAll<HTMLElement>(".change-mark"))
      .find((element) => element.getClientRects().length > 0)
      ?.scrollIntoView({ block: "center" });
  } catch (error) {
    if (request === tab.request && tab.selected === path)
      preview.textContent = `Could not load diff: ${errorText(error)}`;
  }
}
