// sessionDialog.ts owns the new-session dialog: a single native-like
// folder browser (breadcrumbs, search, persisted Hidden toggle,
// single-select list with double-click to open). Start creates the
// session in the selected folder — or the current one — using the
// already-selected model: the default on a fresh launch, or the model
// of the session the dialog was opened from.

import * as api from "../core/api";
import {
  $,
  newSessionCrumbsEl,
  newSessionFolderSearchEl,
  newSessionListEl,
  newSessionModelHintEl,
  newSessionPathEl,
  newSessionShowHiddenEl,
  paintIcons,
  sessionModalEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { shortPath, splitModel } from "../core/format";
import { bare } from "../core/format";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { pickModel } from "./models";
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

// modelHint mirrors the composer label so the dialog states which model
// the new session will use before it starts.
function modelHint(): string {
  const short = state.selectedModel ? bare(splitModel(state.selectedModel).short) : "";
  const provider = bare(state.selectedProvider);
  const label = short ? (provider ? `${provider}/${short}` : short) : provider || "default";
  return `model: ${label}`;
}

// paintFolder repaints the browser from the cached list plus the current
// filter, selection, and toggle. The pending workspace tracks the
// highlight, falling back to the browsed directory, so Start always acts
// on exactly what the path line shows.
function paintFolder(): void {
  if (!currentList) {
    return;
  }
  state.pendingWorkspace = selection?.path ?? currentList.currentPath;
  const visible = filterEntries(currentList, showHidden, query);
  newSessionCrumbsEl.innerHTML = crumbsHTML(currentList.currentPath);
  newSessionPathEl.textContent = state.pendingWorkspace;
  newSessionPathEl.title = state.pendingWorkspace;
  newSessionModelHintEl.textContent = modelHint();
  newSessionModelHintEl.title = [state.selectedProvider, state.selectedModel].filter(Boolean).join(" / ") || "default model";
  newSessionListEl.innerHTML = rowsHTML(
    { ...currentList, directories: visible.directories, files: [] },
    selection,
    { filesEnabled: false },
  );
  paintIcons(sessionModalEl);

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
  openModal(sessionModalEl);
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
  $("#sessionStartBtn").addEventListener("click", startNewSession);

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
