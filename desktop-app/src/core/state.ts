// state.ts holds the shared mutable frontend state.
// Features read and write one object so modules stay free of cycles.

import type { ProviderInfo, SessionInfo, SessionUsage, SubagentLink } from "./types";

// state is the single mutable store the feature modules share.
export const state = {
  sessionId: "",
  // subagent is set while the open session is a subagent's child.
  subagent: null as SubagentLink | null,
  usage: null as SessionUsage | null,
  escArmedAt: 0,
  busy: false,
  // mode is the composer's turn mode: plan keeps the agent read-only.
  mode: "build" as "build" | "plan",
  cwd: "",
  sessions: [] as SessionInfo[],
  providers: [] as ProviderInfo[],
  selectedProvider: "",
  selectedLab: "",
  selectedModel: "",
  pendingDelete: "",
  dark: true,
  collapsed: true,
  customSites: [] as { name: string; url: string }[],
  pendingWorkspace: "",
  // onProvidersChanged repaints the settings provider tab after a background
  // catalog refresh; the settings module assigns it at init.
  onProvidersChanged: (() => {}) as () => void,
};
