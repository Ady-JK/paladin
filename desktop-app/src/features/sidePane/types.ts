import type { Terminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";

export type ChatTab = {
  kind: "chat";
  key: string;
  chatId: string | null;
  title: string;
  body: HTMLElement;
  busy: boolean;
};

export type AgentTab = {
  kind: "agent";
  key: string;
  agentId: string;
  title: string;
  body: HTMLElement;
  // loading is the attach in flight, so two paints never race.
  loading: Promise<void> | null;
  // head is the last painted head, so unchanged repaints are skipped.
  head: string;
};

export type BrowserTab = {
  kind: "browser";
  key: string;
  sessionId: string;
  title: string;
  body: HTMLElement;
  url: string;
  picking: boolean;
  pickToken: number;
  noteCount: number;
  passing: boolean;
};

// WebTab is one site in the integrated browser: Gemini, ChatGPT or any
// https page. It uses the shared persistent profile, so a sign-in done
// in one site carries to the next and survives restarts.
export type WebTab = {
  kind: "web";
  key: string;
  sessionId: string;
  viewName: string;
  title: string;
  url: string;
  body: HTMLElement;
};

export type TerminalTab = {
  kind: "terminal";
  key: string;
  sessionId: string;
  cwd: string;
  title: string;
  body: HTMLElement;
  term: Terminal | null;
  fit: FitAddon | null;
  pendingText: string[];
  pendingInput: string;
  terminalId: string;
  starting: Promise<void> | null;
  closed: boolean;
};

export type ChangesTab = {
  kind: "changes";
  key: string;
  sessionId: string;
  title: string;
  body: HTMLElement;
  turn: number | null;
  selected: string | null;
  request: number;
  // mark highlights new-side lines of one file (a review finding).
  mark: { path: string; start: number; end: number } | null;
};

export type JobsTab = {
  kind: "jobs";
  key: string;
  sessionId: string;
  title: string;
  body: HTMLElement;
};

export type PluginTab = {
  kind: "plugin";
  key: string;
  sessionId: string;
  panelId: string;
  plugin: string;
  view: string;
  title: string;
  workspace: string;
  body: HTMLElement;
  iframe: HTMLIFrameElement;
  openedAt: number;
  stale: boolean;
};

export type Tab = ChatTab | AgentTab | BrowserTab | ChangesTab | JobsTab | PluginTab | TerminalTab | WebTab;
export type TabSet = { tabs: Tab[]; active: string };
