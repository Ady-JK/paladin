// filePicker.ts owns the attach-a-file-or-folder picker behind the
// composer paperclip button. It is always the in-app native-like
// browser: breadcrumbs on top, search plus a persisted "Hidden"
// checkbox, a single-select list, and explicit Select file / Select
// folder / Cancel buttons. Single click selects, double-click (or the
// chevron) opens a folder. Select file inserts the highlighted file;
// Select folder inserts the highlighted folder, or the current folder
// when a file (or nothing) is highlighted.

import * as api from "../core/api";
import {
  fileCrumbsEl,
  fileListEl,
  fileModalEl,
  filePathEl,
  fileSearchEl,
  fileSelectFileBtn,
  fileSelectFolderBtn,
  fileShowHiddenEl,
  inputEl,
  paintIcons,
} from "../core/dom";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { autosizeComposer } from "./chat";
import {
  crumbsHTML,
  filterEntries,
  getShowHidden,
  rowsHTML,
  setShowHidden,
  type PickerSelection,
} from "./picker";

let pickDir = "";
let currentList: FsList | null = null;
let selection: PickerSelection | null = null;
let query = "";
let showHidden = false;

// paintPicker repaints crumbs, rows, and footer state from the cached
// list plus the current filter, selection, and toggle. One entry point
// keeps search, toggle, browse, and select in sync.
function paintPicker(): void {
  if (!currentList) {
    return;
  }
  const visible = filterEntries(currentList, showHidden, query);
  fileCrumbsEl.innerHTML = crumbsHTML(currentList.currentPath);
  filePathEl.textContent = currentList.currentPath;
  fileListEl.innerHTML = rowsHTML(
    { ...currentList, ...visible },
    selection,
    { filesEnabled: true },
  );
  paintIcons(fileModalEl);

  const fileOk = selection?.kind === "file";
  fileSelectFileBtn.toggleAttribute("disabled", !fileOk);
  fileSelectFileBtn.classList.toggle("opacity-40", !fileOk);
  fileSelectFileBtn.classList.toggle("cursor-not-allowed", !fileOk);

  fileListEl.querySelectorAll<HTMLButtonElement>("button[data-fb-crumb]").forEach((button) => {
    button.addEventListener("click", () => {
      void browseFilePicker(button.dataset.fbCrumb ?? "");
    });
  });
  // Parent rows and chevrons navigate; stop the chevron from also
  // selecting so one click opens instead of arming a confirm.
  fileListEl.querySelectorAll<HTMLElement>("[data-fb-open]").forEach((el) => {
    el.addEventListener("click", (event) => {
      event.stopPropagation();
      const path = (el as HTMLElement).dataset.fbOpen ?? "";
      void browseFilePicker(path);
    });
    el.addEventListener("dblclick", (event) => {
      event.stopPropagation();
    });
  });
  fileListEl.querySelectorAll<HTMLButtonElement>("button[data-fb-select]").forEach((button) => {
    const path = button.dataset.fbSelect ?? "";
    const kind = (button.dataset.fbKind === "file" ? "file" : "folder") as PickerSelection["kind"];
    button.addEventListener("click", () => {
      selection = { path, kind };
      paintPicker();
      // Keep keyboard focus in the list for fast arrow/tab flows.
      button.focus();
    });
    if (kind === "folder") {
      button.addEventListener("dblclick", () => {
        void browseFilePicker(path);
      });
    } else {
      button.addEventListener("dblclick", () => {
        selection = { path, kind };
        confirmSelection("file");
      });
    }
  });
}

async function browseFilePicker(path?: string): Promise<void> {
  const target = (path ?? pickDir ?? state.cwd ?? "").trim() || null;

  try {
    const list = await api.fsList(target);
    currentList = list;
    pickDir = list.currentPath;
    // A stale highlight from another folder must not survive.
    if (selection && !selection.path.startsWith(`${list.currentPath}/`) && selection.path !== list.currentPath) {
      selection = null;
    }
    paintPicker();
  } catch (error) {
    toast(String(error), "error");
  }
}

// insertPickedPath drops the path at the caret with word spacing,
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

// confirmSelection resolves the footer buttons: Select file needs a
// highlighted file; Select folder takes the highlighted folder, else
// the current directory (a file highlight never blocks a folder pick).
function confirmSelection(which: "file" | "folder"): void {
  if (which === "file") {
    if (!selection || selection.kind !== "file") {
      toast("Pick a file from the list first");
      return;
    }
    insertPickedPath(selection.path);
    return;
  }
  if (selection?.kind === "folder") {
    insertPickedPath(selection.path);
    return;
  }
  if (currentList) {
    insertPickedPath(currentList.currentPath);
  }
}

// openFilePicker shows the in-app attach browser rooted at the
// workspace. Search and toggle restore their persisted state.
export async function openFilePicker(): Promise<void> {
  pickDir = state.cwd || "";
  currentList = null;
  selection = null;
  query = "";
  showHidden = getShowHidden();
  fileSearchEl.value = "";
  fileShowHiddenEl.checked = showHidden;
  fileModalEl.classList.add("is-open");
  openModal(fileModalEl);
  window.setTimeout(() => fileSearchEl.focus(), 60);
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
  fileSelectFileBtn.addEventListener("click", () => confirmSelection("file"));
  fileSelectFolderBtn.addEventListener("click", () => confirmSelection("folder"));

  fileSearchEl.addEventListener("input", () => {
    query = fileSearchEl.value;
    paintPicker();
  });

  fileShowHiddenEl.addEventListener("change", () => {
    showHidden = fileShowHiddenEl.checked;
    setShowHidden(showHidden);
    paintPicker();
  });

  fileModalEl.addEventListener("click", (event) => {
    if (event.target === fileModalEl) {
      closeFilePicker();
    }
  });
}
