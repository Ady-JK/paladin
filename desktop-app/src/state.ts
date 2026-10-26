import type { ProviderInfo, SessionInfo, SideView } from "./types";

export const state = {
  sessionId: "",
  busy: false,
  cwd: "",
  sessions: [] as SessionInfo[],
  providers: [] as ProviderInfo[],
  selectedProvider: "",
  selectedModel: "",
  sideView: "agent" as SideView,
  chatPage: "gemini",
  customSites: [] as { name: string; url: string }[],
  lastSentRect: "",
  shownMessages: 0,
  onProvidersChanged: (() => {}) as () => void,
};
