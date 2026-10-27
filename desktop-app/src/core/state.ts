// state.ts holds the shared mutable frontend state.
// Features read and write one object so modules stay free of cycles.

import type { ProviderInfo, SessionInfo, SessionUsage, SideView } from "./types";

// state is the single mutable store the feature modules share.
export const state = {
  sessionId: "",
  usage: null as SessionUsage | null,
  escArmedAt: 0,
  busy: false,
  cwd: "",
  sessions: [] as SessionInfo[],
  providers: [] as ProviderInfo[],
  selectedProvider: "",
  selectedLab: "",
  selectedModel: "",
  pendingDelete: "",
  dark: true,
  collapsed: false,
  shownMessages: 0,
  highlightedModel: "",
  treeFilter: null as { provider: string; lab: string } | null,
  sideView: "agent" as SideView,
  chatPage: "",
  customSites: [] as { name: string; url: string }[],
  pendingWorkspace: "",
  lastSentRect: "",
  pageUrlShown: "",
  // onProvidersChanged repaints the settings provider tab after a background
  // catalog refresh; the settings module assigns it at init.
  onProvidersChanged: (() => {}) as () => void,
};

// collapsedProviders tracks collapsed provider groups in the model picker.
export const collapsedProviders = new Set<string>();

// collapsedLabs tracks collapsed labs in the model picker.
export const collapsedLabs = new Set<string>();
