// settings.ts owns the settings modal: workspace, providers, connection,
// analytics. It supports the desktop app frontend.

import * as api from "../core/api";
import {
  $,
  providerCardEl,
  providerCardErrorEl,
  providerCardKeyEl,
  providerCardNameEl,
  providerCardTitleEl,
  providerCardTypeEl,
  providerCardUrlEl,
  settingsConnDetailEl,
  settingsConnDotEl,
  settingsConnLabelEl,
  settingsModalEl,
  settingsNavAnalyticsEl,
  settingsNavAppearanceEl,
  settingsNavResourcesEl,
  settingsPaneAppearanceEl,
  settingsPaneResourcesEl,
  motionOptionsEl,
  settingsNavConnectionEl,
  settingsNavPromptEl,
  settingsNavProvidersEl,
  settingsNavWorkspaceEl,
  settingsPaneAnalyticsEl,
  settingsPaneConnectionEl,
  settingsPanePromptEl,
  settingsPaneProvidersEl,
  settingsPaneTitleEl,
  settingsPaneWorkspaceEl,
  settingsPromptErrorEl,
  settingsProviderListEl,
  settingsSystemPromptEl,
  settingsWorkspaceBrowseBtn,
  settingsWorkspaceErrorEl,
  statusDetailEl,
  statusDotEl,
  statusLabelEl,
  workspaceBrowser,
  workspaceInputEl,
  workspaceLabelEl,
} from "../core/dom";
import { escapeAttr, escapeHtml, shortPath } from "../core/format";
import { state } from "../core/state";
import type { ProviderInfo } from "../core/types";
import { toast } from "../ui/toast";
import { closeModal, openModal } from "../ui/anim";
import { icon } from "../icons";
import { loadProviders, syncModelSelects } from "./models";
import { refreshExternalVisibility } from "./sites";
import { refreshSessions } from "./sessions";
import { browseWorkspace } from "./workspace";
import { load as loadAnalytics } from "./analytics";
import { refreshResources } from "./resources";
import { motionPref, setMotionPref, type MotionPref } from "../ui/motion";

// SettingsNav is a type in this module.
// It supports the desktop app frontend.
export type SettingsNav = "workspace" | "providers" | "prompt" | "connection" | "analytics" | "appearance" | "resources";

// paintSettingsNav is a const in this module.
// It supports the desktop app frontend.
const paintSettingsNav = (tab: SettingsNav): void => {
  const tabs: [SettingsNav, HTMLButtonElement, HTMLElement, string][] = [
    ["workspace", settingsNavWorkspaceEl, settingsPaneWorkspaceEl, "Workspace"],
    ["providers", settingsNavProvidersEl, settingsPaneProvidersEl, "Providers"],
    ["prompt", settingsNavPromptEl, settingsPanePromptEl, "Prompt"],
    ["connection", settingsNavConnectionEl, settingsPaneConnectionEl, "Connection"],
    ["analytics", settingsNavAnalyticsEl, settingsPaneAnalyticsEl, "Analytics"],
    ["appearance", settingsNavAppearanceEl, settingsPaneAppearanceEl, "Appearance"],
    ["resources", settingsNavResourcesEl, settingsPaneResourcesEl, "Resources"],
  ];
  for (const [name, btn, pane, title] of tabs) {
    const active = name === tab;
    btn.classList.toggle("bg-white", active);
    btn.classList.toggle("dark:bg-slate-800", active);
    btn.classList.toggle("text-slate-900", active);
    btn.classList.toggle("dark:text-slate-100", active);
    btn.classList.toggle("shadow-sm", active);
    btn.classList.toggle("text-slate-500", !active);
    btn.classList.toggle("dark:text-slate-400", !active);
    btn.setAttribute("aria-selected", active ? "true" : "false");
    pane.classList.toggle("hidden", !active);
    if (active) {
      settingsPaneTitleEl.textContent = title;
    }
  }
};

// loadPrompt fills the prompt editor from the agent core file
// (SYSTEM_PROMPT.md). Failures stay inline so a broken read never
// looks like an empty prompt.
const loadPrompt = async (): Promise<void> => {
  settingsPromptErrorEl.textContent = "";
  settingsPromptErrorEl.classList.add("hidden");
  try {
    const prompt = await api.getPrompt();
    settingsSystemPromptEl.value = prompt.systemPrompt;
  } catch (error) {
    settingsPromptErrorEl.textContent = String(error);
    settingsPromptErrorEl.classList.remove("hidden");
  }
};

