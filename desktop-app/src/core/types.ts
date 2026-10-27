// types.ts holds the shared frontend types.
// It keeps every feature module speaking the same shapes.

// Turn is a type in this module.
// It supports the desktop app frontend.
export type Turn = {
  role: "user" | "agent" | "error";
  text: string;
  pending?: boolean;
};

// SessionInfo is a type in this module.
// It supports the desktop app frontend.
export type SessionInfo = {
  sessionId: string;
  cwd: string;
  createdAt: number;
  provider: string;
  model: string;
  messageCount: number;
  title: string;
};

// MessageInfo is a type in this module.
// It supports the desktop app frontend.
export type MessageInfo = {
  role: string;
  content: string;
  createdAt: number;
  name?: string;
  arguments?: string;
  status?: string;
};

// ToolInfo is a type in this module.
// It supports the desktop app frontend.
export type ToolInfo = {
  id: string;
  name: string;
  arguments: string;
  result: string;
  status: "running" | "ok" | "failed";
};

// SessionEvent is a type in this module.
// It supports the desktop app frontend.
export type SessionEvent = {
  kind: string;
  sessionId: string;
  cwd: string;
  messageCount: number;
  // Events dropped for a slow client before this one; nonzero means the
  // stream gapped and the view should refetch.
  missed?: number;
};

// ProviderInfo is a type in this module.
// It supports the desktop app frontend.
export type ProviderInfo = {
  name: string;
  apiType: string;
  apiUrl: string;
  model: string;
  models: string[];
  default: boolean;
  hasKey?: boolean;
};

// FsDirectory is a type in this module.
// It supports the desktop app frontend.
export type FsDirectory = {
  name: string;
  path: string;
};

// FsList is a type in this module.
// It supports the desktop app frontend.
export type FsList = {
  currentPath: string;
  parentPath: string;
  directories: FsDirectory[];
};

// GroupedLab is a type in this module.
// It supports the desktop app frontend.
export type GroupedLab = {
  display: string;
  models: { provider: string; id: string }[];
};

// GroupedProvider is a type in this module.
// It supports the desktop app frontend.
export type GroupedProvider = {
  display: string;
  infos: ProviderInfo[];
  isDefault: boolean;
  labs: GroupedLab[];
};

// DiffRow is one rendered line of a two-sided text diff.
// OldNo/newNo are hunk-relative line numbers; one side is null on add/del rows.
export type DiffRow = {
  kind: "context" | "del" | "add";
  oldNo: number | null;
  newNo: number | null;
  text: string;
};

// MatchSpan is one applied replacement span from an edit result.
// Start and end are 1-based line numbers in the pre-edit file.
export type MatchSpan = {
  start: number;
  end: number;
};

// ToolView is a type in this module.
// It supports the desktop app frontend.
export type ToolView = {
  el: HTMLElement;
  update: (tool: ToolInfo) => void;
};

// SideView is a type in this module.
// It supports the desktop app frontend.
export type SideView = "agent" | "chat";

// ChatEntry is a type in this module.
// It supports the desktop app frontend.
export type ChatEntry = {
  id: string;
  title: string;
  url: string;
};
