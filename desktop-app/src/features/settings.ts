// settings.ts owns the settings modal and its panes: workspace,
// providers, prompt, connection, analytics, appearance, resources,
// memory and permissions.

import * as api from "../core/api";
import { bridge } from "../core/bridge";
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
  settingsNavMemoryEl,
  settingsPaneMemoryEl,
  settingsNavPermissionsEl,
  settingsPanePermissionsEl,
  settingsNavMcpEl,
  settingsPaneMcpEl,
  settingsNavHooksEl,
  settingsPaneHooksEl,
  settingsNavToolsEl,
  settingsPaneToolsEl,
  settingsNavCustomizeEl,
  settingsPaneCustomizeEl,
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
import { escapeAttr, escapeHtml, renderMarkdown, shortPath } from "../core/format";
import { state } from "../core/state";
import type { ProviderInfo } from "../core/types";
import { toast } from "../ui/toast";
import { closeModal, openModal } from "../ui/anim";
import { icon } from "../icons";
import { loadProviders, syncModelSelects } from "./models";
import { refreshThinkingOptions } from "./thinking";
import { refreshExternalVisibility } from "./sites";
import { refreshSessions } from "./sessions";
import { browseWorkspace } from "./workspace";
import { load as loadAnalytics } from "./analytics";
import { initBudgetSettings, loadDefaultBudget } from "./budget";
import { refreshResources } from "./resources";
import { loadMemory } from "./memory";
import { initPermissionsPane, loadPermissions } from "./permissions";
import { initMcp, loadMcp, syncMcp } from "./mcp";
import { loadHooks } from "./hooks";
import { initToolSwitches, loadTools, syncTools } from "./toolSwitches";
import { initCustomize, loadCustomize } from "./customize";
import { motionPref, setMotionPref, type MotionPref } from "../ui/motion";
import { getColorMode, setColorMode, type ColorMode } from "./window";

export type SettingsNav =
  | "workspace"
  | "providers"
  | "prompt"
  | "connection"
  | "analytics"
  | "appearance"
  | "resources"
  | "memory"
  | "permissions"
  | "mcp"
  | "hooks"
  | "tools"
  | "customize";

type FontSize = "small" | "default" | "large";
const fontSizeKey = "paladin.fontSize";
function fontSize(): FontSize {
  const saved = localStorage.getItem(fontSizeKey);
  return saved === "small" || saved === "large" ? saved : "default";
}
function setFontSize(size: FontSize): void {
  document.documentElement.dataset.fontSize = size;
  localStorage.setItem(fontSizeKey, size);
}

const paintSettingsNav = (tab: SettingsNav): void => {
  const descriptions: Record<SettingsNav, string> = {
    workspace: "Choose where new sessions start and browse your project folders.",
    providers: "Connect models and choose the default provider.",
    prompt: "Preview, edit, and refine the agent's instructions.",
    connection: "Choose the local or remote core and inspect its status.",
    analytics: "Review usage, spend, and session activity.",
    appearance: "Set the app's color, text size, and motion.",
    resources: "Inspect processes and resources used by the current session.",
    memory: "Review the context Paladin carries into future turns.",
    permissions: "Control what the agent may run or change.",
    mcp: "Manage connected tool servers.",
    hooks: "Manage actions that run at defined points in a turn.",
    tools: "Choose which tools are available to the agent.",
    customize:
      "Edit every built-in text the agent gets: prompts, tool guidance, reminders, subagents, commands, skills and settings files.",
  };
  const tabs: [SettingsNav, HTMLButtonElement, HTMLElement, string][] = [
    ["workspace", settingsNavWorkspaceEl, settingsPaneWorkspaceEl, "Workspace"],
    ["providers", settingsNavProvidersEl, settingsPaneProvidersEl, "Providers"],
    ["prompt", settingsNavPromptEl, settingsPanePromptEl, "Prompt"],
    ["connection", settingsNavConnectionEl, settingsPaneConnectionEl, "Connection"],
    ["analytics", settingsNavAnalyticsEl, settingsPaneAnalyticsEl, "Analytics"],
    ["appearance", settingsNavAppearanceEl, settingsPaneAppearanceEl, "Appearance"],
    ["resources", settingsNavResourcesEl, settingsPaneResourcesEl, "Resources"],
    ["memory", settingsNavMemoryEl, settingsPaneMemoryEl, "Memory"],
    ["permissions", settingsNavPermissionsEl, settingsPanePermissionsEl, "Permissions"],
    ["mcp", settingsNavMcpEl, settingsPaneMcpEl, "MCP servers"],
    ["hooks", settingsNavHooksEl, settingsPaneHooksEl, "Hooks"],
    ["tools", settingsNavToolsEl, settingsPaneToolsEl, "Tools"],
    ["customize", settingsNavCustomizeEl, settingsPaneCustomizeEl, "Customize"],
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
      $("#settingsPaneSubtitle").textContent = descriptions[name];
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
    promptProposal = "";
    $("#settingsPromptProposalWrap").classList.add("hidden");
    paintPromptPreview();
  } catch (error) {
    settingsPromptErrorEl.textContent = String(error);
    settingsPromptErrorEl.classList.remove("hidden");
  }
};