// savePrompt writes the editor back to the prompt file. Turns started
// afterwards use the new text immediately.
const savePrompt = async (): Promise<void> => {
  settingsPromptErrorEl.textContent = "";
  settingsPromptErrorEl.classList.add("hidden");
  try {
    const updated = await api.setPrompt({ systemPrompt: settingsSystemPromptEl.value });
    settingsSystemPromptEl.value = updated.systemPrompt;
    toast("Prompt saved");
  } catch (error) {
    const message = String(error);
    settingsPromptErrorEl.textContent = message;
    settingsPromptErrorEl.classList.remove("hidden");
    toast(message, "error");
  }
};

// resetPrompt rewrites the file from the built-in default.
const resetPrompt = async (): Promise<void> => {
  settingsPromptErrorEl.textContent = "";
  settingsPromptErrorEl.classList.add("hidden");
  try {
    const updated = await api.resetPrompt();
    settingsSystemPromptEl.value = updated.systemPrompt;
    toast("Prompt reset to default");
  } catch (error) {
    const message = String(error);
    settingsPromptErrorEl.textContent = message;
    settingsPromptErrorEl.classList.remove("hidden");
    toast(message, "error");
  }
};
// It supports the desktop app frontend.
// renderProviderList is a const in this module.
// It supports the desktop app frontend.
const renderProviderList = (): void => {
  if (state.providers.length === 0) {
    settingsProviderListEl.innerHTML = `<p class="rounded-lg border border-dashed border-slate-300 px-3 py-6 text-center text-slate-400 dark:border-slate-700">no providers yet — add one above</p>`;
    return;
  }
  settingsProviderListEl.innerHTML = state.providers
    .map((p) => {
      const models =
        p.models.length > 0 ? `${p.models.length} models` : "models not fetched yet";
      const key = p.hasKey ? "key stored" : "no key";
      const badge = p.default
        ? `<span class="shrink-0 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">default</span>`
        : `<button data-setdefault="${escapeAttr(p.name)}" class="shrink-0 rounded-full border border-slate-200 px-2 py-0.5 text-[10px] font-medium text-slate-500 hover:bg-slate-100 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800">set default</button>`;
      return (
        `<div class="flex items-center gap-2.5 rounded-lg border border-slate-200 bg-white px-3 py-2.5 dark:border-slate-700 dark:bg-slate-900">` +
        `<span class="flex-shrink-0 text-accent-500">${icon("globe", "h-4 w-4")}</span>` +
        `<div class="min-w-0 flex-1">` +
        `<div class="truncate text-xs font-semibold text-slate-800 dark:text-slate-100">${escapeHtml(p.name)}</div>` +
        `<div class="truncate font-mono text-[10px] text-slate-400">${escapeHtml(p.apiType)} · ${escapeHtml(p.apiUrl || "no url")} · ${escapeHtml(key)} · ${escapeHtml(models)}</div>` +
        `</div>` +
        badge +
        `<button data-refresh="${escapeAttr(p.name)}" title="Fetch ${escapeAttr(p.name)}'s model list" class="shrink-0 rounded-md border border-slate-200 px-2 py-1 text-[11px] font-medium text-slate-600 hover:bg-slate-100 disabled:opacity-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">refresh</button>` +
        `<button data-connect="${escapeAttr(p.name)}" class="shrink-0 rounded-md bg-accent-600 px-3 py-1 text-[11px] font-semibold text-white hover:bg-accent-700">connect</button>` +
        `</div>`
      );
    })
    .join("");
};

// refreshOneProvider fetches a single provider's catalog on demand.
const refreshOneProvider = async (btn: HTMLButtonElement, name: string): Promise<void> => {
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const res = await api.refreshProvider(name);
    if (res.error) {
      toast(`${name}: ${res.error}`, "error");
    } else {
      toast(`${name}: ${res.count} models`);
    }
    // loadProviders remaps the saved pick onto the fresh catalog.
    await loadProviders();
  } catch (error) {
    toast(String(error), "error");
  } finally {
    btn.disabled = false;
    btn.textContent = "refresh";
  }
};

// reloadProviders is a const in this module.
// It supports the desktop app frontend.
const reloadProviders = async (): Promise<void> => {
  try {
    state.providers = await api.listProviders();
  } catch (error) {
    toast(String(error), "error");
    return;
  }
  renderProviderList();
  syncModelSelects();
};

