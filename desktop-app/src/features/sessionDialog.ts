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
import { bare, shortPath, splitModel } from "../core/format";
import { state } from "../core/state";
import type { FsList } from "../core/types";
import { closeModal, openModal } from "../ui/anim";
import { toast } from "../ui/toast";
import { pickModel } from "./models";
import { newSession, refreshSessions } from "./sessions";
import { crumbsHTML, filterEntries, getShowHidden, rowsHTML, setShowHidden, type PickerSelection } from "./picker";
import { syncMcp } from "./mcp";

let currentList: FsList | null = null;
let selection: PickerSelection | null = null;
let query = "";
let showHidden = false;
let browseGeneration = 0;
let choosingFolder = false;

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
  newSessionModelHintEl.title =
    [state.selectedProvider, state.selectedModel].filter(Boolean).join(" / ") || "default model";
  newSessionListEl.innerHTML = rowsHTML({ ...currentList, directories: visible.directories, files: [] }, selection, {
    filesEnabled: false,
  });
  paintIcons(sessionModalEl);

  newSessionCrumbsEl.querySelectorAll<HTMLButtonElement>("button[data-fb-crumb]").forEach((button) => {
    button.addEventListener("click", () => {
      void browseNewSession(button.dataset.fbCrumb ?? "");
    });
  });
  newSessionListEl.querySelectorAll<HTMLElement>("[data-fb-open]").forEach((el) => {
    el.addEventListener("click", (event) => {
      event.stopPropagation();
      void browseNewSession(el.dataset.fbOpen ?? "");
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

  const request = ++browseGeneration;
  const startButton = $<HTMLButtonElement>("#sessionStartBtn");
  startButton.disabled = true;
  try {
    const list = await api.fsList(target);
    if (request !== browseGeneration) return;
    currentList = list;
    // Browsing to a parent must not leave a previously selected child armed.
    if (selection && selection.path !== list.currentPath) {
      selection = null;
    }
    paintFolder();
  } catch (error) {
    if (request === browseGeneration) toast(String(error), "error");
  } finally {
    if (request === browseGeneration) startButton.disabled = false;
  }
}

export async function openNewSession(options?: { chooseFolder?: boolean }): Promise<void> {
  choosingFolder = !!options?.chooseFolder;
  $("#sessionDialogTitle").textContent = options?.chooseFolder ? "Open folder" : "New Session";
  $("#sessionStartBtn").textContent = options?.chooseFolder ? "Open folder" : "Start";
  newSessionModelHintEl.hidden = !!options?.chooseFolder;
  state.pendingWorkspace = state.cwd;
  selection = null;
  query = "";
  currentList = null;
  newSessionCrumbsEl.replaceChildren();
  newSessionListEl.textContent = "Loading…";
  newSessionPathEl.textContent = state.cwd;
  showHidden = getShowHidden();
  newSessionFolderSearchEl.value = "";
  newSessionShowHiddenEl.checked = showHidden;
  openModal(sessionModalEl);

  await browseNewSession();
}

export function closeNewSession(): void {
  browseGeneration++;
  closeModal(sessionModalEl);
}

function startNewSession(): void {
  if (choosingFolder && state.pendingWorkspace.trim() === state.cwd) {
    closeNewSession();
    return;
  }
  if (state.pendingWorkspace.trim()) {
    state.cwd = state.pendingWorkspace.trim();
    workspaceLabelEl.textContent = shortPath(state.cwd);
    workspaceInputEl.value = state.cwd;
    syncMcp();
  }

  pickModel(state.selectedProvider, state.selectedModel);
  closeNewSession();
  newSession();
  // The workspace may have changed folders: refetch the global list so
  // sessions from other folders stay visible.
  void refreshSessions();
}

// initSessionDialog wires the new-session dialog controls.
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
