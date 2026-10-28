// sessionDialog.ts owns the new-session wizard. Step 1 is the native-like
// folder browser (breadcrumbs, search, persisted Hidden toggle,
// single-select list with double-click to open). "Select this folder"
// advances to step 2, the model picker; the Folder back button returns
// to step 1 so a different folder can be reselected before Start.

import * as api from "../core/api";
import {
  $,
  newSessionCrumbsEl,
  newSessionFolderSearchEl,
  newSessionListEl,
  newSessionModelStepEl,
  newSessionFolderStepEl,
  newSessionPathEl,
  newSessionShowHiddenEl,
  paintIcons,
  sessionBackBtn,
  sessionFolderLabelEl,
  sessionModalEl,
  sessionSelectFolderBtn,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { shortPath } from "../core/format";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { pickModel, syncModelSelects } from "./models";
import { newSession } from "./sessions";
import { refreshExternalVisibility } from "./sites";
import {
  crumbsHTML,
  filterEntries,
  getShowHidden,
  rowsHTML,
  setShowHidden,
  type PickerSelection,
} from "./picker";

let currentList: FsList | null = null;
let selection: PickerSelection | null = null;
let query = "";
let showHidden = false;

// showStep toggles the wizard: folder first, model after confirm.
// Both steps are flex columns filling the fixed-height card; only the
// lists inside scroll, headers and footers stay pinned.
function showStep(which: "folder" | "model"): void {
  const folder = which === "folder";
  newSessionFolderStepEl.classList.toggle("hidden", !folder);
  newSessionFolderStepEl.classList.toggle("flex", folder);
  newSessionModelStepEl.classList.toggle("hidden", folder);
  newSessionModelStepEl.classList.toggle("flex", !folder);
  paintIcons(sessionModalEl);
}

// paintFolder repaints the folder step from the cached list plus the
// current filter, selection, and toggle.
function paintFolder(): void {
  if (!currentList) {
    return;
  }
  const visible = filterEntries(currentList, showHidden, query);
  newSessionCrumbsEl.innerHTML = crumbsHTML(currentList.currentPath);
  newSessionPathEl.textContent = state.pendingWorkspace || currentList.currentPath;
  sessionFolderLabelEl.textContent = state.pendingWorkspace || currentList.currentPath;
  sessionFolderLabelEl.title = state.pendingWorkspace || currentList.currentPath;
  newSessionListEl.innerHTML = rowsHTML(
    { ...currentList, directories: visible.directories, files: [] },
    selection,
    { filesEnabled: false },
  );
  paintIcons(newSessionFolderStepEl);

  const armed = selection?.kind === "folder" || state.pendingWorkspace !== "";
  sessionSelectFolderBtn.toggleAttribute("disabled", false);
  sessionSelectFolderBtn.classList.toggle("opacity-40", !armed);
  void armed;

  newSessionListEl.querySelectorAll<HTMLButtonElement>("button[data-fb-crumb]").forEach((button) => {
    button.addEventListener("click", () => {
      void browseNewSession(button.dataset.fbCrumb ?? "");
    });
  });
  newSessionListEl.querySelectorAll<HTMLElement>("[data-fb-open]").forEach((el) => {
    el.addEventListener("click", (event) => {
      event.stopPropagation();
      void browseNewSession((el as HTMLElement).dataset.fbOpen ?? "");
    });
    el.addEventListener("dblclick", (event) => {
      event.stopPropagation();
    });
  });
  newSessionListEl.querySelectorAll<HTMLButtonElement>("button[data-fb-select]").forEach((button) => {
    const path = button.dataset.fbSelect ?? "";
    button.addEventListener("click", () => {
      selection = { path, kind: "folder" };
      state.pendingWorkspace = path;
      paintFolder();
      button.focus();
    });
    button.addEventListener("dblclick", () => {
      void browseNewSession(path);
    });
  });
}

async function browseNewSession(path?: string): Promise<void> {
  const target = path ?? state.pendingWorkspace ?? state.cwd ?? null;

  try {
    const list = await api.fsList(target);
    currentList = list;
    if (!state.pendingWorkspace) {
      state.pendingWorkspace = list.currentPath;
    }
    if (selection && selection.path !== list.currentPath && !selection.path.startsWith(`${list.currentPath}/`)) {
      selection = null;
    }
    paintFolder();
  } catch (error) {
    toast(String(error), "error");
  }
}

export async function openNewSession(): Promise<void> {
  state.pendingWorkspace = state.cwd;
  selection = null;
  query = "";
  currentList = null;
  showHidden = getShowHidden();
  newSessionFolderSearchEl.value = "";
  newSessionShowHiddenEl.checked = showHidden;
  showStep("folder");
  openModal(sessionModalEl);
  syncModelSelects();
  void refreshExternalVisibility();

  await browseNewSession();
}

// closeNewSession is a function in this module.
// It supports the desktop app frontend.
export function closeNewSession(): void {
  closeModal(sessionModalEl);
  void refreshExternalVisibility();
}

// startNewSession is a function in this module.
// It supports the desktop app frontend.
function startNewSession(): void {
  if (state.pendingWorkspace.trim()) {
    state.cwd = state.pendingWorkspace.trim();
    workspaceLabelEl.textContent = shortPath(state.cwd);
    workspaceInputEl.value = state.cwd;
  }

  pickModel(state.selectedProvider, state.selectedModel);
  closeNewSession();
  newSession();
}

// initSessionDialog wires the new-session dialog controls.
// It supports the desktop app frontend.
export function initSessionDialog(): void {
  $("#sessionCloseBtn").addEventListener("click", closeNewSession);
  $("#sessionCancelBtn").addEventListener("click", closeNewSession);
  document.querySelector("#sessionFolderCancelBtn")?.addEventListener("click", closeNewSession);
  $("#sessionStartBtn").addEventListener("click", startNewSession);

  // Select this folder: with a highlight it wins, otherwise the
  // current directory is the folder. Either way the wizard advances
  // to the model step instead of starting immediately.
  sessionSelectFolderBtn.addEventListener("click", () => {
    if (selection?.kind === "folder") {
      state.pendingWorkspace = selection.path;
    } else if (currentList && !state.pendingWorkspace) {
      state.pendingWorkspace = currentList.currentPath;
    }
    if (!state.pendingWorkspace) {
      toast("Pick a folder from the list first");
      return;
    }
    paintFolder();
    showStep("model");
  });

  sessionBackBtn.addEventListener("click", () => {
    showStep("folder");
  });

  newSessionFolderSearchEl.addEventListener("input", () => {
    query = newSessionFolderSearchEl.value;
    paintFolder();
  });

  newSessionShowHiddenEl.addEventListener("change", () => {
    showHidden = newSessionShowHiddenEl.checked;
    setShowHidden(showHidden);
    paintFolder();
  });

  sessionModalEl.addEventListener("click", (event) => {
    if (event.target === sessionModalEl) {
      closeNewSession();
    }
  });
}