// paintSettingsConnection is a const in this module.
// It supports the desktop app frontend.
const paintSettingsConnection = (): void => {
  settingsConnLabelEl.textContent = statusLabelEl.textContent || "—";
  const dot = statusDotElClass();
  settingsConnDotEl.className = `h-2 w-2 flex-shrink-0 rounded-full ${dot}`;
  const keyed = state.providers.filter((p) => p.hasKey).length;
  const rows: [string, string][] = [
    ["core", statusDetailEl.textContent || "—"],
    ["workspace", state.cwd || "—"],
    ["providers", `${state.providers.length} configured · ${keyed} with keys`],
  ];
  settingsConnDetailEl.innerHTML = rows
    .map(
      ([name, value]) =>
        `<div class="flex gap-2"><span class="w-20 flex-shrink-0 uppercase tracking-wider text-slate-400 dark:text-slate-500" style="font-size:9px">${escapeHtml(name)}</span><span class="min-w-0 flex-1 truncate">${escapeHtml(value)}</span></div>`,
    )
    .join("");
};

// statusDotElClass reads the sidebar dot color for the connection pane.
function statusDotElClass(): string {
  return statusDotEl.className.split(" ").slice(-1)[0] ?? "bg-slate-300";
}

// providerCardMode is a let in this module.
// It supports the desktop app frontend.
let providerCardMode: "add" | "edit" = "add";

// providerCardName is a let in this module.
// It supports the desktop app frontend.
let providerCardName = "";

// openProviderCard is a const in this module.
// It supports the desktop app frontend.
const openProviderCard = (mode: "add" | "edit", info?: ProviderInfo): void => {
  providerCardMode = mode;
  providerCardName = info?.name ?? "";
  providerCardTitleEl.textContent = mode === "add" ? "Add provider" : `Connect ${info?.name ?? ""}`;
  providerCardNameEl.value = info?.name ?? "";
  providerCardNameEl.disabled = mode === "edit";
  providerCardUrlEl.value = info?.apiUrl ?? "";
  providerCardKeyEl.value = "";
  providerCardKeyEl.placeholder =
    mode === "edit" ? "leave empty to keep the stored key" : "paste the provider api key";
  providerCardTypeEl.value = info?.apiType ?? "openai";
  providerCardErrorEl.classList.add("hidden");
  providerCardErrorEl.textContent = "";
  openModal(providerCardEl);
  (mode === "add" ? providerCardNameEl : providerCardUrlEl).focus();
};

// closeProviderCard is a const in this module.
// It supports the desktop app frontend.
const closeProviderCard = (): void => {
  closeModal(providerCardEl);
  providerCardKeyEl.value = "";
};

// saveProviderCard is a const in this module.
// It supports the desktop app frontend.
const saveProviderCard = async (): Promise<void> => {
  const url = providerCardUrlEl.value.trim();
  const key = providerCardKeyEl.value.trim();
  const apiType = providerCardTypeEl.value;
  try {
    if (providerCardMode === "add") {
      const name = providerCardNameEl.value.trim();
      if (!name) {
        throw new Error("name is required");
      }
      await api.addProvider({
        name,
        apiUrl: url,
        apiKey: key,
        apiType,
      });
      toast(`${name} added`);
    } else {
      await api.updateProvider({
        name: providerCardName,
        apiUrl: url,
        apiKey: key,
        apiType,
      });
      toast(`${providerCardName} updated`);
    }
    closeProviderCard();
    await reloadProviders();
  } catch (error) {
    providerCardErrorEl.textContent = String(error);
    providerCardErrorEl.classList.remove("hidden");
  }
};

// openSettings is a const in this module.
// It supports the desktop app frontend.
export const openSettings = (tab: SettingsNav = "workspace"): void => {
  openModal(settingsModalEl);
  paintSettingsNav(tab);
  renderProviderList();
  paintSettingsConnection();
  void (async () => {
    try {
      workspaceInputEl.value = await api.getWorkspace();
    } catch {
    }
  })();
  void refreshExternalVisibility();
};

// closeSettings is a const in this module.
// It supports the desktop app frontend.
export const closeSettings = (): void => {
  closeProviderCard();
  closeModal(settingsModalEl);
  void refreshExternalVisibility();
};

function clearWorkspaceError(): void {
  settingsWorkspaceErrorEl.textContent = "";
  settingsWorkspaceErrorEl.classList.add("hidden");
}

