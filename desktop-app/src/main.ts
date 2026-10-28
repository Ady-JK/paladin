// main.ts bootstraps the desktop app frontend.
// In-process only: no daemon connect, no subscribe — direct runtime calls.

import "./styles.css";
import { version as appVersion } from "../package.json";
import * as api from "./core/api";
import {
  $,
  inputEl,
  messagesEl,
  modelAdvancedBtn,
  modelMenu,
  paintIcons,
  sessionLabelEl,
  statusDetailEl,
  workspaceInputEl,
  workspaceLabelEl,
} from "./core/dom";
import { shortPath } from "./core/format";
import { state } from "./core/state";
import { setStatus } from "./ui/status";
import { closeMenu } from "./ui/anim";
import { initChat } from "./features/chat";
import { renderGreeting } from "./features/chatView";
import { initModelPicker, loadProviders, refreshProviders } from "./features/models";
import {
  closeConfirm,
  initSessions,
  refreshSessions,
} from "./features/sessions";
import {
  closeNewSession,
  initSessionDialog,
  openNewSession,
} from "./features/sessionDialog";
import {
  closeNewSite,
  initSites,
  openNewSite,
  refreshExternalVisibility,
} from "./features/sites";
import { clearUsage, closeUsageDashboard, initUsage } from "./features/usage";
import { closeSettings, initSettings, openSettings } from "./features/settings";
import { initFilePicker, closeFilePicker } from "./features/filePicker";
import { initMentions } from "./features/mentions";
import { initSlash } from "./features/slash";
import { initWindow } from "./features/window";

async function connect(): Promise<void> {
  setStatus("connecting…", "busy");

  try {
    let daemonWorkspace = "";
    try {
      daemonWorkspace = await api.getWorkspace();
    } catch {
      daemonWorkspace = "";
    }
    workspaceInputEl.value = daemonWorkspace;
    if (!state.cwd) {
      state.cwd = daemonWorkspace || (await api.resolveWorkspace(null));
    } else {
      state.cwd = await api.resolveWorkspace(state.cwd);
    }
    workspaceLabelEl.textContent = shortPath(state.cwd);

    state.sessionId = "";
    sessionLabelEl.textContent = "—";
    state.shownMessages = 0;
    clearUsage();

    messagesEl.innerHTML = "";
    renderGreeting();

    await refreshSessions();

    setStatus("local", "ok");
    statusDetailEl.textContent = "in-process";

    await loadProviders();
    void refreshProviders();
  } catch (error) {
    setStatus(String(error), "error");
    statusDetailEl.textContent = "—";
  }
}

window.addEventListener("DOMContentLoaded", () => {
  paintIcons();
  $("#brandVersion").textContent = `v${appVersion}`;

  initWindow();
  initChat();
  initFilePicker();
  initMentions();
  initSlash();
  initModelPicker();
  initSites();
  initSessionDialog();
  initSettings(connect);
  initSessions();
  initUsage();

  modelAdvancedBtn.addEventListener("click", () => {
    closeMenu(modelMenu);
    openSettings();
  });

  $("#newSessionBtn").addEventListener("click", () => {
    if (state.sideView === "chat") {
      openNewSite();
    } else {
      void openNewSession();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") {
      return;
    }
    closeConfirm();
    closeSettings();
    closeNewSession();
    closeNewSite();
    closeFilePicker();
    closeMenu(modelMenu);
    closeUsageDashboard();
    void refreshExternalVisibility();
  });

  void connect();

  inputEl.focus();
});
