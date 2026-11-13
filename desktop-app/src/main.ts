// main.ts bootstraps the desktop app frontend.
// Every backend call goes through api.ts to the agent-host sidecar.

import { version as appVersion } from "../package.json";
import * as api from "./core/api";
import {
  $,
  inputEl,
  messagesEl,
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
import { initModelPicker, loadProviders, refreshDefaultCatalog, resetDefaultRefresh } from "./features/models";
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
import { initMotion, motionMs } from "./ui/motion";
import { initUndo } from "./features/undo";
import { bootLive, initLive, openPendingSession, syncComposer } from "./features/live";
import { syncMcp } from "./features/mcp";
import { syncTools } from "./features/toolSwitches";
import { syncBudget } from "./features/budget";
import { initThinking, syncThinking } from "./features/thinking";
import { initIDEActivity } from "./features/ide/activity";

async function connect(): Promise<void> {
  setStatus("connecting…", "busy");
  resetDefaultRefresh();

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

    await loadProviders();
    void refreshDefaultCatalog();
  } catch (error) {
    setStatus(String(error), "error");
    statusDetailEl.textContent = "—";
  }
}

async function revealApp(connection: Promise<void>): Promise<void> {
  const delay = (ms: number): Promise<void> => new Promise((resolve) => window.setTimeout(resolve, ms));
  await Promise.all([Promise.race([connection, delay(2500)]), delay(motionMs(650))]);
  const startup = $("#startup");
  const shell = $("#appShell");
  shell.inert = false;
  shell.classList.add("is-ready");
  startup.classList.add("is-leaving");
  inputEl.focus();
  await delay(motionMs(300));
  startup.remove();
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
  const ideButton = $<HTMLButtonElement>("#ideModeBtn");
  let ideLoad: Promise<void> | undefined;
  const ensureIDE = (): Promise<void> => {
    ideLoad ??= import("./features/ide")
      .then(({ initIDE }) => {
        initIDE();
        ideButton.removeEventListener("click", loadIDE);
      })
      .catch((error: unknown) => {
        ideLoad = undefined;
        throw error;
      });
    return ideLoad;
  };
  $("#ideExplorerToggle").addEventListener("click", () => {
    void ensureIDE()
      .then(() => {
        const event = $(".ide-explorer").classList.contains("is-closed")
          ? "paladin:ide-show-explorer"
          : "paladin:ide-toggle-explorer";
        window.dispatchEvent(new Event(event));
      })
      .catch((error: unknown) => setStatus(`Could not open Explorer: ${String(error)}`, "error"));
  });
  // A second press while the IDE module is still loading must toggle the
  // mode instead of being dropped (the busy guard used to swallow it, so
  // pressing the shortcut twice quickly left the IDE open).
  let ideToggleQueued = false;
  const loadIDE = (): void => {
    if (ideButton.hasAttribute("aria-busy")) {
      ideToggleQueued = true;
      return;
    }
    ideButton.setAttribute("aria-busy", "true");
    void ensureIDE()
      .then(() => {
        ideButton.removeAttribute("aria-busy");
        ideButton.dataset.ideReplay = "true";
        try {
          ideButton.click();
          if (ideToggleQueued) {
            ideToggleQueued = false;
            ideButton.click();
          }
        } finally {
          delete ideButton.dataset.ideReplay;
        }
      })
      .catch((error: unknown) => {
        ideToggleQueued = false;
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
  initIDEActivity();
  initUsage();
  initResources();
  initContextBox();
  initShortcuts();

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

  void revealApp(connect());
});