// initSettings wires the settings modal. reconnect re-runs bootstrap connect().
// It supports the desktop app frontend.
export function initSettings(reconnect: () => Promise<void>): void {
  state.onProvidersChanged = () => {
    if (!settingsModalEl.classList.contains("hidden")) {
      renderProviderList();
      paintSettingsConnection();
    }
  };

  settingsNavWorkspaceEl.addEventListener("click", () => paintSettingsNav("workspace"));
  settingsNavProvidersEl.addEventListener("click", () => paintSettingsNav("providers"));
  settingsNavPromptEl.addEventListener("click", () => {
    paintSettingsNav("prompt");
    void loadPrompt();
  });
  settingsNavConnectionEl.addEventListener("click", () => {
    paintSettingsConnection();
    paintSettingsNav("connection");
  });
  settingsNavAnalyticsEl.addEventListener("click", () => {
    paintSettingsNav("analytics");
    void loadAnalytics(true);
  });
  settingsNavAppearanceEl.addEventListener("click", () => {
    const pref = motionPref();
    for (const input of Array.from(motionOptionsEl.querySelectorAll<HTMLInputElement>("input[name=motion]"))) {
      input.checked = input.value === pref;
    }
    paintSettingsNav("appearance");
  });
  motionOptionsEl.addEventListener("change", (event) => {
    const input = event.target as HTMLInputElement;
    if (input.name === "motion" && input.checked) {
      setMotionPref(input.value as MotionPref);
    }
  });
  settingsNavResourcesEl.addEventListener("click", () => {
    paintSettingsNav("resources");
    refreshResources();
  });

  settingsProviderListEl.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    const setBtn = target.closest<HTMLButtonElement>("[data-setdefault]");
    if (setBtn?.dataset.setdefault) {
      const name = setBtn.dataset.setdefault;
      void (async () => {
        try {
          state.providers = await api.setDefaultProvider(name);
          renderProviderList();
          syncModelSelects();
          toast(`${name} is now the default provider`);
        } catch (error) {
          toast(String(error), "error");
        }
      })();
      return;
    }
    const refreshBtn = target.closest<HTMLButtonElement>("[data-refresh]");
    if (refreshBtn?.dataset.refresh) {
      void refreshOneProvider(refreshBtn, refreshBtn.dataset.refresh);
      return;
    }
    const connectBtn = target.closest<HTMLButtonElement>("[data-connect]");
    if (connectBtn?.dataset.connect) {
      const info = state.providers.find((p) => p.name === connectBtn.dataset.connect);
      if (info) {
        openProviderCard("edit", info);
      }
    }
  });

  $("#settingsAddProviderBtn").addEventListener("click", () => openProviderCard("add"));
  $("#providerCardCancel").addEventListener("click", closeProviderCard);
  $("#providerCardSave").addEventListener("click", () => {
    void saveProviderCard();
  });
  providerCardEl.addEventListener("click", (event) => {
    if (event.target === providerCardEl) {
      closeProviderCard();
    }
  });

  $("#settingsPromptSaveBtn").addEventListener("click", () => {
    void savePrompt();
  });
  $("#settingsPromptResetBtn").addEventListener("click", () => {
    void resetPrompt();
  });

  settingsWorkspaceBrowseBtn.addEventListener("click", () => {
    void (async () => {
      if (api.canPickNative()) {
        try {
          const picked = await api.pickDirectory(workspaceInputEl.value.trim() || null);
          if (picked) {
            workspaceInputEl.value = picked;
            clearWorkspaceError();
          }
        } catch (error) {
          toast(String(error), "error");
        }
        return;
      }
      if (workspaceBrowser.classList.contains("hidden")) {
        void browseWorkspace();
      } else {
        workspaceBrowser.classList.add("hidden");
      }
    })();
  });

  workspaceInputEl.addEventListener("input", clearWorkspaceError);

  $("#settingsWorkspaceSaveBtn").addEventListener("click", () => {
    void (async () => {
      try {
        const path = await api.setWorkspace(workspaceInputEl.value.trim());
        workspaceInputEl.value = path;
        clearWorkspaceError();
        toast(path ? `Default workspace saved` : "Default workspace cleared");
      } catch (error) {
        const message = String(error);
        settingsWorkspaceErrorEl.textContent = message;
        settingsWorkspaceErrorEl.classList.remove("hidden");
        toast(message, "error");
      }
    })();
  });

  $("#settingsWorkspaceUseBtn").addEventListener("click", () => {
    const path = workspaceInputEl.value.trim();
    closeSettings();
    void (async () => {
      state.cwd = "";
      await reconnect();
      if (path) {
        state.cwd = path;
        workspaceLabelEl.textContent = shortPath(path);
        await refreshSessions();
      }
    })();
  });

  paintSettingsNav("workspace");

  $("#settingsBtn").addEventListener("click", () => openSettings());
  $("#settingsCloseBtn").addEventListener("click", closeSettings);
  $("#settingsCancelBtn").addEventListener("click", closeSettings);

  $("#settingsSaveBtn").addEventListener("click", () => {
    closeSettings();
    void reconnect();
  });
}
