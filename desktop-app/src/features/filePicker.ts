// filePicker.ts owns the attach-a-file picker behind the composer
// paperclip button. It mirrors the new-session folder picker but
// lists files as well as folders, starting at the workspace path.
// Folders navigate deeper; picking a file inserts its absolute path
// into the composer so the agent sees exactly which file was tagged.

import * as api from "../core/api";
import {
  fileListEl,
  fileModalEl,
  filePathEl,
  inputEl,
  paintIcons,
} from "../core/dom";
import { escapeHtml } from "../core/format";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { autosizeComposer } from "./chat";

let pickDir = "";

// fileRows renders the picker list: a parent row, folder rows that
// navigate, and file rows that insert. Icons run larger than the
// session picker so touch targets stay comfortable.
function fileRows(list: FsList): string {
  const rows: string[] = [];

  if (list.parentPath && list.parentPath !== list.currentPath) {
    rows.push(`
      <button
        data-browse="${escapeHtml(list.parentPath)}"
        class="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
        title="Go to the parent folder"
      >
        <span data-icon="angles-left" class="flex-shrink-0 [&>svg]:h-5 [&>svg]:w-5"></span>
        <span class="truncate">..</span>
      </button>`);
  }

  for (const dir of list.directories) {
    rows.push(`
      <button
        data-browse="${escapeHtml(dir.path)}"
        class="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs text-slate-700 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
        title="${escapeHtml(dir.path)}"
      >
        <span data-icon="folder" class="flex-shrink-0 text-agent-amber [&>svg]:h-5 [&>svg]:w-5"></span>
        <span class="truncate">${escapeHtml(dir.name)}</span>
      </button>`);
  }

  for (const file of list.files) {
    rows.push(`
      <button
        data-pick-file="${escapeHtml(file.path)}"
        class="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs text-slate-700 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
        title="${escapeHtml(file.path)}"
      >
        <span data-icon="file-code" class="flex-shrink-0 text-slate-400 [&>svg]:h-5 [&>svg]:w-5"></span>
        <span class="truncate">${escapeHtml(file.name)}</span>
      </button>`);
  }

  if (list.directories.length === 0 && list.files.length === 0) {
    rows.push(`<p class="px-2 py-1 text-slate-400">empty folder</p>`);
  }

  return rows.join("");
}

// insertPickedPath drops the absolute path at the caret with word spacing,
// then hands focus back to the composer.
function insertPickedPath(path: string): void {
  const caret = inputEl.selectionStart ?? inputEl.value.length;
  const before = inputEl.value.slice(0, caret);
  const after = inputEl.value.slice(caret);
  const prefix = before && !/\s$/.test(before) ? " " : "";
  inputEl.value = `${before}${prefix}${path} ${after}`;
  const next = before.length + prefix.length + path.length + 1;
  inputEl.selectionStart = next;
  inputEl.selectionEnd = next;
  closeFilePicker();
  inputEl.focus();
  autosizeComposer();
}

async function browseFilePicker(path?: string): Promise<void> {
  const target = (path ?? pickDir ?? state.cwd ?? "").trim() || null;

  try {
    const list = await api.fsList(target);
    pickDir = list.currentPath;
    filePathEl.textContent = list.currentPath;
    fileListEl.innerHTML = fileRows(list);
    paintIcons(fileListEl);

    fileListEl.querySelectorAll<HTMLButtonElement>("button[data-browse]").forEach((button) => {
      button.addEventListener("click", () => {
        void browseFilePicker(button.dataset.browse ?? "");
      });
    });
    fileListEl.querySelectorAll<HTMLButtonElement>("button[data-pick-file]").forEach((button) => {
      button.addEventListener("click", () => {
        insertPickedPath(button.dataset.pickFile ?? "");
        closeFilePicker();
      });
    });
  } catch (error) {
    toast(String(error), "error");
  }
}

// openFilePicker shows the OS file picker rooted at the workspace
// path, falling back to the in-app picker where native dialogs are
// unavailable (e2e stub, plain web). Cancelling stays silent.
export async function openFilePicker(): Promise<void> {
  if (api.canPickNative()) {
    let picked: string[] | null = null;
    try {
      picked = await api.pickFiles(state.cwd || null);
    } catch (error) {
      toast(String(error), "error");
      return;
    }
    if (picked) {
      for (const path of picked) {
        insertPickedPath(path);
      }
    }
    return;
  }
  pickDir = state.cwd || "";
  openModal(fileModalEl);
  void browseFilePicker();
}

// closeFilePicker is a function in this module.
// It supports the desktop app frontend.
export function closeFilePicker(): void {
  closeModal(fileModalEl);
}

// initFilePicker wires the picker dialog controls.
// It supports the desktop app frontend.
export function initFilePicker(): void {
  document.querySelector("#fileCloseBtn")?.addEventListener("click", closeFilePicker);
  document.querySelector("#fileCancelBtn")?.addEventListener("click", closeFilePicker);

  fileModalEl.addEventListener("click", (event) => {
    if (event.target === fileModalEl) {
      closeFilePicker();
    }
  });
}
