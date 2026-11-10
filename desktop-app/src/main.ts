// main.ts bootstraps the desktop app frontend.
// Every backend call goes through api.ts to the agent-host sidecar.

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
import { clearTodos, initTodos } from "./features/todos";
import { renderGreeting } from "./features/chatView";
import { initModelPicker, loadProviders } from "./features/models";
import { syncPermChip } from "./features/permissions";
import { closeConfirm, initSessions, openSession, refreshSessions } from "./features/sessions";
import { initSidePane } from "./features/sidePane";
import { initPluginPanels } from "./features/pluginPanels";
import { initPluginCards } from "./features/pluginCards";
import { initCreatorMode } from "./features/creatorMode";
import { closeNewSession, initSessionDialog, openNewSession } from "./features/sessionDialog";
import { closeNewSite, initWeb } from "./features/web";
import { clearUsage, closeUsageDashboard, initUsage } from "./features/usage";
import { closeSettings, initSettings, openSettings } from "./features/settings";
import { initMemory } from "./features/memory";
import { initFilePicker, closeFilePicker } from "./features/filePicker";
import { initMentions } from "./features/mentions";
import { initSlash } from "./features/slash";
import { initSkills } from "./features/skills";
import { initWindow } from "./features/window";
import { initResources } from "./features/resources";
import { initShortcuts } from "./features/shortcuts";
import { clearContextBox, initContextBox, reloadContextSources } from "./features/contextBox";
import { initMotion } from "./ui/motion";
import { initUndo } from "./features/undo";
import { bootLive, initLive, openPendingSession, syncComposer } from "./features/live";
import { syncMcp } from "./features/mcp";
import { syncTools } from "./features/toolSwitches";
import { syncBudget } from "./features/budget";
import { initThinking, syncThinking } from "./features/thinking";

async function connect(): Promise<void> {
  setStatus("connecting…", "busy");

  try {
    let savedWorkspace = "";
    try {
      savedWorkspace = await api.getWorkspace();
    } catch {
      savedWorkspace = "";
    }
    workspaceInputEl.value = savedWorkspace;
    if (!state.cwd) {
      state.cwd = savedWorkspace || (await api.resolveWorkspace(null));
    } else {
      state.cwd = await api.resolveWorkspace(state.cwd);
    }
    workspaceLabelEl.textContent = shortPath(state.cwd);
    window.dispatchEvent(new Event("paladin:workspace-changed"));

    state.sessionId = "";
    syncPermChip();
    syncMcp();
    syncTools();
    void syncBudget();
    void syncThinking();
    clearContextBox();
    void reloadContextSources();
    sessionLabelEl.textContent = "—";
    clearUsage();
    clearTodos();

    messagesEl.innerHTML = "";
    renderGreeting();

    await refreshSessions();
    syncComposer();
    // Turns outlive the window: learn what is still running, then honor
    // a notification click that launched this window.
    await bootLive();
    void openPendingSession((id) => void openSession(id));

    setStatus("local", "ok");
    statusDetailEl.textContent = "in-process";

    // Catalogs are only fetched on an explicit user refresh.
    await loadProviders();
  } catch (error) {
    setStatus(String(error), "error");
    statusDetailEl.textContent = "—";
  }
}

window.addEventListener("DOMContentLoaded", () => {
  initMotion();
  paintIcons();
  $("#brandVersion").textContent = `v${appVersion}`;

  initWindow();
  initChat();
  initTodos();
  initFilePicker();
  initMentions();
  initSlash();
  initSkills();
  initModelPicker();
  initThinking();
  initWeb();
  initSessionDialog();
  initSettings(connect);
  initMemory();
  initSessions();
  initSidePane();
  const ideButton = $("#ideModeBtn");
  const loadIDE = (): void => {
    if (ideButton.hasAttribute("aria-busy")) return;
    ideButton.setAttribute("aria-busy", "true");
    void import("./features/ide")
      .then(({ initIDE }) => {
        ideButton.removeEventListener("click", loadIDE);
        ideButton.removeAttribute("aria-busy");
        initIDE();
        ideButton.click();
      })
      .catch((error: unknown) => {
        ideButton.removeAttribute("aria-busy");
        setStatus(`Could not open IDE: ${String(error)}`, "error");
      });
  };
  ideButton.addEventListener("click", loadIDE);
  initPluginPanels();
  initCreatorMode();
  void initPluginCards();
  initUndo();
  initLive((id) => void openSession(id));
  initUsage();
  initResources();
  initContextBox();
  initShortcuts();

  modelAdvancedBtn.addEventListener("click", () => {
    closeMenu(modelMenu);
    openSettings();
  });

  $("#resPill").addEventListener("click", () => {
    openSettings();
    $("#settingsNavResources").click();
  });
  $("#resTotal").addEventListener("click", () => {
    openSettings();
    $("#settingsNavResources").click();
  });

  $("#newSessionBtn").addEventListener("click", () => {
    void openNewSession();
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
  });

  void connect();

  inputEl.focus();
});
