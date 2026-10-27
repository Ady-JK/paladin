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

// UsageModel is one model's slice of a session's usage.
export type UsageModel = {
  model: string;
  contextWindow: number;
  contextKnown: boolean;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  fillPercent?: number;
};

// FileEntry is one `@` completion row from `file_complete`: Path is
// the Linux-style chat path (`src/foo.ts`, `../sib/file`, `src/sub/`
// for folders), ResolvedPath the OS-specific absolute path the
// composer inserts into the message.
export type FileEntry = {
  path: string;
  display: string;
  resolvedPath: string;
  outside: boolean;
  isDir: boolean;
};

// SessionUsage is the client-facing usage view from `session_usage`.
// FillPercent is absent when the model's window is unknown.
export type SessionUsage = {
  sessionId: string;
  model: string;
  contextWindow: number;
  contextKnown: boolean;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  fillPercent?: number;
  models?: UsageModel[];
  daily?: DailyPoint[];
};

// DailyPoint is one UTC calendar day's billed activity.
export type DailyPoint = {
  date: string;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
};

// OverallSessionEntry is one session's slice of the fleet-wide rollup.
export type OverallSessionEntry = {
  sessionId: string;
  title?: string;
  model: string;
  createdAt: number;
  updatedAt: number;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
};

// OverallUsage is the fleet-wide rollup from `overall_usage`.
export type OverallUsage = {
  sessionCount: number;
  sessionsWithUsage: number;
  calls: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  listCost: number;
  hasCost: boolean;
  updatedAt: number;
  models?: UsageModel[];
  sessions?: OverallSessionEntry[];
  daily?: DailyPoint[];
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