let promptProposal = "";
function paintPromptPreview(): void {
  $("#settingsPromptPreview").innerHTML = renderMarkdown(settingsSystemPromptEl.value);
}
function paintPromptModel(): void {
  const provider = state.selectedProvider || "default provider";
  const model = state.selectedModel ? ` · ${state.selectedModel}` : "";
  $("#settingsPromptModelLabel").textContent = `Using ${provider}${model}`;
}
function showPromptTab(tab: "preview" | "edit"): void {
  const preview = tab === "preview";
  $("#settingsPromptPreview").classList.toggle("hidden", !preview);
  $("#settingsPromptEditor").classList.toggle("hidden", preview);
  $("#settingsPromptPreviewTab").setAttribute("aria-selected", String(preview));
  $("#settingsPromptEditTab").setAttribute("aria-selected", String(!preview));
  if (preview) paintPromptPreview();
}

async function suggestPrompt(): Promise<void> {
  const instruction = $<HTMLTextAreaElement>("#settingsPromptInstruction").value.trim();
  const button = $<HTMLButtonElement>("#settingsPromptSuggestBtn");
  if (!instruction) {
    settingsPromptErrorEl.textContent = "Describe what you want to change first.";
    settingsPromptErrorEl.classList.remove("hidden");
    return;
  }
  settingsPromptErrorEl.classList.add("hidden");
  promptProposal = "";
  $("#settingsPromptProposalWrap").classList.add("hidden");
  button.disabled = true;
  button.textContent = "Generating…";
  try {
    const result = await api.suggestPrompt({
      systemPrompt: settingsSystemPromptEl.value,
      instruction,
      provider: state.selectedProvider || undefined,
      model: state.selectedModel || undefined,
    });
    promptProposal = result.systemPrompt;
    $("#settingsPromptProposal").innerHTML = renderMarkdown(promptProposal);
    $("#settingsPromptProposalWrap").classList.remove("hidden");
  } catch (error) {
    settingsPromptErrorEl.textContent = String(error);
    settingsPromptErrorEl.classList.remove("hidden");
  } finally {
    button.disabled = false;
    button.textContent = "Generate proposal";
  }
}

