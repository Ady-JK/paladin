// workspace.ts browses daemon-side folders for the workspace picker.
// It supports the desktop app frontend.

import * as api from "../core/api";
import {
  paintIcons,
  settingsWorkspaceErrorEl,
  workspaceBrowser,
  workspaceBrowserList,
  workspaceBrowserPath,
  workspaceInputEl,
} from "../core/dom";
import { escapeHtml } from "../core/format";
import type { FsList } from "../core/types";
import { toast } from "../ui/toast";

export async function browseWorkspace(path?: string): Promise<void> {
  const target = (path ?? workspaceInputEl.value.trim()) || null;

  try {
    const list = await api.fsList(target);
    renderBrowser(list);
  } catch (error) {
    toast(String(error), "error");
    workspaceBrowser.classList.remove("hidden");
  }
}

// renderBrowser is a function in this module.
// It supports the desktop app frontend.
function renderBrowser(list: FsList): void {
  workspaceBrowserPath.textContent = list.currentPath;
  workspaceBrowserList.innerHTML = folderRows(list);
  workspaceBrowser.classList.remove("hidden");

  wireFolderList(workspaceBrowserList, (path) => void browseWorkspace(path), (path) => {
    workspaceInputEl.value = path;
    settingsWorkspaceErrorEl.textContent = "";
    settingsWorkspaceErrorEl.classList.add("hidden");
    workspaceBrowser.classList.add("hidden");
  });
}

// folderRows is a function in this module.
// It supports the desktop app frontend.
export function folderRows(list: FsList): string {
  const rows: string[] = [];

  rows.push(`
    <button
      data-pick="${escapeHtml(list.currentPath)}"
      class="flex w-full items-center gap-2 rounded-md bg-indigo-50 px-2 py-1.5 text-left text-xs font-medium text-indigo-900 transition hover:bg-indigo-100 dark:bg-indigo-950/40 dark:text-indigo-200 dark:hover:bg-indigo-950/70"
      title="Use this folder as the workspace"
    >
      <span data-icon="check" class="flex-shrink-0"></span>
      <span class="truncate">Use this folder</span>
    </button>`);

  if (list.parentPath && list.parentPath !== list.currentPath) {
    rows.push(`
      <button
        data-browse="${escapeHtml(list.parentPath)}"
        class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
        title="Go to the parent folder"
      >
        <span data-icon="angles-left" class="flex-shrink-0"></span>
        <span class="truncate">..</span>
      </button>`);
  }

  for (const dir of list.directories) {
    rows.push(`
      <button
        data-browse="${escapeHtml(dir.path)}"
        class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-slate-700 transition hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
        title="${escapeHtml(dir.path)}"
      >
        <span data-icon="folder" class="flex-shrink-0 text-agent-amber"></span>
        <span class="truncate">${escapeHtml(dir.name)}</span>
      </button>`);
  }

  if (list.directories.length === 0) {
    rows.push(`<p class="px-2 py-1 text-slate-400">no subfolders</p>`);
  }

  return rows.join("");
}

// wireFolderList is a function in this module.
// It supports the desktop app frontend.
export function wireFolderList(
  listEl: HTMLElement,
  onBrowse: (path: string) => void,
  onPick: (path: string) => void,
): void {
  paintIcons();

  listEl.querySelectorAll<HTMLButtonElement>("button[data-browse]").forEach((button) => {
    button.addEventListener("click", () => {
      onBrowse(button.dataset.browse ?? "");
    });
  });

  listEl.querySelectorAll<HTMLButtonElement>("button[data-pick]").forEach((button) => {
    button.addEventListener("click", () => {
      onPick(button.dataset.pick ?? "");
    });
  });
}
