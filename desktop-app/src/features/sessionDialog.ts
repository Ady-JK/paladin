// sessionDialog.ts owns the new-session dialog and its folder picker.
// It supports the desktop app frontend.

import * as api from "../core/api";
import {
  $,
  newSessionListEl,
  newSessionPathEl,
  newSessionSearchEl,
  sessionModalEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { shortPath } from "../core/format";
import { state } from "../core/state";
import { toast } from "../ui/toast";
import { pickModel, syncModelSelects } from "./models";
import { newSession } from "./sessions";
import { refreshExternalVisibility } from "./sites";
import { folderRows, wireFolderList } from "./workspace";

export async function openNewSession(): Promise<void> {
  state.pendingWorkspace = state.cwd;
  newSessionSearchEl.value = "";
  syncModelSelects();
  sessionModalEl.classList.remove("hidden");
  sessionModalEl.classList.add("flex");
  void refreshExternalVisibility();

  await browseNewSession();
}

// closeNewSession is a function in this module.
// It supports the desktop app frontend.
export function closeNewSession(): void {
  sessionModalEl.classList.add("hidden");
  sessionModalEl.classList.remove("flex");
  void refreshExternalVisibility();
}

async function browseNewSession(path?: string): Promise<void> {
  const target = path ?? state.pendingWorkspace ?? state.cwd ?? null;

  try {
    const list = await api.fsList(target);
    state.pendingWorkspace = list.currentPath;
    newSessionPathEl.textContent = list.currentPath;
    newSessionListEl.innerHTML = folderRows(list);

    wireFolderList(
      newSessionListEl,
      (next) => void browseNewSession(next),
      (picked) => {
        state.pendingWorkspace = picked;
        newSessionPathEl.textContent = picked;
      },
    );
  } catch (error) {
    toast(String(error), "error");
  }
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

  sessionModalEl.addEventListener("click", (event) => {
    if (event.target === sessionModalEl) {
      closeNewSession();
    }
  });
}