// savePrompt writes the editor back to the prompt file. Turns started
// afterwards use the new text immediately.
const savePrompt = async (): Promise<void> => {
  settingsPromptErrorEl.textContent = "";
  settingsPromptErrorEl.classList.add("hidden");
  try {
    const updated = await api.setPrompt({ systemPrompt: settingsSystemPromptEl.value });
    settingsSystemPromptEl.value = updated.systemPrompt;
    paintPromptPreview();
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
    paintPromptPreview();
    toast("Prompt reset to default");
  } catch (error) {
    const message = String(error);
    settingsPromptErrorEl.textContent = message;
    settingsPromptErrorEl.classList.remove("hidden");
    toast(message, "error");
  }
};
const renderProviderList = (): void => {
  if (state.providers.length === 0) {
    settingsProviderListEl.innerHTML = `<p class="rounded-lg border border-dashed border-slate-300 px-3 py-6 text-center text-slate-400 dark:border-slate-700">no providers yet — add one above</p>`;
    return;
  }
  settingsProviderListEl.innerHTML = state.providers
    .map((p) => {
      const models = p.models.length > 0 ? `${p.models.length} models` : "models not fetched yet";
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
    // The listing's metadata (thinking levels, dialect, prices) changed
    // under the picked model, and the model id did not: re-read it, or
    // the thinking control keeps saying the model has no levels.
    refreshThinkingOptions();
  } catch (error) {
    toast(String(error), "error");
  } finally {
    btn.disabled = false;
    btn.textContent = "refresh";
  }
};

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

async function loadCoreConnection(): Promise<void> {
  if (!bridge().core) return;
  try {
    const value = await bridge().core!.connection();
    const mode = $<HTMLSelectElement>("#settingsCoreMode");
    mode.value = value.mode;
    $<HTMLInputElement>("#settingsRemoteTarget").value = value.target ?? "";
    $<HTMLInputElement>("#settingsRemoteBinary").value = value.binary ?? "";
    $("#settingsRemoteFields").classList.toggle("hidden", value.mode !== "ssh");
  } catch (error) {
    $("#settingsRemoteError").textContent = String(error);
  }
}

// statusDotElClass reads the sidebar dot color for the connection pane.
function statusDotElClass(): string {
  return statusDotEl.className.split(" ").slice(-1)[0] ?? "bg-slate-300";
}

let providerCardMode: "add" | "edit" = "add";

let providerCardName = "";

const openProviderCard = (mode: "add" | "edit", info?: ProviderInfo): void => {
  providerCardMode = mode;
  providerCardName = info?.name ?? "";
  providerCardTitleEl.textContent = mode === "add" ? "Add provider" : `Connect ${info?.name ?? ""}`;
  providerCardNameEl.value = info?.name ?? "";
  providerCardNameEl.disabled = mode === "edit";
  providerCardUrlEl.value = info?.apiUrl ?? "";
  providerCardKeyEl.value = "";
  providerCardKeyEl.placeholder = mode === "edit" ? "leave empty to keep the stored key" : "paste the provider api key";
  providerCardTypeEl.value = info?.apiType ?? "openai";
  providerCardErrorEl.classList.add("hidden");
  providerCardErrorEl.textContent = "";
  openModal(providerCardEl);
  (mode === "add" ? providerCardNameEl : providerCardUrlEl).focus();
};

const closeProviderCard = (): void => {
  closeModal(providerCardEl);
  providerCardKeyEl.value = "";
};

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

export const openSettings = (tab: SettingsNav = "workspace"): void => {
  openModal(settingsModalEl);
  paintSettingsNav(tab);
  if (tab === "memory") {
    void loadMemory();
  } else if (tab === "permissions") {
    void loadPermissions(state.cwd);
  } else if (tab === "mcp") {
    void loadMcp();
  } else if (tab === "hooks") {
    void loadHooks();
  } else if (tab === "tools") {
    void loadTools();
  } else if (tab === "customize") {
    void loadCustomize();
  } else if (tab === "resources") {
    refreshResources();
  } else if (tab === "analytics") {
    void loadDefaultBudget();
  } else if (tab === "prompt") {
    paintPromptModel();
    void loadPrompt();
  }
  renderProviderList();
  paintSettingsConnection();
  void loadCoreConnection();
  void (async () => {
    try {
      workspaceInputEl.value = await api.getWorkspace();
    } catch (error) {
      console.warn(`workspace lookup failed: ${String(error)}`);
    }
  })();
  void refreshExternalVisibility();
};

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
export function initSettings(reconnect: () => Promise<void>): void {
  setFontSize(fontSize());
  initBudgetSettings();
  $<HTMLInputElement>("#settingsSearch").addEventListener("input", (event) => {
    const query = (event.target as HTMLInputElement).value.trim().toLowerCase();
    document.querySelectorAll<HTMLButtonElement>("#settingsSidebar button[role=tab]").forEach((button) => {
      button.classList.toggle("hidden", !button.textContent?.toLowerCase().includes(query));
    });
  });
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
    paintPromptModel();
    void loadPrompt();
  });
  settingsNavConnectionEl.addEventListener("click", () => {
    paintSettingsConnection();
    paintSettingsNav("connection");
    void loadCoreConnection();
  });
  $<HTMLSelectElement>("#settingsCoreMode").addEventListener("change", (event) => {
    $("#settingsRemoteFields").classList.toggle("hidden", (event.target as HTMLSelectElement).value !== "ssh");
  });
  $<HTMLButtonElement>("#settingsRemoteSave").addEventListener("click", async () => {
    const errorEl = $("#settingsRemoteError");
    errorEl.textContent = "";
    const mode = $<HTMLSelectElement>("#settingsCoreMode").value;
    try {
      if (!bridge().core) throw new Error("remote connections require the desktop app");
      await bridge().core!.setConnection(
        mode === "ssh"
          ? {
              mode: "ssh",
              target: $<HTMLInputElement>("#settingsRemoteTarget").value.trim(),
              binary: $<HTMLInputElement>("#settingsRemoteBinary").value.trim(),
            }
          : { mode: "local" },
      );
      toast("Core connection updated");
      await reconnect();
      paintSettingsConnection();
    } catch (error) {
      errorEl.textContent = String(error);
    }
  });
  settingsNavAnalyticsEl.addEventListener("click", () => {
    paintSettingsNav("analytics");
    void loadDefaultBudget();
    void loadAnalytics(true);
  });
  settingsNavAppearanceEl.addEventListener("click", () => {
    const pref = motionPref();
    for (const input of Array.from(motionOptionsEl.querySelectorAll<HTMLInputElement>("input[name=motion]"))) {
      input.checked = input.value === pref;
    }
    $<HTMLSelectElement>("#settingsColorMode").value = getColorMode();
    $<HTMLSelectElement>("#settingsFontSize").value = fontSize();
    paintSettingsNav("appearance");
  });
  $<HTMLSelectElement>("#settingsColorMode").addEventListener("change", (event) => {
    setColorMode((event.target as HTMLSelectElement).value as ColorMode);
  });
  $<HTMLSelectElement>("#settingsFontSize").addEventListener("change", (event) => {
    setFontSize((event.target as HTMLSelectElement).value as FontSize);
  });
  motionOptionsEl.addEventListener("change", (event) => {
    const input = event.target as HTMLInputElement;
    if (input.name === "motion" && input.checked) {
      setMotionPref(input.value as MotionPref);
    }
  });
  initPermissionsPane();
  settingsNavPermissionsEl.addEventListener("click", () => {
    paintSettingsNav("permissions");
    void loadPermissions(state.cwd);
  });
  initMcp(() => openSettings("mcp"));
  initToolSwitches(() => openSettings("tools"));
  settingsNavToolsEl.addEventListener("click", () => {
    paintSettingsNav("tools");
    void loadTools();
  });
  initCustomize();
  settingsNavCustomizeEl.addEventListener("click", () => {
    paintSettingsNav("customize");
    void loadCustomize();
  });
  settingsNavMcpEl.addEventListener("click", () => {
    paintSettingsNav("mcp");
    void loadMcp();
  });
  settingsNavHooksEl.addEventListener("click", () => {
    paintSettingsNav("hooks");
    void loadHooks();
  });
  settingsNavMemoryEl.addEventListener("click", () => {
    paintSettingsNav("memory");
    void loadMemory();
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
  $("#settingsPromptPreviewTab").addEventListener("click", () => showPromptTab("preview"));
  $("#settingsPromptEditTab").addEventListener("click", () => showPromptTab("edit"));
  $("#settingsPromptSuggestBtn").addEventListener("click", () => {
    void suggestPrompt();
  });
  $("#settingsPromptApplyBtn").addEventListener("click", () => {
    settingsSystemPromptEl.value = promptProposal;
    promptProposal = "";
    $("#settingsPromptProposalWrap").classList.add("hidden");
    toast("Proposal applied to editor. Save to use it on future turns.");
  });
  $("#settingsPromptDiscardBtn").addEventListener("click", () => {
    promptProposal = "";
    $("#settingsPromptProposalWrap").classList.add("hidden");
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
        syncMcp();
        syncTools();
        await refreshSessions();
      }
    })();
  });

  paintSettingsNav("workspace");

  $("#settingsBtn").addEventListener("click", () => openSettings());
  $("#settingsCloseBtn").addEventListener("click", closeSettings);
  $("#settingsCancelBtn").addEventListener("click", closeSettings);
}
